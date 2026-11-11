package ios

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if err := validID(appID); err != nil {
		return err
	}
	if o.ClearState {
		if err := d.ClearState(ctx, appID); err != nil { // terminates it too
			return err
		}
	} else if o.StopApp {
		d.simctl(ctx, "terminate", d.info.Serial, appID) // not running is fine
	}
	if o.ClearKeychain {
		if err := d.ClearKeychain(ctx); err != nil {
			return err
		}
	}
	if err := d.SetPermissions(ctx, appID, o.Permissions); err != nil {
		return err
	}
	args := []string{"launch", d.info.Serial, appID}
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
// tree: the first ask and up to two more after a transient failure (transientLaunchError).
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
// not up yet) says nothing about the app: the same launch is asked again, without relaunching
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
	m, err := d.call(ctx, "awaitPackage", map[string]any{"pkg": appID, "timeout": timeout.Milliseconds(), "pid": pid, "readTree": readTree}, replyTimeout)
	if err != nil {
		return err
	}
	var r struct {
		Found bool `json:"found"`
	}
	if err := m.Decode(&r); err != nil {
		return err
	}
	if !r.Found {
		return fmt.Errorf("%s did not come to the foreground within %s", appID, timeout)
	}
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
	d.resetTCC(ctx, appID)
	d.simctl(ctx, "privacy", d.info.Serial, "reset", "all", appID)
	d.mu.Lock()
	delete(d.locationSet, appID)
	d.mu.Unlock()
	return nil
}

// stopWait bounds how long ClearState waits for a terminated app to be gone.
var stopWait = 10 * time.Second

// reinstall terminates the app, waits for it to stop, and uninstalls and installs it again
// from a copy of its bundle.
func (d *Driver) reinstall(ctx context.Context, appID string) error {
	bundle, err := d.simctl(ctx, "get_app_container", d.info.Serial, appID, "app")
	if err != nil {
		return fmt.Errorf("app %s is not installed: %w", appID, err)
	}
	bundle = strings.TrimSpace(bundle)
	if !filepath.IsAbs(bundle) || !strings.HasSuffix(bundle, ".app") {
		return fmt.Errorf("app %s: unexpected bundle path %q", appID, bundle)
	}
	d.simctl(ctx, "terminate", d.info.Serial, appID) // not running is fine
	if err := d.waitStopped(ctx, appID, stopWait); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "tb-reinstall-")
	if err != nil {
		return err
	}
	cp := filepath.Join(tmp, filepath.Base(bundle))
	if err := copyTree(bundle, cp); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("copying %s to reinstall it: %w", bundle, err)
	}
	if _, err := d.simctl(ctx, "uninstall", d.info.Serial, appID); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	_, err = d.simctl(ctx, "install", d.info.Serial, cp)
	if err != nil {
		_, err = d.simctl(ctx, "install", d.info.Serial, cp) // once more: CoreSimulator can be busy
	}
	if err != nil {
		// Keep the copy: it is the only one left.
		return fmt.Errorf("reinstalling %s for clearState failed (its bundle is kept at %s): %w", appID, cp, err)
	}
	os.RemoveAll(tmp)
	return nil
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
