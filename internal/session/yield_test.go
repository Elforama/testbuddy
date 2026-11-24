package session

import (
	"context"
	"encoding/json"
	"testing"

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
	// The next command on it opens it again under its name.
	fakeADB(t, `if [ "$3" = "devices" ] || [ "$1" = "devices" ]; then printf 'List of devices attached\nemulator-5750\tdevice product:x model:Pixel_7 device:x\n'; fi`)
	if _, err := s.resolve(context.Background(), devArgs{Device: "alice"}); err != nil {
		t.Errorf("tb look -d alice after the run: %v", err)
	}
	if free() {
		t.Error("the reopened session does not hold the device")
	}
}
