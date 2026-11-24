package android

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elforama/testbuddy/internal/wire"
)

func (d *Driver) SetLocation(ctx context.Context, lat, lon float64) error {
	d.stateMu.Lock()
	sdk := d.sdk
	d.stateMu.Unlock()
	if sdk < 31 {
		if d.info.Emulator {
			_, err := d.adb.Run(ctx, "emu", "geo", "fix", strconv.FormatFloat(lon, 'f', -1, 64), strconv.FormatFloat(lat, 'f', -1, 64))
			return err
		}
		return fmt.Errorf("setLocation needs Android 12 or later, or an emulator")
	}
	return d.mockLocation(ctx, lat, lon)
}

const locationResend = time.Second

type locPoint struct{ lat, lon float64 }

func (d *Driver) mockLocation(ctx context.Context, lat, lon float64) error {
	d.locMu.Lock()
	defer d.locMu.Unlock()
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

func (d *Driver) resumeLocation(c *wire.Conn, leftOn bool) {
	d.resumeMu.Lock()
	p := d.locAt.Load()
	if p == nil {
		d.resumeMu.Unlock()
		if leftOn {
			c.Call("location", map[string]any{"off": true}, 5*time.Second)
		}
		return
	}
	done := make(chan struct{})
	d.resumeDone = done
	d.resumeMu.Unlock()
	if h := resumeHook.Load(); h != nil {
		(*h)()
	}
	sent := p
	c.Call("location", locationParams(p.lat, p.lon), 10*time.Second)
	for {
		d.resumeMu.Lock()
		q := d.locAt.Load()
		if q == sent {
			d.resumeDone = nil
			d.resumeMu.Unlock()
			close(done)
			return
		}
		d.resumeMu.Unlock()
		if q == nil {
			c.Call("location", map[string]any{"off": true}, 5*time.Second)
		} else {
			c.Call("location", locationParams(q.lat, q.lon), 10*time.Second)
		}
		sent = q
	}
}

func (d *Driver) waitResume(ctx context.Context) {
	d.resumeMu.Lock()
	done := d.resumeDone
	d.resumeMu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

var resumeHook atomic.Pointer[func()]

func (d *Driver) stopLocation() {
	d.locMu.Lock()
	defer d.locMu.Unlock()
	d.resumeMu.Lock()
	old := d.locAt.Swap(nil)
	resuming := d.resumeDone != nil
	d.resumeMu.Unlock()
	if old == nil || resuming {
		return
	}
	if c := d.conn(); c != nil && c.Abandoned() > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d.call(ctx, "location", map[string]any{"off": true}, 5*time.Second)
}

func intExtra(k string, v int64) []string {
	if v >= math.MinInt32 && v <= math.MaxInt32 {
		return []string{"--ei", shellQuote(k), strconv.FormatInt(v, 10)}
	}
	return []string{"--el", shellQuote(k), strconv.FormatInt(v, 10)}
}

func (d *Driver) SetOrientation(ctx context.Context, orientation string) error {
	key := strings.ToUpper(strings.ReplaceAll(orientation, "_", ""))
	to := map[string]string{"PORTRAIT": "portrait", "LANDSCAPELEFT": "landscapeLeft", "LANDSCAPERIGHT": "landscapeRight", "UPSIDEDOWN": "upsideDown"}[key]
	if to == "" {
		to = strings.ToLower(orientation)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := d.call(ctx, "rotate", map[string]any{"to": to}, 5*time.Second)
	if err != nil {
		return err
	}
	tall := map[string]bool{"portrait": true, "upsideDown": true}[to]
	wide := strings.HasPrefix(to, "landscape")
	for deadline := time.Now().Add(rotateWait); ; {
		d.refreshSize(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		w, h := d.Size()
		if (!tall && !wide) || (tall && h >= w) || (wide && w > h) || !time.Now().Before(deadline) {
			return nil
		}
		t := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

var rotateWait = 3 * time.Second

func (d *Driver) refreshSize(ctx context.Context) {
	if m, err := d.call(ctx, "info", nil, infoWait); err == nil {
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

var (
	animMu         sync.Mutex
	originalScales = map[string][]string{}
)

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
	serial := d.adb.Serial
	animMu.Lock()
	if kept, ok := originalScales[serial]; ok {
		old = kept
	} else {
		originalScales[serial] = old
	}
	animMu.Unlock()
	restore = func() {
		animMu.Lock()
		delete(originalScales, serial)
		animMu.Unlock()
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
		d.restoreSettings(strings.Join(cmds, "; "))
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.call(c, "animations", map[string]any{"on": on}, 5*time.Second)
	}
	var set []string
	for _, k := range keys {
		set = append(set, "settings put global "+k+" 0")
	}
	if err := ctx.Err(); err != nil {
		return func() {}, err
	}
	setCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), animationSetWait)
	_, err = d.shOK(setCtx, strings.Join(set, "; "))
	cancel()
	if err != nil {
		return restore, err
	}
	if err := ctx.Err(); err != nil {
		return restore, err
	}
	d.call(ctx, "animations", map[string]any{"on": false}, 5*time.Second)
	return restore, nil
}

const animationSetWait = 5 * time.Second

func (d *Driver) restoreSettings(cmd string) {
	try := func(run func(context.Context) error) bool {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return run(c) == nil
	}
	if try(func(c context.Context) error { _, err := d.adb.Shell(c, cmd); return err }) {
		return
	}
	try(func(c context.Context) error { _, _, err := d.Sh(c, cmd); return err })
}

func (d *Driver) Raw(ctx context.Context, op string, p map[string]any) (json.RawMessage, error) {
	m, err := d.call(ctx, op, p, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Raw, nil
}

func (d *Driver) SetDarkMode(ctx context.Context, on bool) error {
	v := "no"
	if on {
		v = "yes"
	}
	_, err := d.shOK(ctx, "cmd uimode night "+v)
	return err
}

func (d *Driver) DarkMode(ctx context.Context) (bool, error) {
	out, err := d.shOK(ctx, "cmd uimode night")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "yes"), nil
}
