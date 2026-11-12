package ios

import (
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// Issue #50: launchApp ran "simctl terminate" and then "simctl launch" straight after.
// Terminate is asynchronous, and a plain launch of an app that is still running only brings it
// forward and returns the running process's pid (seen on an iOS 27 simulator: three launches in
// a row return the same pid). So a launch that comes before the old process is gone hands back
// the dying process, and the agent's wait for the first tree fails with "launched process …
// exited before the first tree". The fake simctl here behaves that way: after a terminate the
// old process (pid 1111) is still up, a plain launch returns it, and the agent reports it gone.
// "simctl launch --terminate-running-process" stops the old process and starts a new one in one
// request, and returns the new pid (5555).
func TestRelaunchGetsTheNewProcess(t *testing.T) {
	for _, o := range []struct {
		name string
		opts driver.LaunchOpts
	}{
		{"stopApp", driver.LaunchOpts{StopApp: true}},
		{"stopApp with arguments", driver.LaunchOpts{StopApp: true, Arguments: map[string]any{"mode": "x"}}},
	} {
		t.Run(o.name, func(t *testing.T) {
			ts := stubTools(t)
			ts.set("xcrun", []stubRule{
				{match: "simctl launch --terminate-running-process " + testUDID + " " + app, out: app + ": 5555"},
				{match: "simctl launch " + testUDID + " " + app, out: app + ": 1111"}, // the old process, still running
			})
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
			for _, c := range ts.simctl() {
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
