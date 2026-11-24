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

func TestCloseEndsHelpersLeftAfterACleanExit(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	pidFile := withHelper(t, f, false)
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	pid := helperPID(t, pidFile)
	d.Close()
	if !gone(pid) {
		t.Error("issue #124: a helper process still runs after the browser exited and Close returned")
	}
}

func TestKillAfterCloseKillsNothing(t *testing.T) {
	newFakeBrowser(t, true, onePage)
	var kills int
	old := killBrowser
	killBrowser = func(d *Driver) { kills++; old(d) }
	defer func() { killBrowser = old }()
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	d.Kill()
	if kills != 0 {
		t.Errorf("issue #124: Kill after the browser exited killed its process group %d times, want 0", kills)
	}
}

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
