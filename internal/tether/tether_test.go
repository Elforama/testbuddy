//go:build !windows

package tether

// Issue #52: the watchdog (this test binary, started again) runs the process, passes SIGINT on
// and its output through; an executable that does not answer as a watchdog gets the process
// started directly. Killing tb itself is tested in the iOS and Android drivers.

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
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

// Issue #52 (device check): once tb is gone, a process the onGone commands told to stop gets
// time to end on its own before it is interrupted. On an emulator, adb shell interrupted right
// after the device was told to stop cut screenrecord off before it wrote its file out ("moov
// atom not found"). The stand-in here finishes 0.3 s after its stop file appears and, interrupted
// before, exits without its file.
func TestGoneLetsOnGoneFinish(t *testing.T) {
	dir := t.TempDir()
	stop, done := filepath.Join(dir, "stop"), filepath.Join(dir, "done")
	proc := `trap 'exit 1' INT; echo hi; while [ ! -e "$1" ]; do /bin/sleep 0.02; done; /bin/sleep 0.3; : > "$2"`
	p, err := Start([]string{"/bin/sh", "-c", proc, "sh", stop, done}, [][]string{{"/usr/bin/touch", stop}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Tethered {
		t.Fatal("not tethered")
	}
	r := bufio.NewReader(p.Out)
	if got := readLine(t, r); got != "hi" {
		t.Fatalf("first line %q", got)
	}
	p.keep.Close() // as tb's end does
	go io.Copy(io.Discard, r)
	waited := make(chan error, 1)
	go func() { waited <- p.Wait() }()
	select {
	case <-waited:
	case <-time.After(15 * time.Second):
		p.Kill()
		t.Fatal("the watchdog did not end the process")
	}
	if _, err := os.Stat(done); err != nil {
		t.Errorf("issue #52: the process was interrupted before it could finish after the stop commands: %v", err)
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
