package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/daemon"
)

const failsAtThree = `appId: com.example.app
---
- launchApp
- assertVisible: Home
- extendedWaitUntil: {visible: Checkout, timeout: 300}
- tapOn: Next
`

func TestReplayFailureNamesTheStepAndHowToFinish(t *testing.T) {
	withFakePhone(t)
	dir := t.TempDir()
	path := writeFlow(t, dir, "fail.yaml", failsAtThree)
	var code int
	out, errOut := capture(t, func() {
		code = cmdReplay([]string{path, "-d", "emulator-5554", "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 1 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{"Checkout", "screen was:", "failed at step 3 of 4: "} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if last := "tb replay " + path + " -d emulator-5554 --from 4\n"; !strings.HasSuffix(out, last) {
		t.Errorf("the output does not end with %q:\n%s", last, out)
	}
	if !strings.Contains(out, "tb do -d emulator-5554") {
		t.Errorf("the hint does not say where to do the step:\n%s", out)
	}
}

func TestReplayFailureJSONHasTheStepAndTheTotal(t *testing.T) {
	withFakePhone(t)
	dir := t.TempDir()
	path := writeFlow(t, dir, "fail.yaml", failsAtThree)
	out, _ := capture(t, func() {
		cmdReplay([]string{path, "-d", "emulator-5554", "--json", "--debug-output", filepath.Join(dir, "debug")})
	})
	var report struct {
		Flows []struct {
			FailedStep int
			Steps      int
		}
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(report.Flows) != 1 || report.Flows[0].FailedStep != 3 || report.Flows[0].Steps != 4 {
		t.Errorf("want failedStep 3 of steps 4:\n%s", out)
	}
}

func TestReplayFromRunsOnlyTheStepsThatAreLeft(t *testing.T) {
	app := withFakePhone(t)
	app.screen = "details"
	dir := t.TempDir()
	path := writeFlow(t, dir, "pass.yaml", strings.Replace(passingFlow, "- launchApp", "- launchApp: {clearState: true}", 1))
	var code int
	out, errOut := capture(t, func() {
		code = cmdReplay([]string{path, "-d", "emulator-5554", "--from", "4", "--json", "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(app.launched) != 0 {
		t.Errorf("launched %v: a step before 4 ran", app.launched)
	}
	if strings.Contains(errOut, "clear app data") || !strings.Contains(errOut, "from step 4 of 6") {
		t.Errorf("stderr does not say what this run does: %q", errOut)
	}
	if app.screen != "home" {
		t.Errorf("steps 4 to 6 did not run: the app is on %q", app.screen)
	}
}

func TestReplayFromRefusesAStepTheFlowDoesNotHave(t *testing.T) {
	app := withFakePhone(t)
	path := writeFlow(t, t.TempDir(), "pass.yaml", passingFlow)
	for _, n := range []string{"0", "7"} {
		var code int
		_, errOut := capture(t, func() { code = cmdReplay([]string{path, "-d", "emulator-5554", "--from", n}) })
		if code != 2 || !strings.Contains(errOut, "--from: pass.yaml has steps 1 to 6") {
			t.Errorf("--from %s: exit %d, stderr %q", n, code, errOut)
		}
	}
	if len(app.launched) != 0 {
		t.Errorf("launched %v", app.launched)
	}
}

func holdInSession(t *testing.T, target string) {
	old := ownSession
	ownSession = func(ctx context.Context, name string) bool { return name == target }
	t.Cleanup(func() { ownSession = old })
}

func TestReplayFromRunsInTheOpenSession(t *testing.T) {
	app := withFakePhone(t)
	f := startFakeDaemon(t)
	holdInSession(t, "alice")
	dir := t.TempDir()
	path := writeFlow(t, dir, "fail.yaml", "appId: com.example.app\nenv:\n  USER: sam\n---\n- launchApp\n- assertVisible: Home\n- tapOn: Next\n- assertVisible: Checkout\n- tapOn: Back\n")
	done, _ := json.Marshal(map[string]any{"exit": 1, "error": `"Checkout" not found`, "steps": []map[string]any{
		{"description": `Tap on "Next"`, "status": "passed"},
		{"description": `Assert that "Checkout" is visible`, "status": "failed", "error": `"Checkout" not found`}}})
	f.answer("do", daemon.Response{Exit: 1, Text: "ok Tap on \"Next\"  0.1s\nFAILED Assert that \"Checkout\" is visible  0.3s\n", JSON: done})
	var code int
	out, errOut := capture(t, func() { code = cmdReplay([]string{path, "-d", "alice", "--from", "3", "-e", "PASS=x"}) })
	if code != 1 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	cmd, args, cwd := f.last(t)
	if cmd != "do" || args["device"] != "alice" || args["app"] != "com.example.app" || cwd != dir ||
		args["commands"] != "- tapOn: Next\n- assertVisible: Checkout\n- tapOn: Back\n" {
		t.Errorf("asked %s %v in %s", cmd, args, cwd)
	}
	if env, _ := args["env"].(map[string]any); env["USER"] != "sam" || env["PASS"] != "x" {
		t.Errorf("env %v", args["env"])
	}
	if len(app.launched) != 0 {
		t.Errorf("launched %v: the run did not stay in the session", app.launched)
	}
	if !strings.Contains(out, "FAILED Assert that") || !strings.Contains(out, `failed at step 4 of 5: Assert that "Checkout" is visible`) ||
		!strings.HasSuffix(out, "tb replay "+path+" -d alice --from 5\n") {
		t.Errorf("stdout:\n%s", out)
	}

	out, _ = capture(t, func() { code = cmdReplay([]string{path, "-d", "alice", "--from", "3", "--json"}) })
	var report struct {
		Failed int
		Flows  []struct {
			Status, Error, Device string
			FailedStep, Steps     int
		}
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if code != 1 || report.Failed != 1 || len(report.Flows) != 1 || report.Flows[0].FailedStep != 4 || report.Flows[0].Steps != 5 ||
		report.Flows[0].Status != "ERROR" || !strings.Contains(report.Flows[0].Error, "Checkout") || report.Flows[0].Device != "alice" {
		t.Errorf("exit %d, json:\n%s", code, out)
	}
}

func TestReplayFromOnTheWebNeedsAnOpenSession(t *testing.T) {
	withFakePhone(t)
	path := writeFlow(t, t.TempDir(), "web.yaml", "url: http://localhost:7360\n---\n- launchApp\n- assertVisible: Home\n")
	var code int
	_, errOut := capture(t, func() { code = cmdReplay([]string{path, "-d", "web", "--from", "2"}) })
	if code != 2 || !strings.Contains(errOut, "--from: a browser replay opens a new browser") || !strings.Contains(errOut, "tb open -p web") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}
