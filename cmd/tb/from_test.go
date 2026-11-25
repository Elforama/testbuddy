package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/workspace"
)

func TestTestFromStartsAtTheStepOnTheScreenAsItIs(t *testing.T) {
	app := withFakePhone(t)
	app.screen = "details"
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", `appId: com.example.app
onFlowStart:
  - launchApp
---
- launchApp
- assertVisible: Home
- tapOn: Next
- assertVisible: Details
- tapOn: {id: back}
- assertVisible: Home
`)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--from", "4", "--json", "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(app.launched) != 0 {
		t.Errorf("launched %v: onFlowStart or a step before 4 ran", app.launched)
	}
}

func TestTestFromSaysTheStepToStartFrom(t *testing.T) {
	withFakePhone(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "fail.yaml", `appId: com.example.app
---
- launchApp
- tapOn: Next
- extendedWaitUntil: {visible: Checkout, timeout: 300}
- assertVisible: Details
`)
	run := func(args ...string) string {
		var code int
		out, errOut := capture(t, func() {
			code = cmdTest(append([]string{flow, "--debug-output", t.TempDir()}, args...))
		})
		if code != 1 {
			t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
		}
		return out + errOut
	}
	if got := run(); !strings.Contains(got, "failed at step 3: to run this flow from there, pass --from 3") {
		t.Errorf("a full run does not name step 3:\n%s", got)
	}
	if got := run("--from", "3"); !strings.Contains(got, "failed at step 3: to run this flow from there, pass --from 3") {
		t.Errorf("a run from step 3 does not name step 3:\n%s", got)
	}
}

func TestTestFromRefuses(t *testing.T) {
	dir := t.TempDir()
	one := writeFlow(t, dir, "one.yaml", "appId: a\n---\n- launchApp\n- back\n")
	two := writeFlow(t, dir, "two.yaml", "appId: a\n---\n- launchApp\n")
	web := writeFlow(t, dir, "web.yaml", "url: https://example.com\n---\n- tapOn: Go\n")
	multi := writeFlow(t, dir, "multi.yaml", "devices:\n  alice: {platform: android, appId: a}\n  bob: {platform: android, appId: a}\n---\n- alice:\n    - launchApp\n")
	cases := []struct {
		name  string
		paths []string
		n     int
		want  string
	}{
		{"step 0", []string{one}, 0, "one.yaml has steps 1 to 2"},
		{"past the end", []string{one}, 3, "one.yaml has steps 1 to 2"},
		{"two flows", []string{one, two}, 1, "name one flow, not 2"},
		{"several devices", []string{multi}, 1, "not for a flow of several devices"},
		{"web", []string{web}, 1, "not for a web flow: each run opens a new browser"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := workspace.Build(c.paths, workspace.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := startFrom(plan, c.n); err == nil || err.Error() != c.want {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
	var code int
	_, errOut := capture(t, func() { code = cmdTest([]string{one, "--from", "5"}) })
	if code != 2 || !strings.Contains(errOut, "--from: one.yaml has steps 1 to 2") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestFlowEndNamesNoStepWhenNoStepFailed(t *testing.T) {
	con, out := plainConsole(false, false)
	con.fromHint = true
	r := result("Login", engine.Failed, errors.New("onFlowComplete failed"), time.Second)
	r.Steps = []engine.StepResult{{Status: engine.Passed}}
	con.flowEnd(r, "/d")
	if strings.Contains(out.String(), "--from") {
		t.Errorf("names a step though every step passed:\n%s", out.String())
	}
}

func TestFlowEndNamesNoStepOnReplay(t *testing.T) {
	con, out := plainConsole(false, false)
	r := result("Login", engine.Failed, errors.New("not found"), time.Second)
	r.Steps = []engine.StepResult{{Status: engine.Passed}, {Status: engine.Failed}}
	con.flowEnd(r, "/d")
	if strings.Contains(out.String(), "--from") {
		t.Errorf("tb replay has no --from, but the hint names it:\n%s", out.String())
	}
	con.fromHint = true
	con.flowEnd(r, "/d")
	if !strings.Contains(out.String(), "failed at step 2: to run this flow from there, pass --from 2") {
		t.Errorf("tb test does not name step 2:\n%s", out.String())
	}
}
