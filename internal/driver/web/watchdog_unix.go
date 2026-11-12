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
// The watchdog is Chrome's parent, and one loop both reaps Chrome and kills its group: when tb
// is gone it kills the group before it reaps Chrome, so the group is Chrome's for sure (Chrome's
// pid, and so the group's id, cannot go to another process before it is reaped). When Chrome
// exits on its own, it can only see that by reaping it, and sweeps the helpers left right after,
// as sweepGroup does (issue #124): a group with members left cannot be reused.
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
	pid := cmd.Process.Pid
	// Only this function reaps Chrome (never cmd.Wait): status is set once it has.
	var status syscall.WaitStatus
	reaped := false
	reap := func(block bool) {
		opts := syscall.WNOHANG
		if block {
			opts = 0
		}
		for !reaped {
			wpid, err := syscall.Wait4(pid, &status, opts, nil)
			if err == syscall.EINTR {
				continue
			}
			reaped = wpid == pid || err != nil // ECHILD: nothing left to wait for
			if !block {
				return
			}
		}
	}
	end := func() int { // kill Chrome's group (not reaped yet: Chrome's), reap, clean up
		reap(false)
		if !reaped {
			syscall.Kill(-pid, syscall.SIGKILL)
			cmd.Process.Kill()
			reap(true)
		} else {
			syscall.Kill(-pid, syscall.SIGKILL) // helpers left
		}
		removeProfile(profile)
		return exitCode(status)
	}

	if _, err := fmt.Fprintf(os.Stdout, "%s %d\n", watchdogReady, pid); err != nil {
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
		if reap(false); reaped {
			// Chrome exited on its own (Browser.close, or a crash).
			return end()
		}
	}
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
