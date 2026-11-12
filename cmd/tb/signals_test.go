//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// hangingApp is a fakeApp whose screen reads wait until their context ends (or the test does).
type hangingApp struct {
	*fakeApp
	release chan struct{}
}

func (h *hangingApp) Screen(ctx context.Context) (*ui.Screen, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.release:
		return h.fakeApp.Screen(ctx)
	}
}

// withHangingPhone is withFakePhone with screen reads that hang.
func withHangingPhone(t *testing.T) *hangingApp {
	t.Helper()
	h := &hangingApp{fakeApp: withFakePhone(t), release: make(chan struct{})}
	restore := connect.Override(device.Android, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		return h, nil
	})
	t.Cleanup(restore)
	return h
}

// catchSignals keeps the test binary alive when the test signals itself (a command that does
// not handle the signal would otherwise end it).
func catchSignals(t *testing.T) {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	t.Cleanup(func() { signal.Stop(ch) })
}

// runSignalled runs fn, sends sig to this process once fn is under way, and checks that fn
// returns soon after and that the device was closed: a web device's Chrome runs in a process
// group of its own (issue #124), so the terminal's Ctrl-C or hang-up no longer reaches it and
// tb must close it.
func runSignalled(t *testing.T, h *hangingApp, sig syscall.Signal, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		capture(t, fn)
	}()
	time.Sleep(300 * time.Millisecond) // the command is waiting on the screen
	syscall.Kill(os.Getpid(), sig)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Errorf("issue #124: the command did not stop on %v", sig)
		close(h.release)
		<-done
		return
	}
	h.get(func() {
		if !h.closed {
			t.Errorf("issue #124: the device was not closed after %v", sig)
		}
	})
}

func (f *fakeApp) get(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

// issue #124 review: tb hierarchy closes its device on Ctrl-C, SIGTERM and a closed terminal.
func TestHierarchyClosesOnSignals(t *testing.T) {
	catchSignals(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			h := withHangingPhone(t)
			runSignalled(t, h, sig, func() { cmdHierarchy(nil) })
		})
	}
}

// issue #124 review: tb test closes its devices when the terminal closes (SIGHUP), as it does
// on Ctrl-C and SIGTERM.
func TestTestRunClosesOnHangUp(t *testing.T) {
	catchSignals(t)
	// The cancelled flow's failure files are gathered with a time limit of their own; the
	// hanging screen reads use all of it.
	old := failureArtifactTimeout
	failureArtifactTimeout = 300 * time.Millisecond
	t.Cleanup(func() { failureArtifactTimeout = old })
	h := withHangingPhone(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	runSignalled(t, h, syscall.SIGHUP, func() {
		cmdTest([]string{flow, "--debug-output", filepath.Join(dir, "debug")})
	})
}
