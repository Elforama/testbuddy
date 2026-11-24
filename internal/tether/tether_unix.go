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
	"strings"
	"syscall"
	"time"
)

var (
	answerWait = 10 * time.Second
	onGoneWait = 10 * time.Second
	grace      = 10 * time.Second
)

func init() {
	if len(os.Args) > 3 && os.Args[1] == arg && os.Getenv(env) == proto {
		os.Exit(runWatchdog(os.Args[2], os.Args[3:]))
	}
}

var watchdogPath = func() string { return SelfPath }

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
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	cmd.Process.Kill()
	cmd.Wait()
	inW.Close()
	outR.Close()
	return nil, nil
}

func (p *Proc) Interrupt() error { return p.cmd.Process.Signal(os.Interrupt) }

func (p *Proc) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Tethered && !p.waited {
		syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	p.cmd.Process.Kill()
}

func runWatchdog(spec string, argv []string) int {
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT)
	for _, s := range []os.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		if !signal.Ignored(s) {
			signal.Notify(sigs, s)
		}
	}
	var onGone [][]string
	if err := json.Unmarshal([]byte(spec), &onGone); err != nil {
		fmt.Fprintln(os.Stderr, "tb watchdog:", err)
		return 2
	}
	var penv []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, env+"=") {
			penv = append(penv, e)
		}
	}
	if _, err := fmt.Fprintln(os.Stdout, ready); err != nil {
		return 1
	}
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
		select {
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
		if len(onGone) > 0 {
			select {
			case code := <-exited:
				return code
			case <-time.After(grace):
			}
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
