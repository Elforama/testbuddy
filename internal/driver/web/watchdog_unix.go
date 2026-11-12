//go:build !windows

package web

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// The browser's watchdog (issue #81). tb does not start Chrome itself: it starts its own
// executable again as a watchdog, which starts Chrome in a process group of its own and waits.
// The watchdog's stdin is a pipe whose other end only tb holds, so it reads end of file the
// moment tb is gone, however it went (SIGKILL included): it then kills Chrome's process group
// and removes the profile. A killed tb could not do either itself.
//
// The watchdog is Chrome's parent, and only its chromeChild reaps Chrome. When tb is gone (or a
// signal, or its answer failed) it kills Chrome's group without reaping first, then reaps:
// until it reaps, Chrome, even exited (a zombie), keeps its pid and so its group's id, and the
// group killed is Chrome's for sure. When Chrome exits on its own, the watchdog can only see that
// by reaping it, and sweeps the helpers left right after, as sweepGroup does (issue #124): a
// group with members left cannot be reused.
//
// The watchdog runs as `<tb> watchdogArg <profile> <chrome> <args...>` with watchdogEnv set to
// watchdogProto; it answers watchdogReady on its stdout once Chrome has started. Anything else
// (another tb version at the path, which runs as the CLI) gives no answer, and tb starts Chrome
// directly instead.

func init() {
	if len(os.Args) > 3 && os.Args[1] == watchdogArg && os.Getenv(watchdogEnv) == watchdogProto {
		os.Exit(runWatchdog(os.Args[2], os.Args[3:]))
	}
}

// selfPath is this executable, resolved when tb started: on Linux /proc/self/exe, which is
// this very binary even once the file at its path is replaced or removed; elsewhere its path
// with symbolic links resolved (a Homebrew tb is a link that an upgrade points elsewhere, while
// the old version's own folder goes: starting it then fails and tb starts Chrome directly).
var selfPath = func() string {
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/proc/self/exe"); err == nil {
			return "/proc/self/exe"
		}
	}
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(self); err == nil {
		return p
	}
	return self
}()

// watchdogPath is the executable to start as the browser's watchdog, or "" to start the
// browser directly. Tests replace it.
var watchdogPath = func() string { return selfPath }

// runWatchdog starts the browser (argv) and waits for it to exit or for its stdin to close (tb
// gone, or tb's Kill), and returns its exit code.
func runWatchdog(profile string, argv []string) int {
	// A write to a pipe tb no longer reads (its answer, with tb gone) must fail, not end the
	// watchdog with Chrome left running (issue #81 second review). Notify, not Ignore: an
	// ignored signal would stay ignored in Chrome.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	// The terminal's signals do not reach it (tb starts it in a group of its own); a signal sent
	// to it alone ends the browser as tb's end would. One ignored when tb started (nohup) stays
	// ignored.
	sigs := make(chan os.Signal, 1)
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) {
			signal.Notify(sigs, s)
		}
	}
	children := make(chan os.Signal, 1)
	signal.Notify(children, syscall.SIGCHLD)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stderr = os.Stderr // tb reads Chrome's stderr; its stdout goes nowhere, as before
	cmd.Env = []string{}   // not nil: nil would hand Chrome watchdogEnv too
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, watchdogEnv+"=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.SysProcAttr = ownGroup()
	if err := cmd.Start(); err != nil {
		// No answer: tb starts Chrome itself, and reports the error. The profile is tb's.
		fmt.Fprintln(os.Stderr, "tb browser watchdog:", err)
		return 127
	}
	c := newChromeChild(cmd.Process.Pid)
	end := func() int { // tb gone, a signal, or the answer failed
		c.end()
		removeProfile(profile)
		return exitCode(c.status)
	}

	if _, err := fmt.Fprintf(os.Stdout, "%s %d\n", watchdogReady, c.pid); err != nil {
		return end() // tb is gone already
	}
	os.Stdout.Close()
	tbGone := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(tbGone)
	}()
	tick := time.NewTicker(200 * time.Millisecond) // in case a SIGCHLD is merged with another
	defer tick.Stop()
	for {
		select {
		case <-children:
		case <-tick.C:
		case <-tbGone:
			return end()
		case <-sigs:
			return end()
		}
		if c.exitedOnItsOwn() { // Browser.close, or a crash
			removeProfile(profile)
			return exitCode(c.status)
		}
	}
}

// chromeChild is the watchdog's Chrome: only its methods reap it (never cmd.Wait), so they
// know whether its pid, and its group's id, are still Chrome's. wait4 and kill are
// syscall.Wait4 and syscall.Kill; tests replace them.
type chromeChild struct {
	pid    int
	reaped bool
	status syscall.WaitStatus // set once reaped
	wait4  func(pid int, status *syscall.WaitStatus, options int) (int, error)
	kill   func(pid int, sig syscall.Signal) error
}

func newChromeChild(pid int) *chromeChild {
	return &chromeChild{pid: pid, kill: syscall.Kill,
		wait4: func(pid int, st *syscall.WaitStatus, opts int) (int, error) { return syscall.Wait4(pid, st, opts, nil) }}
}

// reap reaps Chrome if it has exited (block: once it has).
func (c *chromeChild) reap(block bool) {
	opts := syscall.WNOHANG
	if block {
		opts = 0
	}
	for !c.reaped {
		wpid, err := c.wait4(c.pid, &c.status, opts)
		if err == syscall.EINTR {
			continue
		}
		c.reaped = wpid == c.pid || err != nil // ECHILD: nothing left to wait for
		if !block {
			return
		}
	}
}

// end ends Chrome for good: it kills Chrome's group and Chrome before it reaps Chrome. Exited
// or not, an unreaped Chrome (a zombie included) keeps its pid and its group's id, so the group
// killed is Chrome's (issue #81 final check: reaping first could free them before the kill).
func (c *chromeChild) end() {
	if c.reaped {
		return // seen exiting on its own: exitedOnItsOwn swept its group
	}
	c.kill(-c.pid, syscall.SIGKILL)
	c.kill(c.pid, syscall.SIGKILL)
	c.reap(true)
}

// exitedOnItsOwn reports whether Chrome has exited, reaping it, and then kills the helpers left
// in its group, as sweepGroup does (issue #124): only reaping shows that it exited, so this
// sweep comes after the reap, when a group with members left still cannot be reused.
func (c *chromeChild) exitedOnItsOwn() bool {
	if c.reap(false); !c.reaped {
		return false
	}
	c.kill(-c.pid, syscall.SIGKILL)
	return true
}

// exitCode is the watchdog's exit code for Chrome's status: Chrome's own, or 1 when a signal
// ended it.
func exitCode(status syscall.WaitStatus) int {
	if status.Exited() {
		return status.ExitStatus()
	}
	return 1
}

// removeProfile removes the browser's profile: a tb-chrome-* folder right in the temporary
// folder, as Open makes, never any other folder.
func removeProfile(profile string) {
	if profile == "" || !filepath.IsAbs(profile) || !strings.HasPrefix(filepath.Base(profile), "tb-chrome-") ||
		filepath.Dir(filepath.Clean(profile)) != filepath.Clean(os.TempDir()) {
		return
	}
	os.RemoveAll(profile)
}
