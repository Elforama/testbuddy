package session

import (
	"context"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// issue #396: with several devices connected and no session open, a command that names no
// device took the first one: an agent that forgot -d cleared the app's state on another
// agent's emulator. It is refused, listing the devices; with one device, -d stays optional.
func TestNoDeviceNamedWithSeveralConnected(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5594 sdk_gphone64_arm64", "emulator-5750 sdk_gphone64_arm64", "emulator-5752 sdk_gphone64_arm64")
	opened := ""
	s := New()
	s.openDrv = func(_ context.Context, info device.Info) (driver.Driver, error) {
		opened = info.Serial
		f := newFake(info.Serial, info.Name, info.Platform)
		f.show(loginScreen()...)
		return f, nil
	}
	for _, h := range []struct {
		name string
		h    handler
		args map[string]any
	}{
		{"look", s.look, map[string]any{}},
		{"do", s.do, map[string]any{"commands": "launchApp"}},
		{"open", s.open, map[string]any{}},
	} {
		r, _ := call(t, h.h, h.args, "")
		if r.OK || !strings.Contains(r.Text, "3 devices are connected (emulator-5594, emulator-5750, emulator-5752): say which with -d") {
			t.Errorf("issue #396: tb %s with no device named, 3 connected: %q", h.name, r.Text)
		}
		if opened != "" {
			t.Fatalf("issue #396: tb %s took %s without being told to", h.name, opened)
		}
	}
	// Named: it runs there.
	if r, _ := call(t, s.look, map[string]any{"device": "emulator-5750"}, ""); !r.OK || opened != "emulator-5750" {
		t.Errorf("named: %q on %q", r.Text, opened)
	}
}
