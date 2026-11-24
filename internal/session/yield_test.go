package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
)

// issue #395: tb look, then tb test on the same device. The run asks the caller's own daemon for
// the device: an idle session gives it up (its name kept, so the next tb look opens it again);
// one running a command keeps it and says so; a device with no session here is not this
// daemon's to give.
func TestYieldGivesUpAnIdleSession(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	f := newFake("emulator-5750", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	s := New()
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) { return f, nil }
	d, err := s.connect(context.Background(), f.info, "alice")
	if err != nil {
		t.Fatal(err)
	}
	yield := func(serial string) Yielded {
		t.Helper()
		args, _ := json.Marshal(YieldArgs{Serial: serial})
		r := s.yield(context.Background(), daemon.Request{Cmd: "yield", Args: args}, nil)
		var y Yielded
		if !r.OK || json.Unmarshal(r.JSON, &y) != nil {
			t.Fatalf("yield %s: %+v", serial, r)
		}
		return y
	}
	free := func() bool {
		l, err := devlock.TryAcquire("emulator-5750", "tb test")
		if err != nil {
			return false
		}
		l.Release()
		return true
	}

	if y := yield("emulator-9999"); y.Yielded || y.Busy {
		t.Errorf("a device this daemon has no session on: %+v", y)
	}
	// A command running: kept, and said to be busy.
	d.mu.Lock()
	if y := yield("emulator-5750"); y.Yielded || !y.Busy || y.Label == "" {
		t.Errorf("a session running a command: %+v", y)
	}
	d.mu.Unlock()
	if free() {
		t.Fatal("a busy session gave its device up")
	}
	// Idle: given up, the device free for the run, the name kept.
	if y := yield("emulator-5750"); !y.Yielded || y.Label != "alice (Pixel 7)" {
		t.Errorf("an idle session: %+v", y)
	}
	if !free() {
		t.Error("the device is still held after the session gave it up")
	}
	s.mu.Lock()
	_, open := s.devs["emulator-5750"]
	name := s.names["alice"]
	s.mu.Unlock()
	if open || name != "emulator-5750" {
		t.Errorf("after yield: open %v, alice -> %q (want the name kept)", open, name)
	}
	// tb close -d alice still finds it (a session whose connection dropped) and stops tb's agent.
	if r, _ := call(t, s.close, map[string]any{"device": "alice"}, ""); !r.OK || !strings.Contains(r.Text, "closed alice") {
		t.Errorf("tb close -d alice after yield: %+v", r)
	}
	if f.released != 1 {
		t.Errorf("tb's agent stopped %d times by tb close, want 1", f.released)
	}
	// The next command on it opens it again under its name.
	s.mu.Lock()
	s.names["alice"] = "emulator-5750" // tb close forgot it; a yield alone keeps it (above)
	s.mu.Unlock()
	fakeADB(t, `if [ "$3" = "devices" ] || [ "$1" = "devices" ]; then printf 'List of devices attached\nemulator-5750\tdevice product:x model:Pixel_7 device:x\n'; fi`)
	if _, err := s.resolve(context.Background(), devArgs{Device: "alice"}); err != nil {
		t.Errorf("tb look -d alice after the run: %v", err)
	}
	if free() {
		t.Error("the reopened session does not hold the device")
	}
}

// Review of issue #395: a tb watch holds the session's lock only while it reads the screen, but
// it runs between reads too: its session is busy, not given up under it. So is a session
// still closing (its connection dropped under a command): it holds the device.
func TestYieldKeepsAWatchedOrClosingSession(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	f := newFake("emulator-5750", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	s := New()
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) { return f, nil }
	d, err := s.connect(context.Background(), f.info, "")
	if err != nil {
		t.Fatal(err)
	}
	yield := func() Yielded {
		args, _ := json.Marshal(YieldArgs{Serial: "emulator-5750"})
		var y Yielded
		json.Unmarshal(s.yield(context.Background(), daemon.Request{Args: args}, nil).JSON, &y)
		return y
	}
	watching := make(chan daemon.Response, 1)
	go func() {
		r, _ := call(t, s.watch, map[string]any{"device": "emulator-5750", "timeout": 1500}, "")
		watching <- r
	}()
	for i := 0; d.watching.Load() == 0; i++ {
		if i > 500 {
			t.Fatal("the watch did not start")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if y := yield(); y.Yielded || !y.Busy {
		t.Errorf("a session running tb watch: %+v", y)
	}
	if r := <-watching; !r.OK && strings.Contains(r.Text, "closed") {
		t.Errorf("the watch's session was closed under it: %s", r.Text)
	}
	if y := yield(); !y.Yielded {
		t.Errorf("after the watch ended: %+v", y)
	}

	s.mu.Lock()
	s.closing["emulator-5750"] = d
	s.mu.Unlock()
	if y := yield(); y.Yielded || !y.Busy {
		t.Errorf("a session still closing: %+v", y)
	}
}
