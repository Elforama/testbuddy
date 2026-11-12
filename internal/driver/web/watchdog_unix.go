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

// watchdogEnv names the profile of the browser a watchdog looks after; set, it makes this
// executable a watchdog (the init below) instead of tb.
const watchdogEnv = "TB_WEB_BROWSER_WATCHDOG"

func init() {
	if profile, ok := os.LookupEnv(watchdogEnv); ok {
		os.Exit(runWatchdog(profile, os.Args[1:]))
	}
}

// watchdogPath is the executable to start as the browser's watchdog, or "" to start the
// browser directly.
func watchdogPath() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	return self
}

// runWatchdog starts the browser (argv) and waits for it to exit or for its stdin to close (tb
// gone, or tb's Kill), and returns its exit code.
func runWatchdog(profile string, argv []string) int {
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "tb browser watchdog: no browser to start")
		return 2
	}
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
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, watchdogEnv+"=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.SysProcAttr = ownGroup()
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "tb browser watchdog:", err)
		removeProfile(profile)
		return 127
	}
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

// removeProfile removes the browser's profile: one Open made, never any other folder.
func removeProfile(profile string) {
	if profile != "" && filepath.IsAbs(profile) && strings.HasPrefix(filepath.Base(profile), "tb-chrome-") {
		os.RemoveAll(profile)
	}
}
