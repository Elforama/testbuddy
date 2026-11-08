package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdReplayRunsOnce(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		exit       int
	}{{"pass", passingFlow, 0}, {"failure", failingFlow, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			app := withFakePhone(t)
			dir := t.TempDir()
			path := writeFlow(t, dir, tc.name+".yaml", tc.body)
			var code int
			out, stderr := capture(t, func() {
				code = cmdReplay([]string{path, "-d", app.serial, "--json", "--debug-output", filepath.Join(dir, "debug")})
			})
			if code != tc.exit || len(app.launched) != 1 || !strings.Contains(stderr, "relaunch using existing app data") {
				t.Fatalf("exit %d, launches %v, stdout %q, stderr %q", code, app.launched, out, stderr)
			}
			var report struct {
				Passed int
				Failed int
				Flows  []struct {
					Error    string
					DebugDir string
				}
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatal(err)
			}
			if report.Passed != 1-tc.exit || report.Failed != tc.exit || len(report.Flows) != 1 {
				t.Fatalf("unexpected report %+v", report)
			}
			if tc.exit == 1 {
				f := report.Flows[0]
				if !strings.Contains(f.Error, "Checkout") {
					t.Fatalf("unexpected failure: %q", f.Error)
				}
				if _, err := os.Stat(filepath.Join(f.DebugDir, "screen.txt")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCmdReplayRequiresAnExplicitDeviceAndFreshLaunch(t *testing.T) {
	dir := t.TempDir()
	plain := writeFlow(t, dir, "plain.yaml", "appId: com.example.app\n---\n- tapOn: Next\n")
	noRestart := writeFlow(t, dir, "no-restart.yaml", strings.Replace(passingFlow, "- launchApp", "- launchApp: {stopApp: false}", 1))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{plain}, "one explicit --device"},
		{[]string{plain, "-d", "a,b"}, "one explicit --device"},
		{[]string{plain, "-d", "a", "--retries", "1"}, "unknown option"},
		{[]string{plain, "-d", "a"}, "requires a leading launchApp"},
		{[]string{noRestart, "-d", "a"}, "requires a leading launchApp"},
	} {
		var code int
		_, stderr := capture(t, func() { code = cmdReplay(tc.args) })
		if code != 2 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: exit %d, stderr %q, want %q", tc.args, code, stderr, tc.want)
		}
	}
}
