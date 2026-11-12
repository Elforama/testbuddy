package android

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/wire"
)

func (d *Driver) SetLocation(ctx context.Context, lat, lon float64) error {
	d.stateMu.Lock()
	sdk := d.sdk
	d.stateMu.Unlock()
	if sdk < 31 { // 0: not known yet
		// No test providers from the shell before Android 12: the emulator's own GPS.
		if d.info.Emulator {
			_, err := d.adb.Run(ctx, "emu", "geo", "fix", strconv.FormatFloat(lon, 'f', -1, 64), strconv.FormatFloat(lat, 'f', -1, 64))
			return err
		}
		return fmt.Errorf("setLocation needs Android 12 or later, or an emulator")
	}
	return d.mockLocation(ctx, lat, lon)
}

// Location on Android 12+: the shell's test providers (gps, network and fused), which apps read
// through the location service whatever provider they ask for. An emulator's own GPS
// (`emu geo fix`) does not reach apps that use the fused provider. The agent adds the providers,
// sets the point and sends it again every locationResend while the run lasts (a watch that asks
// for updates at an interval gets them, and the fused provider forgets a fix that does not come
// again): one "location" request per point, nothing in between (issue #72). The repeats end,
// and the providers go, at Close or Release ("off"), and when this connection closes: a tb that
// is killed leaves nothing repeating. A new connection (a reconnect, a restarted agent) gets the
// point again in adopt.
const locationResend = time.Second

// locPoint is the point setLocation last set on the test providers.
type locPoint struct{ lat, lon float64 }

func (d *Driver) mockLocation(ctx context.Context, lat, lon float64) error {
	d.locMu.Lock()
	defer d.locMu.Unlock()
	// Recorded first: a reconnect while the request is on its way sends it in adopt too.
	d.locAt.Store(&locPoint{lat, lon})
	_, err := d.call(ctx, "location", locationParams(lat, lon), 10*time.Second)
	if err != nil {
		return fmt.Errorf("setLocation: %w", err)
	}
	return nil
}

func locationParams(lat, lon float64) map[string]any {
	return map[string]any{"lat": lat, "lon": lon, "everyMs": locationResend.Milliseconds()}
}

// resumeLocation sets the point again on a new connection c (adopt): the agent ended the
// repeats when the old connection closed, or a restarted agent never had them. Sent on c itself:
// adopt runs inside reconnect, where d.call could reconnect again.
func (d *Driver) resumeLocation(c *wire.Conn) {
	if p := d.locAt.Load(); p != nil {
		c.Call("location", locationParams(p.lat, p.lon), 10*time.Second)
	}
}

// stopLocation ends the repeats and removes the test providers, so the device reports its
// own location again.
func (d *Driver) stopLocation() {
	d.locMu.Lock()
	defer d.locMu.Unlock()
	if d.locAt.Swap(nil) == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.call(ctx, "location", map[string]any{"off": true}, 5*time.Second)
}

// intExtra is an am start integer extra: --ei when it fits an int32, else --el.
func intExtra(k string, v int64) []string {
	if v >= math.MinInt32 && v <= math.MaxInt32 {
		return []string{"--ei", shellQuote(k), strconv.FormatInt(v, 10)}
	}
	return []string{"--el", shellQuote(k), strconv.FormatInt(v, 10)}
}

func (d *Driver) SetOrientation(ctx context.Context, orientation string) error {
	// Flow names (LANDSCAPE_LEFT) and protocol names (landscapeLeft) map alike; others go lower-cased.
	key := strings.ToUpper(strings.ReplaceAll(orientation, "_", ""))
	to := map[string]string{"PORTRAIT": "portrait", "LANDSCAPELEFT": "landscapeLeft", "LANDSCAPERIGHT": "landscapeRight", "UPSIDEDOWN": "upsideDown"}[key]
	if to == "" {
		to = strings.ToLower(orientation)
	}
	_, err := d.call(ctx, "rotate", map[string]any{"to": to}, 5*time.Second)
	if err != nil {
		return err
	}
	// The display turns after the request returns: until it has, the agent reports the old
	// size, and swipes from it would land off a screen that turned. Ask again (every 100 ms,
	// up to rotateWait) until the size has the orientation asked for.
	tall := map[string]bool{"portrait": true, "upsideDown": true}[to]
	wide := strings.HasPrefix(to, "landscape")
	for deadline := time.Now().Add(rotateWait); ; {
		d.refreshSize(ctx)
		w, h := d.Size()
		if (!tall && !wide) || (tall && h >= w) || (wide && w > h) || !time.Now().Before(deadline) || ctx.Err() != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// rotateWait bounds the wait for the display to turn after setOrientation.
var rotateWait = 3 * time.Second

func (d *Driver) refreshSize(ctx context.Context) {
	if m, err := d.call(ctx, "info", nil, 3*time.Second); err == nil {
		var h hello
		m.Decode(&h)
		if h.W > 0 {
			d.stateMu.Lock()
			d.w, d.h = h.W, h.H
			d.stateMu.Unlock()
		}
	}
}

func (d *Driver) SetAirplaneMode(ctx context.Context, on bool) error {
	v := "disable"
	if on {
		v = "enable"
	}
	_, err := d.shOK(ctx, "cmd connectivity airplane-mode "+v)
	return err
}

func (d *Driver) ToggleAirplaneMode(ctx context.Context) error {
	out, err := d.shOK(ctx, "settings get global airplane_mode_on")
	if err != nil {
		return err
	}
	return d.SetAirplaneMode(ctx, strings.TrimSpace(out) != "1")
}

func (d *Driver) AddMedia(ctx context.Context, paths []string) error {
	for _, p := range paths {
		remote := "/sdcard/Pictures/" + filepath.Base(p)
		if _, err := d.adb.Run(ctx, "push", p, remote); err != nil {
			return err
		}
		d.Sh(ctx, "am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE -d "+shellQuote("file://"+remote)+" >/dev/null; content call --uri content://media/external/file --method scan_file --arg "+shellQuote(remote)+" >/dev/null 2>&1")
	}
	return nil
}

// DisableAnimations turns the system animation scales off and returns a function that restores them.
func (d *Driver) DisableAnimations(ctx context.Context) (restore func(), err error) {
	keys := []string{"window_animation_scale", "transition_animation_scale", "animator_duration_scale"}
	var get []string
	for _, k := range keys {
		get = append(get, "settings get global "+k)
	}
	out, err := d.shOK(ctx, strings.Join(get, "; "))
	if err != nil {
		return func() {}, err
	}
	old := strings.Fields(out)
	var set []string
	for _, k := range keys {
		set = append(set, "settings put global "+k+" 0")
	}
	if _, err := d.shOK(ctx, strings.Join(set, "; ")); err != nil {
		return func() {}, err
	}
	// The agent treats frames drawn on a never-still screen differently with animations off.
	d.call(ctx, "animations", map[string]any{"on": false}, 5*time.Second)
	return func() {
		var cmds []string
		on := false
		for i, k := range keys {
			v := "1"
			if i < len(old) && old[i] != "null" {
				v = old[i]
			}
			if f, err := strconv.ParseFloat(v, 64); k == "animator_duration_scale" && (err != nil || f != 0) {
				on = true
			}
			cmds = append(cmds, "settings put global "+k+" "+v)
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.Sh(c, strings.Join(cmds, "; "))
		d.call(c, "animations", map[string]any{"on": on}, 5*time.Second)
	}, nil
}

// raw JSON helper for diagnostics
func (d *Driver) Raw(ctx context.Context, op string, p map[string]any) (json.RawMessage, error) {
	m, err := d.call(ctx, op, p, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Raw, nil
}

// SetDarkMode switches the system's night mode (`cmd uimode night`), which apps follow.
func (d *Driver) SetDarkMode(ctx context.Context, on bool) error {
	v := "no"
	if on {
		v = "yes"
	}
	_, err := d.shOK(ctx, "cmd uimode night "+v)
	return err
}

// DarkMode reads the system's night mode: "Night mode: yes" when dark.
func (d *Driver) DarkMode(ctx context.Context) (bool, error) {
	out, err := d.shOK(ctx, "cmd uimode night")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "yes"), nil
}
