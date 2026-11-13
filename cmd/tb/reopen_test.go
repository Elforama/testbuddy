package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// dyingApp is a fakeApp whose connection dies for good during its first launch: the launch
// fails, and every read after it fails as a dead driver's do (driver.LostChecker says why).
type dyingApp struct {
	*fakeApp
	mu   sync.Mutex
	dead error
}

func (a *dyingApp) LaunchApp(ctx context.Context, id string, o driver.LaunchOpts) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dead = errors.New("browser connection closed: EOF")
	return errors.New("lost the connection to the device agent")
}

func (a *dyingApp) Lost() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dead
}

func (a *dyingApp) Screenshot(ctx context.Context, s float64, fm string) ([]byte, error) {
	if err := a.Lost(); err != nil {
		return nil, err
	}
	return a.fakeApp.Screenshot(ctx, s, fm)
}

func (a *dyingApp) Screen(ctx context.Context) (*ui.Screen, error) {
	if err := a.Lost(); err != nil {
		return nil, err
	}
	return a.fakeApp.Screen(ctx)
}

// animState is a device's animation setting, which outlives the drivers opened on it.
type animState struct {
	mu        sync.Mutex
	scales    string
	orig      string // the scales before the run, kept by the first disable ("" = none)
	deadCalls int    // restores run through a driver that was dead by then
}

func (s *animState) get() string { s.mu.Lock(); defer s.mu.Unlock(); return s.scales }

// disable is DisableAnimations as the Android driver does it: the first one in a run keeps the
// scales it read; the restore of any of them puts those back (issue #101). dead: the driver
// it runs on dies later in the run, so a restore through it is a call on a dead driver.
func (s *animState) disable(dead bool) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.orig == "" {
		s.orig = s.scales
	}
	s.scales = "0"
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if dead {
			s.deadCalls++
		}
		s.scales, s.orig = s.orig, ""
	}
}

type animatedApp struct {
	*fakeApp
	state *animState
}

func (a animatedApp) DisableAnimations(context.Context) (func(), error) {
	return a.state.disable(false), nil
}

type animatedDyingApp struct {
	*dyingApp
	state *animState
}

func (a animatedDyingApp) DisableAnimations(context.Context) (func(), error) {
	return a.state.disable(true), nil
}

// withDyingPhone makes tb see one Android device whose first driver dies during its first
// launch; each later open gives a fresh, working one. opens counts the opens.
func withDyingPhone(t *testing.T, anim *animState) (opens func() int) {
	t.Helper()
	info := device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Fake phone", Emulator: true, State: "device"}
	oldList := listDevices
	listDevices = func(ctx context.Context) []device.Info { return []device.Info{info} }
	var mu sync.Mutex
	n := 0
	restore := connect.Override(device.Android, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		app := &fakeApp{serial: i.Serial, screen: "home"}
		if n == 1 {
			d := &dyingApp{fakeApp: app}
			if anim != nil {
				return animatedDyingApp{d, anim}, nil
			}
			return d, nil
		}
		if anim != nil {
			return animatedApp{app, anim}, nil
		}
		return app, nil
	})
	t.Cleanup(func() { listDevices = oldList; restore() })
	return func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// Issue #101: a flow that failed because the device's connection died got no screenshot: the
// failure's files were read through the dead driver (pool.Driver hands out whatever is open),
// whose reads fail. They must be read from the device opened again.
func TestCmdTestFailureShotFromTheReopenedDevice(t *testing.T) {
	opens := withDyingPhone(t, nil)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "fail.yaml", failingFlow)
	debug := filepath.Join(dir, "debug")
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--debug-output", debug, "--flatten-debug-output"})
	})
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	found := map[string]bool{}
	filepath.Walk(debug, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			found[filepath.Base(p)] = true
		}
		return nil
	})
	for _, want := range []string{"error.txt", "screenshot.png", "screen.txt"} {
		if !found[want] {
			t.Errorf("issue #101: no %s in the debug output after the device's connection died (%v)", want, found)
		}
	}
	if n := opens(); n != 2 {
		t.Errorf("the device was opened %d times, want 2 (once, then again for the failure's files)", n)
	}
	if !strings.Contains(out+errOut, "opened it again") {
		t.Errorf("the output does not say the device was opened again:\n%s%s", out, errOut)
	}
	// issue #101 (review): the log says the screen files are from after the reopen
	logs, _ := filepath.Glob(filepath.Join(debug, "*", "log.txt"))
	if more, _ := filepath.Glob(filepath.Join(debug, "log.txt")); len(more) > 0 {
		logs = append(logs, more...)
	}
	if len(logs) != 1 {
		t.Fatalf("log.txt: %v", logs)
	}
	if b, _ := os.ReadFile(logs[0]); !strings.Contains(string(b), "the screen was read after tb opened the device again") {
		t.Errorf("issue #101: log.txt does not say the screen was read after the reopen:\n%s", b)
	}
}

// Issue #101: the setup step runs again on a device opened again, so animations are turned
// off twice; the second time reads them already off. The run's end must still put back the
// scales the device had before the run, not the zeros the second read saw.
func TestCmdTestRestoresAnimationsAfterAReopen(t *testing.T) {
	anim := &animState{scales: "1"}
	withDyingPhone(t, anim)
	dir := t.TempDir()
	writeFlow(t, dir, "a_dies.yaml", "appId: com.example.app\n---\n- launchApp\n")
	writeFlow(t, dir, "b_passes.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{dir, "--debug-output", filepath.Join(t.TempDir(), "debug")})
	})
	if code != 1 {
		t.Fatalf("exit %d, want 1 (a_dies fails, b_passes passes)\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if got := anim.get(); got != "1" {
		t.Errorf("issue #101: the animation scales are %q after the run, want the %q the device had before it", got, "1")
	}
	// issue #101 (review): one restore per device, the latest driver's: the dead first
	// driver's would reconnect (up to 5 s per reopen) and may leave a connection open
	anim.mu.Lock()
	dead := anim.deadCalls
	anim.mu.Unlock()
	if dead != 0 {
		t.Errorf("issue #101: %d restore(s) ran through the dead driver", dead)
	}
}
