//go:build !windows

package web

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

// withHelper makes the stub browser start a helper process first (Chrome starts its renderers,
// GPU process and crash handler the same way) and returns the file its pid is written to.
// With hang, the stub ignores Browser.close and runs until it is killed.
func withHelper(t *testing.T, f *fakeBrowser, hang bool) string {
	t.Helper()
	rulesPath := filepath.Join(f.dir, "chrome.rules")
	rules, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(f.dir, "helper.pid")
	body := string(rules)
	if hang {
		body = strings.Replace(body, sq(filepath.Join(f.dir, "quit")), sq(filepath.Join(f.dir, "never")), 1)
	}
	helper := "/bin/sleep 60 &\necho $! > " + sq(pidFile) + "\n"
	if err := os.WriteFile(rulesPath, []byte(helper+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return pidFile
}

// helperPID reads the helper's pid, and kills the helper when the test ends.
func helperPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(pidFile)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the stub browser did not start its helper")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// gone reports whether pid has ended (within a moment: an orphan is reaped by init).
func gone(pid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// issue #124 (part 4): Kill ends Chrome's helper processes too, not only its main process: a
// stopping daemon whose Close did not finish in time left them running.
func TestKillEndsHelpers(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	pidFile := withHelper(t, f, false)
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	pid := helperPID(t, pidFile)
	d.Kill()
	if !gone(pid) {
		t.Error("issue #124: a helper process of the browser still runs after Kill")
	}
}

// issue #124 (part 4): a browser that does not exit within 3 s of Browser.close is killed by
// Close with its helper processes.
func TestCloseKillsHelpersOfAHungBrowser(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	pidFile := withHelper(t, f, true)
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	pid := helperPID(t, pidFile)
	d.Close()
	if !gone(pid) {
		t.Error("issue #124: a helper process of a browser that did not close still runs after Close")
	}
}
