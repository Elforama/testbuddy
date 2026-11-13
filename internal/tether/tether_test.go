//go:build !windows

package tether

// Issue #52: the watchdog (this test binary, started again) runs the process, passes SIGINT on
// and its output through; an executable that does not answer as a watchdog gets the process
// started directly. Killing tb itself is tested in the iOS and Android drivers.

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

const script = `echo hi; trap 'echo bye; exit 3' INT; while :; do /bin/sleep 0.02; done`

func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line := make(chan string, 1)
	go func() { s, _ := r.ReadString('\n'); line <- s }()
	select {
	case s := <-line:
		return strings.TrimSuffix(s, "\n")
	case <-time.After(5 * time.Second):
		t.Fatal("no output")
		return ""
	}
}

func TestTethered(t *testing.T) {
	p, err := Start([]string{"/bin/sh", "-c", script}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Tethered {
		t.Error("not tethered")
	}
	r := bufio.NewReader(p.Out)
	if got := readLine(t, r); got != "hi" {
		t.Fatalf("first line %q", got)
	}
	if err := p.Interrupt(); err != nil {
		t.Fatal(err)
	}
	if got := readLine(t, r); got != "bye" {
		t.Errorf("after the interrupt %q", got)
	}
	if err := p.Wait(); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("wait = %v, want the process's own exit status", err)
	}
}

func TestNotAWatchdog(t *testing.T) {
	old := watchdogPath
	watchdogPath = func() string { return "/bin/echo" } // answers something else
	t.Cleanup(func() { watchdogPath = old })
	p, err := Start([]string{"/bin/sh", "-c", script}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Wait()
	defer p.Kill()
	if p.Tethered {
		t.Error("tethered to an executable that is not a watchdog")
	}
	if got := readLine(t, bufio.NewReader(p.Out)); got != "hi" {
		t.Errorf("first line %q", got)
	}
}
