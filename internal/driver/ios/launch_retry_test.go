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

const axIPCTimeout = `Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorIPCTimeout"`

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

func TestLaunchAsksAgainAfterALateAnswer(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			r := launchRunner(t)
			var asks atomic.Int32
			r.on("awaitPackage", func(q map[string]any) map[string]any {
				if asks.Add(1) == 1 {
					time.Sleep(700 * time.Millisecond)
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
		{waited, false, true},
		{waited, true, true},
		{"some other wording", true, true},
		{`Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorInvalidUIElement"`, false, false},
		{`Error Domain=com.apple.dt.xctest.automation-support.error Code=8 "Error getting main window kAXErrorAPIDisabled"`, false, false},
		{"launched process com.example.app pid 4242 exited before the first tree", false, false},
		{"kAXErrorServerNotFound", false, false},
	} {
		err := error(&wire.AgentError{Op: "awaitPackage", Msg: c.msg, Transient: c.transient})
		if got := transientLaunchError(err) != ""; got != c.want {
			t.Errorf("transientLaunchError(%q, transient=%v) = %v, want %v", c.msg, c.transient, got, c.want)
		}
	}
}

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

func TestLaunchRetryKeepsToTheLimit(t *testing.T) {
	t.Setenv("TB_CI", "1")
	r := launchRunner(t)
	var asks atomic.Int32
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		if asks.Add(1) == 1 {
			time.Sleep(1200 * time.Millisecond)
		} else {
			time.Sleep(900 * time.Millisecond)
		}
		return map[string]any{"ok": false, "error": axIPCTimeout}
	})
	d := newIOSDriver(t, r)
	t0 := time.Now()
	err := d.awaitLaunch(ctxT(t), app, 4242, time.Second, 1500*time.Millisecond)
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
