//go:build !windows

package web

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/elforama/testbuddy/internal/tether"
)

func init() {
	if len(os.Args) > 3 && os.Args[1] == watchdogArg && os.Getenv(watchdogEnv) == watchdogProto {
		os.Exit(runWatchdog(os.Args[2], os.Args[3:]))
	}
}

var watchdogPath = func() string { return tether.SelfPath }

func runWatchdog(profile string, argv []string) int {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	sigs := make(chan os.Signal, 1)
	for _, s := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) {
			signal.Notify(sigs, s)
		}
	}
	children := make(chan os.Signal, 1)
	signal.Notify(children, syscall.SIGCHLD)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stderr = os.Stderr
	cmd.Env = []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, watchdogEnv+"=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.SysProcAttr = ownGroup()
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "tb browser watchdog:", err)
		return 127
	}
	c := newChromeChild(cmd.Process.Pid)
	end := func() int {
		c.end()
		removeProfile(profile)
		return exitCode(c.status)
	}

	if _, err := fmt.Fprintf(os.Stdout, "%s %d\n", watchdogReady, c.pid); err != nil {
		return end()
	}
	os.Stdout.Close()
	tbGone := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(tbGone)
	}()
	tick := time.NewTicker(200 * time.Millisecond)
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
		if c.exitedOnItsOwn() {
			removeProfile(profile)
			return exitCode(c.status)
		}
	}
}

type chromeChild struct {
	pid    int
	reaped bool
	status syscall.WaitStatus
	wait4  func(pid int, status *syscall.WaitStatus, options int) (int, error)
	kill   func(pid int, sig syscall.Signal) error
}

func newChromeChild(pid int) *chromeChild {
	return &chromeChild{pid: pid, kill: syscall.Kill,
		wait4: func(pid int, st *syscall.WaitStatus, opts int) (int, error) { return syscall.Wait4(pid, st, opts, nil) }}
}

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
		c.reaped = wpid == c.pid || err != nil
		if !block {
			return
		}
	}
}

func (c *chromeChild) end() {
	if c.reaped {
		return
	}
	c.kill(-c.pid, syscall.SIGKILL)
	c.kill(c.pid, syscall.SIGKILL)
	c.reap(true)
}

func (c *chromeChild) exitedOnItsOwn() bool {
	if c.reap(false); !c.reaped {
		return false
	}
	c.kill(-c.pid, syscall.SIGKILL)
	return true
}

func exitCode(status syscall.WaitStatus) int {
	if status.Exited() {
		return status.ExitStatus()
	}
	return 1
}

func removeProfile(profile string) {
	if profile == "" || !filepath.IsAbs(profile) || !strings.HasPrefix(filepath.Base(profile), "tb-chrome-") ||
		filepath.Dir(filepath.Clean(profile)) != filepath.Clean(os.TempDir()) {
		return
	}
	os.RemoveAll(profile)
}
