package android

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// SetOrientation maps flow names to the protocol's, then re-reads the size (it swaps in landscape).
func TestSetOrientation(t *testing.T) {
	defer func(w time.Duration) { rotateWait = w }(rotateWait)
	rotateWait = 200 * time.Millisecond // info always says landscape here: portrait cases wait it out
	cases := map[string]string{"PORTRAIT": "portrait", "landscape_left": "landscapeLeft", "LANDSCAPE_RIGHT": "landscapeRight",
		"upside_down": "upsideDown", "Landscape": "landscape"}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			a := newFakeAgent(t)
			a.on("rotate", func(map[string]any) map[string]any { return nil })
			d := newDriver(t, a)
			a.on("info", func(map[string]any) map[string]any { return map[string]any{"w": 2400, "h": 1080} })
			if err := d.SetOrientation(ctxT(t), in); err != nil {
				t.Fatal(err)
			}
			if got := a.last("rotate")["to"]; got != want {
				t.Errorf("to = %v, want %q", got, want)
			}
			if w, h := d.Size(); w != 2400 || h != 1080 {
				t.Errorf("Size = %dx%d after rotating", w, h)
			}
		})
	}
}

// The display turns after the rotate request returns: right after it the agent can still
// report the old size (Maestro's e2e demo_app setOrientation.yaml, rotating back to portrait:
// tb kept 2400x1080 and swiped at x=1200 on a 1080-wide screen). The size is asked again until
// it has the orientation asked for, for up to rotateWait.
func TestSetOrientationWaitsForTheTurn(t *testing.T) {
	a := newFakeAgent(t)
	a.on("rotate", func(map[string]any) map[string]any { return nil })
	d := newDriver(t, a)
	var asked atomic.Int64
	a.on("info", func(map[string]any) map[string]any {
		if asked.Add(1) <= 2 {
			return map[string]any{"w": 2400, "h": 1080} // still landscape
		}
		return map[string]any{"w": 1080, "h": 2400}
	})
	if err := d.SetOrientation(ctxT(t), "PORTRAIT"); err != nil {
		t.Fatal(err)
	}
	if w, h := d.Size(); w != 1080 || h != 2400 {
		t.Errorf("Size = %dx%d after rotating to portrait, want 1080x2400 (asked %d times)", w, h, asked.Load())
	}
}

// A failed rotation keeps the old size; an info without a size does not zero it.
func TestSetOrientationErrors(t *testing.T) {
	defer func(w time.Duration) { rotateWait = w }(rotateWait)
	rotateWait = 200 * time.Millisecond // an info without a size never shows the turn
	a := newFakeAgent(t)
	a.on("rotate", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "locked"} })
	d := newDriver(t, a)
	a.reset()
	if err := d.SetOrientation(ctxT(t), "PORTRAIT"); err == nil {
		t.Error("rotate failure not reported")
	}
	if len(a.requests("info")) != 0 {
		t.Error("size refreshed after a failed rotation")
	}
	a.on("rotate", func(map[string]any) map[string]any { return nil })
	a.on("info", func(map[string]any) map[string]any { return map[string]any{} })
	d.SetOrientation(ctxT(t), "PORTRAIT")
	if w, h := d.Size(); w != 1080 || h != 2400 {
		t.Errorf("Size = %dx%d, want it kept", w, h)
	}
}

// A protocol name passed straight through (not a flow name) should keep its case.
func TestSetOrientationKeepsProtocolNames(t *testing.T) {
	defer func(w time.Duration) { rotateWait = w }(rotateWait)
	rotateWait = 200 * time.Millisecond // its info does not turn
	a := newFakeAgent(t)
	a.on("rotate", func(map[string]any) map[string]any { return nil })
	d := newDriver(t, a)
	d.SetOrientation(ctxT(t), "landscapeLeft")
	if got := a.last("rotate")["to"]; got != "landscapeLeft" {
		t.Errorf("to = %v", got)
	}
}

// Airplane mode goes through cmd connectivity; toggling reads the current setting first.
func TestAirplaneMode(t *testing.T) {
	for _, c := range []struct {
		current string
		want    string
	}{{"1\n", "cmd connectivity airplane-mode disable"}, {"0\n", "cmd connectivity airplane-mode enable"}, {"null", "cmd connectivity airplane-mode enable"}} {
		a := shAgent(t, map[string]shAnswer{"airplane_mode_on": {c.current, 0}})
		d := newDriver(t, a)
		if err := d.ToggleAirplaneMode(ctxT(t)); err != nil {
			t.Fatal(err)
		}
		cmds := a.shCmds()
		if len(cmds) != 2 || cmds[0] != "settings get global airplane_mode_on" || cmds[1] != c.want {
			t.Errorf("current %q: ran %v, want %q", c.current, cmds, c.want)
		}
	}
	a := shAgent(t, map[string]shAnswer{"airplane_mode_on": {"", 1}})
	d := newDriver(t, a)
	if err := d.ToggleAirplaneMode(ctxT(t)); err == nil || len(a.shCmds()) != 1 {
		t.Errorf("failed read: %v, ran %v", err, a.shCmds())
	}
	a.reset()
	d.SetAirplaneMode(ctxT(t), true)
	if got := a.shCmds(); len(got) != 1 || got[0] != "cmd connectivity airplane-mode enable" {
		t.Errorf("SetAirplaneMode(true) ran %v", got)
	}
}

// Foreground returns the package the agent reports.
func TestForeground(t *testing.T) {
	a := newFakeAgent(t)
	a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": "com.android.launcher3"} })
	d := newDriver(t, a)
	if p, err := d.Foreground(ctxT(t)); err != nil || p != "com.android.launcher3" {
		t.Errorf("Foreground = %q, %v", p, err)
	}
	a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if _, err := d.Foreground(ctxT(t)); err == nil {
		t.Error("error not reported")
	}
}

// DisableAnimations zeroes the three scales and tells the agent; restore puts back the old values (null → 1).
func TestDisableAnimations(t *testing.T) {
	cases := []struct {
		name    string
		current string
		restore string
		on      bool
	}{
		{"defaults", "1\n1\n1\n", "settings put global window_animation_scale 1; settings put global transition_animation_scale 1; settings put global animator_duration_scale 1", true},
		{"null and custom", "null\n0.5\nnull\n", "settings put global window_animation_scale 1; settings put global transition_animation_scale 0.5; settings put global animator_duration_scale 1", true},
		{"already off", "0\n0\n0\n", "settings put global window_animation_scale 0; settings put global transition_animation_scale 0; settings put global animator_duration_scale 0", false},
		{"short output", "0.5\n", "settings put global window_animation_scale 0.5; settings put global transition_animation_scale 1; settings put global animator_duration_scale 1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"settings get global": {c.current, 0}})
			a.on("animations", func(map[string]any) map[string]any { return nil })
			d := newDriver(t, a)
			restore, err := d.DisableAnimations(ctxT(t))
			if err != nil {
				t.Fatal(err)
			}
			cmds := a.shCmds()
			wantGet := "settings get global window_animation_scale; settings get global transition_animation_scale; settings get global animator_duration_scale"
			wantSet := "settings put global window_animation_scale 0; settings put global transition_animation_scale 0; settings put global animator_duration_scale 0"
			if len(cmds) != 2 || cmds[0] != wantGet || cmds[1] != wantSet {
				t.Fatalf("ran %v", cmds)
			}
			if on := a.last("animations")["on"]; on != false {
				t.Errorf("animations on = %v, want false", on)
			}
			a.reset()
			restore()
			if got := a.shCmds(); len(got) != 1 || got[0] != c.restore {
				t.Errorf("restore ran %v\nwant %q", got, c.restore)
			}
			if on := a.last("animations")["on"]; on != c.on {
				t.Errorf("restored animations on = %v, want %v", on, c.on)
			}
		})
	}
}

// DisableAnimations returns a usable no-op restore when reading or writing the settings fails.
func TestDisableAnimationsErrors(t *testing.T) {
	for _, failing := range []string{"settings get", "settings put"} {
		a := shAgent(t, map[string]shAnswer{failing: {"denied", 1}})
		d := newDriver(t, a)
		restore, err := d.DisableAnimations(ctxT(t))
		if err == nil || restore == nil {
			t.Fatalf("%s failing: err %v, restore nil %v", failing, err, restore == nil)
		}
		a.reset()
		restore()
		if ops := a.ops(); len(ops) != 0 {
			t.Errorf("no-op restore sent %v", ops)
		}
	}
}

// Raw returns the agent's full JSON answer.
func TestRaw(t *testing.T) {
	a := newFakeAgent(t)
	a.on("setQuiet", func(req map[string]any) map[string]any { return map[string]any{"quiet": req["ms"]} })
	d := newDriver(t, a)
	raw, err := d.Raw(ctxT(t), "setQuiet", map[string]any{"ms": 150})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m["quiet"] != 150.0 || m["ok"] != true {
		t.Errorf("Raw = %s", raw)
	}
	if _, err := d.Raw(ctxT(t), "nope", nil); err == nil {
		t.Error("unknown op accepted")
	}
}

// StopRecording without a recording is a no-op; SetLocation on a device before Android 12 (or of
// unknown version) needs an emulator.
func TestRecordingAndLocationWithoutADB(t *testing.T) {
	d := bareDriver(nil)
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Errorf("StopRecording = %v", err)
	}
	d.info.Emulator = false
	if err := d.SetLocation(ctxT(t), 1, 2); err == nil || !strings.Contains(err.Error(), "needs Android 12") {
		t.Errorf("SetLocation on a device = %v", err)
	}
}

// TB_DEBUG logs each request's time and each action's result to stderr.
func TestDebugLogging(t *testing.T) {
	a := actionAgent(t)
	d := newDriver(t, a)
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr, debug = f, true
	defer func() { os.Stderr, debug = stderr, false }()
	d.Tap(ctxT(t), 1, 2, driver.ActionOpts{})
	d.Foreground(ctxT(t))
	os.Stderr = stderr
	f.Close()
	b, _ := os.ReadFile(f.Name())
	log := string(b)
	if !strings.Contains(log, "[tb] tap map[") || !strings.Contains(log, "settle changed 340ms, seq 21") {
		t.Errorf("no action line in:\n%s", log)
	}
	if !strings.Contains(log, "[tb] foreground ") || strings.Contains(log, "[tb] tap 0ms") {
		t.Errorf("request timing lines wrong:\n%s", log)
	}
}
