package session

import (
	"context"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

func TestClosedSessionsName(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5750 sdk_gphone64_arm64", "emulator-5752 sdk_gphone64_arm64")
	s := New()
	s.openDrv = func(_ context.Context, info device.Info) (driver.Driver, error) {
		f := newFake(info.Serial, info.Name, info.Platform)
		f.show(loginScreen()...)
		return f, nil
	}
	defer s.CloseAll(context.Background())
	if r, _ := call(t, s.open, map[string]any{"device": "emulator-5750", "name": "a1"}, ""); !r.OK {
		t.Fatalf("open: %q", r.Text)
	}
	if r, _ := call(t, s.close, map[string]any{"device": "a1"}, ""); !r.OK {
		t.Fatalf("close: %q", r.Text)
	}
	r, _ := call(t, s.look, map[string]any{"device": "a1"}, "")
	if r.OK || !strings.Contains(r.Text, `session "a1" (on emulator-5750) was closed: open it again with tb open -d emulator-5750 --name a1`) {
		t.Errorf("issue #400: a closed session's name: %q", r.Text)
	}
	if r, _ := call(t, s.open, map[string]any{"device": "a1"}, ""); r.OK || !strings.Contains(r.Text, `session "a1" (on emulator-5750) was closed: open it again with tb open -d emulator-5750 --name a1`) {
		t.Errorf("tb open -d a1 after the close: %q", r.Text)
	}
	if r, _ := call(t, s.close, map[string]any{"device": "b7"}, ""); r.OK || r.Exit != 2 || !strings.Contains(r.Text, `no session or connected device is named "b7": nothing to close`) {
		t.Errorf("tb close -d b7: %q", r.Text)
	}
	if r, _ := call(t, s.open, map[string]any{"device": "emulator-5752", "name": "a1"}, ""); !r.OK {
		t.Fatalf("open again: %q", r.Text)
	}
	if r, _ := call(t, s.look, map[string]any{"device": "a1"}, ""); !r.OK {
		t.Errorf("a1 opened again: %q", r.Text)
	}
	call(t, s.close, map[string]any{"device": "all"}, "")
	if r, _ := call(t, s.look, map[string]any{"device": "a1"}, ""); r.OK || !strings.Contains(r.Text, `session "a1" (on emulator-5752) was closed`) {
		t.Errorf("after tb close all: %q", r.Text)
	}
	r, _ = call(t, s.look, map[string]any{"device": "b7"}, "")
	if r.OK || !strings.Contains(r.Text, `no session or connected device is named "b7". Sessions end with tb close or after 30 minutes idle: open one with tb open -d DEVICE --name b7 (devices: emulator-5750, emulator-5752)`) {
		t.Errorf("issue #400: an unknown name: %q", r.Text)
	}
}
