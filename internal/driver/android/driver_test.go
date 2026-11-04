package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/wire"
)

// dialHello reads the size and SDK from info and the version from the agent's greeting.
func TestDialHelloAndAdopt(t *testing.T) {
	a := newFakeAgent(t)
	a.greet(func(int) map[string]any {
		return map[string]any{"ev": "hello", "platform": "android", "version": "9.9.9", "w": 1, "h": 2, "seq": 0}
	})
	d := newDriver(t, a)
	if d.port != a.port() || d.w != 1080 || d.h != 2400 || d.sdk != 34 {
		t.Errorf("adopted port %d size %dx%d sdk %d", d.port, d.w, d.h, d.sdk)
	}
	if got := a.ops(); !reflect.DeepEqual(got, []string{"info", "ping"}) {
		t.Errorf("dialHello sent %v, want info then ping", got)
	}
	if d.Conn() == nil || d.Conn() != d.conn() {
		t.Error("Conn does not expose the current connection")
	}
	c, h, err := dialHello(a.port(), time.Second)
	if err != nil || h.Version != "9.9.9" || h.W != 1080 {
		t.Fatalf("dialHello = %+v, %v; want the greeting's version and info's size", h, err)
	}
	c.Close()
}

// dialHello fails (and closes the connection) when info or ping fails.
func TestDialHelloFailures(t *testing.T) {
	for _, op := range []string{"info", "ping"} {
		t.Run(op, func(t *testing.T) {
			a := newFakeAgent(t)
			a.on(op, func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "not ready"} })
			_, _, err := dialHello(a.port(), time.Second)
			var ae *wire.AgentError
			if !errors.As(err, &ae) || ae.Op != op {
				t.Errorf("err = %v, want the agent's %s error", err, op)
			}
		})
	}
	if _, _, err := dialHello(1, 100*time.Millisecond); err == nil {
		t.Error("dialing a closed port succeeded")
	}
}

// adopt enables NoopService (Compose sends accessibility events only with a service on) unless TB_NO_A11Y_SERVICE is set.
func TestAdoptEnablesNoopService(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "")
	a := newFakeAgent(t)
	newDriver(t, a)
	cmds := a.shCmds()
	if len(cmds) != 1 {
		t.Fatalf("sh calls %v, want one", cmds)
	}
	for _, want := range []string{
		"settings get secure enabled_accessibility_services",
		"*" + noopService + "*) ;;",
		`null|"") settings put secure enabled_accessibility_services ` + noopService,
		`settings put secure enabled_accessibility_services "$cur:` + noopService + `"`,
	} {
		if !strings.Contains(cmds[0], want) {
			t.Errorf("service command lacks %q:\n%s", want, cmds[0])
		}
	}
}

// TB_NO_A11Y_SERVICE keeps adopt from touching the accessibility settings.
func TestAdoptWithoutNoopService(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	newDriver(t, a)
	if cmds := a.shCmds(); len(cmds) != 0 {
		t.Errorf("sh calls %v, want none", cmds)
	}
}

// A restarted agent (its counter back at 0) continues the driver's count: seqBase moves past the last seq.
func TestAdoptKeepsSeqIncreasing(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "changed", "seq": 50})
	if e := waitEvent(t, events, "changed"); e.Seq != 50 {
		t.Fatalf("event seq %d, want 50", e.Seq)
	}
	if d.Seq() != 50 {
		t.Fatalf("Seq = %d", d.Seq())
	}
	b := newFakeAgent(t)
	c, h, err := dialHello(b.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	old := d.conn()
	d.adopt(c, b.port(), h)
	old.Close()
	if d.seqBase != 51 {
		t.Errorf("seqBase = %d, want 51", d.seqBase)
	}
	if d.Seq() != 51 {
		t.Errorf("Seq after restart = %d, want 51", d.Seq())
	}
	b.push(map[string]any{"ev": "changed", "seq": 3})
	if e := waitEvent(t, events, "changed"); e.Seq != 54 {
		t.Errorf("event seq after restart = %d, want 54", e.Seq)
	}
	b.push(map[string]any{"ev": "settled", "seq": 3})
	if e := waitEvent(t, events, "settled"); e.Seq != 54 {
		t.Errorf("settled seq = %d, want 54", e.Seq)
	}
	if d.port != b.port() {
		t.Errorf("port %d, want the new agent's", d.port)
	}
}

// Pushed events reach subscribers with their package; a closed driver's pump stops quietly.
func TestEventsFromAgent(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "changed", "seq": 7, "pkg": "com.example.app"})
	e := waitEvent(t, events, "changed")
	if e.Seq != 7 || e.Pkg != "com.example.app" {
		t.Errorf("event %+v", e)
	}
	a.push(map[string]any{"ev": "settled", "seq": 7})
	if e := waitEvent(t, events, "settled"); e.Seq != 7 || e.Pkg != "" {
		t.Errorf("event %+v", e)
	}
	d.Close()
	a.dropConns()
	select {
	case e := <-events:
		t.Errorf("closed driver sent %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

// reconnect does nothing when the dropped connection was already replaced.
func TestReconnectAlreadyReplaced(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	other, _, err := dialHello(a.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := d.reconnect(ctxT(t), other); err != nil {
		t.Errorf("reconnect = %v", err)
	}
	if d.conn() == other {
		t.Error("reconnect replaced the current connection")
	}
}

const treeJSON = `{
 "seq": 12, "keyboard": true,
 "windows": [
  {"id": 40, "type": 2, "active": false, "focused": false, "pkg": "com.google.android.inputmethod.latin", "b": [0, 1600, 1080, 2400],
   "nodes": [{"d": 0, "b": [0, 1600, 1080, 2400], "c": "android.widget.FrameLayout", "f": 4096}]},
  {"id": 7, "type": 1, "active": true, "focused": true, "pkg": "com.example.app", "title": "Example", "b": [0, 0, 1080, 2400],
   "nodes": [
    {"d": 0, "b": [0, 0, 1080, 2400], "c": "android.widget.FrameLayout", "f": 4098},
    {"d": 1, "b": [0, 0, 1080, 200], "c": "android.widget.TextView", "t": "Sign in", "id": "com.example.app:id/title", "f": 4098},
    {"d": 1, "b": [40, 400, 1040, 520], "c": "android.widget.EditText", "h": "Email", "t": "Email", "e": "Required", "f": 7431},
    {"d": 2, "b": [40, 400, 100, 520], "c": "android.widget.ImageView", "cd": "clear", "f": 4099},
    {"d": 1, "b": [40, 600, 1040, 720], "c": "android.widget.Switch", "t": "Remember", "sd": "on", "f": 4179}
   ]},
  {"id": 3, "type": 3, "pkg": "com.android.systemui", "b": [0, 0, 1080, 80], "nodes": []},
  {"id": 9, "type": 4, "pkg": "dev.overlay", "b": [0, 0, 10, 10]},
  {"id": 11, "type": 6, "pkg": "x", "b": [0, 0, 1, 1]}
 ]}`

// Screen turns the agent's tree into a linked ui.Screen: window types, depths into a tree, flags, keyboard.
func TestScreen(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any {
		var m map[string]any
		json.Unmarshal([]byte(treeJSON), &m)
		return m
	})
	d := newDriver(t, a)
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Platform != "android" || s.Width != 1080 || s.Height != 2400 || s.Seq != 12 || !s.Keyboard {
		t.Errorf("screen header %+v", s)
	}
	var types []string
	for _, w := range s.Windows {
		types = append(types, w.Type)
	}
	if !reflect.DeepEqual(types, []string{"keyboard", "app", "system", "overlay", "other"}) {
		t.Errorf("window types %v", types)
	}
	app := s.Windows[1]
	if app.ID != 7 || app.Package != "com.example.app" || app.Title != "Example" || !app.Active || !app.Focused || app.Bounds.B != 2400 {
		t.Errorf("app window %+v", app)
	}
	if s.Foreground() != "com.example.app" {
		t.Errorf("Foreground = %q", s.Foreground())
	}
	if len(s.Nodes) != 6 {
		t.Fatalf("%d nodes, want 6", len(s.Nodes))
	}
	root := app.Roots[0]
	if len(app.Roots) != 1 || len(root.Children) != 3 {
		t.Fatalf("app tree: %d roots, %d children", len(app.Roots), len(root.Children))
	}
	title, field, sw := root.Children[0], root.Children[1], root.Children[2]
	if title.Text != "Sign in" || title.ID != "com.example.app:id/title" || title.Parent != root || title.Window != 1 {
		t.Errorf("title %+v", title)
	}
	if !field.Editable || !field.Focused || !field.ShowingHint || field.Hint != "Email" || field.Error != "Required" || !field.Clickable {
		t.Errorf("field %+v", field)
	}
	if field.Label() != "" {
		t.Errorf("a field showing its hint has no label, got %q", field.Label())
	}
	if len(field.Children) != 1 || field.Children[0].Desc != "clear" || !field.Children[0].Clickable {
		t.Errorf("field's child %+v", field.Children)
	}
	if !sw.Checkable || !sw.Checked || sw.State != "on" || sw.Index != 5 {
		t.Errorf("switch %+v", sw)
	}
	if s.Nodes[0].Window != 0 || s.Nodes[0].Class != "android.widget.FrameLayout" {
		t.Errorf("first node is the keyboard's: %+v", s.Nodes[0])
	}
}

// Screen maps the tree's seq through seqBase like events do.
func TestScreenSeqUsesBase(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 4, "windows": []any{}} })
	d := newDriver(t, a)
	d.seqBase = 100
	s, err := d.Screen(ctxT(t))
	if err != nil || s.Seq != 104 || len(s.Windows) != 0 {
		t.Errorf("Screen = %+v, %v; want seq 104", s, err)
	}
}

// A malformed tree or an agent error is returned, not a partial screen.
func TestScreenErrors(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"windows": "nope"} })
	if s, err := d.Screen(ctxT(t)); err == nil {
		t.Errorf("bad windows: got %+v", s)
	}
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "no root"} })
	_, err := d.Screen(ctxT(t))
	var ae *wire.AgentError
	if !errors.As(err, &ae) || ae.Msg != "no root" {
		t.Errorf("agent error: %v", err)
	}
}

func actionAgent(t *testing.T) *fakeAgent {
	a := newFakeAgent(t)
	for _, op := range []string{"tap", "doubleTap", "press", "swipe", "type", "erase", "key"} {
		a.on(op, func(map[string]any) map[string]any {
			return map[string]any{"result": "changed", "injectMs": 2, "settleMs": 340, "seq": 21}
		})
	}
	return a
}

// Each input op sends its own params plus the settle params, and decodes the action result.
func TestActions(t *testing.T) {
	a := actionAgent(t)
	d := newDriver(t, a)
	ctx := ctxT(t)
	d.seqBase = 1000
	cases := []struct {
		name string
		run  func() (driver.ActionResult, error)
		op   string
		want map[string]any
		nope []string
	}{
		{"tap", func() (driver.ActionResult, error) { return d.Tap(ctx, 10, 20, driver.ActionOpts{}) },
			"tap", map[string]any{"x": 10.0, "y": 20.0, "noChange": 1000.0}, []string{"settle", "react", "timeout"}},
		{"tap react", func() (driver.ActionResult, error) {
			return d.Tap(ctx, 1, 2, driver.ActionOpts{React: true, Timeout: 3 * time.Second})
		}, "tap", map[string]any{"react": true, "timeout": 3000.0}, nil},
		{"double tap", func() (driver.ActionResult, error) { return d.DoubleTap(ctx, 5, 6, driver.ActionOpts{NoSettle: true}) },
			"doubleTap", map[string]any{"x": 5.0, "y": 6.0, "settle": false}, nil},
		{"long press", func() (driver.ActionResult, error) {
			return d.LongPress(ctx, 7, 8, 1500*time.Millisecond, driver.ActionOpts{})
		}, "press", map[string]any{"x": 7.0, "y": 8.0, "ms": 1500.0}, nil},
		{"swipe", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, 300*time.Millisecond, driver.ActionOpts{})
		}, "swipe", map[string]any{"x1": 1.0, "y1": 2.0, "x2": 3.0, "y2": 4.0, "ms": 300.0}, []string{"drag"}},
		{"drag", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, 0, driver.ActionOpts{Drag: true})
		}, "swipe", map[string]any{"drag": true, "ms": 0.0}, nil},
		{"type", func() (driver.ActionResult, error) { return d.TypeText(ctx, "héllo", driver.ActionOpts{}) },
			"type", map[string]any{"text": "héllo", "noChange": 300.0}, nil},
		{"type keeps noChange", func() (driver.ActionResult, error) {
			return d.TypeText(ctx, "x", driver.ActionOpts{NoChangeMs: 50})
		}, "type", map[string]any{"noChange": 50.0}, nil},
		{"erase", func() (driver.ActionResult, error) { return d.EraseText(ctx, 4, driver.ActionOpts{}) },
			"erase", map[string]any{"n": 4.0, "noChange": 1000.0}, nil},
		{"key", func() (driver.ActionResult, error) { return d.PressKey(ctx, "Volume_Up", driver.ActionOpts{}) },
			"key", map[string]any{"code": 24.0}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := c.run()
			if err != nil {
				t.Fatal(err)
			}
			want := driver.ActionResult{Settle: driver.Changed, InjectMs: 2, SettleMs: 340, Seq: 1021}
			if r != want {
				t.Errorf("result %+v, want %+v", r, want)
			}
			req := a.last(c.op)
			for k, v := range c.want {
				if !reflect.DeepEqual(req[k], v) {
					t.Errorf("%s = %#v, want %#v (request %v)", k, req[k], v, req)
				}
			}
			for _, k := range c.nope {
				if _, ok := req[k]; ok {
					t.Errorf("request has %q: %v", k, req)
				}
			}
		})
	}
}

// An unknown key fails before anything is sent.
func TestPressKeyUnknown(t *testing.T) {
	a := actionAgent(t)
	d := newDriver(t, a)
	a.reset()
	if _, err := d.PressKey(ctxT(t), "f13", driver.ActionOpts{}); err == nil || !strings.Contains(err.Error(), `unknown key "f13"`) {
		t.Errorf("err = %v", err)
	}
	if ops := a.ops(); len(ops) != 0 {
		t.Errorf("sent %v", ops)
	}
}

// An action the agent rejects returns its error and an empty result.
func TestActionError(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tap", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "inject failed"} })
	d := newDriver(t, a)
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), "inject failed") || r != (driver.ActionResult{}) {
		t.Errorf("Tap = %+v, %v", r, err)
	}
}

// The context's deadline caps a request's timeout: an unanswered request fails when the context ends.
func TestCallUsesContextDeadline(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tap", func(map[string]any) map[string]any { return noReply })
	d := newDriver(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, err := d.Tap(ctx, 1, 1, driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("err = %v", err)
	}
	if el := time.Since(t0); el > 2*time.Second {
		t.Errorf("took %s, want about the context's 80 ms", el)
	}
}

// Settle sends since/noChange/timeout and returns the agent's result; WaitSettled sends quiet.
func TestSettleAndWaitSettled(t *testing.T) {
	a := newFakeAgent(t)
	a.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "no_change", "seq": 3} })
	a.on("quiet", func(map[string]any) map[string]any { return map[string]any{"quiet": true} })
	d := newDriver(t, a)
	ctx := ctxT(t)
	r, err := d.Settle(ctx, 42, 250, 2*time.Second)
	if err != nil || r != driver.NoChange {
		t.Errorf("Settle = %q, %v", r, err)
	}
	req := a.last("settle")
	if num(t, req, "since") != 42 || num(t, req, "noChange") != 250 || num(t, req, "timeout") != 2000 {
		t.Errorf("settle request %v", req)
	}
	if err := d.WaitSettled(ctx, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if num(t, a.last("quiet"), "timeout") != 1500 {
		t.Errorf("quiet request %v", a.last("quiet"))
	}
	a.on("settle", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if r, err := d.Settle(ctx, 0, 1, time.Second); err == nil || r != "" {
		t.Errorf("failed Settle = %q, %v", r, err)
	}
}

// HideKeyboard presses Escape only while the keyboard shows, then Back if it is still up.
func TestHideKeyboard(t *testing.T) {
	cases := []struct {
		name     string
		keyboard []bool // info answers, in order
		keys     []float64
	}{
		{"no keyboard", []bool{false}, nil},
		{"escape closes it", []bool{true, false}, []float64{111}},
		{"back as a fallback", []bool{true, true}, []float64{111, 4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newFakeAgent(t)
			for _, op := range []string{"key"} {
				a.on(op, func(map[string]any) map[string]any { return map[string]any{"result": "changed"} })
			}
			d := newDriver(t, a)
			n := 0
			a.on("info", func(map[string]any) map[string]any {
				kb := c.keyboard[min(n, len(c.keyboard)-1)]
				n++
				return map[string]any{"keyboard": kb}
			})
			if err := d.HideKeyboard(ctxT(t)); err != nil {
				t.Fatal(err)
			}
			var keys []float64
			for _, r := range a.requests("key") {
				keys = append(keys, num(t, r, "code"))
			}
			if !reflect.DeepEqual(keys, c.keys) {
				t.Errorf("keys %v, want %v", keys, c.keys)
			}
		})
	}
}

// HideKeyboard reports a failed info or key request.
func TestHideKeyboardErrors(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("info", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "gone"} })
	if err := d.HideKeyboard(ctxT(t)); err == nil {
		t.Error("info failure not reported")
	}
	a.on("info", func(map[string]any) map[string]any { return map[string]any{"keyboard": true} })
	a.on("key", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "no"} })
	if err := d.HideKeyboard(ctxT(t)); err == nil {
		t.Error("key failure not reported")
	}
}

// Screenshot defaults to PNG and returns the binary payload.
func TestScreenshot(t *testing.T) {
	a := newFakeAgent(t)
	png := []byte("\x89PNG\r\n\x1a\nfake\nimage")
	a.on("screenshot", func(map[string]any) map[string]any {
		return map[string]any{"w": 540, "h": 1200, "format": "png", "_bin": png}
	})
	d := newDriver(t, a)
	b, err := d.Screenshot(ctxT(t), 0.5, "")
	if err != nil || string(b) != string(png) {
		t.Fatalf("Screenshot = %q, %v", b, err)
	}
	req := a.last("screenshot")
	if req["format"] != "png" || num(t, req, "scale") != 0.5 {
		t.Errorf("request %v", req)
	}
	if _, err := d.Screenshot(ctxT(t), 1, "jpeg"); err != nil || a.last("screenshot")["format"] != "jpeg" {
		t.Errorf("jpeg: %v %v", err, a.last("screenshot"))
	}
	a.on("screenshot", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "secure window"} })
	if _, err := d.Screenshot(ctxT(t), 1, ""); err == nil {
		t.Error("error not reported")
	}
}

// Sh returns output and exit code; shOK turns a non-zero exit into an error with the output.
func TestShAndShOK(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("sh", func(req map[string]any) map[string]any {
		if req["cmd"] == "false" {
			return map[string]any{"code": 1, "out": "  failed here \n"}
		}
		return map[string]any{"code": 0, "out": "ok\n"}
	})
	out, code, err := d.Sh(ctxT(t), "echo ok")
	if out != "ok\n" || code != 0 || err != nil {
		t.Errorf("Sh = %q %d %v", out, code, err)
	}
	if num(t, a.last("sh"), "timeout") != 60000 {
		t.Errorf("sh timeout %v", a.last("sh"))
	}
	if _, err := d.shOK(ctxT(t), "false"); err == nil || err.Error() != "false: exit 1: failed here" {
		t.Errorf("shOK = %v", err)
	}
	a.on("sh", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "denied"} })
	if _, code, err := d.Sh(ctxT(t), "x"); code != -1 || err == nil {
		t.Errorf("failed Sh = %d %v", code, err)
	}
}

// shAgent answers sh requests from a table of (substring → out, code), recording the commands.
func shAgent(t *testing.T, answers map[string]struct {
	out  string
	code int
}) *fakeAgent {
	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		cmd := fmt.Sprint(req["cmd"])
		best := ""
		for sub := range answers {
			if strings.Contains(cmd, sub) && len(sub) > len(best) {
				best = sub
			}
		}
		if best == "" {
			return map[string]any{"code": 0, "out": ""}
		}
		return map[string]any{"code": answers[best].code, "out": answers[best].out}
	})
	a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": true} })
	return a
}

type shAnswer = struct {
	out  string
	code int
}

const resolveOut = "priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=true\ncom.example.app/.MainActivity\n"

// The launcher activity is the last line of resolve-activity, cached per package.
func TestLauncher(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	for i := 0; i < 2; i++ {
		c, err := d.launcher(ctxT(t), "com.example.app")
		if err != nil || c != "com.example.app/.MainActivity" {
			t.Fatalf("launcher = %q, %v", c, err)
		}
	}
	if n := len(a.requests("sh")); n != 1 {
		t.Errorf("%d resolve calls, want 1 (cached)", n)
	}
	want := "cmd package resolve-activity --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER com.example.app"
	if got := a.shCmds()[0]; got != want {
		t.Errorf("command %q", got)
	}
}

// A package with no launcher (no component in the output, or a failed command) is an error.
func TestLauncherMissing(t *testing.T) {
	for name, ans := range map[string]shAnswer{
		"no activity": {"No activity found\n", 0},
		"failed":      {"", 1},
	} {
		t.Run(name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"resolve-activity": ans})
			d := newDriver(t, a)
			_, err := d.launcher(ctxT(t), "com.missing")
			if err == nil || !strings.Contains(err.Error(), "not installed or has no launcher activity") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

const grantAll = "for p in $(dumpsys package com.example.app | grep -E '^ +android.permission.[A-Z_]+: granted=false' | sed 's/:.*//' | sort -u); do pm grant com.example.app $p 2>/dev/null; done"
const amStart = "am start -n com.example.app/.MainActivity -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f 0x10200000"

// LaunchApp runs one script (stop, clear, grant missing permissions, start), then waits for the app's window.
func TestLaunchAppScript(t *testing.T) {
	cases := []struct {
		name string
		o    driver.LaunchOpts
		want string
	}{
		{"plain", driver.LaunchOpts{}, grantAll + "; " + amStart},
		{"stop", driver.LaunchOpts{StopApp: true}, "am force-stop com.example.app; " + grantAll + "; " + amStart},
		{"clear", driver.LaunchOpts{ClearState: true}, "am force-stop com.example.app; " + clearCmd("com.example.app") + "; " + grantAll + "; " + amStart},
		{"all allow", driver.LaunchOpts{Permissions: map[string]string{"all": "ALLOW"}}, grantAll + "; " + amStart},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {"Starting: Intent { ... }\n", 0}})
			d := newDriver(t, a)
			if err := d.LaunchApp(ctxT(t), "com.example.app", c.o); err != nil {
				t.Fatal(err)
			}
			cmds := a.shCmds()
			if len(cmds) != 2 || cmds[1] != c.want {
				t.Errorf("commands:\n%s\nwant launch script:\n%s", strings.Join(cmds, "\n"), c.want)
			}
			w := a.last("awaitPackage")
			if w["pkg"] != "com.example.app" || num(t, w, "timeout") != 20000 {
				t.Errorf("awaitPackage %v", w)
			}
		})
	}
}

// Launch arguments become typed intent extras, keys and string values shell-quoted.
func TestLaunchAppArguments(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	args := map[string]any{"flag": true, "count": 3, "big": int64(1) << 40, "whole": 2.0, "ratio": 0.25,
		"name": "it's me", "list": []int{1, 2}}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{Arguments: args}); err != nil {
		t.Fatal(err)
	}
	script := a.shCmds()[1]
	for _, want := range []string{
		"--ez 'flag' true", "--ei 'count' 3", "--el 'big' 1099511627776", "--ei 'whole' 2", "--ef 'ratio' 0.25",
		`--es 'name' 'it'\''s me'`, "--es 'list' '[1 2]'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if !strings.HasPrefix(script, grantAll+"; "+amStart+" --") {
		t.Errorf("extras do not follow am start:\n%s", script)
	}
}

// Whole numbers that do not fit an int32 (am's --ei) should go as --el.
func TestLaunchAppLargeIntegerArgument(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{Arguments: map[string]any{"ts": 1.7e12, "n": 1 << 40}}); err != nil {
		t.Fatal(err)
	}
	script := a.shCmds()[1]
	for _, want := range []string{"--el 'ts' 1700000000000", "--el 'n' 1099511627776"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

// Explicit permissions run stop/clear first, then SetPermissions, then a script that only starts the app.
func TestLaunchAppWithPermissions(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{
		"resolve-activity": {resolveOut, 0},
		"dumpsys package":  {"    android.permission.CAMERA: granted=false, flags=[ USER_SET ]\n", 0},
	})
	d := newDriver(t, a)
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true, Permissions: map[string]string{"camera": "allow"}}); err != nil {
		t.Fatal(err)
	}
	cmds := a.shCmds()
	want := []string{
		"cmd package resolve-activity --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER com.example.app",
		"am force-stop com.example.app",
		"dumpsys package com.example.app | grep -E '^ +android.permission.[A-Z_]+: granted=' | sort -u",
		"pm grant com.example.app android.permission.CAMERA 2>/dev/null",
		amStart,
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(cmds, "\n"), strings.Join(want, "\n"))
	}
}

// LaunchApp fails on a bad id, a missing app, am's "Error:" output, a failing script, or no window.
func TestLaunchAppFailures(t *testing.T) {
	cases := []struct {
		name    string
		app     string
		answers map[string]shAnswer
		found   bool
		want    string
	}{
		{"bad id", "com.app; reboot", nil, true, "invalid app id"},
		{"not installed", "com.example.app", map[string]shAnswer{"resolve-activity": {"No activity found", 0}}, true, "not installed"},
		{"am error", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {"Error: Activity class does not exist.\n", 0}}, true, "could not start com.example.app: Error: Activity class"},
		{"script fails", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {"boom", 255}}, true, "exit 255: boom"},
		{"never shows", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}}, false, "did not come to the foreground within 20s"},
		{"stop fails before permissions", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "force-stop": {"no", 1}}, true, "exit 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, c.answers)
			a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": c.found} })
			d := newDriver(t, a)
			o := driver.LaunchOpts{}
			if c.name == "stop fails before permissions" {
				o = driver.LaunchOpts{StopApp: true, Permissions: map[string]string{"camera": "deny"}}
			}
			err := d.LaunchApp(ctxT(t), c.app, o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// WaitForApp asks the agent to wait for the package's window, with the timeout in ms.
func TestWaitForApp(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	if err := d.WaitForApp(ctxT(t), "com.example.app", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if r := a.last("awaitPackage"); r["pkg"] != "com.example.app" || num(t, r, "timeout") != 1500 {
		t.Errorf("request %v", r)
	}
	a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if err := d.WaitForApp(ctxT(t), "com.example.app", time.Second); err == nil {
		t.Error("agent error not reported")
	}
}

// Stop, kill and clear run their am/pm commands, and validate the id first.
func TestAppCommands(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	ctx := ctxT(t)
	cases := []struct {
		run  func(string) error
		want string
	}{
		{func(p string) error { return d.StopApp(ctx, p) }, "am force-stop com.example.app"},
		{func(p string) error { return d.KillApp(ctx, p) }, "am kill com.example.app; am force-stop com.example.app"},
		{func(p string) error { return d.ClearState(ctx, p) }, clearCmd("com.example.app")},
	}
	for _, c := range cases {
		a.reset()
		if err := c.run("com.example.app"); err != nil {
			t.Fatal(err)
		}
		if got := a.shCmds(); len(got) != 1 || got[0] != c.want {
			t.Errorf("commands %v, want %q", got, c.want)
		}
		a.reset()
		if err := c.run("bad id"); err == nil || len(a.requests("")) != 0 {
			t.Errorf("bad id: %v, sent %v", err, a.ops())
		}
	}
}

// OpenLink starts a VIEW intent, aimed at the app unless the browser is asked for.
func TestOpenLink(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	ctx := ctxT(t)
	cases := []struct {
		link, app string
		browser   bool
		want      string
	}{
		{"https://example.com/a?b=c&d", "", false, "am start -a android.intent.action.VIEW -d 'https://example.com/a?b=c&d'"},
		{"myapp://x", "com.example.app", false, "am start -a android.intent.action.VIEW -d 'myapp://x' com.example.app"},
		{"https://e.com/it's", "com.example.app", true, `am start -a android.intent.action.VIEW -d 'https://e.com/it'\''s'`},
	}
	for _, c := range cases {
		a.reset()
		if err := d.OpenLink(ctx, c.link, c.app, false, c.browser); err != nil {
			t.Fatal(err)
		}
		if got := a.shCmds(); len(got) != 1 || got[0] != c.want {
			t.Errorf("OpenLink(%q, %q, %v) ran %v, want %q", c.link, c.app, c.browser, got, c.want)
		}
	}
	a.reset()
	if err := d.OpenLink(ctx, "x://", "bad;id", false, false); err == nil || len(a.requests("")) != 0 {
		t.Errorf("bad app id: %v", err)
	}
}

const dumpsysPerms = `    android.permission.CAMERA: granted=false, flags=[ USER_SENSITIVE_WHEN_GRANTED ]
    android.permission.ACCESS_FINE_LOCATION: granted=true, flags=[ USER_SET ]
    android.permission.ACCESS_COARSE_LOCATION: granted=false
    android.permission.RECORD_AUDIO: granted=true
    android.permission.POST_NOTIFICATIONS: granted=false
    com.example.permission.C2D: granted=true
`

// SetPermissions grants, revokes or resets only permissions the app requests, and only what changes.
func TestSetPermissions(t *testing.T) {
	const app = "com.example.app"
	grant := func(p string) string { return "pm grant " + app + " android.permission." + p + " 2>/dev/null" }
	revoke := func(p string) string { return "pm revoke " + app + " android.permission." + p + " 2>/dev/null" }
	cases := []struct {
		name  string
		perms map[string]string
		want  []string // commands of the second sh, in any order
	}{
		{"empty grants all", nil, []string{grant("CAMERA"), grant("ACCESS_COARSE_LOCATION"), grant("POST_NOTIFICATIONS")}},
		{"all deny", map[string]string{"all": "deny"}, []string{revoke("ACCESS_FINE_LOCATION"), revoke("RECORD_AUDIO")}},
		{"group allow", map[string]string{"camera": "allow"}, []string{grant("CAMERA")}},
		{"group case-insensitive", map[string]string{"Camera": "ALLOW"}, []string{grant("CAMERA")}},
		{"group deny", map[string]string{"location": "deny"}, []string{revoke("ACCESS_FINE_LOCATION")}},
		{"already granted", map[string]string{"microphone": "allow"}, nil},
		{"not requested", map[string]string{"contacts": "allow", "sms": "deny"}, nil},
		{"qualified name", map[string]string{"android.permission.RECORD_AUDIO": "deny"}, []string{revoke("RECORD_AUDIO")}},
		{"unset", map[string]string{"camera": "unset"}, []string{revoke("CAMERA") + "; pm clear-permission-flags " + app + " android.permission.CAMERA user-set user-fixed 2>/dev/null"}},
		{"unknown value", map[string]string{"camera": "sometimes"}, nil},
		{"unknown group", map[string]string{"telepathy": "allow"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"dumpsys package": {dumpsysPerms, 0}})
			d := newDriver(t, a)
			if err := d.SetPermissions(ctxT(t), app, c.perms); err != nil {
				t.Fatal(err)
			}
			cmds := a.shCmds()
			if cmds[0] != "dumpsys package "+app+" | grep -E '^ +android.permission.[A-Z_]+: granted=' | sort -u" {
				t.Errorf("first command %q", cmds[0])
			}
			if len(c.want) == 0 {
				if len(cmds) != 1 {
					t.Errorf("ran %v, want only the dumpsys", cmds)
				}
				return
			}
			if len(cmds) != 2 {
				t.Fatalf("ran %v", cmds)
			}
			got := strings.Split(cmds[1], "; ")
			want := strings.Split(strings.Join(c.want, "; "), "; ")
			if !sameSet(got, want) {
				t.Errorf("commands %v\nwant %v", got, want)
			}
		})
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

// SetPermissions validates the id and reports a failed dumpsys.
func TestSetPermissionsErrors(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	if err := d.SetPermissions(ctxT(t), "", nil); err == nil {
		t.Error("empty id accepted")
	}
	a.on("sh", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "dead"} })
	if err := d.SetPermissions(ctxT(t), "com.example.app", nil); err == nil {
		t.Error("sh failure not reported")
	}
}

// SetOrientation maps flow names to the protocol's, then re-reads the size (it swaps in landscape).
func TestSetOrientation(t *testing.T) {
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

// A failed rotation keeps the old size; an info without a size does not zero it.
func TestSetOrientationErrors(t *testing.T) {
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

// App commands report a failed shell request; LaunchApp reports a failed permission step.
func TestAppCommandErrors(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	if _, err := d.launcher(ctxT(t), "com.example.app"); err != nil {
		t.Fatal(err)
	}
	a.on("sh", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "dead"} })
	if err := d.StopApp(ctxT(t), "com.example.app"); err == nil {
		t.Error("StopApp: error not reported")
	}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{Permissions: map[string]string{"camera": "deny"}}); err == nil {
		t.Error("LaunchApp: permission failure not reported")
	}
}

// A clear Android refuses (some vendors' builds) fails launchApp and clearState with the reason,
// instead of running the flow on the app's old data.
func TestClearStateRefused(t *testing.T) {
	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		cmd, _ := req["cmd"].(string)
		if strings.Contains(cmd, "pm clear") {
			return map[string]any{"code": 1, "out": "TB_CLEAR_FAILED: java.lang.SecurityException: PID 123 does not have permission\n"}
		}
		return map[string]any{"code": 0, "out": ""}
	})
	d := stubbedDriver(t, stubADB(t, nil), a)
	d.launch["com.example.app"] = "com.example.app/.Main"
	err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{ClearState: true})
	if err == nil || !strings.Contains(err.Error(), "could not clear the data of com.example.app") || !strings.Contains(err.Error(), "SecurityException") {
		t.Errorf("LaunchApp = %v", err)
	}
	if err := d.ClearState(ctxT(t), "com.example.app"); err == nil || !strings.Contains(err.Error(), "Developer options") {
		t.Errorf("ClearState = %v", err)
	}
	if clearFailed("x", "Success\nStarting: Intent") != nil {
		t.Error("a successful clear reported as failed")
	}
}
