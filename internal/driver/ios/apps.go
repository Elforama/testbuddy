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

func animationSpeedup() bool { return os.Getenv("TB_IOS_FAST_ANIMATIONS") != "0" }

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

func (d *Driver) gestureTracePath() string {
	return filepath.Join(d.dir, "gesture-trace-"+d.info.Serial+".log")
}

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if err := validID(appID); err != nil {
		return err
	}
	justReset := false
	if o.ClearState {
		var err error
		if justReset, err = d.clearState(ctx, appID, false); err != nil {
			return err
		}
	}
	if o.ClearKeychain {
		if err := d.ClearKeychain(ctx); err != nil {
			return err
		}
	}
	if err := d.setPermissions(ctx, appID, o.Permissions, o.ClearState, justReset); err != nil {
		return err
	}
	args := []string{"launch"}
	if o.StopApp || o.ClearState {
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

const maxLaunchAsks = 3

const launchRetryGap = 500 * time.Millisecond

var axNotAnsweringYet = []struct{ cause, why string }{
	{"kAXErrorIPCTimeout", "an accessibility query timed out"},
	{"kAXErrorServerNotFound", "the app's accessibility server was not up yet"},
	{"kAXErrorCannotComplete", "an accessibility query to the app could not complete"},
}

func (d *Driver) awaitLaunch(ctx context.Context, appID string, pid int, budget, replyTimeout time.Duration) error {
	start, limit := time.Now(), replyTimeout+budget
	asked := start
	err := d.awaitAppReply(ctx, appID, budget, pid, true, replyTimeout)
	last := err
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

func (d *Driver) note(s string) {
	d.mu.Lock()
	d.notes = append(d.notes, s)
	d.mu.Unlock()
}

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
		p["ci"] = true
	}
	m, err := d.call(ctx, "awaitPackage", p, replyTimeout)
	if err != nil {
		return err
	}
	var r struct {
		Found          bool  `json:"found"`
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

func (d *Driver) KillApp(ctx context.Context, appID string) error { return d.StopApp(ctx, appID) }

func (d *Driver) ClearState(ctx context.Context, appID string) error {
	_, err := d.clearState(ctx, appID, true)
	return err
}

func (d *Driver) clearState(ctx context.Context, appID string, resetTCC bool) (reset bool, err error) {
	if err := validID(appID); err != nil {
		return false, err
	}
	if os.Getenv("TB_IOS_FAST_CLEARSTATE") == "1" {
		if err := d.emptyContainers(ctx, appID); err != nil {
			return false, err
		}
	} else if err := d.reinstall(ctx, appID); err != nil {
		return false, err
	}
	var wg sync.WaitGroup
	if resetTCC {
		wg.Add(1)
		go func() { defer wg.Done(); d.resetTCC(ctx, appID) }()
	}
	_, perr := d.simctl(ctx, "privacy", d.info.Serial, "reset", "all", appID)
	wg.Wait()
	d.mu.Lock()
	delete(d.locationSet, appID)
	d.mu.Unlock()
	return perr == nil, nil
}

var stopWait = 10 * time.Second

var reinstallWait = 60 * time.Second

var reinstallNotice = time.Second

var progressOut io.Writer = os.Stderr

func notInstalled(out string) bool {
	return strings.Contains(out, "domain=NSPOSIXErrorDomain, code=2")
}

func (d *Driver) reinstall(ctx context.Context, appID string) error {
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
	_, termErr := d.simctl(ctx, "terminate", d.info.Serial, appID)
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
	if err := ctx.Err(); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	pair, cancel := context.WithTimeout(context.WithoutCancel(ctx), reinstallWait)
	defer cancel()
	d.mu.Lock()
	delete(d.locationSet, appID)
	d.mu.Unlock()
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
		probe, perr := d.simctlOwnSession(pair, "get_app_container", d.info.Serial, appID, "app")
		switch {
		case perr == nil:
			os.RemoveAll(tmp)
			return err
		case !notInstalled(probe):
			return fmt.Errorf("uninstalling %s for clearState failed (its bundle is kept at %s): %w", appID, cp, err)
		}
	}
	_, err := d.simctlOwnSession(pair, "install", d.info.Serial, cp)
	if err != nil {
		_, err = d.simctlOwnSession(pair, "install", d.info.Serial, cp)
	}
	if err != nil {
		return fmt.Errorf("reinstalling %s for clearState failed (its bundle is kept at %s): %w", appID, cp, err)
	}
	os.RemoveAll(tmp)
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (d *Driver) simAppProcs(ctx context.Context) (map[int]string, bool) {
	pat := regexp.QuoteMeta("/" + d.info.Serial + "/data/Containers/Bundle/Application/")
	out, err := exec.CommandContext(ctx, "pgrep", "-lf", pat).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 {
			return nil, true
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

func appPids(procs map[int]string, bundle string) []int {
	var pids []int
	for pid, args := range procs {
		rest, ok := strings.CutPrefix(args, bundle+"/")
		if !ok || rest == "" {
			continue
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 && !strings.Contains(rest[:i], " ") {
			continue
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids
}

func (d *Driver) awaitStopped(ctx context.Context, appID, bundle string, procs map[int]string, listed bool, termErr error) error {
	if listed {
		if pids := appPids(procs, bundle); len(pids) > 0 {
			gone, err := d.waitPids(ctx, appID, pids, pidWait)
			if gone || err != nil {
				return err
			}
		}
		if termErr != nil && strings.Contains(termErr.Error(), "found nothing to terminate") {
			return nil
		}
	}
	return d.waitStopped(ctx, appID, stopWait)
}

var pidWait = 2 * time.Second

func (d *Driver) waitPids(ctx context.Context, appID string, pids []int, timeout time.Duration) (gone bool, err error) {
	deadline := time.Now().Add(timeout)
	again := time.Now().Add(200 * time.Millisecond)
	for {
		running := false
		for _, pid := range pids {
			running = running || pidAlive(pid)
		}
		if !running {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		if time.Now().After(again) {
			d.simctl(ctx, "terminate", d.info.Serial, appID)
			again = time.Now().Add(200 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (d *Driver) waitStopped(ctx context.Context, appID string, timeout time.Duration) error {
	label := "UIKitApplication:" + appID + "["
	deadline := time.Now().Add(timeout)
	for {
		out, err := d.simctl(ctx, "spawn", d.info.Serial, "launchctl", "list")
		if err != nil {
			return nil
		}
		running := false
		for _, line := range strings.Split(out, "\n") {
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
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func cloneTree(src, dst string) error {
	if err := exec.Command("/bin/cp", "-Rc", src, dst).Run(); err == nil {
		return nil
	}
	os.RemoveAll(dst)
	return copyTree(src, dst)
}

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

func (d *Driver) emptyContainers(ctx context.Context, appID string) error {
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

func (d *Driver) LaunchWithXCTest(ctx context.Context, bundleID string, args []string, env map[string]string) error {
	_, err := d.call(ctx, "launch", map[string]any{"bundleId": bundleID, "args": args, "env": env}, 60*time.Second)
	return err
}

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
