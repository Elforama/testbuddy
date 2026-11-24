package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/lint"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/session"
)

const version = "0.6.3"

const usage = `tb: fast UI tests for Android, iOS and the web (runs Maestro-compatible YAML flows)

Run tests:
  tb test <flows or folders> [options]   run flows (tb test -h for options)
  tb check <flows...> [--lint]           check flow syntax without a device; --lint also
                                         lists fragile steps (tb check -h for options)
  tb mark [-d DEVICE] <text>...         show an event in a running tb test --annotate's
                                         videos (tracking calls, webhooks; tb mark -h)
  tb a11y [paths] [--changed]            check app code for UI tb cannot detect or tap
                                         (tb a11y -h for options; runs in CI too)

Drive a device (for AI agents and people; the device stays connected between calls):
  tb devices                             list devices, and which are free or in use (and by whom)
  tb open [-d DEVICE] [--name alice] [--app ID]
                                         connect to a device, optionally naming it
  tb open -p web --headless --app URL    open a headless Chrome session
  tb look [-d alice] [--shot] [--all] [--json]
                                         what is on screen: one line per element, with @refs,
                                         indented where layout matters (rows, lists),
                                         what scroll areas hide, * on new elements
  tb do [-d alice] [--json] '<command>' ...  run flow commands and report the outcome
  tb do - < steps.yaml                   commands from stdin
  tb wait [-d alice] <text> [--gone] [--timeout ms]
                                         wait (event-driven) until text appears or goes
  tb locate [-d alice] [--json] <selector>
                                         inspect matches and the runner's pick on screen
  tb watch [-d alice] [--until text] [--timeout ms]
                                         stream screen changes as they happen
  tb shot [-d alice] [path] [--scale 0.5]
                                         save a screenshot
  tb save <file.yaml> [-d alice]         save the commands that passed as a flow
  tb save --preview [-d alice] [--json] preview the recorded YAML and replay warnings
  tb replay <file.yaml> -d DEVICE       verify a saved flow from its launchApp step
  tb log | tb clear [-d alice]           show or reset the recorded commands
  tb close [-d alice | all] [-p ios] [--force]  disconnect a device (and stop tb's agent on it)
  tb release [-d DEVICE | all] [-p ios] [--force]  free devices for other test tools: stop tb's agents
                                         (other agents' sessions only with --force)
  tb hierarchy [-d DEVICE] [--json]      the screen as an outline (--json: every raw node)
  tb mcp                                 serve the same as MCP tools over stdio
  tb agent-guide                         portable CLI instructions for coding agents
  tb daemon stop                         stop the background process

-d takes a session name, serial/UDID or device name; TB_DEVICE is its default (not with -p).
TB_SESSION_OWNER (one per agent) makes the sessions you open yours: other agents are refused.
A command queued behind another on its session says so on stderr; --no-wait fails at once instead.
With one device connected, or one session open (except for tb open), -d is optional; with
several, tb asks which.
tb open, close and release also take the device without -d: tb release emulator-5554.
Exit status: 0 ok, 1 a command or flow failed, 2 bad input.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, rest := os.Args[1], os.Args[2:]
	var code int
	switch cmd {
	case "test":
		code = cmdTest(rest)
	case "mark":
		code = cmdMark(rest)
	case "check", "check-syntax":
		code = cmdCheck(rest)
	case "a11y", "accessibility":
		code = cmdA11y(rest)
	case "devices", "list-devices":
		code = cmdDevices(rest)
	case "open", "session":
		if cmd == "session" && len(rest) > 0 {
			switch rest[0] {
			case "start":
				rest = rest[1:]
			case "stop":
				code = agentCall("close", rest[1:], nil)
				os.Exit(code)
			case "save":
				code = cmdSave(rest[1:])
				os.Exit(code)
			case "log":
				code = agentCall("log", rest[1:], nil)
				os.Exit(code)
			}
		}
		code = agentCall("open", rest, []flagSpec{{[]string{"name", "n"}, textFlag}, {[]string{"app", "a"}, textFlag},
			{[]string{"headless"}, boolFlag}})
	case "close":
		code = agentCall("close", rest, []flagSpec{{[]string{"force"}, boolFlag}})
	case "release":
		code = cmdRelease(rest)
	case "look":
		code = agentCall("look", rest, lookFlags)
	case "do", "run":
		code = cmdDo(rest)
	case "wait":
		code = cmdWait(rest)
	case "locate":
		code = agentCall("locate", rest, []flagSpec{{[]string{"json"}, boolFlag}})
	case "watch":
		code = agentCall("watch", rest, watchFlags)
	case "shot", "screenshot":
		code = agentCall("shot", rest, shotFlags)
	case "save":
		code = cmdSave(rest)
	case "replay":
		code = cmdReplay(rest)
	case "log":
		code = agentCall("log", rest, nil)
	case "clear":
		code = agentCall("clear", rest, nil)
	case "hierarchy":
		code = cmdHierarchy(rest)
	case "mcp":
		code = cmdMCP(rest)
	case "daemon":
		code = cmdDaemon(rest)
	case "agent-guide", "guide":
		code = cmdGuide(rest)
	case "version", "--version", "-v":
		fmt.Println("tb", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}

const checkUsage = `tb check: check flows without a device

  tb check <flows or folders> [options]   folders: every .yaml/.yml flow in them

Prints "ok <flow>" for each flow that parses, or its error.

Options:
  --lint            also list fragile steps under each flow (they still run):
                      coordinate_tap, coordinate_swipe   taps and swipes by position
                      indexed_selector                   a selector that relies on match order
                      regex_text                         text read as a regex that does not
                                                         match itself ("Sign in?")
                      optional_assertion                 an assertion that can never fail
                      missing_assertion                  a flow that asserts nothing
                      unknown_header_key                 a header key tb does not read
                                                         ("tgas:"; did you mean tags?)
                      duplicate_key                      a key written twice in one map
  --fail-on LEVEL   error (default), warning (lint warnings fail too), or none
  --json            one JSON object: each flow with its error or warnings

Exit status: 0 ok, 1 a flow does not parse (or has warnings, with --fail-on warning),
2 bad input.
`

type checkResult struct {
	File     string         `json:"file"`
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Notes    []string       `json:"notes,omitempty"`
	Warnings []lint.Warning `json:"warnings"`
}

func cmdCheck(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"lint"}, boolFlag}, flagSpec{[]string{"fail-on"}, textFlag},
		flagSpec{[]string{"json"}, boolFlag}, flagSpec{[]string{"help", "h"}, boolFlag})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n\n%s", err, checkUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Print(checkUsage)
		return 0
	}
	failOn := a.str("fail-on")
	if failOn == "" {
		failOn = "error"
	}
	if failOn != "error" && failOn != "warning" && failOn != "none" {
		fmt.Fprintf(os.Stderr, "--fail-on: want error, warning or none, not %q\n", failOn)
		return 2
	}
	doLint, asJSON := a.flag("lint"), a.flag("json")
	var results []checkResult
	bad, warnings := 0, 0
	paths := checkFiles(a.pos)
	for _, f := range paths {
		r := checkResult{File: f, Warnings: []lint.Warning{}}
		fl, err := flow.ParseFile(f)
		if err != nil {
			r.Error = err.Error()
			bad++
		} else {
			r.OK = true
			r.Notes = fl.Config.Ignored()
			if doLint {
				r.Warnings = append(r.Warnings, lint.Flow(fl)...)
				warnings += len(r.Warnings)
			}
		}
		if asJSON {
			results = append(results, r)
			continue
		}
		if !r.OK {
			fmt.Println(r.Error)
			continue
		}
		fmt.Println("ok", f)
		for _, note := range r.Notes {
			fmt.Println("  note:", note)
		}
		for _, w := range r.Warnings {
			fmt.Printf("  ! %s%s: %s\n", lintWhere(w, fl.Path), w.Code, w.Message)
		}
	}
	files := len(paths)
	if asJSON {
		if results == nil {
			results = []checkResult{}
		}
		b, _ := json.Marshal(struct {
			Files    []checkResult `json:"files"`
			Errors   int           `json:"errors"`
			Warnings int           `json:"warnings"`
		}{results, bad, warnings})
		fmt.Println(string(b))
	} else if doLint && files > 0 {
		if warnings == 0 {
			fmt.Printf("ok: %s checked, no warnings\n", plural(files, "flow"))
		} else {
			fmt.Printf("! %s (%s checked)\n", plural(warnings, "warning"), plural(files, "flow"))
		}
	}
	if (failOn != "none" && bad > 0) || (failOn == "warning" && warnings > 0) {
		return 1
	}
	return 0
}

func lintWhere(w lint.Warning, flowPath string) string {
	if w.Line == 0 {
		return ""
	}
	if w.File != "" && w.File != flowPath {
		return fmt.Sprintf("%s:%d ", w.File, w.Line)
	}
	return fmt.Sprintf(":%d ", w.Line)
}

func checkFiles(argv []string) []string {
	var files []string
	for _, a := range argv {
		if st, err := os.Stat(a); err != nil || !st.IsDir() {
			files = append(files, a)
			continue
		}
		before := len(files)
		err := filepath.WalkDir(a, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			ext, base := filepath.Ext(p), filepath.Base(p)
			if (ext == ".yaml" || ext == ".yml") && strings.TrimSuffix(base, ext) != "config" {
				files = append(files, p)
			}
			return nil
		})
		if err != nil && len(files) == before {
			files = append(files, a)
		}
	}
	return files
}

var listDevices = device.List

func cmdDevices(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"json"}, boolFlag}, flagSpec{[]string{"platform", "p"}, textFlag})
	if err == nil {
		err = a.noPos()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if daemon.Running() {
		return agentCall("devices", argv, []flagSpec{{[]string{"json"}, boolFlag}})
	}
	devs := listDevices(context.Background())
	if a.flag("json") {
		b, _ := json.MarshalIndent(devs, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(devs) == 0 {
		fmt.Println("no devices: start an Android emulator or boot an iOS simulator")
		return 0
	}
	for _, d := range devs {
		fmt.Println(d)
	}
	return 0
}

var (
	lookFlags = []flagSpec{{[]string{"shot"}, boolFlag}, {[]string{"all"}, boolFlag},
		{[]string{"json"}, boolFlag}, {[]string{"scale"}, fractionFlag}, {[]string{"wait"}, boolFlag}}
	watchFlags = []flagSpec{{[]string{"until"}, textFlag}, {[]string{"timeout"}, intFlag}}
	shotFlags  = []flagSpec{{[]string{"scale"}, fractionFlag}}
)

func platformFlag(a *args) (string, error) {
	p := strings.ToLower(a.str("platform"))
	switch device.Platform(p) {
	case "", device.Android, device.IOS, device.Web:
		return p, nil
	}
	return "", fmt.Errorf("-p: want android, ios or web, not %q", a.str("platform"))
}

func agentCall(cmd string, argv []string, extra []flagSpec) int {
	specs := []flagSpec{{[]string{"device", "d"}, textFlag}, {[]string{"platform", "p"}, textFlag}}
	if cmd != "close" && cmd != "devices" && cmd != "log" {
		specs = append(specs, noWaitFlag)
	}
	specs = append(specs, extra...)
	a, err := parseArgs(argv, specs...)
	if err == nil {
		switch cmd {
		case "close":
			err = a.devicePos()
		case "shot":
			if len(a.pos) > 1 {
				err = fmt.Errorf("unexpected argument %q: tb shot takes one path", a.pos[1])
			}
		case "open", "locate":
		default:
			err = a.noPos()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	platform, err := platformFlag(a)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	args := map[string]any{"device": a.str("device"), "platform": platform}
	noWait(a, args)
	for _, s := range extra {
		name := s.names[0]
		if s.kind != boolFlag {
			if v := a.str(name); v != "" {
				switch s.kind {
				case floatFlag, fractionFlag:
					args[name] = a.float(name, 0)
				case intFlag:
					args[name] = a.num(name, 0)
				default:
					args[name] = v
				}
			}
		} else if a.flag(name) {
			args[name] = true
		}
	}
	switch cmd {
	case "locate":
		if len(a.pos) == 0 {
			fmt.Fprintln(os.Stderr, "usage: tb locate <text, @ref, or {selector}> [--json]")
			return 2
		}
		args["selector"] = strings.Join(a.pos, " ")
	case "shot":
		if len(a.pos) > 0 {
			args["path"] = a.pos[0]
		}
	case "open":
		if len(a.pos) > 0 && args["device"] == "" {
			args["device"] = a.pos[0]
		}
	}
	return callDaemon(cmd, args)
}

func callDaemon(cmd string, args map[string]any) int {
	defaultDevice(args)
	withOwner(args)
	c, err := daemon.Connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Close()
	raw, _ := json.Marshal(args)
	cwd, _ := os.Getwd()
	resp, err := c.Call(daemon.Request{Cmd: cmd, Args: raw, Cwd: cwd}, func(part daemon.Response) {
		if part.Stderr {
			fmt.Fprint(os.Stderr, part.Text)
			return
		}
		fmt.Print(part.Text)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if wantJSON, _ := args["json"].(bool); wantJSON && len(resp.JSON) > 0 {
		os.Stdout.Write(resp.JSON)
		fmt.Println()
		return resp.Exit
	}
	if resp.Exit != 0 && !resp.OK {
		fmt.Fprint(os.Stderr, resp.Text)
		if !strings.HasSuffix(resp.Text, "\n") {
			fmt.Fprintln(os.Stderr)
		}
	} else {
		fmt.Print(resp.Text)
	}
	return resp.Exit
}

func cmdDo(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"platform", "p"}, textFlag},
		flagSpec{[]string{"app", "a"}, textFlag}, flagSpec{[]string{"env", "e"}, textFlag}, flagSpec{[]string{"json"}, boolFlag},
		noWaitFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var parts []string
	for _, p := range a.pos {
		if p == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			parts = append(parts, string(b))
			continue
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tb do '<command>' ...   e.g. tb do 'tapOn: Login' 'inputText: hi'")
		return 2
	}
	yaml := toCommandList(parts)
	env := map[string]string{}
	for _, kv := range a.vals["env"] {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	req := map[string]any{"device": a.str("device"), "platform": a.str("platform"),
		"commands": yaml, "app": a.str("app"), "env": env}
	if a.flag("json") {
		req["json"] = true
	}
	noWait(a, req)
	return callDaemon("do", req)
}

func toCommandList(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		p = strings.TrimRight(p, "\n")
		if strings.HasPrefix(strings.TrimSpace(p), "- ") || strings.Contains(p, "\n- ") {
			b.WriteString(p + "\n")
			continue
		}
		lines := strings.Split(p, "\n")
		b.WriteString("- " + lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}

func cmdWait(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"platform", "p"}, textFlag},
		flagSpec{[]string{"gone"}, boolFlag}, flagSpec{[]string{"timeout", "t"}, intFlag}, noWaitFlag)
	if err != nil || len(a.pos) == 0 {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		fmt.Fprintln(os.Stderr, "usage: tb wait <text or @ref or selector> [--gone] [--timeout ms]")
		return 2
	}
	target := strings.Join(a.pos, " ")
	sel := target
	if !strings.HasPrefix(target, "@") && !strings.HasPrefix(target, "{") {
		b, _ := json.Marshal(target)
		sel = string(b)
	}
	key := "visible"
	if a.flag("gone") {
		key = "notVisible"
	}
	timeout := a.num("timeout", 17000)
	yaml := fmt.Sprintf("- extendedWaitUntil:\n    %s: %s\n    timeout: %d\n", key, sel, timeout)
	req := map[string]any{"device": a.str("device"), "platform": a.str("platform"), "commands": yaml}
	noWait(a, req)
	return callDaemon("do", req)
}

func cmdSave(argv []string) int {
	const saveUsage = "usage: tb save <file.yaml> [-d device] [--app id]\n       tb save --preview [file.yaml] [-d device] [--app id] [--json]"
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"app", "a"}, textFlag},
		flagSpec{[]string{"preview"}, boolFlag}, flagSpec{[]string{"json"}, boolFlag}, flagSpec{[]string{"help", "h"}, boolFlag}, noWaitFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, saveUsage)
		return 2
	}
	if a.flag("help") {
		fmt.Println(saveUsage)
		return 0
	}
	preview := a.flag("preview")
	if len(a.pos) > 1 || (!preview && (len(a.pos) != 1 || a.flag("json"))) {
		fmt.Fprintln(os.Stderr, saveUsage)
		return 2
	}
	req := map[string]any{"device": a.str("device"), "app": a.str("app")}
	noWait(a, req)
	if len(a.pos) == 1 {
		req["path"] = a.pos[0]
	}
	if preview {
		req["preview"] = true
		req["json"] = a.flag("json")
	}
	return callDaemon("save", req)
}

func stopSignals() []os.Signal {
	var sigs []os.Signal
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(sig) {
			sigs = append(sigs, sig)
		}
	}
	return sigs
}

func stopContext() (context.Context, context.CancelFunc) {
	sigs := stopSignals()
	if len(sigs) == 0 {
		return context.WithCancel(context.Background())
	}
	return signal.NotifyContext(context.Background(), sigs...)
}

func cmdHierarchy(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"platform", "p"}, textFlag},
		flagSpec{[]string{"json"}, boolFlag})
	if err == nil {
		err = a.noPos()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, cancel := stopContext()
	defer cancel()
	info, err := device.PickOne(listDevices(ctx), device.Platform(a.str("platform")), a.str("device"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	drv, err := openDriver(ctx, info)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer drv.Close()
	s, err := drv.Screen(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !a.flag("json") {
		fmt.Print(look.Build(s, look.NewRefs()).Text())
		return 0
	}
	b, _ := json.MarshalIndent(hierarchyJSON(s), "", "  ")
	fmt.Println(string(b))
	return 0
}

func cmdDaemon(argv []string) int {
	if len(argv) > 0 {
		switch argv[0] {
		case "stop":
			resp, err := daemon.StopDaemon()
			if errors.Is(err, daemon.ErrNotRunning) {
				fmt.Println("tb daemon is not running")
				return 0
			}
			if resp.Text != "" {
				fmt.Println(strings.TrimRight(resp.Text, "\n"))
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			return resp.Exit
		case "status":
			if daemon.Running() {
				fmt.Println("tb daemon is running:", daemon.SocketPath())
			} else {
				fmt.Println("tb daemon is not running")
			}
			return 0
		}
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	srv := daemon.NewServer(30 * time.Minute)
	session.New().Register(srv)
	defer srv.StopOnSignals(os.Interrupt, syscall.SIGTERM)()
	if err := srv.Serve(); err != nil {
		log.Println(err)
		return 1
	}
	return 0
}

func orEnvDevice(d string) string {
	if d == "" {
		return strings.TrimSpace(os.Getenv("TB_DEVICE"))
	}
	return d
}

func defaultDevice(args map[string]any) {
	if p, _ := args["platform"].(string); p != "" {
		return
	}
	if d, ok := args["device"].(string); ok && d == "" {
		args["device"] = orEnvDevice("")
	}
}

func withOwner(args map[string]any) {
	if o := strings.TrimSpace(os.Getenv("TB_SESSION_OWNER")); o != "" && args != nil {
		args["owner"] = o
	}
}

var noWaitFlag = flagSpec{[]string{"no-wait"}, boolFlag}

func noWait(a *args, req map[string]any) {
	if a.flag("no-wait") {
		req["no_wait"] = true
	}
}
