package ios

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

// axIPCTimeout is the error the agent passed on from XCTest in issue #85's CI run, where the
// accessibility query for the launched app's main window timed out once on a busy machine.
const axIPCTimeout = `Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorIPCTimeout"`

// axServerNotFound is the error from issue #126's CI run: the same XCTest error, with the
// launched app's accessibility server not up yet.
const axServerNotFound = `Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorServerNotFound"`

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
// app's: in CI mode launchApp asks again on the same launch, and says so in the trace.
func TestLaunchAsksAgainAfterAXIPCTimeout(t *testing.T) {
	t.Setenv("TB_CI", "1")
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
	if q := r.last("awaitPackage"); num(t, q, "pid") != 4242 || q["readTree"] != true || q["ci"] != true {
		t.Errorf("second ask %v: want the same launch (pid 4242) and its tree, in CI mode", q)
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

// Issue #126: the launched app's accessibility server was not up yet ("Error getting main
// window kAXErrorServerNotFound", the same XCTest error as #85 with another cause), and
// launchApp failed on the first ask. The app is alive but not answering accessibility yet:
// in CI mode launchApp asks the same launch again, and says so in the trace.
func TestLaunchAsksAgainAfterAXServerNotFound(t *testing.T) {
	t.Setenv("TB_CI", "1")
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242"}})
	r := launchRunner(t)
	var asks atomic.Int32
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		if asks.Add(1) == 1 {
			return map[string]any{"ok": false, "error": axServerNotFound}
		}
		return launchTree(q)
	})
	d := newIOSDriver(t, r)
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{}); err != nil {
		t.Fatalf("launchApp failed while the app's accessibility server was not up yet: %v", err)
	}
	if n := len(r.requests("awaitPackage")); n != 2 {
		t.Fatalf("awaitPackage asked %d times, want 2", n)
	}
	if q := r.last("awaitPackage"); num(t, q, "pid") != 4242 || q["readTree"] != true {
		t.Errorf("second ask %v: want the same launch (pid 4242) and its tree", q)
	}
	notes := driverNotes(d)
	if len(notes) != 1 || !strings.Contains(notes[0], "kAXErrorServerNotFound") || !strings.Contains(notes[0], "asking the same launch again") {
		t.Errorf("trace notes %q: want one line naming the error and the retry", notes)
	}
}

// Outside CI mode, launchApp asks once, as before issues #85 and #126: an app whose
// accessibility has not answered yet fails the launch with the agent's error, and the agent is
// not told to wait on it ("ci" is not sent).
func TestLaunchAsksOnceOutsideCI(t *testing.T) {
	t.Setenv("TB_CI", "0")
	for _, reply := range []map[string]any{
		{"ok": false, "error": axIPCTimeout},
		{"ok": false, "error": axServerNotFound},
		{"ok": false, "error": "launch of com.example.app pid 4242 had no readable tree within 2000 ms", "transient": true},
	} {
		ts := stubTools(t)
		ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242"}})
		r := launchRunner(t)
		r.on("awaitPackage", func(map[string]any) map[string]any { return reply })
		d := newIOSDriver(t, r)
		err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{})
		if err == nil || !strings.Contains(err.Error(), reply["error"].(string)) {
			t.Fatalf("err = %v, want the agent's error %q", err, reply["error"])
		}
		reqs := r.requests("awaitPackage")
		if len(reqs) != 1 {
			t.Fatalf("awaitPackage asked %d times, want 1", len(reqs))
		}
		if _, ok := reqs[0]["ci"]; ok {
			t.Errorf("request %v: \"ci\" sent outside CI mode", reqs[0])
		}
		if notes := driverNotes(d); len(notes) != 0 {
			t.Errorf("notes %q for a launch that was not asked again", notes)
		}
	}
}

// A late answer (XCTest still finishing a slow snapshot) is asked once more, in and out of CI
// mode: that predates CI mode.
func TestLaunchAsksAgainAfterALateAnswer(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			r := launchRunner(t)
			var asks atomic.Int32
			r.on("awaitPackage", func(q map[string]any) map[string]any {
				if asks.Add(1) == 1 {
					time.Sleep(700 * time.Millisecond) // past the first ask's reply timeout
				}
				return launchTree(q)
			})
			d := newIOSDriver(t, r)
			if err := d.awaitLaunch(ctxT(t), app, 4242, 2*time.Second, 300*time.Millisecond); err != nil {
				t.Fatalf("awaitLaunch: %v", err)
			}
			if n := len(r.requests("awaitPackage")); n != 2 {
				t.Errorf("awaitPackage asked %d times, want 2", n)
			}
		})
	}
}

// Issue #126: which agent errors are the simulator's (asked again) and which are the launch's
// own (returned at once). The family is XCTest's "Error getting main window" with an
// accessibility cause that says the app is not answering yet; an error the agent marks
// transient is believed without the text.
func TestTransientLaunchError(t *testing.T) {
	const waited = "launch of com.example.app pid 4242 had no readable tree within 30000 ms: Error getting main window kAXErrorServerNotFound"
	for _, c := range []struct {
		msg       string
		transient bool
		want      bool
	}{
		{axIPCTimeout, false, true},
		{axServerNotFound, false, true},
		{`Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorCannotComplete"`, false, true},
		{waited, false, true}, // the agent's own timeout after waiting on it
		{waited, true, true},
		{"some other wording", true, true},
		// Not the app failing to answer yet: the window is gone, or accessibility is off.
		{`Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorInvalidUIElement"`, false, false},
		{`Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorAPIDisabled"`, false, false},
		{"launched process com.example.app pid 4242 exited before the first tree", false, false},
		{"kAXErrorServerNotFound", false, false}, // a cause outside "Error getting main window"
	} {
		err := error(&wire.AgentError{Op: "awaitPackage", Msg: c.msg, Transient: c.transient})
		if got := transientLaunchError(err) != ""; got != c.want {
			t.Errorf("transientLaunchError(%q, transient=%v) = %v, want %v", c.msg, c.transient, got, c.want)
		}
	}
}

// Issue #126: an agent that marks its error transient ("transient": true in the reply) is
// asked again whatever the error's text.
func TestLaunchAsksAgainWhenTheAgentSaysTransient(t *testing.T) {
	t.Setenv("TB_CI", "1")
	r := launchRunner(t)
	var asks atomic.Int32
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		if asks.Add(1) == 1 {
			return map[string]any{"ok": false, "error": "launch of com.example.app pid 4242 had no readable tree within 2000 ms", "transient": true}
		}
		return launchTree(q)
	})
	d := newIOSDriver(t, r)
	if err := d.awaitLaunch(ctxT(t), app, 4242, 2*time.Second, 7*time.Second); err != nil {
		t.Fatalf("awaitLaunch: %v", err)
	}
	if n := len(r.requests("awaitPackage")); n != 2 {
		t.Errorf("awaitPackage asked %d times, want 2", n)
	}
}

// Issue #126: an error that comes back at once is not asked again at once; the asks start at
// least launchRetryGap apart, so a server that is coming up has a moment to do so.
func TestLaunchRetriesAreSpaced(t *testing.T) {
	t.Setenv("TB_CI", "1")
	r := launchRunner(t)
	var times []time.Time
	var mu sync.Mutex
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		mu.Lock()
		times = append(times, time.Now())
		n := len(times)
		mu.Unlock()
		if n < 3 {
			return map[string]any{"ok": false, "error": axServerNotFound}
		}
		return launchTree(q)
	})
	d := newIOSDriver(t, r)
	if err := d.awaitLaunch(ctxT(t), app, 4242, 2*time.Second, 7*time.Second); err != nil {
		t.Fatalf("awaitLaunch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) != 3 {
		t.Fatalf("awaitPackage asked %d times, want 3", len(times))
	}
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < launchRetryGap-20*time.Millisecond {
			t.Errorf("ask %d came %s after the one before, want at least %s", i+1, gap, launchRetryGap)
		}
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
	t.Setenv("TB_CI", "1")
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
	t.Setenv("TB_CI", "1")
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
