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

func countOpens(s *Service, f driver.Driver) *atomic.Int32 {
	var n atomic.Int32
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) {
		n.Add(1)
		return f, nil
	}
	return &n
}

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
	if r, _ := call(t, s.close, map[string]any{"device": "ghost"}, ""); r.Exit != 2 {
		t.Errorf("close -d ghost = %+v", r)
	}
}

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

type killCount struct {
	*fakeDriver
	kills atomic.Int32
}

func (k *killCount) Kill() { k.kills.Add(1) }

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
		d.mu.Lock()
		dropAndWait(t, s, f)
		limit := 5 * time.Second
		if stuck {
			limit = 100 * time.Millisecond
		} else {
			time.AfterFunc(100*time.Millisecond, d.mu.Unlock)
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

func TestCloseDoesNotMatchADroppedSessionByDeviceName(t *testing.T) {
	adbLists(t, "emulator-5556 Pixel_7")
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

func closeAllSetup(t *testing.T) (*Service, map[string]*fakeDriver) {
	t.Helper()
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	s := New()
	fakes := map[string]*fakeDriver{
		"ivy":   newFake("UDID-1", "iPhone 17", device.IOS),
		"ian":   newFake("UDID-2", "iPhone 16", device.IOS),
		"alice": newFake("emulator-5554", "Pixel 7", device.Android),
		"amy":   newFake("emulator-5556", "Pixel 8", device.Android),
		"wes":   newFake("web", "Chrome", device.Web),
	}
	for name, f := range fakes {
		countOpens(s, f)
		if _, err := s.connect(context.Background(), f.info, name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ian", "amy"} {
		f := fakes[name]
		dropAndWait(t, s, f)
		deadline := time.Now().Add(5 * time.Second)
		for {
			s.mu.Lock()
			closing := s.closing[f.info.Serial] != nil
			s.mu.Unlock()
			if !closing {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the dropped connection was not closed", name)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return s, fakes
}

func TestCloseAllByPlatform(t *testing.T) {
	s, fakes := closeAllSetup(t)
	r, _ := call(t, s.close, map[string]any{"device": "all", "platform": "ios"}, "")
	if !r.OK || r.Text != "closed all ios sessions\n" {
		t.Errorf("close all -p ios = %+v", r)
	}
	for name, f := range fakes {
		want := 0
		if f.info.Platform == device.IOS {
			want = 1
		}
		var released int
		f.get(func() { released = f.released })
		if released != want {
			t.Errorf("issue #184: close all -p ios stopped tb's agent on %s's device %d times, want %d", name, released, want)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, serial := range []string{"emulator-5554", "web"} {
		if s.devs[serial] == nil {
			t.Errorf("issue #184: close all -p ios closed the open session on %s", serial)
		}
	}
	if s.devs["UDID-1"] != nil || s.dropped["UDID-2"] != nil {
		t.Errorf("close all -p ios left an iOS session: devs %v dropped %v", s.devs, s.dropped)
	}
	if s.dropped["emulator-5556"] == nil {
		t.Error("issue #184: close all -p ios forgot the dropped Android session")
	}
	for _, name := range []string{"ivy", "ian"} {
		if _, ok := s.names[name]; ok {
			t.Errorf("close all -p ios kept the name %q", name)
		}
	}
	for _, name := range []string{"alice", "amy", "wes"} {
		if _, ok := s.names[name]; !ok {
			t.Errorf("issue #184: close all -p ios forgot the name %q", name)
		}
	}
}

func TestCloseAllWithoutPlatformClosesEverySession(t *testing.T) {
	s, fakes := closeAllSetup(t)
	r, _ := call(t, s.close, map[string]any{"device": "all"}, "")
	if !r.OK || r.Text != "closed all devices\n" {
		t.Errorf("close all = %+v", r)
	}
	for name, f := range fakes {
		var released int
		f.get(func() { released = f.released })
		if released != 1 {
			t.Errorf("close all stopped tb's agent on %s's device %d times, want 1", name, released)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.devs) != 0 || len(s.dropped) != 0 || len(s.names) != 0 {
		t.Errorf("close all left devs %v dropped %v names %v", s.devs, s.dropped, s.names)
	}
}

func TestCancelledCloseAllKeepsDroppedSessionsName(t *testing.T) {
	s, fakes := closeAllSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _ := callCtx(t, ctx, s.close, map[string]any{"device": "all"}, "")
	if r.OK || r.Exit != 1 || !strings.Contains(r.Text, "tb close was cancelled") {
		t.Errorf("cancelled close all = %+v", r)
	}
	s.mu.Lock()
	dropped, named := s.dropped["emulator-5556"] != nil, s.names["amy"] == "emulator-5556"
	s.mu.Unlock()
	if !dropped || !named {
		t.Fatalf("issue #184 review: after a cancelled close all, amy's dropped session kept %v, her name kept %v", dropped, named)
	}
	r, _ = call(t, s.close, map[string]any{"device": "amy"}, "")
	if !r.OK || !strings.HasPrefix(r.Text, "closed amy (Pixel 8)") {
		t.Errorf("close -d amy after a cancelled close all = %+v", r)
	}
	f := fakes["amy"]
	var released int
	f.get(func() { released = f.released })
	if released != 1 {
		t.Errorf("tb's agent on amy's device was stopped %d times, want 1", released)
	}
}

func TestCloseAllByPlatformWithNoSession(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, f, "alice")
	r, _ := call(t, s.close, map[string]any{"device": "all", "platform": "ios"}, "")
	if !r.OK || r.Text != "no ios session is open: nothing to close\n" {
		t.Errorf("close all -p ios with only an Android session = %+v", r)
	}
	var closed int
	f.get(func() { closed = f.closed })
	if closed != 0 {
		t.Error("issue #184: close all -p ios closed the Android session")
	}
}
