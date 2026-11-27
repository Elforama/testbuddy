package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/report"
	"github.com/elforama/testbuddy/internal/workspace"
)

const replayUsage = `Usage: tb replay <flow.yaml> -d DEVICE [options]

Verifies one saved flow once on a device. The flow must begin with a required
launchApp step. That step controls whether app data is cleared. Without --from,
close an open session on the target before replaying on it.

A failed replay names the step that failed ("step 3 of 7") and how to finish:
do that step yourself in a session (tb do), then --from 4 runs the steps that
are left.

  -d, --device DEVICE       one device serial, UDID or name (web for Chrome); with
                          --from, also the name of your open session
  --from N                start at the flow's Nth step, on the app as it is now: the
                          steps before N do not run (no launchApp, no clearState).
                          On a device you hold in an open session, the steps run in
                          that session: it stays open and the app is not restarted.
                          A browser replay needs that session (-d NAME): without one
                          each run opens a new browser. In a session, --from 1 runs
                          the whole flow and leaves the app where a step failed.
  -e, --env KEY=VALUE      set a flow variable (repeatable)
  --headless              web: run Chrome without a window
  --debug-output DIR      where to write logs and failure artifacts
  --timeline              also write timeline.html there: each step with its screens
  --record                record the screen from start to finish (recording.mp4 there)
  --annotate[=TIME]       with --record: a video a person can follow (one for a PR): each
                          tap's result stays up TIME after it (default 1s; e.g. 0.5s, 3s; 0
                          for no holds), captions name each step, rings show each touch
                          (drawn with ffmpeg)
  --events-port N         with --annotate: show events sent to 127.0.0.1:N/event (tb mark)
                          in the video (default 7357; 0 for none)
  --json                  print the test result as JSON; a failed flow has failedStep
                          and steps (how many steps the flow has)
`

func cmdReplay(argv []string) int {
	a, err := parseArgs(argv,
		flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"env", "e"}, textFlag},
		flagSpec{[]string{"headless"}, boolFlag}, flagSpec{[]string{"debug-output"}, textFlag},
		flagSpec{[]string{"json"}, boolFlag}, flagSpec{[]string{"timeline"}, boolFlag}, flagSpec{[]string{"record"}, boolFlag},
		flagSpec{[]string{"annotate"}, optTimeFlag}, flagSpec{[]string{"events-port"}, intFlag},
		flagSpec{[]string{"from"}, intFlag},
		flagSpec{[]string{"help", "h"}, boolFlag})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if a.flag("help") {
		fmt.Print(replayUsage)
		return 0
	}
	target := strings.TrimSpace(a.str("device"))
	if len(a.pos) != 1 || len(a.vals["device"]) != 1 || target == "" || strings.ContainsAny(target, ",=") {
		fmt.Fprintln(os.Stderr, "replay requires one flow file and one explicit --device")
		return 2
	}
	f, err := flow.ParseFile(a.pos[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(f.Config.Devices) != 0 {
		fmt.Fprintln(os.Stderr, "replay supports single-device flows")
		return 2
	}
	launch := f.FreshLaunch()
	if launch == nil {
		fmt.Fprintln(os.Stderr, "replay requires a leading launchApp step that is required and restarts the app")
		return 2
	}

	state := "relaunch using existing app data"
	if launch.ClearState {
		state = "clear app data and launch"
	}
	if a.str("from") != "" {
		from, total := a.num("from", 0), len(f.Commands)
		if from < 1 || from > total {
			fmt.Fprintf(os.Stderr, "--from: %s has steps 1 to %d\n", filepath.Base(f.Path), total)
			return 2
		}
		if ownSession(context.Background(), target) {
			return replayInSession(a, f, target, from)
		}
		if f.Config.Web() {
			fmt.Fprintf(os.Stderr, "--from: a browser replay opens a new browser, and no session of yours is open as %q: open one (tb open -p web --name NAME) and pass its name to -d\n", target)
			return 2
		}
		if from > 1 {
			state = fmt.Sprintf("from step %d of %d, on the app as it is", from, total)
		}
	}
	fmt.Fprintf(os.Stderr, "Replay on %s: %s\n", target, state)
	plan := &workspace.Plan{Flows: []*flow.Flow{f}}
	return runTest(argv, a, plan, true)
}

func failedStep(f report.Flow) (int, string) {
	n := len(f.Steps)
	if n == 0 || f.Steps[n-1].Status != "failed" {
		return 0, ""
	}
	return n, f.Steps[n-1].Desc
}

func replayFailure(file, target string, n, total int, desc string, newBrowser bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Replay failed at step %d of %d: %s\n", n, total, desc)
	where := "in a session (tb do -d " + target + " '...')"
	if newBrowser {
		target = "NAME"
		where = "in a session, where the browser stays open (tb open -p web --name NAME; tb replay " + file +
			" -d NAME --from 1 runs the flow there up to that step; tb do -d NAME '...')"
	}
	if n == total {
		fmt.Fprintf(&b, "To finish it, do that step yourself %s: it is the flow's last step.\n", where)
		return b.String()
	}
	fmt.Fprintf(&b, "To finish it, do that step yourself %s, then run the steps that are left:\n  tb replay %s -d %s --from %d\n", where, file, target, n+1)
	return b.String()
}

var ownSession = func(ctx context.Context, target string) bool {
	cl := daemon.Dial()
	if cl == nil {
		return false
	}
	defer cl.Close()
	cl.SetDeadline(time.Now().Add(10 * time.Second))
	args, _ := json.Marshal(map[string]string{"owner": strings.TrimSpace(os.Getenv("TB_SESSION_OWNER"))})
	resp, err := cl.Call(daemon.Request{Cmd: "devices", Args: args}, nil)
	var devs []struct {
		Serial, Name, Session string
		Open, Others          bool
	}
	if err != nil || !resp.OK || json.Unmarshal(resp.JSON, &devs) != nil {
		return false
	}
	for _, d := range devs {
		if d.Open && !d.Others && (d.Session == target || d.Serial == target || strings.EqualFold(d.Name, target)) {
			return true
		}
	}
	return false
}

func replayInSession(a *args, f *flow.Flow, target string, from int) int {
	for _, k := range []string{"record", "timeline", "debug-output"} {
		if a.flag(k) || a.str(k) != "" {
			fmt.Fprintf(os.Stderr, "--from runs in your open session on %s, which takes no --%s: close the session (tb close -d %s) for a run of its own\n", target, k, target)
			return 2
		}
	}
	src, err := os.ReadFile(f.Path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	total := len(f.Commands)
	lines := strings.SplitAfter(string(src), "\n")
	env := map[string]string{}
	for _, kv := range f.Config.Env {
		env[kv.Key] = kv.Value
	}
	for _, kv := range a.vals["env"] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "-e needs KEY=VALUE, got %q\n", kv)
			return 2
		}
		env[k] = v
	}
	app := f.Config.AppID
	if f.Config.Web() {
		app = f.Config.URL
	}
	req := map[string]any{"device": target, "commands": strings.Join(lines[f.Commands[from-1].Line-1:], ""), "app": app, "env": env, "json": true}
	withOwner(req)
	c, err := daemon.Connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()
	dir, _ := filepath.Abs(filepath.Dir(f.Path))
	raw, _ := json.Marshal(req)
	fmt.Fprintf(os.Stderr, "Replay on %s: from step %d of %d, in your open session (it stays open; the app is not restarted)\n", target, from, total)
	start := time.Now()
	resp, err := c.Call(daemon.Request{Cmd: "do", Args: raw, Cwd: dir}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var done struct {
		Error string
		Steps []struct{ Description, Status, Error string }
	}
	if json.Unmarshal(resp.JSON, &done) != nil || resp.Exit == 2 {
		fmt.Fprintln(os.Stderr, strings.TrimRight(resp.Text, "\n"))
		return max(resp.Exit, 1)
	}
	fr := report.Flow{Name: f.Name(), File: f.Path, Device: target, Duration: time.Since(start), Start: start,
		Status: "SUCCESS", Steps: make([]report.Step, from-1)}
	for i := range fr.Steps {
		fr.Steps[i].Status = "skipped"
	}
	for _, s := range done.Steps {
		fr.Steps = append(fr.Steps, report.Step{Desc: s.Description, Status: s.Status, Error: s.Error})
	}
	if resp.Exit != 0 {
		fr.Status, fr.Error = "ERROR", done.Error
	}
	if a.flag("json") {
		fmt.Println(string(testJSON([]report.Flow{fr}, total)))
		return resp.Exit
	}
	fmt.Print(resp.Text)
	if n, desc := failedStep(fr); n > 0 {
		fmt.Print(replayFailure(a.pos[0], target, n, total, desc, false))
	} else if resp.Exit == 0 {
		fmt.Printf("Replay passed: steps %d to %d of %d\n", from, total, total)
	}
	return resp.Exit
}

func pickReplayDevice(devs []device.Info, platform device.Platform, target string) (device.Info, error) {
	matches := 0
	for _, d := range devs {
		if d.Serial == target {
			return d, nil
		}
		if strings.EqualFold(d.Name, target) {
			matches++
		}
	}
	if matches > 1 {
		return device.Info{}, fmt.Errorf("device name %q is ambiguous; use a serial or UDID", target)
	}
	return device.Pick(devs, platform, target)
}
