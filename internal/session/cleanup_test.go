package session

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// countOpens makes s connect to f, counting how many times it connects.
func countOpens(s *Service, f driver.Driver) *atomic.Int32 {
	var n atomic.Int32
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) {
		n.Add(1)
		return f, nil
	}
	return &n
}

// dropAndWait pushes a dropped connection to f and waits until the session forgot the device.
func dropAndWait(t *testing.T, s *Service, f *fakeDriver) {
	t.Helper()
	f.push("disconnected")
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		_, open := s.devs[f.info.Serial]
		s.mu.Unlock()
		if !open {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the dropped device was not forgotten")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// issue #124 (part 2): `tb close -d X` on a device with no session (`tb release` sends one
// for every device it frees) answers at once: it does not connect to the device (starting
// tb's agent and taking its lock) only to close it again.
func TestCloseWithNoSessionDoesNotConnect(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	opens := countOpens(s, f)
	for _, dev := range []string{"emulator-5554", "Pixel 7", ""} {
		r, _ := call(t, s.close, map[string]any{"device": dev}, "")
		if !r.OK || !strings.Contains(r.Text, "no tb session is open") {
			t.Errorf("close -d %q with no session = %+v", dev, r)
		}
	}
	if n := opens.Load(); n != 0 {
		t.Errorf("issue #124: tb close on a device with no session connected to it %d times, want 0", n)
	}
	var closed, released int
	f.get(func() { closed, released = f.closed, f.released })
	if closed != 0 || released != 0 {
		t.Errorf("closed %d, released %d: nothing was open", closed, released)
	}
	// A device that is not there at all is still an error.
	if r, _ := call(t, s.close, map[string]any{"device": "ghost"}, ""); r.Exit != 2 {
		t.Errorf("close -d ghost = %+v", r)
	}
}

// issue #124 (part 2): `tb close -d alice` after alice's connection dropped stops tb's agent
// on her device without connecting to it again, and forgets her name.
func TestCloseAfterDropDoesNotReconnect(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	opens := countOpens(s, f)
	if _, err := s.connect(context.Background(), f.info, "alice"); err != nil {
		t.Fatal(err)
	}
	dropAndWait(t, s, f)
	r, _ := call(t, s.close, map[string]any{"device": "alice"}, "")
	if !r.OK || !strings.HasPrefix(r.Text, "closed alice (Pixel 7)") {
		t.Errorf("close -d alice after a drop = %+v", r)
	}
	if n := opens.Load(); n != 1 {
		t.Errorf("issue #124: tb close after a drop connected to the device again (%d connects, want 1)", n)
	}
	var released int
	f.get(func() { released = f.released })
	if released != 1 {
		t.Errorf("tb's agent was stopped %d times, want 1", released)
	}
	s.mu.Lock()
	_, named := s.names["alice"]
	s.mu.Unlock()
	if named {
		t.Error("alice's name was kept after tb close")
	}
	// Closed now: a second close finds no session, and still does not connect.
	if r, _ := call(t, s.close, map[string]any{"device": "alice"}, ""); r.Exit != 2 && !strings.Contains(r.Text, "no tb session is open") {
		t.Errorf("second close = %+v", r)
	}
	if n := opens.Load(); n != 1 {
		t.Errorf("issue #124: a second tb close connected (%d connects, want 1)", n)
	}
}

// killCount is a fake driver that counts Kill.
type killCount struct {
	*fakeDriver
	kills atomic.Int32
}

func (k *killCount) Kill() { k.kills.Add(1) }

// issue #124 (part 1): a session whose connection dropped while a command held its device
// closes once the command ends. The daemon stopping meanwhile (CloseAll) waits for that close
// too, and kills the driver if it does not end in time: before, CloseAll no longer saw the
// dropped session, so the daemon could exit with its Chrome and profile left behind.
func TestCloseAllClosesADroppedSession(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	for _, stuck := range []bool{false, true} {
		s := New()
		f := newFake("chrome", "Chrome", device.Web)
		k := &killCount{fakeDriver: f}
		countOpens(s, k)
		d, err := s.connect(context.Background(), f.info, "alice")
		if err != nil {
			t.Fatal(err)
		}
		d.mu.Lock() // a command runs on the device
		dropAndWait(t, s, f)
		limit := 5 * time.Second
		if stuck {
			limit = 100 * time.Millisecond
		} else {
			time.AfterFunc(100*time.Millisecond, d.mu.Unlock) // the command ends
		}
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		s.CloseAll(ctx)
		cancel()
		var closed int
		f.get(func() { closed = f.closed })
		if !stuck && closed != 1 {
			t.Errorf("issue #124: CloseAll returned with the dropped session's driver closed %d times, want 1", closed)
		}
		if stuck && k.kills.Load() != 1 {
			t.Errorf("issue #124: CloseAll ran out of time and killed the dropped session's driver %d times, want 1", k.kills.Load())
		}
		if stuck {
			d.mu.Unlock()
		}
	}
}
