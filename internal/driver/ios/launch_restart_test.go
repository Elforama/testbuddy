package ios

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// Issue #50: launchApp ran "simctl terminate" and then "simctl launch" straight after. A plain
// launch of an app that is still running only brings it forward and returns the running
// process's pid (seen on an iOS 27 simulator: three launches in a row return the same pid). The
// issue's hypothesis is that a terminate may return before the process is gone; then the launch
// hands back the dying process, and the agent's wait for the first tree fails with "launched
// process … exited before the first tree". That stale process was not observed on a simulator
// (no terminate there returned with the old process alive): it is the fake's assumption here.
// After a terminate the old process (pid 1111) is still up, a plain launch returns it, and the
// agent reports it gone. "simctl launch --terminate-running-process" stops the old process and
// starts a new one in one step, and returns the new pid (5555).
func TestRelaunchGetsTheNewProcess(t *testing.T) {
	for _, o := range []struct {
		name string
		opts driver.LaunchOpts
	}{
		{"stopApp", driver.LaunchOpts{StopApp: true}},
		{"stopApp with arguments", driver.LaunchOpts{StopApp: true, Arguments: map[string]any{"mode": "x"}}},
		// clearState alone (stopApp: false) must stop the app in the launch too: the fast
		// clearState (TB_IOS_FAST_CLEARSTATE=1) terminates without waiting.
		{"clearState without stopApp", driver.LaunchOpts{ClearState: true}},
	} {
		t.Run(o.name, func(t *testing.T) {
			ts := stubTools(t)
			rules := []stubRule{
				{match: "simctl launch --terminate-running-process " + testUDID + " " + app, out: app + ": 5555"},
				{match: "simctl launch " + testUDID + " " + app, out: app + ": 1111"}, // the old process, still running
			}
			if o.opts.ClearState { // clearState reinstalls the app from its bundle
				bundle := filepath.Join(ts.home, "Library/Developer/CoreSimulator/Devices", testUDID, "data/Containers/Bundle/Application/A1/App.app")
				os.MkdirAll(bundle, 0o755)
				rules = append(rules, stubRule{match: app + " app", out: bundle + "\n"})
			}
			ts.set("xcrun", rules)
			r := newFakeRunner(t)
			r.on("awaitPackage", func(q map[string]any) map[string]any {
				if q["pid"] == float64(1111) {
					return map[string]any{"ok": false, "error": "launched process " + app + " pid 1111 exited before the first tree"}
				}
				return launchTree(q)
			})
			d := newIOSDriver(t, r)
			if err := d.LaunchApp(ctxT(t), app, o.opts); err != nil {
				t.Fatalf("launchApp of a running app: %v (simctl calls %q)", err, ts.simctl())
			}
			if q := r.last("awaitPackage"); num(t, q, "pid") != 5555 {
				t.Errorf("waited on pid %v, want the new process 5555", q["pid"])
			}
			calls := ts.simctl()
			if last := calls[len(calls)-1]; !strings.HasPrefix(last, "launch --terminate-running-process ") {
				t.Errorf("last simctl call %q: want the launch to stop the running copy itself", last)
			}
			if o.opts.ClearState {
				return // clearState terminates the app (and waits) before reinstalling it
			}
			for _, c := range calls {
				if strings.HasPrefix(c, "terminate ") {
					t.Errorf("separate %q before the launch: the launch must stop the old process itself", c)
				}
			}
		})
	}
}

// Without stopApp, launchApp brings a running app forward: no terminate, and no
// --terminate-running-process.
func TestLaunchWithoutStopKeepsTheRunningProcess(t *testing.T) {
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242"}})
	d := newIOSDriver(t, launchRunner(t))
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range ts.simctl() {
		if strings.HasPrefix(c, "terminate ") || strings.Contains(c, "--terminate-running-process") {
			t.Errorf("simctl %q: a launch without stopApp must not stop the app", c)
		}
	}
}

// Issue #290: an app brought back without stopApp is listed in front while SpringBoard still
// animates its window in, and a touch then lands where the scaled window is. The agent waits for
// the app to take input before it answers the launch, and says how long: the trace shows it.
func TestLaunchNotesTheWaitForTheAppToTakeInput(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"active", map[string]any{"awaitedInputMs": 180}, "was coming to the foreground: waited 180 ms for it to take input"},
		{"still entering", map[string]any{"awaitedInputMs": 3000, "appActive": false}, "was still coming to the foreground after 3000 ms"},
		{"a short wait (a cold launch held a moment)", map[string]any{"awaitedInputMs": 20}, ""},
		{"no wait", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			ts := stubTools(t)
			ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242"}})
			r := newFakeRunner(t)
			r.on("awaitPackage", func(q map[string]any) map[string]any {
				m := launchTree(q)
				for k, v := range c.extra {
					m[k] = v
				}
				return m
			})
			d := newIOSDriver(t, r)
			if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{}); err != nil {
				t.Fatal(err)
			}
			notes := strings.Join(d.Notes(), "\n")
			if c.want == "" && notes != "" {
				t.Errorf("notes %q, want none", notes)
			}
			if c.want != "" && !strings.Contains(notes, c.want) {
				t.Errorf("notes %q, want %q", notes, c.want)
			}
		})
	}
}
