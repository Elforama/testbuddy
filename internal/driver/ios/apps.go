package ios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

// launchEnv is simctl launch's environment: the probe injected and UIKit's animation speed
// preference enabled (SIMCTL_CHILD_X reaches the app as X), appended to any values the
// caller's environment already sets.
func (d *Driver) launchEnv() []string {
	add := map[string]string{}
	if d.probe != "" {
		add["SIMCTL_CHILD_DYLD_INSERT_LIBRARIES"] = d.probe
	}
	if animationSpeedup() {
		add["SIMCTL_CHILD_FEATUREFLAGS_ENABLED"] = "UIKit/UIPreferences"
	}
	if d.probe != "" && gestureTrace() {
		add["SIMCTL_CHILD_TB_GESTURE_TRACE"] = d.gestureTracePath()
	}
	env := os.Environ()
	for i, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if a, ok := add[k]; ok {
			if v == "" {
				env[i] = k + "=" + a
			} else if !strings.Contains(v, a) {
				env[i] = k + "=" + v + ":" + a
			}
			delete(add, k)
		}
	}
	keys := make([]string, 0, len(add))
	for k := range add {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+add[k])
	}
	return env
}

// animationSpeedup: UIKit animations 10x faster (-UIAnimationDragCoefficient 0.1), as the
// simulator's "Slow Animations" setting does the opposite. TB_IOS_FAST_ANIMATIONS=0 turns it off.
func animationSpeedup() bool { return os.Getenv("TB_IOS_FAST_ANIMATIONS") != "0" }

// gestureTrace: whether the probe traces what the app makes of each gesture (issue #337;
// TBGestureTrace.h) into gestureTracePath. TB_IOS_GESTURE_TRACE=1 or 0 decides; unset, it is on
// in CI mode, where the e2e job keeps the file. Off, the probe installs none of it.
func gestureTrace() bool {
	if v, ok := os.LookupEnv("TB_IOS_GESTURE_TRACE"); ok && strings.TrimSpace(v) != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	return ci.On()
}

// gestureTracePath: the trace's file, beside the runner's log (logPath). The app appends to it
// (a simulator app writes the Mac's files), one block per gesture.
func (d *Driver) gestureTracePath() string {
	return filepath.Join(d.dir, "gesture-trace-"+d.info.Serial+".log")
}

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if err := validID(appID); err != nil {
		return err
	}
	if o.ClearState {
		// Issue #371: the app's TCC rows are deleted in the same sqlite3 run that writes the
		// flow's permissions (setPermissions below), after the privacy reset.
		if err := d.clearState(ctx, appID, false); err != nil { // terminates it too
			return err
		}
	}
	if o.ClearKeychain {
		if err := d.ClearKeychain(ctx); err != nil {
			return err
		}
	}
	if err := d.setPermissions(ctx, appID, o.Permissions, o.ClearState); err != nil {
		return err
	}
	args := []string{"launch"}
	if o.StopApp || o.ClearState {
		// Issue #50: a terminate may return before the process is gone (waitStopped assumes
		// so), and a plain launch of an app that is still running brings it forward and returns
		// its pid, so a launch right after a terminate could hand back the dying process.
		// --terminate-running-process makes the stop and the start one step and returns the new
		// pid (the old process is gone when it returns); an app that is not running just launches.
		args = append(args, "--terminate-running-process")
	}
	args = append(args, d.info.Serial, appID)
	user := map[string]bool{}
	keys := make([]string, 0, len(o.Arguments))
	for k := range o.Arguments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Launch arguments go in as NSUserDefaults' argument domain: -key value.
		name := strings.TrimLeft(k, "-")
		user[name] = true
		args = append(args, "-"+name, argString(o.Arguments[k]))
	}
	if animationSpeedup() && !user["UIAnimationDragCoefficient"] {
		args = append(args, "-UIAnimationDragCoefficient", "0.1")
	}
	d.ready.Store(nil)
	output, err := d.simctlEnv(ctx, d.launchEnv(), args...)
	if err != nil {
		return err
	}
	pid := 0
	for _, line := range strings.Split(output, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && name == appID {
			pid, _ = strconv.Atoi(strings.TrimSpace(value))
			break
		}
	}
	if pid <= 0 {
		return fmt.Errorf("simctl launch did not return a PID for %s: %q", appID, output)
	}
	budget := 30 * time.Second
	if ci.On() {
		budget = ciFirstReadTimeout
	}
	return d.awaitLaunch(ctx, appID, pid, budget, budget+5*time.Second)
}

// maxLaunchAsks bounds how many times launchApp asks the agent for the launched app's first
// tree in CI mode: the first ask and up to two more after a transient failure
// (transientLaunchError).
const maxLaunchAsks = 3

// launchRetryGap is the least time from the start of one ask of a launch to the next.
const launchRetryGap = 500 * time.Millisecond

// axNotAnsweringYet are the accessibility causes in XCTest's "Error getting main window"
// (automation-support error 8) that mean the launched app has not answered yet, with what
// each says for the trace. The agent waits on the same list (agents/ios LaunchWait.swift);
// other causes (an invalid element, accessibility turned off) are the launch's own failure.
var axNotAnsweringYet = []struct{ cause, why string }{
	{"kAXErrorIPCTimeout", "an accessibility query timed out"},                  // issue #85
	{"kAXErrorServerNotFound", "the app's accessibility server was not up yet"}, // issue #126
	{"kAXErrorCannotComplete", "an accessibility query to the app could not complete"},
}

// awaitLaunch waits for the launched process's first readable tree. A late answer (XCTest
// still finishing a slow snapshot) or an app whose accessibility has not answered yet (issue
// #85: the query timed out on a busy machine; issue #126: the app's accessibility server was
// not up yet; in CI mode only) says nothing about the app: the same launch is asked again, without relaunching
// or clearing the app, and the agent checks on each ask that the launched process is still
// alive. Every other error is the launch's own and returns at once. All asks share one limit:
// the first ask's reply timeout and one more budget (what a late first answer has always had);
// each retry is noted for the trace. A retry starts no sooner than launchRetryGap after the
// failed ask began: an error that comes back at once is not asked again at once.
func (d *Driver) awaitLaunch(ctx context.Context, appID string, pid int, budget, replyTimeout time.Duration) error {
	start, limit := time.Now(), replyTimeout+budget
	asked := start
	err := d.awaitAppReply(ctx, appID, budget, pid, true, replyTimeout)
	last := err // the latest ask's error; err holds every ask's
	// A CI allowance (package ci): an app whose accessibility has not answered yet (issues #85
	// and #126, on busy runners) is waited on by the agent and asked again here only in CI.
	// Elsewhere only a late answer is asked again, once, as before those issues.
	if !ci.On() {
		var late *wire.ReplyTimeout
		if err == nil || !errors.As(err, &late) || ctx.Err() != nil {
			return err
		}
		d.note(fmt.Sprintf("ios: launch of %s (pid %d): the agent answered late (%v); asking the same launch again, up to %s", appID, pid, err, budget))
		if next := d.awaitAppReply(ctx, appID, budget, pid, true, budget+5*time.Second); next != nil {
			return fmt.Errorf("%w; checking the same launch again: %w", err, next)
		}
		return nil
	}
	for ask := 1; err != nil; ask++ {
		why := transientLaunchError(last)
		if why == "" || ctx.Err() != nil {
			return err
		}
		left := (limit - time.Since(start)).Truncate(time.Millisecond)
		if ask >= maxLaunchAsks || left < time.Second {
			return fmt.Errorf("%w (gave up after %d asks in %s, limit %s)", err, ask, time.Since(start).Round(time.Millisecond), limit)
		}
		if gap := launchRetryGap - time.Since(asked); gap > 0 {
			left -= gap
			if left < time.Second {
				return fmt.Errorf("%w (gave up after %d asks in %s, limit %s)", err, ask, time.Since(start).Round(time.Millisecond), limit)
			}
			select {
			case <-ctx.Done():
				return err
			case <-time.After(gap):
			}
		}
		wait := min(budget, left)
		d.note(fmt.Sprintf("ios: launch of %s (pid %d): %s (%v); asking the same launch again, up to %s", appID, pid, why, err, wait))
		asked = time.Now()
		next := d.awaitAppReply(ctx, appID, wait, pid, true, wait+5*time.Second)
		if next == nil {
			return nil
		}
		err, last = fmt.Errorf("%w; checking the same launch again: %w", err, next), next
		if transientLaunchError(next) == "" {
			return err
		}
	}
	return nil
}

// transientLaunchError says why err, from asking for a launched app's first tree, is the
// simulator's and not the app's ("" when it is not): the agent answered late, the agent marked
// its error transient, or (for an agent that does not mark it) the error is XCTest's "Error
// getting main window" with a cause in axNotAnsweringYet. Only these are retried.
func transientLaunchError(err error) string {
	var late *wire.ReplyTimeout
	if errors.As(err, &late) {
		return "the agent answered late"
	}
	var agent *wire.AgentError
	if !errors.As(err, &agent) {
		return ""
	}
	if strings.Contains(agent.Msg, "Error getting main window") {
		for _, c := range axNotAnsweringYet {
			if strings.Contains(agent.Msg, c.cause) {
				return c.why
			}
		}
	}
	if agent.Transient {
		return "the agent says the app is not answering yet"
	}
	return ""
}

// note keeps a line for the run's trace (driver.Noter).
func (d *Driver) note(s string) {
	d.mu.Lock()
	d.notes = append(d.notes, s)
	d.mu.Unlock()
}

// noteInputWait traces the agent's wait for an app coming to the foreground to take input
// (issue #290), from a reply's awaitedInputMs and appActive: a wait of 50 ms or more, and
// always one that ended with the app still on its way in (it went on anyway, at the agent's
// 3 s cap or the op's deadline: never silent).
func (d *Driver) noteInputWait(who string, waitedMs *int, active *bool) {
	if waitedMs == nil {
		return
	}
	switch {
	case active != nil && !*active:
		d.note(fmt.Sprintf("ios: %s was still coming to the foreground after %d ms; going on anyway", who, *waitedMs))
	case *waitedMs >= 50:
		d.note(fmt.Sprintf("ios: %s was coming to the foreground: waited %d ms for it to take input", who, *waitedMs))
	}
}

// Notes hands out what the driver did on its own since the last call (driver.Noter): the
// retries of a launch's first tree.
func (d *Driver) Notes() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.notes
	d.notes = nil
	return n
}

func argString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func (d *Driver) WaitForApp(ctx context.Context, appID string, timeout time.Duration) error {
	return d.awaitApp(ctx, appID, timeout, 0, false)
}

func (d *Driver) awaitApp(ctx context.Context, appID string, timeout time.Duration, pid int, readTree bool) error {
	return d.awaitAppReply(ctx, appID, timeout, pid, readTree, timeout+5*time.Second)
}

func (d *Driver) awaitAppReply(ctx context.Context, appID string, timeout time.Duration, pid int, readTree bool, replyTimeout time.Duration) error {
	p := map[string]any{"pkg": appID, "timeout": timeout.Milliseconds(), "pid": pid, "readTree": readTree}
	if readTree && ci.On() {
		// The agent cannot read the host's environment: in CI mode it waits on an app whose
		// accessibility has not answered yet (issues #85, #126) instead of failing at once.
		p["ci"] = true
	}
	m, err := d.call(ctx, "awaitPackage", p, replyTimeout)
	if err != nil {
		return err
	}
	var r struct {
		Found bool `json:"found"`
		// Issue #290: the app was still coming to the foreground (SpringBoard animating its
		// window in) and the agent waited this long for it to take input; appActive false: it
		// had not by the agent's cap.
		AwaitedInputMs *int  `json:"awaitedInputMs"`
		AppActive      *bool `json:"appActive"`
	}
	if err := m.Decode(&r); err != nil {
		return err
	}
	if !r.Found {
		return fmt.Errorf("%s did not come to the foreground within %s", appID, timeout)
	}
	d.noteInputWait(appID, r.AwaitedInputMs, r.AppActive)
	if readTree {
		s, err := d.decodeScreen(m)
		if err != nil {
			return err
		}
		valid := false
		for _, w := range s.Windows {
			if w.Type == "app" && w.Package == appID && (pid == 0 || w.ID == pid) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("launch of %s (pid %d) returned a tree for a different process", appID, pid)
		}
		d.ready.Store(s)
	}
	return nil
}

func (d *Driver) StopApp(ctx context.Context, appID string) error {
	if err := validID(appID); err != nil {
		return err
	}
	if _, err := d.simctl(ctx, "terminate", d.info.Serial, appID); err != nil && !strings.Contains(err.Error(), "found nothing to terminate") {
		return err
	}
	return nil
}

// KillApp: iOS has no "killed in the background" short of terminating the process.
func (d *Driver) KillApp(ctx context.Context, appID string) error { return d.StopApp(ctx, appID) }

// ClearState resets an app to just installed: it stops the app, waits until it has really
// stopped, and reinstalls it from a copy of its own bundle, then resets its privacy decisions.
// Emptying the data container in place (Maestro's old way) leaves UserDefaults cached in
// cfprefsd and the notification authorization behind (Maestro #1601, #1306), so it is only
// done when TB_IOS_FAST_CLEARSTATE=1 (~0.1 s instead of a couple of seconds).
func (d *Driver) ClearState(ctx context.Context, appID string) error {
	return d.clearState(ctx, appID, true)
}

// clearState is ClearState; with resetTCC false the caller deletes the app's TCC rows itself
// (launchApp, in the sqlite3 run that writes its permissions: issue #371).
func (d *Driver) clearState(ctx context.Context, appID string, resetTCC bool) error {
	if err := validID(appID); err != nil {
		return err
	}
	if os.Getenv("TB_IOS_FAST_CLEARSTATE") == "1" {
		if err := d.emptyContainers(ctx, appID); err != nil {
			return err
		}
	} else if err := d.reinstall(ctx, appID); err != nil {
		return err
	}
	// The two resets touch the same rows and both only delete: they run at once (issue #371).
	var wg sync.WaitGroup
	if resetTCC {
		wg.Add(1)
		go func() { defer wg.Done(); d.resetTCC(ctx, appID) }()
	}
	_, perr := d.simctl(ctx, "privacy", d.info.Serial, "reset", "all", appID)
	wg.Wait()
	d.mu.Lock()
	if perr == nil {
		// The reset took the location grant with it (locationd drops the app's authorization):
		// a flow's `location: unset` has nothing left to do.
		if d.locationSet == nil {
			d.locationSet = map[string]string{}
		}
		d.locationSet[appID] = "unset"
	} else {
		delete(d.locationSet, appID)
	}
	d.mu.Unlock()
	return nil
}

// stopWait bounds how long ClearState waits for a terminated app to be gone.
var stopWait = 10 * time.Second

// reinstallWait bounds clearState's uninstall-and-install, which a cancelled run does not stop.
// The pair took 0.35 s idle and up to 1.4 s with every core busy and the disk written to (the
// bench app, padded to 1 GB or not: the install clones the bundle); cutting it short would leave
// the app uninstalled, so the bound is generous.
var reinstallWait = 60 * time.Second

// reinstallNotice is how long a cancelled run waits for the reinstall before saying so on
// progressOut.
var reinstallNotice = time.Second

var progressOut io.Writer = os.Stderr

// notInstalled tells simctl's "no such app" answer (get_app_container: NSPOSIXErrorDomain
// code 2) from other failures.
func notInstalled(out string) bool {
	return strings.Contains(out, "domain=NSPOSIXErrorDomain, code=2")
}

// reinstall terminates the app, waits for it to stop, and uninstalls and installs it again
// from a copy of its bundle.
func (d *Driver) reinstall(ctx context.Context, appID string) error {
	// Issue #371: the bundle is looked up and copied while the app is being stopped (each simctl
	// call is ~100 ms). Before the terminate, the simulator's app processes are listed, so the
	// wait for the app to be gone watches its pid instead of asking the simulator's launchd
	// (~230 ms a look), and is skipped when the app was not running at all.
	var (
		wg              sync.WaitGroup
		bundle, tmp, cp string
		lookErr, cpErr  error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		bundle, lookErr = d.simctl(ctx, "get_app_container", d.info.Serial, appID, "app")
		if lookErr != nil {
			lookErr = fmt.Errorf("app %s is not installed: %w", appID, lookErr)
			return
		}
		bundle = strings.TrimSpace(bundle)
		if !filepath.IsAbs(bundle) || !strings.HasSuffix(bundle, ".app") {
			lookErr = fmt.Errorf("app %s: unexpected bundle path %q", appID, bundle)
			return
		}
		if tmp, cpErr = os.MkdirTemp("", "tb-reinstall-"); cpErr != nil {
			return
		}
		cp = filepath.Join(tmp, filepath.Base(bundle))
		if err := cloneTree(bundle, cp); err != nil {
			cpErr = fmt.Errorf("copying %s to reinstall it: %w", bundle, err)
		}
	}()
	procs, listed := d.simAppProcs(ctx)
	_, termErr := d.simctl(ctx, "terminate", d.info.Serial, appID) // not running is fine
	wg.Wait()
	if lookErr != nil {
		return lookErr
	}
	if cpErr != nil {
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		return cpErr
	}
	if err := d.awaitStopped(ctx, appID, bundle, procs, listed, termErr); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := ctx.Err(); err != nil { // cancelled before the uninstall: nothing changed
		os.RemoveAll(tmp)
		return err
	}
	// From the uninstall on, the app is gone until the install is done, so the pair runs to its
	// end even when the run is cancelled (Ctrl-C, a timeout): out of ctx, in a session of its own
	// (a Ctrl-C at the terminal reaches tb's whole process group), bounded by reinstallWait.
	// Cancelling it used to leave the app uninstalled for every later flow (issue #49).
	pair, cancel := context.WithTimeout(context.WithoutCancel(ctx), reinstallWait)
	defer cancel()
	// The uninstall takes the app's location grant with it, cancelled run or not: forget it now.
	d.mu.Lock()
	delete(d.locationSet, appID)
	d.mu.Unlock()
	// A cancelled run waits for the pair: say so if that takes a while.
	done := make(chan struct{})
	var notice sync.WaitGroup
	notice.Add(1)
	go func() {
		defer notice.Done()
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		select {
		case <-done:
		case <-time.After(reinstallNotice):
			msg := fmt.Sprintf("ios: finishing clearState's reinstall of %s on %s before stopping (up to %s)", appID, d.info.Name, reinstallWait)
			fmt.Fprintln(progressOut, "tb: "+msg)
			d.note(msg)
		}
	}()
	defer func() { close(done); notice.Wait() }()
	if _, err := d.simctlOwnSession(pair, "uninstall", d.info.Serial, appID); err != nil {
		// simctl can fail once CoreSimulator has removed the app: then it is put back. Only a
		// probe that says so counts.
		probe, perr := d.simctlOwnSession(pair, "get_app_container", d.info.Serial, appID, "app")
		switch {
		case perr == nil: // still installed, as before
			os.RemoveAll(tmp)
			return err
		case !notInstalled(probe):
			// Cannot tell (a failing probe, or the bound over: the probe then fails at once,
			// and an install would too). Keep the copy: it may be the only one left.
			return fmt.Errorf("uninstalling %s for clearState failed (its bundle is kept at %s): %w", appID, cp, err)
		}
	}
	_, err := d.simctlOwnSession(pair, "install", d.info.Serial, cp)
	if err != nil {
		_, err = d.simctlOwnSession(pair, "install", d.info.Serial, cp) // once more: CoreSimulator can be busy
	}
	if err != nil {
		// Keep the copy: it is the only one left.
		return fmt.Errorf("reinstalling %s for clearState failed (its bundle is kept at %s): %w", appID, cp, err)
	}
	os.RemoveAll(tmp)
	if err := ctx.Err(); err != nil { // reinstalled, but the run was cancelled meanwhile
		return err
	}
	return nil
}

// simAppProcs lists the processes this simulator runs from an app bundle, pid → command line
// (pgrep -lf: one host process, ~40 ms; a simulator's app is a process of the Mac's). ok false:
// it cannot tell (no pgrep, a failure).
func (d *Driver) simAppProcs(ctx context.Context) (map[int]string, bool) {
	pat := regexp.QuoteMeta("/" + d.info.Serial + "/data/Containers/Bundle/Application/")
	out, err := exec.CommandContext(ctx, "pgrep", "-lf", pat).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 {
			return nil, true // none
		}
		return nil, false
	}
	procs := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		pid, args, ok := strings.Cut(strings.TrimSpace(line), " ")
		n, err := strconv.Atoi(pid)
		if !ok || err != nil || n <= 0 {
			continue
		}
		procs[n] = args
	}
	return procs, true
}

// appPids picks the app's own process out of simAppProcs: the one whose executable is at the
// top of its bundle (its extensions run from PlugIns/ or Extensions/ and are not terminated
// with it). The command line is the executable's path and then the launch arguments.
func appPids(procs map[int]string, bundle string) []int {
	var pids []int
	for pid, args := range procs {
		rest, ok := strings.CutPrefix(args, bundle+"/")
		if !ok || rest == "" {
			continue
		}
		// A '/' before the first space is in the executable's path: a subfolder of the bundle.
		if i := strings.IndexByte(rest, '/'); i >= 0 && !strings.Contains(rest[:i], " ") {
			continue
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

// awaitStopped waits after the terminate until the app is gone. With the simulator's app
// processes listed before it (issue #371): the app's pids are watched until they are gone, and
// an app that was not running (none listed, and simctl found nothing to terminate) is not
// waited on. Anything else (no list, or a terminate that stopped an app the list did not
// show) asks the simulator's launchd, as before.
func (d *Driver) awaitStopped(ctx context.Context, appID, bundle string, procs map[int]string, listed bool, termErr error) error {
	if listed {
		if pids := appPids(procs, bundle); len(pids) > 0 {
			return d.waitPids(ctx, appID, pids, stopWait)
		}
		if termErr != nil && strings.Contains(termErr.Error(), "found nothing to terminate") {
			return nil
		}
	}
	return d.waitStopped(ctx, appID, stopWait)
}

// waitPids waits until none of pids is alive, terminating the app again now and then, as
// waitStopped does.
func (d *Driver) waitPids(ctx context.Context, appID string, pids []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	again := time.Now().Add(200 * time.Millisecond)
	for {
		running := false
		for _, pid := range pids {
			running = running || pidAlive(pid)
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s was still running %s after it was terminated", appID, timeout)
		}
		if time.Now().After(again) {
			d.simctl(ctx, "terminate", d.info.Serial, appID)
			again = time.Now().Add(200 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// waitStopped polls launchd until the app's process is gone (a terminate is asynchronous).
func (d *Driver) waitStopped(ctx context.Context, appID string, timeout time.Duration) error {
	label := "UIKitApplication:" + appID + "["
	deadline := time.Now().Add(timeout)
	for {
		out, err := d.simctl(ctx, "spawn", d.info.Serial, "launchctl", "list")
		if err != nil {
			return nil // cannot tell: go on, as before
		}
		running := false
		for _, line := range strings.Split(out, "\n") {
			// "PID\tStatus\tLabel"; a "-" PID is a registered job that is not running.
			f := strings.Fields(line)
			if len(f) >= 3 && strings.HasPrefix(f[2], label) && f[0] != "-" {
				running = true
				break
			}
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s was still running %s after it was terminated", appID, timeout)
		}
		d.simctl(ctx, "terminate", d.info.Serial, appID)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond): // waiting on another process
		}
	}
}

// cloneTree copies a directory tree with clonefile (cp -c: copy-on-write, no bytes copied, on
// APFS), so a large bundle costs no more than a small one (issue #371). Where cloning fails (another
// file system), copyTree copies it byte by byte.
func cloneTree(src, dst string) error {
	if err := exec.Command("/bin/cp", "-Rc", src, dst).Run(); err == nil {
		return nil
	}
	os.RemoveAll(dst)
	return copyTree(src, dst)
}

// copyTree copies a directory tree (files, modes, symlinks as links).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, to)
		case fi.IsDir():
			return os.MkdirAll(to, fi.Mode().Perm()|0o700)
		case fi.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm()|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil
	})
}

// emptyContainers empties the app's data container and its app group containers in place
// (TB_IOS_FAST_CLEARSTATE=1).
func (d *Driver) emptyContainers(ctx context.Context, appID string) error {
	// The app's folders are looked up while it terminates (each simctl call is ~90 ms).
	var wg sync.WaitGroup
	var groups string
	var groupsErr error
	wg.Add(2)
	go func() { defer wg.Done(); d.simctl(ctx, "terminate", d.info.Serial, appID) }()
	go func() {
		defer wg.Done()
		groups, groupsErr = d.simctl(ctx, "get_app_container", d.info.Serial, appID, "groups")
	}()
	data, err := d.simctl(ctx, "get_app_container", d.info.Serial, appID, "data")
	wg.Wait()
	if err != nil {
		return fmt.Errorf("app %s is not installed: %w", appID, err)
	}
	if err := emptyDir(data); err != nil {
		return err
	}
	for _, sub := range []string{"Documents", "Library", "Library/Caches", "Library/Preferences", "tmp"} {
		os.MkdirAll(filepath.Join(data, sub), 0o755)
	}
	if groupsErr == nil {
		for _, line := range strings.Split(groups, "\n") {
			if _, path, ok := strings.Cut(line, "\t"); ok && strings.Contains(path, "/Shared/AppGroup/") {
				emptyDir(strings.TrimSpace(path))
			}
		}
	}
	return nil
}

func emptyDir(dir string) error {
	if dir == "" || !filepath.IsAbs(dir) || !strings.Contains(dir, "/CoreSimulator/Devices/") {
		return fmt.Errorf("refusing to empty %q", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) ClearKeychain(ctx context.Context) error {
	_, err := d.simctl(ctx, "keychain", d.info.Serial, "reset")
	return err
}

func (d *Driver) OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error {
	_, err := d.simctl(ctx, "openurl", d.info.Serial, link)
	return err
}

// LaunchWithXCTest launches (or relaunches) an app through XCTest instead of simctl: no
// probe, XCTest's own launch wait.
func (d *Driver) LaunchWithXCTest(ctx context.Context, bundleID string, args []string, env map[string]string) error {
	_, err := d.call(ctx, "launch", map[string]any{"bundleId": bundleID, "args": args, "env": env}, 60*time.Second)
	return err
}

// AppState is XCUIApplication.State (1 not running, 2 suspended, 3 background, 4 foreground).
func (d *Driver) AppState(ctx context.Context, bundleID string) (int, error) {
	m, err := d.call(ctx, "appState", map[string]any{"bundleId": bundleID}, 10*time.Second)
	if err != nil {
		return 0, err
	}
	var r struct {
		State int `json:"state"`
	}
	m.Decode(&r)
	return r.State, nil
}
