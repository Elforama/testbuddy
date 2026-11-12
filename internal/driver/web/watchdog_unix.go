//go:build !windows

package web

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// The browser's watchdog (issue #81). tb does not start Chrome itself: it starts its own
// executable again as a watchdog, which starts Chrome in a process group of its own and waits.
// The watchdog's stdin is a pipe whose other end only tb holds, so it reads end of file the
// moment tb is gone, however it went (SIGKILL included): it then kills Chrome's process group
// and removes the profile. A killed tb could not do either itself.
//
// The watchdog is Chrome's parent and reaps it only once it has killed the group, so the group
// it kills is always Chrome's: its pid, and so its group's id, cannot go to another process
// before then.
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
	// The terminal's signals do not reach it (tb starts it in a group of its own); a signal sent
	// to it alone ends the browser as tb's end would. One ignored when tb started (nohup) stays
	// ignored.
	sigs := make(chan os.Signal, 1)
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) {
			signal.Notify(sigs, s)
		}
	}
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
	fmt.Fprintf(os.Stdout, "%s %d\n", watchdogReady, cmd.Process.Pid)
	os.Stdout.Close()
	tbGone := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(tbGone)
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	var err error
	select {
	case err = <-exited:
		// Chrome exited on its own (Browser.close, or a crash): end the helpers it left, as
		// sweepGroup does (issue #124).
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	case <-tbGone:
		killProcGroup(cmd.Process) // not reaped yet: the group is still Chrome's
		err = <-exited
	case <-sigs:
		killProcGroup(cmd.Process)
		err = <-exited
	}
	removeProfile(profile)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		return 1 // killed
	}
	if err != nil {
		return 1
	}
	return 0
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
