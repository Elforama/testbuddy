package ios

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

// probePort asks a runner for its info (without the keyboard query) and adopt takes its size.
func TestProbePortAndAdopt(t *testing.T) {
	r := newFakeRunner(t)
	d := newIOSDriver(t, r)
	if w, h := d.Size(); w != 393 || h != 852 || d.Port() != r.port() || d.Conn() == nil {
		t.Errorf("adopted %dx%d port %d", w, h, d.Port())
	}
	if kb := r.last("info")["keyboard"]; kb != false {
		t.Errorf("info keyboard = %v, want false (skips the keyboard query)", kb)
	}
	c, in := probePort(r.port())
	if c == nil || in.UDID != testUDID || in.Build != BuildID() || in.Agent != "test" {
		t.Fatalf("probePort = %v, %+v", c, in)
	}
	c.Close()
}

// adopt falls back to the greeting's size when info has none.
func TestAdoptSizeFromHello(t *testing.T) {
	r := newFakeRunner(t)
	r.on("info", func(map[string]any) map[string]any { return map[string]any{"udid": testUDID} })
	d := newIOSDriver(t, r)
	if w, h := d.Size(); w != 393 || h != 852 {
		t.Errorf("Size = %dx%d, want the greeting's 393x852", w, h)
	}
}

// probePort returns nothing for a closed port or a runner that fails info.
func TestProbePortNothing(t *testing.T) {
	if c, in := probePort(1); c != nil || in != nil {
		t.Error("closed port answered")
	}
	r := newFakeRunner(t)
	r.on("info", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "starting"} })
	if c, in := probePort(r.port()); c != nil || in != nil {
		t.Error("a failing runner counted")
	}
}

const iosTree = `{"seq": 31, "keyboard": %s, "timing": {"snapMs": 5},
 "windows": [
  {"id": 812, "type": 1, "active": true, "focused": true, "pkg": "com.example.app", "title": "Example", "b": %s,
   "nodes": [
    {"d": 0, "b": [0, 0, 393, 852], "c": "Application", "cd": "Example", "f": 4098},
    {"d": 1, "b": [0, 0, 393, 852], "c": "Window", "f": 4098},
    {"d": 2, "b": [16, 100, 377, 144], "c": "TextField", "t": "hi", "v": "hi", "cd": "Email", "id": "email", "h": "you@example.com", "f": 5383},
    {"d": 2, "b": [16, 200, 377, 244], "c": "Switch", "cd": "Remember me", "v": "1", "f": 4179},
    {"d": 2, "b": [16, 300, 377, 900], "c": "Table", "f": 4106}
   ]},
  {"id": 0, "type": 2, "pkg": "com.example.app", "b": [0, 516, 393, 852],
   "nodes": [{"d": 0, "b": [0, 516, 393, 852], "c": "Keyboard", "f": 4098}, {"d": 1, "b": [0, 520, 39, 562], "c": "Key", "cd": "q", "f": 4099}]},
  {"id": 1, "type": 3, "pkg": "com.apple.springboard", "b": [0, 0, 393, 852], "nodes": []}
 ]}`

func treeHandler(keyboard, bounds string) func(map[string]any) map[string]any {
	return func(map[string]any) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.Replace(strings.Replace(iosTree, "%s", keyboard, 1), "%s", bounds, 1)), &m); err != nil {
			panic(err)
		}
		return m
	}
}

// Screen links the runner's tree: app, keyboard and system windows, flags, values; seq is the runner's own.
func TestScreen(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tree", treeHandler("true", "[0, 0, 393, 852]"))
	d := newIOSDriver(t, r)
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Platform != "ios" || s.Width != 393 || s.Height != 852 || s.Seq != 31 || !s.Keyboard {
		t.Errorf("header %+v", s)
	}
	var types []string
	for _, w := range s.Windows {
		types = append(types, w.Type)
	}
	if !reflect.DeepEqual(types, []string{"app", "keyboard", "system"}) {
		t.Errorf("types %v", types)
	}
	if s.Foreground() != "com.example.app" || s.Windows[0].ID != 812 || s.Windows[0].Title != "Example" {
		t.Errorf("app window %+v", s.Windows[0])
	}
	if len(s.Nodes) != 7 {
		t.Fatalf("%d nodes", len(s.Nodes))
	}
	field, sw, table := s.Nodes[2], s.Nodes[3], s.Nodes[4]
	if field.Parent != s.Nodes[1] || field.Value != "hi" || field.Hint != "you@example.com" || !field.Editable || !field.Focused || field.Label() != "hi" {
		t.Errorf("field %+v", field)
	}
	if !sw.Checked || !sw.Checkable || sw.Label() != "Remember me" {
		t.Errorf("switch %+v", sw)
	}
	if !table.Scrollable || table.Clickable {
		t.Errorf("table %+v", table)
	}
	key := s.Nodes[6]
	if key.Window != 1 || key.Desc != "q" || key.Parent != s.Nodes[5] {
		t.Errorf("key %+v", key)
	}
}

// A landscape app window sets the screen size, and Size follows it.
func TestScreenLandscape(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tree", treeHandler("false", "[0, 0, 852, 393]"))
	d := newIOSDriver(t, r)
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Width != 852 || s.Height != 393 || s.Keyboard {
		t.Errorf("screen %dx%d keyboard %v", s.Width, s.Height, s.Keyboard)
	}
	if w, h := d.Size(); w != 852 || h != 393 {
		t.Errorf("Size = %dx%d after a landscape read", w, h)
	}
}

// Without a usable app window (empty bounds) the last known size stays.
func TestScreenKeepsSizeWithoutAppBounds(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tree", func(map[string]any) map[string]any {
		return map[string]any{"seq": 1, "windows": []any{
			map[string]any{"type": 3, "b": []int{0, 0, 300, 300}},
			map[string]any{"type": 1, "b": []int{0, 0, 0, 0}},
		}}
	})
	d := newIOSDriver(t, r)
	s, err := d.Screen(ctxT(t))
	if err != nil || s.Width != 393 || s.Height != 852 {
		t.Errorf("Screen = %dx%d, %v", s.Width, s.Height, err)
	}
}

// Screen returns decode and runner errors.
func TestScreenErrors(t *testing.T) {
	r := newFakeRunner(t)
	d := newIOSDriver(t, r)
	r.on("tree", func(map[string]any) map[string]any { return map[string]any{"windows": "x"} })
	if _, err := d.Screen(ctxT(t)); err == nil {
		t.Error("bad windows accepted")
	}
	r.on("tree", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "no app"} })
	var ae *wire.AgentError
	if _, err := d.Screen(ctxT(t)); !errors.As(err, &ae) {
		t.Errorf("err = %v", err)
	}
}

func actionRunner(t *testing.T) *fakeRunner {
	r := newFakeRunner(t)
	for _, op := range []string{"tap", "doubleTap", "press", "swipe", "type", "erase", "key", "hideKeyboard"} {
		r.on(op, func(map[string]any) map[string]any {
			return map[string]any{"result": "changed", "injectMs": 30, "settleMs": 410, "seq": 77, "probed": true}
		})
	}
	return r
}

// Each input op sends its params plus settle params; the result's seq is the runner's.
func TestActions(t *testing.T) {
	r := actionRunner(t)
	d := newIOSDriver(t, r)
	ctx := ctxT(t)
	cases := []struct {
		name string
		run  func() (driver.ActionResult, error)
		op   string
		want map[string]any
		nope []string
	}{
		{"tap", func() (driver.ActionResult, error) { return d.Tap(ctx, 10, 20, driver.ActionOpts{}) },
			"tap", map[string]any{"x": 10.0, "y": 20.0, "noChange": 1000.0}, []string{"hold", "react", "settle"}},
		{"tap hold", func() (driver.ActionResult, error) {
			return d.Tap(ctx, 1, 2, driver.ActionOpts{Hold: 20 * time.Millisecond, React: true})
		}, "tap", map[string]any{"hold": 20.0, "react": true}, nil},
		{"double tap", func() (driver.ActionResult, error) { return d.DoubleTap(ctx, 3, 4, driver.ActionOpts{NoSettle: true}) },
			"doubleTap", map[string]any{"x": 3.0, "y": 4.0, "settle": false}, nil},
		{"long press", func() (driver.ActionResult, error) { return d.LongPress(ctx, 5, 6, 2*time.Second, driver.ActionOpts{}) },
			"press", map[string]any{"ms": 2000.0}, nil},
		{"swipe", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, 250*time.Millisecond, driver.ActionOpts{})
		},
			"swipe", map[string]any{"x1": 1.0, "y2": 4.0, "ms": 250.0}, []string{"drag", "rest"}},
		// Issue #127: a swipe that places what it starts on asks the agent to rest the finger.
		{"resting swipe", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, 400*time.Millisecond, driver.ActionOpts{Rest: true})
		}, "swipe", map[string]any{"rest": true, "ms": 400.0}, []string{"drag"}},
		{"drag", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, 0, driver.ActionOpts{Drag: true, Timeout: time.Second})
		}, "swipe", map[string]any{"drag": true, "timeout": 1000.0}, nil},
		{"type", func() (driver.ActionResult, error) { return d.TypeText(ctx, "hello", driver.ActionOpts{}) },
			"type", map[string]any{"text": "hello", "noChange": 300.0}, nil},
		{"erase", func() (driver.ActionResult, error) { return d.EraseText(ctx, 3, driver.ActionOpts{NoChangeMs: 100}) },
			"erase", map[string]any{"n": 3.0, "noChange": 100.0}, nil},
		{"key delete", func() (driver.ActionResult, error) { return d.PressKey(ctx, "Delete", driver.ActionOpts{}) },
			"key", map[string]any{"code": 67.0}, nil},
		{"key home", func() (driver.ActionResult, error) { return d.PressKey(ctx, "HOME", driver.ActionOpts{}) },
			"key", map[string]any{"code": 3.0}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := c.run()
			if err != nil {
				t.Fatal(err)
			}
			if want := (driver.ActionResult{Probed: true, Settle: driver.Changed, InjectMs: 30, SettleMs: 410, Seq: 77}); res != want {
				t.Errorf("result %+v", res)
			}
			req := r.last(c.op)
			for k, v := range c.want {
				if !reflect.DeepEqual(req[k], v) {
					t.Errorf("%s = %#v, want %#v (%v)", k, req[k], v, req)
				}
			}
			for _, k := range c.nope {
				if _, ok := req[k]; ok {
					t.Errorf("unexpected %q in %v", k, req)
				}
			}
		})
	}
}

// Keys iOS has no equivalent for fail before anything is sent.
func TestPressKeyUnsupported(t *testing.T) {
	r := actionRunner(t)
	d := newIOSDriver(t, r)
	r.reset()
	_, err := d.PressKey(ctxT(t), "remote_dpad_up", driver.ActionOpts{})
	if err == nil || err.Error() != `key "remote_dpad_up" is not supported on iOS` || len(r.ops()) != 0 {
		t.Errorf("err = %v, sent %v", err, r.ops())
	}
}

// HideKeyboard is one action op with the default settle params.
func TestHideKeyboard(t *testing.T) {
	r := actionRunner(t)
	d := newIOSDriver(t, r)
	if err := d.HideKeyboard(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if num(t, r.last("hideKeyboard"), "noChange") != 1000 {
		t.Errorf("request %v", r.last("hideKeyboard"))
	}
	r.on("hideKeyboard", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "no keyboard"} })
	if err := d.HideKeyboard(ctxT(t)); err == nil {
		t.Error("error not reported")
	}
}

// The context's deadline caps the request: an unanswered tap fails when it ends.
func TestCallUsesContextDeadline(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tap", func(map[string]any) map[string]any { return noReply })
	d := newIOSDriver(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	if _, err := d.Tap(ctx, 1, 1, driver.ActionOpts{}); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("err = %v", err)
	}
	if time.Since(t0) > 2*time.Second {
		t.Error("the context deadline was not applied")
	}
}

// Settle and WaitSettled send their params and return the runner's result.
func TestSettle(t *testing.T) {
	r := newFakeRunner(t)
	r.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "timeout"} })
	r.on("quiet", func(map[string]any) map[string]any { return map[string]any{"quiet": true} })
	d := newIOSDriver(t, r)
	res, err := d.Settle(ctxT(t), 9, 400, 3*time.Second)
	if err != nil || res != driver.Restless {
		t.Errorf("Settle = %q, %v", res, err)
	}
	q := r.last("settle")
	if num(t, q, "since") != 9 || num(t, q, "noChange") != 400 || num(t, q, "timeout") != 3000 {
		t.Errorf("settle request %v", q)
	}
	if err := d.WaitSettled(ctxT(t), 2*time.Second); err != nil || num(t, r.last("quiet"), "timeout") != 2000 {
		t.Errorf("WaitSettled: %v %v", err, r.last("quiet"))
	}
	r.on("settle", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if res, err := d.Settle(ctxT(t), 0, 1, time.Second); err == nil || res != "" {
		t.Errorf("failed Settle = %q, %v", res, err)
	}
}

// Screenshot defaults to PNG and returns the binary payload.
func TestScreenshot(t *testing.T) {
	r := newFakeRunner(t)
	img := []byte("\xff\xd8\xffjpeg\n\x00bytes")
	r.on("screenshot", func(map[string]any) map[string]any { return map[string]any{"format": "jpeg", "_bin": img} })
	d := newIOSDriver(t, r)
	b, err := d.Screenshot(ctxT(t), 0.25, "")
	if err != nil || string(b) != string(img) {
		t.Fatalf("Screenshot = %q, %v", b, err)
	}
	if q := r.last("screenshot"); q["format"] != "png" || num(t, q, "scale") != 0.25 {
		t.Errorf("request %v", q)
	}
	r.on("screenshot", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if _, err := d.Screenshot(ctxT(t), 1, "jpeg"); err == nil {
		t.Error("error not reported")
	}
}

// SetOrientation maps flow names to the protocol's and passes anything else through unchanged.
func TestSetOrientation(t *testing.T) {
	r := newFakeRunner(t)
	r.on("rotate", func(map[string]any) map[string]any { return nil })
	d := newIOSDriver(t, r)
	for in, want := range map[string]string{"PORTRAIT": "portrait", "landscape_left": "landscapeLeft", "LANDSCAPE_RIGHT": "landscapeRight",
		"UPSIDE_DOWN": "upsideDown", "landscapeRight": "landscapeRight", "landscape": "landscape"} {
		if err := d.SetOrientation(ctxT(t), in); err != nil {
			t.Fatal(err)
		}
		if got := r.last("rotate")["to"]; got != want {
			t.Errorf("SetOrientation(%q) sent %v, want %q", in, got, want)
		}
	}
}

// issue #115: setOrientation on a cancelled run reports the cancellation and sends no
// rotation (the request's limit follows only a deadline, so it used to go out and succeed).
func TestSetOrientationCancelled(t *testing.T) {
	r := newFakeRunner(t)
	r.on("rotate", func(map[string]any) map[string]any { return nil })
	d := newIOSDriver(t, r)
	ctx, cancel := context.WithCancel(ctxT(t))
	cancel()
	if err := d.SetOrientation(ctx, "LANDSCAPE_LEFT"); !errors.Is(err, context.Canceled) {
		t.Errorf("issue #115: SetOrientation on a cancelled run = %v, want context.Canceled", err)
	}
	if n := len(r.requests("rotate")); n != 0 {
		t.Errorf("issue #115: %d rotations sent on a cancelled run, want none", n)
	}
}

// The runner-only ops: foreground, app state, XCTest launch, stopping the runner, raw ops.
func TestRunnerOps(t *testing.T) {
	r := newFakeRunner(t)
	r.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": "com.apple.springboard", "ms": 3} })
	r.on("appState", func(q map[string]any) map[string]any { return map[string]any{"state": 4} })
	r.on("launch", func(map[string]any) map[string]any { return map[string]any{"state": 4} })
	r.on("exit", func(map[string]any) map[string]any { return nil })
	r.on("axStats", func(map[string]any) map[string]any { return map[string]any{"counts": map[string]int{"-1": 3}} })
	d := newIOSDriver(t, r)
	ctx := ctxT(t)
	if p, err := d.Foreground(ctx); err != nil || p != "com.apple.springboard" {
		t.Errorf("Foreground = %q, %v", p, err)
	}
	if s, err := d.AppState(ctx, "com.example.app"); err != nil || s != 4 || r.last("appState")["bundleId"] != "com.example.app" {
		t.Errorf("AppState = %d, %v", s, err)
	}
	if err := d.LaunchWithXCTest(ctx, "com.example.app", []string{"-a", "1"}, map[string]string{"K": "V"}); err != nil {
		t.Fatal(err)
	}
	l := r.last("launch")
	if l["bundleId"] != "com.example.app" || !reflect.DeepEqual(l["args"], []any{"-a", "1"}) || !reflect.DeepEqual(l["env"], map[string]any{"K": "V"}) {
		t.Errorf("launch request %v", l)
	}
	raw, err := d.Raw(ctx, "axStats", nil)
	if err != nil || !strings.Contains(string(raw), `"counts":{"-1":3}`) {
		t.Errorf("Raw = %s, %v", raw, err)
	}
	if err := d.StopRunner(); err != nil || len(r.requests("exit")) != 1 {
		t.Errorf("StopRunner = %v", err)
	}
	for _, op := range []string{"foreground", "appState"} {
		r.on(op, func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	}
	if _, err := d.Foreground(ctx); err == nil {
		t.Error("Foreground error not reported")
	}
	if _, err := d.AppState(ctx, "x"); err == nil {
		t.Error("AppState error not reported")
	}
	if _, err := d.Raw(ctx, "nope", nil); err == nil {
		t.Error("Raw error not reported")
	}
}

// WaitForApp asks the runner to wait for the app, and fails when it does not show.
func TestWaitForApp(t *testing.T) {
	r := newFakeRunner(t)
	var missing atomic.Bool
	r.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": !missing.Load()} })
	d := newIOSDriver(t, r)
	if err := d.WaitForApp(ctxT(t), "com.example.app", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if q := r.last("awaitPackage"); q["pkg"] != "com.example.app" || num(t, q, "timeout") != 2000 {
		t.Errorf("request %v", q)
	}
	missing.Store(true)
	if err := d.WaitForApp(ctxT(t), "com.example.app", time.Second); err == nil || err.Error() != "com.example.app did not come to the foreground within 1s" {
		t.Errorf("err = %v", err)
	}
	r.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if err := d.WaitForApp(ctxT(t), "com.example.app", time.Second); err == nil {
		t.Error("runner error not reported")
	}
}

// Events forwards the runner's pushes with their package.
func TestEvents(t *testing.T) {
	r := newFakeRunner(t)
	d := newIOSDriver(t, r)
	events, cancel := d.Events()
	defer cancel()
	r.push(map[string]any{"ev": "changed", "seq": 5, "pkg": "com.example.app"})
	if e := waitEvent(t, events, "changed"); e.Seq != 5 || e.Pkg != "com.example.app" {
		t.Errorf("event %+v", e)
	}
	r.push(map[string]any{"ev": "settled", "seq": 5})
	if e := waitEvent(t, events, "settled"); e.Seq != 5 {
		t.Errorf("event %+v", e)
	}
	if d.Seq() != 5 {
		t.Errorf("Seq = %d", d.Seq())
	}
}

// A runner that is gone and cannot be started again ends in "disconnected".
func TestEventsDisconnected(t *testing.T) {
	ts := stubTools(t)
	ts.hide() // no xcodebuild: the runner cannot be restarted
	defer func(v time.Duration) { reconnectFor = v }(reconnectFor)
	reconnectFor = 1500 * time.Millisecond
	r := newFakeRunner(t)
	d := newIOSDriver(t, r)
	events, cancel := d.Events()
	defer cancel()
	r.close()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind == "disconnected" {
				// issue #54: the run opens the simulator again instead of reusing this driver
				if err := d.Lost(); err == nil {
					t.Error("Lost = nil after reconnecting gave up")
				}
				return
			}
		case <-timeout:
			t.Fatal("no disconnected event")
		}
	}
}

// Issue #53: a reconnect, a runner restart included, had reconnectFor (60 s) in CI too, though
// a runner can take a minute to start on a slow CI Mac. In CI mode it gets twice that; outside
// CI the budget is reconnectFor, as before.
func TestReconnectBudgetInCI(t *testing.T) {
	for _, tc := range []struct {
		ci       string
		min, max time.Duration
	}{
		{"1", 1900 * time.Millisecond, 8 * time.Second},
		{"0", 0, 1900 * time.Millisecond},
	} {
		t.Run("TB_CI="+tc.ci, func(t *testing.T) {
			t.Setenv("TB_CI", tc.ci)
			ts := stubTools(t)
			ts.hide() // no xcodebuild: the runner cannot be restarted, every attempt fails
			defer func(v time.Duration) { reconnectFor = v }(reconnectFor)
			reconnectFor = time.Second
			r := newFakeRunner(t)
			d := newIOSDriver(t, r)
			events, cancel := d.Events()
			defer cancel()
			t0 := time.Now()
			r.close()
			timeout := time.After(10 * time.Second)
			for {
				select {
				case e := <-events:
					if e.Kind != "disconnected" {
						continue
					}
					if took := time.Since(t0); took < tc.min || took > tc.max {
						t.Errorf("gave up reconnecting after %s, want %s to %s", took.Round(time.Millisecond), tc.min, tc.max)
					}
					return
				case <-timeout:
					t.Fatal("no disconnected event")
				}
			}
		})
	}
}

// Cancelling ends the stream without a "disconnected"; cancelling twice is safe.
func TestEventsCancel(t *testing.T) {
	r := newFakeRunner(t)
	d := newIOSDriver(t, r)
	events, cancel := d.Events()
	cancel()
	cancel()
	r.push(map[string]any{"ev": "changed", "seq": 1})
	select {
	case e := <-events:
		t.Errorf("event %+v after cancel", e)
	case <-time.After(200 * time.Millisecond):
	}
}
