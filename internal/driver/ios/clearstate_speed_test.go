//go:build !windows

package ios

// issue #371: launchApp with clearState ran about ten processes one after another (~1 s of a
// ~3.3 s iOS launch). The clear is as thorough as before, with less waiting.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// issue #371: the bundle's path is looked up while the app is terminated, not before it.
func TestClearStateLooksUpBundleWhileTerminating(t *testing.T) {
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	marks := t.TempDir()
	m := func(name string) string { return sq(filepath.Join(marks, name)) }
	// The lookup waits up to 2 s for the terminate to start; a terminate that starts only once
	// the lookup is over says so.
	ts.set("xcrun", []stubRule{
		{match: app + " app", sh: "i=0; while [ ! -e " + m("term") + " ] && [ $i -lt 200 ]; do /bin/sleep 0.01; i=$((i+1)); done; : > " + m("looked") + ";", out: bundle + "\n"},
		{match: "simctl terminate " + testUDID, sh: ": > " + m("term") + "; [ -e " + m("looked") + " ] && : > " + m("after") + ";"},
	})
	start := time.Now()
	if err := (&Driver{info: testInfo()}).ClearState(ctxT(t), app); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(marks, "after")); err == nil {
		t.Errorf("the terminate waited for the bundle lookup (ClearState took %s): simctl %q", time.Since(start).Round(time.Millisecond), ts.simctl())
	}
}

// issue #371: an app that was not running (simctl found nothing to terminate, no process of it
// listed) is not waited on: no look at the simulator's launchd (~230 ms each).
func TestClearStateNotRunningSkipsStopPoll(t *testing.T) {
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	ts.set("xcrun", []stubRule{
		{match: app + " app", out: bundle + "\n"},
		{match: "simctl terminate " + testUDID, code: 3,
			out: "An error was encountered processing the command (domain=NSPOSIXErrorDomain, code=3):\nSimulator device failed to terminate " + app + ".\nfound nothing to terminate"},
	})
	ts.set("pgrep", []stubRule{{match: "-lf", code: 1}}) // no app process on this simulator
	if err := (&Driver{info: testInfo()}).ClearState(ctxT(t), app); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range ts.simctl() {
		order = append(order, strings.Fields(c)[0])
		if strings.Contains(c, "launchctl") {
			t.Errorf("asked launchd whether the app runs: %q", c)
		}
	}
	if got := strings.Join(order[2:], " "); got != "uninstall install privacy" {
		t.Errorf("simctl %v, want it to end uninstall install privacy", order)
	}
}

// issue #371: a running app's process, listed before the terminate, is watched until it is
// gone (not launchd), and the uninstall comes only after; an extension running from the
// bundle is not the app and is not waited on.
func TestClearStateWaitsForAppPid(t *testing.T) {
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	start := func() *exec.Cmd {
		c := exec.Command("/bin/sleep", "30")
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Process.Kill() })
		return c
	}
	main, ext := start(), start()
	reaped := make(chan struct{})
	go func() { main.Wait(); close(reaped) }() // a zombie would still be alive
	go ext.Wait()
	mp, ep := main.Process.Pid, ext.Process.Pid
	ts.set("pgrep", []stubRule{{match: "-lf", out: fmt.Sprintf("%d %s/App -UIAnimationDragCoefficient 0.1 -path /x/y\n%d %s/PlugIns/W.appex/W\n", mp, bundle, ep, bundle)}})
	ts.set("xcrun", []stubRule{
		{match: app + " app", out: bundle + "\n"},
		// The process dies a little after the terminate returns, as a terminate allows.
		{match: "simctl terminate " + testUDID, sh: fmt.Sprintf("( /bin/sleep 0.2; kill %d ) >/dev/null 2>&1 & ", mp)},
		{match: "simctl uninstall " + testUDID, sh: fmt.Sprintf("kill -0 %d 2>/dev/null && { printf 'still running'; exit 9; };", mp)},
	})
	if err := (&Driver{info: testInfo()}).ClearState(ctxT(t), app); err != nil {
		t.Fatalf("%v (simctl %q)", err, ts.simctl())
	}
	// The uninstall checked that the process was gone; the test's own reaper may lag a little.
	select {
	case <-reaped:
	case <-time.After(time.Second):
		t.Error("the app's process was never gone")
	}
	for _, c := range ts.simctl() {
		if strings.Contains(c, "launchctl") {
			t.Errorf("asked launchd whether the app runs: %q", c)
		}
	}
	if syscall.Kill(ep, 0) != nil {
		t.Error("the extension was stopped")
	}
	if got := ts.calls("pgrep"); len(got) != 1 || got[0][0] != "-lf" || !strings.Contains(got[0][1], testUDID) {
		t.Errorf("pgrep %q", got)
	}
}

// issue #371: launchApp with clearState deletes the app's TCC rows in the sqlite3 run that
// writes its permissions (one run, not two), and a `location: unset` right after the privacy
// reset has nothing left to reset.
func TestLaunchAppClearStateOneTCCRun(t *testing.T) {
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	ts.set("xcrun", []stubRule{{match: app + " app", out: bundle + "\n"}})
	d := newIOSDriver(t, launchRunner(t))
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{ClearState: true, Permissions: map[string]string{"all": "unset"}}); err != nil {
		t.Fatal(err)
	}
	sql := ts.sql()
	if len(sql) != 1 {
		t.Fatalf("sqlite3 ran %d times: %q, want once", len(sql), sql)
	}
	if want := "BEGIN;DELETE FROM access WHERE client='" + app + "' AND client_type=0;DELETE FROM access WHERE service="; !strings.HasPrefix(sql[0], want) {
		t.Errorf("SQL %q, want it to start %q", sql[0], want)
	}
	var privacy []string
	for _, c := range ts.simctl() {
		if strings.HasPrefix(c, "privacy ") {
			privacy = append(privacy, c)
		}
	}
	if want := "privacy " + testUDID + " reset all " + app; len(privacy) != 1 || privacy[0] != want {
		t.Errorf("privacy %q, want only %q", privacy, want)
	}

	// Allowed, location is granted after the reset, as before.
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{ClearState: true}); err != nil {
		t.Fatal(err)
	}
	var last []string
	for _, c := range ts.simctl() {
		if strings.HasPrefix(c, "privacy ") {
			last = append(last, strings.Fields(c)[2])
		}
	}
	if got := strings.Join(last[1:], " "); got != "reset grant" {
		t.Errorf("privacy %v, want reset all then the location grant", last)
	}
}

// issue #371 (review): the clear does not leave location remembered as unset. The app can ask
// for location afterwards and the flow allow it: a later `location: unset` must reset it.
// Only the launch that has just reset it skips its own `location: unset`.
func TestClearStateDoesNotCacheLocationUnset(t *testing.T) {
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	ts.set("xcrun", []stubRule{{match: app + " app", out: bundle + "\n"}})
	d := newIOSDriver(t, launchRunner(t))
	resets := func() int {
		n := 0
		for _, c := range ts.simctl() {
			if strings.HasPrefix(c, "privacy "+testUDID+" reset location-always") {
				n++
			}
		}
		return n
	}
	unset := map[string]string{"location": "unset"}
	// launchApp clearState with permissions that do not mention location, then standalone
	// clearState: each time the app then gets location granted in the flow (a tapped Allow).
	for _, clear := range []func() error{
		func() error {
			return d.LaunchApp(ctxT(t), app, driver.LaunchOpts{ClearState: true, Permissions: map[string]string{"camera": "allow"}})
		},
		func() error { return d.ClearState(ctxT(t), app) },
	} {
		if err := clear(); err != nil {
			t.Fatal(err)
		}
		before := resets()
		if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{Permissions: unset}); err != nil {
			t.Fatal(err)
		}
		if resets() != before+1 {
			t.Errorf("a later `location: unset` was skipped (simctl %q)", ts.simctl())
		}
	}
	// The launch that clears skips only its own, redundant reset.
	before := resets()
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{ClearState: true, Permissions: unset}); err != nil {
		t.Fatal(err)
	}
	if resets() != before {
		t.Error("reset location right after the privacy reset")
	}
}

// issue #371 (review): a listed pid that does not go away (a reused pid, another executable at
// the top of the bundle) does not fail the clear: launchd is asked, as before the fix.
func TestClearStatePidStillThereAsksLaunchd(t *testing.T) {
	defer func(v time.Duration) { pidWait = v }(pidWait)
	pidWait = 100 * time.Millisecond
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	other := exec.Command("/bin/sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })
	ts.set("pgrep", []stubRule{{match: "-lf", out: fmt.Sprintf("%d %s/Helper\n", other.Process.Pid, bundle)}})
	ts.set("xcrun", []stubRule{{match: app + " app", out: bundle + "\n"}}) // launchd: not running
	if err := (&Driver{info: testInfo()}).ClearState(ctxT(t), app); err != nil {
		t.Fatalf("%v (simctl %q)", err, ts.simctl())
	}
	asked := false
	for _, c := range ts.simctl() {
		asked = asked || strings.Contains(c, "launchctl list")
	}
	if !asked {
		t.Errorf("launchd not asked: simctl %q", ts.simctl())
	}
}
