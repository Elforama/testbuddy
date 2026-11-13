//go:build !windows

package tether

import (
	"bufio"
	"context"
	"encoding/json"
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
	"time"
)

var (
	// answerWait bounds the wait for the watchdog's answer; one that does not answer in time is
	// ended and the process started directly.
	answerWait = 10 * time.Second
	// onGoneWait bounds each stop command the watchdog runs once tb is gone.
	onGoneWait = 10 * time.Second
	// grace is how long the interrupted process has to finish its file before it is killed.
	grace = 10 * time.Second
)

func init() {
	if len(os.Args) > 3 && os.Args[1] == arg && os.Getenv(env) == proto {
		os.Exit(runWatchdog(os.Args[2], os.Args[3:]))
	}
}

// selfPath is this executable, resolved when tb started: on Linux /proc/self/exe, which is this
// very binary even once the file at its path is replaced or removed; elsewhere its path with
// symbolic links resolved (a Homebrew tb is a link that an upgrade points elsewhere).
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

// watchdogPath is the executable to start as the watchdog, or "" to start processes directly.
// Tests replace it.
var watchdogPath = func() string { return selfPath }

// startWatchdog starts argv under a watchdog. It returns nil and no error when the watchdog
// cannot be started or does not answer: the caller then starts argv directly.
func startWatchdog(argv []string, onGone [][]string) (*Proc, error) {
	self := watchdogPath()
	if self == "" {
		return nil, nil
	}
	spec, err := json.Marshal(onGone)
	if err != nil {
		return nil, err
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, nil
	}
	cmd := exec.Command(self, append([]string{arg, string(spec)}, argv...)...)
	cmd.Env = append(os.Environ(), env+"="+proto)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, outW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, nil
	}
	br := bufio.NewReader(outR)
	line := make(chan string, 1)
	go func() {
		s, _ := br.ReadString('\n')
		line <- s
	}()
	select {
	case s := <-line:
		if strings.TrimSuffix(s, "\n") == ready {
			return &Proc{cmd: cmd, Out: &closeAtEOF{r: br, f: outR}, Tethered: true, keep: inW}, nil
		}
	case <-time.After(answerWait):
	}
	// Not a watchdog of this protocol: end it (its group: it has not been reaped, so the group
	// is its own) and start the process directly.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	cmd.Process.Kill()
	cmd.Wait()
	inW.Close()
	outR.Close()
	return nil, nil
}

// Interrupt sends the process SIGINT (tethered: the watchdog passes it on).
func (p *Proc) Interrupt() error { return p.cmd.Process.Signal(os.Interrupt) }

// Kill ends the process at once (tethered: the watchdog and the process, its group).
func (p *Proc) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Tethered && !p.waited { // reaped, the group's id could be another's
		syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	p.cmd.Process.Kill()
}

// runWatchdog starts argv and waits for it to exit (its exit code is the watchdog's), passing
// SIGINT on to it. When its stdin closes (tb gone), or on SIGTERM or SIGHUP, it runs the onGone
// commands (JSON in spec), interrupts argv and kills it after grace.
func runWatchdog(spec string, argv []string) int {
	// A write to a pipe tb no longer reads (the answer, with tb gone) must fail, not end the
	// watchdog with the process left running.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT) // how tb stops the process: always heard
	for _, s := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) { // one ignored when tb started (nohup) stays ignored
			signal.Notify(sigs, s)
		}
	}
	var onGone [][]string
	if err := json.Unmarshal([]byte(spec), &onGone); err != nil {
		fmt.Fprintln(os.Stderr, "tb watchdog:", err)
		return 2
	}
	var penv []string // not nil: nil would hand the process env too
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, env+"=") {
			penv = append(penv, e)
		}
	}
	// The answer comes first, before the process can say anything on the same pipe.
	if _, err := fmt.Fprintln(os.Stdout, ready); err != nil {
		return 1 // tb is gone already: nothing started
	}
	// The process writes into a pipe of the watchdog's own, which relay copies to tb. It must not
	// write into tb's pipe itself: once tb is gone, its next write would raise SIGPIPE, and simctl
	// says "Recording completed" on SIGINT before it writes the file out, so it would die with
	// the file unfinished (issue #52 review). relay keeps reading after tb is gone and drops the
	// rest.
	outR, outW, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 127
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Env = outW, outW, penv
	err = cmd.Start()
	outW.Close()
	if err != nil {
		outR.Close()
		fmt.Fprintln(os.Stderr, err)
		return 127
	}
	relayed := make(chan struct{})
	go func() {
		relay(os.Stdout, outR)
		close(relayed)
	}()
	exited := make(chan int, 1)
	go func() {
		code := exitCode(cmd.Wait())
		select { // its last words reach tb, unless something it started still holds the pipe
		case <-relayed:
		case <-time.After(time.Second):
		}
		exited <- code
	}()
	tbGone := make(chan struct{})
	go func() {
		io.Copy(io.Discard, os.Stdin)
		close(tbGone)
	}()
	gone := func() int {
		for _, c := range onGone {
			if len(c) == 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), onGoneWait)
			stop := exec.CommandContext(ctx, c[0], c[1:]...)
			stop.Env = penv
			stop.Run()
			cancel()
		}
		cmd.Process.Signal(os.Interrupt)
		select {
		case code := <-exited:
			return code
		case <-time.After(grace):
			cmd.Process.Kill()
			return <-exited
		}
	}
	for {
		select {
		case code := <-exited:
			return code
		case s := <-sigs:
			if s == syscall.SIGINT {
				cmd.Process.Signal(os.Interrupt)
				continue
			}
			return gone()
		case <-tbGone:
			return gone()
		}
	}
}

// relay copies r to w until r ends. After the first failed write (tb gone), it reads on and
// drops the rest, so the writer never meets a closed pipe.
func relay(w io.Writer, r io.Reader) {
	buf := make([]byte, 32*1024)
	ok := true
	for {
		n, err := r.Read(buf)
		if n > 0 && ok {
			if _, werr := w.Write(buf[:n]); werr != nil {
				ok = false
			}
		}
		if err != nil {
			return
		}
	}
}

// exitCode is the watchdog's exit code for the process's end: its own, or 1 when a signal
// ended it.
func exitCode(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return ee.ExitCode()
	}
	return 1
}
