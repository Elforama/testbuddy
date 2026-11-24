package session

import (
	"context"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// newPickService is a Service whose devices are fakes, noting the serial it opened in *opened.
func newPickService(opened *string) *Service {
	s := New()
	s.openDrv = func(_ context.Context, info device.Info) (driver.Driver, error) {
		*opened = info.Serial
		f := newFake(info.Serial, info.Name, info.Platform)
		f.show(loginScreen()...)
		return f, nil
	}
	return s
}

// issue #396: with several devices connected and no session open, a command that names no
// device took the first one: an agent that forgot -d cleared the app's state on another
// agent's emulator. It is refused, listing the devices; named, it runs there.
func TestNoDeviceNamedWithSeveralConnected(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5594 sdk_gphone64_arm64", "emulator-5750 sdk_gphone64_arm64", "emulator-5752 sdk_gphone64_arm64")
	for _, c := range []struct {
		name string
		h    func(*Service) handler
		args map[string]any
	}{
		{"look", func(s *Service) handler { return s.look }, map[string]any{}},
		{"do", func(s *Service) handler { return s.do }, map[string]any{"commands": "launchApp"}},
		{"open", func(s *Service) handler { return s.open }, map[string]any{}},
	} {
		opened := ""
		s := newPickService(&opened)
		r, _ := call(t, c.h(s), c.args, "")
		if r.OK || !strings.Contains(r.Text, "3 devices are connected (emulator-5594, emulator-5750, emulator-5752): say which with -d") {
			t.Errorf("issue #396: tb %s with no device named, 3 connected: %q", c.name, r.Text)
		}
		if opened != "" {
			t.Errorf("issue #396: tb %s took %s without being told to", c.name, opened)
		}
		s.CloseAll(context.Background())
	}
	opened := ""
	s := newPickService(&opened)
	defer s.CloseAll(context.Background())
	if r, _ := call(t, s.look, map[string]any{"device": "emulator-5750"}, ""); !r.OK || opened != "emulator-5750" {
		t.Errorf("named: %q on %q", r.Text, opened)
	}
}

// Review of issue #396: with several devices connected and one session open (another agent's,
// maybe), tb open with no device named is refused too, never attached to that session; tb
// look with no device still uses the one session open, as documented.
func TestOpenWithNoDeviceNeverTakesTheOneSessionOpen(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5594 sdk_gphone64_arm64", "emulator-5750 sdk_gphone64_arm64")
	opened := ""
	s := newPickService(&opened)
	defer s.CloseAll(context.Background())
	if r, _ := call(t, s.open, map[string]any{"device": "emulator-5594"}, ""); !r.OK {
		t.Fatalf("agent A's open: %q", r.Text)
	}
	r, _ := call(t, s.open, map[string]any{"name": "bob"}, "")
	if r.OK || !strings.Contains(r.Text, "2 devices are connected") {
		t.Errorf("tb open --name bob with no device: %q", r.Text)
	}
	s.mu.Lock()
	name := s.devs["emulator-5594"].Name
	s.mu.Unlock()
	if name != "" {
		t.Errorf("agent A's session was renamed %q", name)
	}
	if r, _ := call(t, s.look, map[string]any{}, ""); !r.OK {
		t.Errorf("tb look with the one session open: %q", r.Text)
	}
}
