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
	// Closed now: alice names nothing any more (exit 2), and a close by serial finds no
	// session; neither connects, nor stops the agent again.
	if r, _ := call(t, s.close, map[string]any{"device": "alice"}, ""); r.OK || r.Exit != 2 || !strings.Contains(r.Text, `no connected device matches "alice"`) {
		t.Errorf("second close -d alice = %+v", r)
	}
	if r, _ := call(t, s.close, map[string]any{"device": "emulator-5554"}, ""); !r.OK || r.Text != "no tb session is open on Pixel 7 emulator-5554: nothing to close\n" {
		t.Errorf("close -d emulator-5554 after the close = %+v", r)
	}
	f.get(func() { released = f.released })
	if released != 1 {
		t.Errorf("tb's agent was stopped %d times after the second close, want 1", released)
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

// issue #124 review: the CLI sends the platform with every tb close. `tb close -p android`
// with no -d closes the one Android session open, as before; with two open on that platform
// it asks which.
func TestCloseByPlatformOnly(t *testing.T) {
	s := New()
	fa := newFake("emulator-5554", "Pixel 7", device.Android)
	fi := newFake("UDID-1", "iPhone 17", device.IOS)
	addDev(s, fa, "alice")
	addDev(s, fi, "bob")
	r, _ := call(t, s.close, map[string]any{"platform": "android"}, "")
	if !r.OK || r.Text != "closed alice (Pixel 7)\n" {
		t.Errorf("close -p android = %+v", r)
	}
	var closed int
	fa.get(func() { closed = fa.closed })
	if closed != 1 {
		t.Errorf("the Android session's driver was closed %d times, want 1", closed)
	}
	fi.get(func() { closed = fi.closed })
	if closed != 0 {
		t.Error("close -p android closed the iOS session")
	}

	addDev(s, newFake("emulator-5554", "Pixel 7", device.Android), "alice")
	addDev(s, newFake("emulator-5556", "Pixel 8", device.Android), "carol")
	r, _ = call(t, s.close, map[string]any{"platform": "android"}, "")
	if r.Exit != 2 || !strings.Contains(r.Text, "several devices are open (alice (Pixel 7), carol (Pixel 8)): say which with -d") {
		t.Errorf("close -p android with two Android sessions = %+v", r)
	}
}

// issue #124 review: a plain `tb close` with no session open and one session whose connection
// dropped closes that one (stopping tb's agent), without connecting.
func TestPlainCloseClosesTheDroppedSession(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	for _, platform := range []string{"", "android"} {
		s := New()
		f := newFake("emulator-5554", "Pixel 7", device.Android)
		opens := countOpens(s, f)
		if _, err := s.connect(context.Background(), f.info, "alice"); err != nil {
			t.Fatal(err)
		}
		dropAndWait(t, s, f)
		r, _ := call(t, s.close, map[string]any{"platform": platform}, "")
		if !r.OK || r.Text != "closed alice (Pixel 7)\n" {
			t.Errorf("close -p %q after a drop = %+v", platform, r)
		}
		var released int
		f.get(func() { released = f.released })
		if released != 1 || opens.Load() != 1 {
			t.Errorf("close -p %q after a drop: agent stopped %d times, %d connects; want 1, 1", platform, released, opens.Load())
		}
	}
}

// issue #124 review: a dropped session is found by its session name or serial, not by the
// device's name: an emulator of that name may run on another serial since.
func TestCloseDoesNotMatchADroppedSessionByDeviceName(t *testing.T) {
	adbLists(t, "emulator-5556 Pixel_7") // the AVD now runs on another serial
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	countOpens(s, f)
	if _, err := s.connect(context.Background(), f.info, "alice"); err != nil {
		t.Fatal(err)
	}
	dropAndWait(t, s, f)
	r, _ := call(t, s.close, map[string]any{"device": "Pixel 7"}, "")
	if !r.OK || r.Text != "no tb session is open on Pixel 7 emulator-5556: nothing to close\n" {
		t.Errorf("close -d 'Pixel 7' = %+v", r)
	}
	var released int
	f.get(func() { released = f.released })
	if released != 0 {
		t.Errorf("close -d 'Pixel 7' stopped the agent of the dropped session on emulator-5554 (%d times)", released)
	}
}

// issue #124 review: `tb close all` stops tb's agent on the devices of dropped sessions too,
// as `tb close -d` does, instead of only forgetting them.
func TestCloseAllStopsDroppedSessionsAgents(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	opens := countOpens(s, f)
	if _, err := s.connect(context.Background(), f.info, "alice"); err != nil {
		t.Fatal(err)
	}
	dropAndWait(t, s, f)
	r, _ := call(t, s.close, map[string]any{"device": "all"}, "")
	if !r.OK || r.Text != "closed all devices\n" {
		t.Errorf("close all = %+v", r)
	}
	var released int
	f.get(func() { released = f.released })
	if released != 1 || opens.Load() != 1 {
		t.Errorf("close all after a drop: agent stopped %d times, %d connects; want 1, 1", released, opens.Load())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.dropped) != 0 || len(s.names) != 0 {
		t.Errorf("dropped %v names %v after close all", s.dropped, s.names)
	}
}

// issue #124 review: a session that finishes connecting after the daemon began stopping
// (after CloseAll took its list) is closed at once instead of left open.
func TestConnectAfterCloseAllClosesTheDriver(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	countOpens(s, f)
	s.CloseAll(context.Background())
	if _, err := s.connect(context.Background(), f.info, "alice"); err == nil || !strings.Contains(err.Error(), "the tb daemon is stopping") {
		t.Errorf("connect after CloseAll: %v", err)
	}
	var closed int
	f.get(func() { closed = f.closed })
	if closed != 1 {
		t.Errorf("the driver opened after CloseAll was closed %d times, want 1", closed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.devs) != 0 {
		t.Errorf("a session was left open after CloseAll: %v", s.devs)
	}
}

// issue #124 second review: `tb close -d "Pixel 7"` after an unnamed session on that device
// dropped stops tb's agent there, without connecting: the device name is looked up in the
// device list, and its current serial is the dropped session's.
func TestCloseByDeviceNameFindsTheDroppedSessionOnItsSerial(t *testing.T) {
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	opens := countOpens(s, f)
	if _, err := s.connect(context.Background(), f.info, ""); err != nil {
		t.Fatal(err)
	}
	dropAndWait(t, s, f)
	r, _ := call(t, s.close, map[string]any{"device": "Pixel 7"}, "")
	if !r.OK || r.Text != "closed Pixel 7 emulator-5554\n" {
		t.Errorf("close -d 'Pixel 7' after a drop = %+v", r)
	}
	var released int
	f.get(func() { released = f.released })
	if released != 1 || opens.Load() != 1 {
		t.Errorf("issue #124: close -d 'Pixel 7' after a drop: agent stopped %d times, %d connects; want 1, 1", released, opens.Load())
	}
}

// issue #124 second review: a plain `tb close` with no session open and several dropped ones
// names them and asks for -d, as it does for several open sessions.
func TestPlainCloseWithSeveralDroppedSessionsAsks(t *testing.T) {
	s := New()
	fa := newFake("emulator-5554", "Pixel 7", device.Android)
	fb := newFake("emulator-5556", "Pixel 8", device.Android)
	for _, x := range []struct {
		f    *fakeDriver
		name string
	}{{fa, "alice"}, {fb, "bob"}} {
		countOpens(s, x.f)
		if _, err := s.connect(context.Background(), x.f.info, x.name); err != nil {
			t.Fatal(err)
		}
		dropAndWait(t, s, x.f)
	}
	r, _ := call(t, s.close, map[string]any{}, "")
	if r.Exit != 2 || !strings.Contains(r.Text, "alice (Pixel 7), bob (Pixel 8)") || !strings.Contains(r.Text, "say which with -d") {
		t.Errorf("plain close with two dropped sessions = %+v", r)
	}
}
