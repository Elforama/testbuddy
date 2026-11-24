package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
)

// issue #398: tb devices marked only the sessions of the caller's own daemon; a device another
// tb process held (a tb test run, another TB_HOME's session) looked free. It now reads the
// device locks: who holds each device, and which are free, in the text and in --json.
func TestDevicesShowsWhoHoldsEachDevice(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5594 sdk_gphone64_arm64", "emulator-5750 sdk_gphone64_arm64", "emulator-5752 sdk_gphone64_arm64")
	other, err := devlock.TryAcquire("emulator-5750", `tb session "a1" (tb open/do/look)`) // another daemon's
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	s := New()
	s.openDrv = func(_ context.Context, info device.Info) (driver.Driver, error) {
		f := newFake(info.Serial, info.Name, info.Platform)
		f.show(loginScreen()...)
		return f, nil
	}
	defer s.CloseAll(context.Background())
	if r, _ := call(t, s.open, map[string]any{"device": "emulator-5594", "name": "mine"}, ""); !r.OK {
		t.Fatalf("open: %q", r.Text)
	}
	r, _ := call(t, s.devices, map[string]any{}, "")
	lines := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(r.Text), "\n") {
		for _, serial := range []string{"emulator-5594", "emulator-5750", "emulator-5752"} {
			if strings.Contains(l, serial) {
				lines[serial] = l
			}
		}
	}
	if !strings.Contains(lines["emulator-5594"], "session: mine") {
		t.Errorf("own session: %q", lines["emulator-5594"])
	}
	if !strings.Contains(lines["emulator-5750"], `in use: tb session "a1" (tb open/do/look) (pid `) {
		t.Errorf("issue #398: a device another tb holds looks free: %q", lines["emulator-5750"])
	}
	if !strings.HasSuffix(lines["emulator-5752"], "  free") {
		t.Errorf("a free device: %q", lines["emulator-5752"])
	}
	var js []map[string]any
	if err := json.Unmarshal(r.JSON, &js); err != nil || len(js) != 3 {
		t.Fatalf("json %s: %v", r.JSON, err)
	}
	for _, d := range js {
		switch d["serial"] {
		case "emulator-5750":
			if u, _ := d["in_use"].(map[string]any); u == nil || !strings.Contains(u["what"].(string), `tb session "a1"`) || d["free"] != false {
				t.Errorf("issue #398, json: %v", d)
			}
		case "emulator-5752":
			if d["free"] != true || d["in_use"] != nil {
				t.Errorf("json, free: %v", d)
			}
		case "emulator-5594":
			if _, has := d["free"]; has || d["session"] != "mine" {
				t.Errorf("json, own session: %v", d)
			}
		}
	}
}

// Review of issue #398: this daemon's own session still closing (its connection dropped under a
// command) holds its device: marked so, not "in use" by another tb; with locking off tb cannot
// tell, and marks nothing (no "free" in --json either).
func TestDevicesClosingAndUnknown(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	adbLists(t, "emulator-5594 sdk_gphone64_arm64", "emulator-5750 sdk_gphone64_arm64")
	l, err := devlock.TryAcquire("emulator-5594", `tb session "x" (tb open/do/look)`)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	s := New()
	s.closing["emulator-5594"] = &Dev{Info: device.Info{Serial: "emulator-5594"}}
	r, _ := call(t, s.devices, map[string]any{}, "")
	if !strings.Contains(r.Text, "emulator-5594 \"sdk gphone64 arm64\" (emulator)  closing (this daemon's session") || strings.Contains(r.Text, "in use") ||
		!strings.Contains(string(r.JSON), `"closing":true`) {
		t.Errorf("closing:\n%s", r.Text)
	}
	t.Setenv("TB_NO_DEVICE_LOCK", "1")
	r, _ = call(t, New().devices, map[string]any{}, "")
	if strings.Contains(r.Text, "free") || strings.Contains(r.Text, "in use") || strings.Contains(string(r.JSON), `"free"`) {
		t.Errorf("locking off:\n%s\n%s", r.Text, r.JSON)
	}
}
