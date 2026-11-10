package ios

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// axIPCTimeout is the error the agent passed on from XCTest in issue #85's CI run, where the
// accessibility query for the launched app's main window timed out once on a busy machine.
const axIPCTimeout = `Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorIPCTimeout"`

func launchTree(q map[string]any) map[string]any {
	return map[string]any{"found": true, "windows": []any{map[string]any{"id": q["pid"], "type": 1, "pkg": q["pkg"], "active": true}}}
}

func driverNotes(d any) []string {
	if n, ok := d.(driver.Noter); ok {
		return n.Notes()
	}
	return nil
}

// Issue #85: one accessibility IPC timeout while the agent waited for the launched app's
// window failed launchApp ("Unable to launch app dev.testbuddy.bench: awaitPackage: ...
// kAXErrorIPCTimeout"), though the app had launched. The timeout is the simulator's, not the
// app's: launchApp asks again on the same launch, and says so in the trace.
func TestLaunchAsksAgainAfterAXIPCTimeout(t *testing.T) {
	t.Setenv("TB_CI", "0")
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242"}})
	r := launchRunner(t)
	var asks atomic.Int32
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		if asks.Add(1) == 1 {
			return map[string]any{"ok": false, "error": axIPCTimeout}
		}
		return launchTree(q)
	})
	d := newIOSDriver(t, r)
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{}); err != nil {
		t.Fatalf("launchApp failed on a one-off accessibility timeout: %v", err)
	}
	if n := len(r.requests("awaitPackage")); n != 2 {
		t.Fatalf("awaitPackage asked %d times, want 2", n)
	}
	if q := r.last("awaitPackage"); num(t, q, "pid") != 4242 || q["readTree"] != true {
		t.Errorf("second ask %v: want the same launch (pid 4242) and its tree", q)
	}
	launches := 0
	for _, c := range ts.simctl() {
		if strings.HasPrefix(c, "launch ") {
			launches++
		}
	}
	if launches != 1 {
		t.Errorf("simctl launch ran %d times, want 1 (no relaunch for a live app)", launches)
	}
	notes := driverNotes(d)
	if len(notes) != 1 || !strings.Contains(notes[0], "kAXErrorIPCTimeout") || !strings.Contains(notes[0], "asking the same launch again") {
		t.Errorf("trace notes %q: want one line naming the timeout and the retry", notes)
	}
}

// A real launch failure is not retried: the agent's error comes back at once.
func TestLaunchDoesNotRetryARealFailure(t *testing.T) {
	r := launchRunner(t)
	r.on("awaitPackage", func(map[string]any) map[string]any {
		return map[string]any{"ok": false, "error": "launched process com.example.app pid 4242 exited before the first tree"}
	})
	d := newIOSDriver(t, r)
	err := d.awaitLaunch(ctxT(t), app, 4242, 2*time.Second, 7*time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited before the first tree") {
		t.Fatalf("err = %v", err)
	}
	if n := len(r.requests("awaitPackage")); n != 1 {
		t.Errorf("awaitPackage asked %d times, want 1", n)
	}
	if notes := driverNotes(d); len(notes) != 0 {
		t.Errorf("notes %q for a failure that was not retried", notes)
	}
}

// An accessibility timeout that keeps coming back still fails, after a bounded number of asks,
// with every error in the message and every retry in the trace.
func TestLaunchGivesUpOnRepeatedAXIPCTimeouts(t *testing.T) {
	r := launchRunner(t)
	r.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": axIPCTimeout} })
	d := newIOSDriver(t, r)
	t0 := time.Now()
	err := d.awaitLaunch(ctxT(t), app, 4242, 2*time.Second, 7*time.Second)
	if err == nil || strings.Count(err.Error(), "kAXErrorIPCTimeout") != maxLaunchAsks {
		t.Fatalf("err = %v: want each of the %d asks' errors", err, maxLaunchAsks)
	}
	if n := len(r.requests("awaitPackage")); n != maxLaunchAsks {
		t.Errorf("awaitPackage asked %d times, want %d", n, maxLaunchAsks)
	}
	if notes := driverNotes(d); len(notes) != maxLaunchAsks-1 {
		t.Errorf("notes %q: want one per retry", notes)
	}
	if time.Since(t0) > 2*time.Second {
		t.Errorf("gave up after %s", time.Since(t0))
	}
}

// The asks share one limit: the first ask's reply timeout and one more budget, what a late
// first answer already had. An ask after an accessibility timeout gets at most what is left,
// and none when less than a second is left.
func TestLaunchRetryKeepsToTheLimit(t *testing.T) {
	r := launchRunner(t)
	var asks atomic.Int32
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		if asks.Add(1) == 1 {
			time.Sleep(1200 * time.Millisecond) // the timeout came late in the first ask
		} else {
			time.Sleep(900 * time.Millisecond)
		}
		return map[string]any{"ok": false, "error": axIPCTimeout}
	})
	d := newIOSDriver(t, r)
	t0 := time.Now()
	err := d.awaitLaunch(ctxT(t), app, 4242, time.Second, 1500*time.Millisecond) // limit 2.5 s
	if err == nil || !strings.Contains(err.Error(), "gave up after 2 asks") || !strings.Contains(err.Error(), "limit 2.5s") {
		t.Fatalf("err = %v", err)
	}
	if n := len(r.requests("awaitPackage")); n != 2 {
		t.Errorf("awaitPackage asked %d times, want 2 (0.4 s left after the second)", n)
	}
	if got := num(t, r.last("awaitPackage"), "timeout"); got != 1000 {
		t.Errorf("second ask's timeout %v ms, want the budget (1.3 s were left)", got)
	}
	if time.Since(t0) > 2500*time.Millisecond {
		t.Errorf("took %s, past the limit", time.Since(t0))
	}
}
