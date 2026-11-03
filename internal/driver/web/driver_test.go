package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const snapshotExpr = "window.__tb ? window.__tb.snapshot() : null"

var pageSnap = map[string]any{
	"w": 1024, "h": 700, "url": "https://shop.example.com/login", "title": "Log in", "loading": false,
	"nodes": []map[string]any{
		{"d": 0, "b": []int{0, 0, 1024, 700}, "c": "body", "f": 4098},
		{"d": 1, "b": []int{10, 10, 300, 40}, "c": "h1", "t": "Welcome", "f": 4098},
		{"d": 1, "b": []int{10, 60, 300, 90}, "c": "input:email", "id": "email", "h": "you@example.com", "v": "", "f": 2048 | 4096 | 256 | 2 | 1},
		{"d": 1, "b": []int{10, 100, 300, 130}, "c": "input:password", "id": "pw", "h": "Password", "v": "secret", "f": 128 | 256 | 4 | 2 | 1 | 4096},
		{"d": 1, "b": []int{10, 140, 100, 170}, "c": "div", "t": "Sign in", "r": "button", "cd": "Sign in now", "f": 4099},
		{"d": 1, "b": []int{0, 0, 0, 0}, "c": "", "r": "presentation", "f": 4096},
		{"d": 1, "b": []int{10, 180, 30, 200}, "c": "input:checkbox", "f": 16 | 32 | 64 | 8 | 1 | 2 | 4096},
	},
}

func snapshotPage(t *testing.T, snap any) (*fakeCDP, *Driver) {
	page := newFakeCDP(t)
	js := jsonArg(snap)
	page.evaluator(func(expr, session string) any {
		if expr == snapshotExpr {
			return value(js)
		}
		return nil
	})
	return page, newWebDriver(t, page, nil)
}

// Screen turns the page helper's snapshot into one app window: flags, roles in the class, the
// placeholder only while a field is empty, every node visible.
func TestScreen(t *testing.T) {
	_, d := snapshotPage(t, pageSnap)
	d.changed("")
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Platform != "web" || s.Width != 1024 || s.Height != 700 || s.Seq != 1 || s.Keyboard {
		t.Errorf("header %+v", s)
	}
	if w, h := d.Size(); w != 1024 || h != 700 {
		t.Errorf("Size = %dx%d (the viewport)", w, h)
	}
	if len(s.Windows) != 1 {
		t.Fatalf("%d windows", len(s.Windows))
	}
	win := s.Windows[0]
	if win.Type != "app" || win.Package != "https://shop.example.com/login" || win.Title != "Log in" || !win.Active || !win.Focused || win.Bounds.R != 1024 || win.Bounds.B != 700 {
		t.Errorf("window %+v", win)
	}
	if s.Foreground() != "https://shop.example.com/login" {
		t.Errorf("Foreground = %q", s.Foreground())
	}
	n := s.Nodes
	if len(n) != 7 || len(win.Roots) != 1 || len(win.Roots[0].Children) != 6 {
		t.Fatalf("%d nodes", len(n))
	}
	email, pw, btn, pres, box := n[2], n[3], n[4], n[5], n[6]
	if email.Hint != "you@example.com" || !email.ShowingHint || !email.Editable || email.Label() != "" || email.Class != "input:email" {
		t.Errorf("empty field %+v", email)
	}
	if pw.Hint != "" || pw.Value != "secret" || !pw.Password || !pw.Focused {
		t.Errorf("filled field %+v (its placeholder no longer shows)", pw)
	}
	if btn.Class != "div[button]" || btn.Label() != "Sign in" || btn.Desc != "Sign in now" || !btn.Clickable {
		t.Errorf("role button %+v", btn)
	}
	if pres.Class != "" {
		t.Errorf("a role without a tag got class %q", pres.Class)
	}
	if !box.Checked || !box.Selected || !box.Checkable || !box.Scrollable || box.LongClickable {
		t.Errorf("checkbox %+v", box)
	}
	for _, x := range n {
		if !x.Visible {
			t.Errorf("node %q not visible", x.Class)
		}
	}
}

// A document that predates the helper (about:blank) gets it installed, then snapshotted.
func TestScreenInstallsHelper(t *testing.T) {
	page := newFakeCDP(t)
	page.evaluator(func(expr, session string) any {
		if expr == "window.__tb.snapshot()" {
			return value(jsonArg(map[string]any{"w": 800, "h": 600, "url": "about:blank", "nodes": []any{}}))
		}
		return nil // the first snapshot: no helper, so undefined
	})
	d := newWebDriver(t, page, nil)
	s, err := d.Screen(ctxT(t))
	if err != nil || s.Windows[0].Package != "about:blank" || len(s.Nodes) != 0 {
		t.Fatalf("Screen = %+v, %v", s, err)
	}
	var exprs []string
	for _, m := range page.calls("Runtime.evaluate") {
		exprs = append(exprs, m.params()["expression"].(string))
		if m.params()["returnByValue"] != true || m.params()["awaitPromise"] != true {
			t.Errorf("evaluate params %v", m.params())
		}
	}
	if len(exprs) != 3 || exprs[0] != snapshotExpr || exprs[1] != pageJS || exprs[2] != "window.__tb.snapshot()" {
		t.Errorf("evaluated %d expressions", len(exprs))
	}
}

// Script exceptions, protocol errors and a malformed snapshot are errors.
func TestScreenErrors(t *testing.T) {
	cases := []struct {
		name   string
		answer any
		want   string
	}{
		{"exception", map[string]any{"result": map[string]any{}, "exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "TypeError: x is null"}}}, "page script: TypeError: x is null"},
		{"exception text only", map[string]any{"result": map[string]any{}, "exceptionDetails": map[string]any{"text": "Uncaught SyntaxError"}}, "page script: Uncaught SyntaxError"},
		{"protocol error", errReply{cdpError{Code: -32000, Message: "Execution context was destroyed."}}, "Runtime.evaluate: Execution context was destroyed."},
		{"bad snapshot", value("{not json"), "invalid character"},
		{"wrong type", value(42), "cannot unmarshal number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page := newFakeCDP(t)
			page.on("Runtime.evaluate", func(cdpMsg) any { return c.answer })
			d := newWebDriver(t, page, nil)
			if _, err := d.Screen(ctxT(t)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// Cross-origin frames: each placeholder gets its frame session's nodes, nested under it and shifted
// to where the frame sits; CSS indices map past the spliced nodes.
func TestScreenSplicesFrames(t *testing.T) {
	snap := map[string]any{"w": 1000, "h": 800, "url": "https://shop.example.com/pay", "nodes": []map[string]any{
		{"d": 0, "b": []int{0, 0, 1000, 800}, "c": "body", "f": 4098},
		{"d": 1, "b": []int{100, 200, 500, 400}, "c": "iframe", "f": 4098, "frame": map[string]any{"src": "https://pay.example.net/card", "x": 102, "y": 203}},
		{"d": 1, "b": []int{0, 500, 100, 530}, "c": "button", "t": "Pay", "f": 4099},
		{"d": 1, "b": []int{0, 600, 10, 610}, "c": "iframe", "f": 4098, "frame": map[string]any{"src": "https://nobody.example.org/", "x": 0, "y": 600}},
		{"d": 1, "b": []int{0, 700, 10, 710}, "c": "iframe", "f": 4098, "frame": map[string]any{"src": "https://broken.example.org/", "x": 0, "y": 700}},
	}}
	inner := map[string]any{"nodes": []map[string]any{
		{"d": 0, "b": []int{0, 0, 400, 200}, "c": "body", "f": 4098},
		{"d": 1, "b": []int{10, 20, 210, 50}, "c": "input:text", "id": "card", "f": 4355, "frame": map[string]any{"src": "x", "x": 1, "y": 1}},
	}}
	page := newFakeCDP(t)
	page.evaluator(func(expr, session string) any {
		switch {
		case session == "" && expr == snapshotExpr:
			return value(jsonArg(snap))
		case session == "S-PAY" && expr == "window.__tb ? window.__tb.snapshot() : ''":
			return value("") // the frame's helper is not there yet
		case session == "S-PAY" && expr == "window.__tb.snapshot()":
			return value(jsonArg(inner))
		case session == "S-BROKEN" && expr == "window.__tb ? window.__tb.snapshot() : ''":
			return value("") // installing works, the snapshot after it fails
		case session == "S-BROKEN" && expr == "window.__tb.snapshot()":
			return errReply{cdpError{Code: -32000, Message: "gone"}}
		case strings.HasPrefix(expr, "window.__tb ? window.__tb.css("):
			return value("[0, 2, 3, 4, 9, -1]")
		}
		return nil
	})
	d := newWebDriver(t, page, nil)
	d.mu.Lock()
	d.frames["S-PAY"] = &frame{session: "S-PAY", target: "T1", url: "https://pay.example.net/card", order: 1}
	d.frames["S-BROKEN"] = &frame{session: "S-BROKEN", target: "T2", url: "https://broken.example.org/", order: 0}
	d.mu.Unlock()
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range s.Nodes {
		got = append(got, fmt.Sprintf("%d %s %v", n.Depth, n.Class, n.Bounds))
	}
	want := []string{
		"0 body [0,0][1000,800]",
		"1 iframe [100,200][500,400]",
		"2 body [102,203][502,403]",
		"3 input:text [112,223][312,253]",
		"1 button [0,500][100,530]",
		"1 iframe [0,600][10,610]",
		"1 iframe [0,700][10,710]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nodes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if s.Nodes[3].Parent != s.Nodes[2] || s.Nodes[2].Parent != s.Nodes[1] || s.Nodes[3].ID != "card" {
		t.Error("the frame's nodes are not nested under its placeholder")
	}
	var sessCalls []string
	for _, m := range page.calls("Runtime.evaluate") {
		if m.SessionID == "S-PAY" {
			sessCalls = append(sessCalls, m.params()["expression"].(string))
		}
	}
	if len(sessCalls) != 3 || sessCalls[1] != pageJS {
		t.Errorf("frame session evaluations: %d (want snapshot, install, snapshot)", len(sessCalls))
	}
	idx, err := d.QueryCSS(ctxT(t), `a[href="x"]`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(idx, []int{0, 4, 5, 6}) {
		t.Errorf("QueryCSS = %v, want main-document indices mapped past the frame's nodes", idx)
	}
	css := page.calls("Runtime.evaluate")
	if e := css[len(css)-1].params()["expression"]; e != `window.__tb ? window.__tb.css("a[href=\"x\"]") : '[]'` {
		t.Errorf("css expression %v", e)
	}
}

// QueryCSS reports script errors and bad answers.
func TestQueryCSSErrors(t *testing.T) {
	page := newFakeCDP(t)
	answer := value("not json")
	page.on("Runtime.evaluate", func(cdpMsg) any { return answer })
	d := newWebDriver(t, page, nil)
	if _, err := d.QueryCSS(ctxT(t), "div"); err == nil {
		t.Error("bad answer accepted")
	}
	page.on("Runtime.evaluate", func(cdpMsg) any {
		return map[string]any{"exceptionDetails": map[string]any{"text": "SyntaxError: '##' is not a valid selector"}}
	})
	if _, err := d.QueryCSS(ctxT(t), "##"); err == nil || !strings.Contains(err.Error(), "not a valid selector") {
		t.Errorf("err = %v", err)
	}
}

// frameFor prefers the frame showing exactly the placeholder's src, then the earliest same-origin
// (or not yet navigated) frame, never one already used.
func TestFrameFor(t *testing.T) {
	d := newBareDriver()
	d.frames = map[string]*frame{
		"A": {session: "A", url: "https://pay.example.net/a", order: 0},
		"B": {session: "B", url: "https://pay.example.net/b", order: 1},
		"C": {session: "C", url: "", order: 2},
		"D": {session: "D", url: "https://other.example.org/", order: 3},
	}
	cases := []struct {
		src  string
		used []string
		want string
	}{
		{"https://pay.example.net/b", nil, "B"},
		{"https://pay.example.net/z", nil, "A"},
		{"https://pay.example.net/z", []string{"A"}, "B"},
		{"https://pay.example.net/z", []string{"A", "B"}, "C"},
		{"https://other.example.org/", nil, "D"},
		{"https://other.example.org/", []string{"D"}, "C"},
		{"https://elsewhere.example.com/", []string{"C"}, ""},
	}
	for _, c := range cases {
		used := map[string]bool{}
		for _, u := range c.used {
			used[u] = true
		}
		if got := d.frameFor(c.src, used); got != c.want {
			t.Errorf("frameFor(%q, used %v) = %q, want %q", c.src, c.used, got, c.want)
		}
	}
}

func inputPage(t *testing.T) (*fakeCDP, *Driver) {
	page := newFakeCDP(t)
	return page, newWebDriver(t, page, nil)
}

// mouseEvents lists the Input.dispatchMouseEvent params sent.
func mouseEvents(f *fakeCDP) []map[string]any {
	var out []map[string]any
	for _, m := range f.calls("Input.dispatchMouseEvent") {
		out = append(out, m.params())
	}
	return out
}

func keyEvents(f *fakeCDP) []string {
	var out []string
	for _, m := range f.calls("Input.dispatchKeyEvent") {
		p := m.params()
		s := fmt.Sprintf("%v %v", p["type"], p["key"])
		if p["text"] != nil {
			s += fmt.Sprintf(" text=%q", p["text"])
		}
		if p["code"] != nil {
			s += fmt.Sprintf(" code=%v vk=%v", p["code"], p["windowsVirtualKeyCode"])
		}
		out = append(out, s)
	}
	return out
}

// A tap is a move, a press and a release at the point, with the left button.
func TestTap(t *testing.T) {
	page, d := inputPage(t)
	r, err := d.Tap(ctxT(t), 15, 25, driver.ActionOpts{NoSettle: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.Settle != "" || r.Seq != 0 {
		t.Errorf("result %+v (no settle asked for)", r)
	}
	want := []map[string]any{
		{"type": "mouseMoved", "x": 15.0, "y": 25.0, "button": "none", "clickCount": 0.0},
		{"type": "mousePressed", "x": 15.0, "y": 25.0, "button": "left", "clickCount": 1.0, "buttons": 1.0},
		{"type": "mouseReleased", "x": 15.0, "y": 25.0, "button": "left", "clickCount": 1.0},
	}
	if got := mouseEvents(page); !reflect.DeepEqual(got, want) {
		t.Errorf("mouse events %v\nwant %v", got, want)
	}
}

// A tap the page reacts to waits for it to settle; one it ignores ends after the no-change window.
func TestTapSettles(t *testing.T) {
	page, d := inputPage(t)
	page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
		if m.params()["type"] == "mouseReleased" {
			page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "payload": `{"a":0,"s":1}`}, "")
		}
		return map[string]any{}
	})
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{})
	if err != nil || r.Settle != driver.Changed || r.Seq != 1 || r.SettleMs < int(quietWindow/time.Millisecond)-20 {
		t.Errorf("Tap = %+v, %v", r, err)
	}
	page.on("Input.dispatchMouseEvent", func(cdpMsg) any { return map[string]any{} })
	t0 := time.Now()
	r, err = d.Tap(ctxT(t), 1, 1, driver.ActionOpts{NoChangeMs: 40})
	if err != nil || r.Settle != driver.NoChange || time.Since(t0) < 35*time.Millisecond {
		t.Errorf("ignored tap = %+v, %v after %s", r, err, time.Since(t0))
	}
}

// React returns as soon as the page starts changing, without waiting for it to settle.
func TestTapReact(t *testing.T) {
	page, d := inputPage(t)
	page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
		if m.params()["type"] == "mouseReleased" {
			page.event("Page.frameStartedLoading", map[string]any{"frameId": "main"}, "")
		}
		return map[string]any{}
	})
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{React: true, Timeout: 5 * time.Second})
	if err != nil || r.Settle != driver.Changed || r.SettleMs > 1000 {
		t.Errorf("Tap = %+v, %v (the page is still loading: it must not wait for it)", r, err)
	}
	page.on("Input.dispatchMouseEvent", func(cdpMsg) any { return map[string]any{} })
	if r, _ := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{React: true, NoChangeMs: 30}); r.Settle != driver.NoChange {
		t.Errorf("no reaction: %+v", r)
	}
}

// A failed input is returned before any waiting.
func TestTapFails(t *testing.T) {
	for _, failing := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		page, d := inputPage(t)
		page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
			if m.params()["type"] == failing {
				return errReply{cdpError{Code: -32602, Message: "Invalid parameters"}}
			}
			return map[string]any{}
		})
		if _, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{}); err == nil || !strings.Contains(err.Error(), "Invalid parameters") {
			t.Errorf("%s failing: %v", failing, err)
		}
	}
}

// Settle errors (a cancelled context) come back from the action.
func TestActionContextCancelled(t *testing.T) {
	_, d := inputPage(t)
	ctx, cancel := context.WithCancel(ctxT(t))
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if _, err := d.Tap(ctx, 1, 1, driver.ActionOpts{NoChangeMs: 5000}); err != context.Canceled {
		t.Errorf("err = %v", err)
	}
}

// A double tap is two press/release pairs with click counts 1 then 2.
func TestDoubleTap(t *testing.T) {
	page, d := inputPage(t)
	if _, err := d.DoubleTap(ctxT(t), 7, 8, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range mouseEvents(page) {
		got = append(got, fmt.Sprintf("%v %v", p["type"], p["clickCount"]))
	}
	if want := []string{"mousePressed 1", "mouseReleased 1", "mousePressed 2", "mouseReleased 2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events %v", got)
	}
	for _, failing := range []string{"mousePressed", "mouseReleased"} {
		page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
			if m.params()["clickCount"] == 2.0 && m.params()["type"] == failing {
				return errReply{cdpError{Message: "x"}}
			}
			return map[string]any{}
		})
		if _, err := d.DoubleTap(ctxT(t), 7, 8, driver.ActionOpts{NoSettle: true}); err == nil {
			t.Errorf("%s failure not reported", failing)
		}
	}
}

// A long press holds the button for the duration; a cancelled context releases nothing.
func TestLongPress(t *testing.T) {
	page, d := inputPage(t)
	t0 := time.Now()
	if _, err := d.LongPress(ctxT(t), 3, 4, 60*time.Millisecond, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) < 55*time.Millisecond {
		t.Errorf("held %s", time.Since(t0))
	}
	ev := mouseEvents(page)
	if len(ev) != 2 || ev[0]["type"] != "mousePressed" || ev[1]["type"] != "mouseReleased" {
		t.Errorf("events %v", ev)
	}
	page.reset()
	ctx, cancel := context.WithTimeout(ctxT(t), 30*time.Millisecond)
	defer cancel()
	if _, err := d.LongPress(ctx, 3, 4, 5*time.Second, driver.ActionOpts{NoSettle: true}); err != context.DeadlineExceeded {
		t.Errorf("err = %v", err)
	}
	if ev := mouseEvents(page); len(ev) != 1 {
		t.Errorf("events after cancel %v", ev)
	}
	page.on("Input.dispatchMouseEvent", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if _, err := d.LongPress(ctxT(t), 3, 4, time.Millisecond, driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("press failure not reported")
	}
}

// A swipe is a wheel event at the start point with the swipe's distance as its delta.
func TestSwipeWheel(t *testing.T) {
	page, d := inputPage(t)
	if _, err := d.Swipe(ctxT(t), 500, 600, 480, 200, 300*time.Millisecond, driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	ev := mouseEvents(page)
	want := map[string]any{"type": "mouseWheel", "x": 500.0, "y": 600.0, "button": "none", "clickCount": 0.0, "deltaX": 20.0, "deltaY": 400.0}
	if len(ev) != 1 || !reflect.DeepEqual(ev[0], want) {
		t.Errorf("events %v", ev)
	}
	if len(page.calls("Runtime.evaluate")) != 0 {
		t.Error("a plain swipe evaluated a script")
	}
}

// A drag scrolls the element under the point directly; when nothing moved (or the point is over a
// frame, or the script fails) it falls back to the wheel.
func TestSwipeDrag(t *testing.T) {
	cases := []struct {
		answer any
		wheel  bool
	}{
		{value("moved"), false},
		{value("still"), true},
		{value("frame"), true},
		{map[string]any{"exceptionDetails": map[string]any{"text": "boom"}}, true},
	}
	for _, c := range cases {
		page, d := inputPage(t)
		page.on("Runtime.evaluate", func(cdpMsg) any { return c.answer })
		if _, err := d.Swipe(ctxT(t), 100, 700, 100, 300, 0, driver.ActionOpts{Drag: true, NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		ev := page.calls("Runtime.evaluate")
		if len(ev) != 1 || ev[0].params()["expression"] != fmt.Sprintf(scrollByJS, 100, 700, 0, 400) {
			t.Errorf("%v: drag script not sent as expected", c.answer)
		}
		if got := len(mouseEvents(page)) == 1; got != c.wheel {
			t.Errorf("%v: wheel used = %v, want %v", c.answer, got, c.wheel)
		}
	}
}

// Typing sends a key down/up per character (Enter for a newline).
func TestTypeText(t *testing.T) {
	page, d := inputPage(t)
	if _, err := d.TypeText(ctxT(t), "aé\nb", driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`keyDown a text="a"`, "keyUp a",
		`keyDown é text="é"`, "keyUp é",
		`keyDown Enter text="\r" code=Enter vk=13`, "keyUp Enter code=Enter vk=13",
		`keyDown b text="b"`, "keyUp b",
	}
	if got := keyEvents(page); !reflect.DeepEqual(got, want) {
		t.Errorf("keys %q\nwant %q", got, want)
	}
	for _, m := range page.calls("Input.dispatchKeyEvent") {
		if p := m.params(); p["type"] == "keyDown" && p["key"] != "Enter" && p["unmodifiedText"] != p["text"] {
			t.Errorf("unmodifiedText %v", p)
		}
	}
}

// Typing stops at the first failed key event.
func TestTypeTextFails(t *testing.T) {
	for _, failAt := range []int{1, 2, 3, 5} {
		page, d := inputPage(t)
		n := 0
		page.on("Input.dispatchKeyEvent", func(cdpMsg) any {
			n++
			if n == failAt {
				return errReply{cdpError{Message: "x"}}
			}
			return map[string]any{}
		})
		if _, err := d.TypeText(ctxT(t), "a\nb", driver.ActionOpts{NoSettle: true}); err == nil {
			t.Errorf("failure at key event %d not reported", failAt)
		}
	}
}

// Erasing presses Backspace n times, but no more than the focused field holds.
func TestEraseText(t *testing.T) {
	cases := []struct {
		length any
		n      int
		want   int
	}{
		{2, 5, 2}, {10, 3, 3}, {0, 4, 0}, {-1, 3, 3},
	}
	for _, c := range cases {
		page, d := inputPage(t)
		page.on("Runtime.evaluate", func(cdpMsg) any { return value(c.length) })
		if _, err := d.EraseText(ctxT(t), c.n, driver.ActionOpts{NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		keys := keyEvents(page)
		if len(keys) != 2*c.want {
			t.Errorf("length %v, n %d: %d key events, want %d", c.length, c.n, len(keys), 2*c.want)
		}
		for i, k := range keys {
			if (i%2 == 0 && k != "rawKeyDown Backspace code=Backspace vk=8") || (i%2 == 1 && k != "keyUp Backspace code=Backspace vk=8") {
				t.Errorf("key %d = %q", i, k)
			}
		}
	}
	page, d := inputPage(t)
	page.on("Runtime.evaluate", func(cdpMsg) any { return value(-1) })
	page.on("Input.dispatchKeyEvent", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if _, err := d.EraseText(ctxT(t), 2, driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("key failure not reported")
	}
}

// When the field-length probe fails, EraseText should still press n times.
func TestEraseTextWhenLengthUnknown(t *testing.T) {
	page, d := inputPage(t)
	page.on("Runtime.evaluate", func(cdpMsg) any { return map[string]any{"exceptionDetails": map[string]any{"text": "boom"}} })
	d.EraseText(ctxT(t), 3, driver.ActionOpts{NoSettle: true})
	if got := len(keyEvents(page)); got != 6 {
		t.Errorf("%d key events, want 6", got)
	}
}

// PressKey sends the key's DOM names and virtual key code; text keys get a keyDown, others a rawKeyDown.
func TestPressKey(t *testing.T) {
	cases := map[string][]string{
		"Enter":     {`keyDown Enter text="\r" code=Enter vk=13`, "keyUp Enter code=Enter vk=13"},
		" TAB ":     {"rawKeyDown Tab code=Tab vk=9", "keyUp Tab code=Tab vk=9"},
		"escape":    {"rawKeyDown Escape code=Escape vk=27", "keyUp Escape code=Escape vk=27"},
		"home":      {"rawKeyDown Home code=Home vk=36", "keyUp Home code=Home vk=36"},
		"BACKSPACE": {"rawKeyDown Backspace code=Backspace vk=8", "keyUp Backspace code=Backspace vk=8"},
	}
	for key, want := range cases {
		page, d := inputPage(t)
		if _, err := d.PressKey(ctxT(t), key, driver.ActionOpts{NoSettle: true}); err != nil {
			t.Fatal(err)
		}
		if got := keyEvents(page); !reflect.DeepEqual(got, want) {
			t.Errorf("PressKey(%q) sent %q, want %q", key, got, want)
		}
	}
	page, d := inputPage(t)
	_, err := d.PressKey(ctxT(t), "volume up", driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), `key "volume up" is not supported on web`) || len(page.calls("")) != 0 {
		t.Errorf("unsupported key: %v, sent %v", err, page.methods())
	}
	page.on("Input.dispatchKeyEvent", func(m cdpMsg) any {
		if m.params()["type"] == "keyUp" {
			return errReply{cdpError{Message: "x"}}
		}
		return map[string]any{}
	})
	if _, err := d.PressKey(ctxT(t), "tab", driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("keyUp failure not reported")
	}
}

// Back goes to the previous history entry, and does nothing on the first one.
func TestPressBack(t *testing.T) {
	page, d := inputPage(t)
	var index atomic.Int32
	index.Store(2)
	page.on("Page.getNavigationHistory", func(cdpMsg) any {
		return map[string]any{"currentIndex": index.Load(), "entries": []map[string]any{{"id": 11}, {"id": 12}, {"id": 13}}}
	})
	if _, err := d.PressKey(ctxT(t), "Back", driver.ActionOpts{NoSettle: true}); err != nil {
		t.Fatal(err)
	}
	nav := page.calls("Page.navigateToHistoryEntry")
	if len(nav) != 1 || nav[0].params()["entryId"] != 12.0 {
		t.Errorf("navigations %v", page.methods())
	}
	index.Store(0)
	page.reset()
	if _, err := d.PressKey(ctxT(t), "back", driver.ActionOpts{NoSettle: true}); err != nil || len(page.calls("Page.navigateToHistoryEntry")) != 0 {
		t.Errorf("back on the first entry: %v, %v", err, page.methods())
	}
	page.on("Page.getNavigationHistory", func(cdpMsg) any { return errReply{cdpError{Message: "no history"}} })
	if _, err := d.PressKey(ctxT(t), "back", driver.ActionOpts{NoSettle: true}); err == nil {
		t.Error("history failure not reported")
	}
}

// Screenshots: PNG by default, JPEG at quality 80, a scaled clip of the viewport below scale 1.
func TestScreenshot(t *testing.T) {
	page, d := inputPage(t)
	img := []byte("\x89PNG fake")
	page.on("Page.captureScreenshot", func(cdpMsg) any { return map[string]any{"data": base64.StdEncoding.EncodeToString(img)} })
	cases := []struct {
		scale  float64
		format string
		want   map[string]any
	}{
		{1, "", map[string]any{"format": "png"}},
		{0, "jpeg", map[string]any{"format": "jpeg", "quality": 80.0}},
		{0.5, "png", map[string]any{"format": "png", "clip": map[string]any{"x": 0.0, "y": 0.0, "width": 1280.0, "height": 800.0, "scale": 0.5}}},
	}
	for _, c := range cases {
		b, err := d.Screenshot(ctxT(t), c.scale, c.format)
		if err != nil || string(b) != string(img) {
			t.Fatalf("Screenshot = %q, %v", b, err)
		}
		calls := page.calls("Page.captureScreenshot")
		if got := calls[len(calls)-1].params(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("scale %v format %q: params %v, want %v", c.scale, c.format, got, c.want)
		}
	}
	page.on("Page.captureScreenshot", func(cdpMsg) any { return map[string]any{"data": "!!not base64"} })
	if _, err := d.Screenshot(ctxT(t), 1, ""); err == nil {
		t.Error("bad base64 accepted")
	}
	page.on("Page.captureScreenshot", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if _, err := d.Screenshot(ctxT(t), 1, ""); err == nil {
		t.Error("protocol error not reported")
	}
}

// navPage answers Page.navigate with a loader id and fires the load event a little later.
func navPage(t *testing.T) (*fakeCDP, *Driver) {
	page, d := inputPage(t)
	page.on("Page.navigate", func(m cdpMsg) any {
		go func() {
			time.Sleep(30 * time.Millisecond)
			page.event("Page.loadEventFired", map[string]any{"timestamp": 1}, "")
		}()
		return map[string]any{"frameId": "F", "loaderId": "L1"}
	})
	return page, d
}

// LaunchApp opens the URL and returns once it has loaded; ClearState clears the origin's storage first.
func TestLaunchApp(t *testing.T) {
	page, d := navPage(t)
	t0 := time.Now()
	if err := d.LaunchApp(ctxT(t), "https://shop.example.com/start?x=1", driver.LaunchOpts{ClearState: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) < 25*time.Millisecond {
		t.Error("returned before the load event")
	}
	got := page.methods()
	if !reflect.DeepEqual(got, []string{"Storage.clearDataForOrigin", "Page.navigate"}) {
		t.Errorf("commands %v", got)
	}
	if p := page.calls("Storage.clearDataForOrigin")[0].params(); p["origin"] != "https://shop.example.com" || p["storageTypes"] != "all" {
		t.Errorf("clear params %v", p)
	}
	if p := page.calls("Page.navigate")[0].params(); p["url"] != "https://shop.example.com/start?x=1" {
		t.Errorf("navigate params %v", p)
	}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{}); err == nil || !strings.Contains(err.Error(), "is not a URL") {
		t.Errorf("non-URL app: %v", err)
	}
	page.on("Storage.clearDataForOrigin", func(cdpMsg) any { return errReply{cdpError{Message: "denied"}} })
	if err := d.LaunchApp(ctxT(t), "https://x.example/", driver.LaunchOpts{ClearState: true}); err == nil {
		t.Error("clear failure not reported")
	}
}

// Navigation errors are reported; a same-document navigation (no loader) returns at once.
func TestNavigate(t *testing.T) {
	page, d := inputPage(t)
	page.on("Page.navigate", func(cdpMsg) any { return map[string]any{"errorText": "net::ERR_NAME_NOT_RESOLVED"} })
	if err := d.OpenLink(ctxT(t), "https://nope.invalid/", "", false, false); err == nil ||
		err.Error() != "could not open https://nope.invalid/: net::ERR_NAME_NOT_RESOLVED" {
		t.Errorf("err = %v", err)
	}
	page.on("Page.navigate", func(cdpMsg) any { return map[string]any{"frameId": "F"} })
	t0 := time.Now()
	if err := d.OpenLink(ctxT(t), "https://shop.example.com/#reviews", "", false, false); err != nil || time.Since(t0) > time.Second {
		t.Errorf("same-document navigation: %v after %s", err, time.Since(t0))
	}
	page.on("Page.navigate", func(cdpMsg) any { return errReply{cdpError{Message: "Cannot navigate to invalid URL"}} })
	if err := d.StopApp(ctxT(t), "https://x/"); err == nil {
		t.Error("protocol error not reported")
	}
}

// Stopping or killing the "app" opens about:blank; ClearState of a non-origin does nothing.
func TestStopKillClear(t *testing.T) {
	page, d := navPage(t)
	for _, run := range []func() error{
		func() error { return d.StopApp(ctxT(t), "https://shop.example.com") },
		func() error { return d.KillApp(ctxT(t), "https://shop.example.com") },
	} {
		page.reset()
		if err := run(); err != nil {
			t.Fatal(err)
		}
		if n := page.calls("Page.navigate"); len(n) != 1 || n[0].params()["url"] != "about:blank" {
			t.Errorf("navigations %v", page.methods())
		}
	}
	page.reset()
	if err := d.ClearState(ctxT(t), "about:blank"); err != nil || len(page.calls("")) != 0 {
		t.Errorf("ClearState(about:blank): %v, %v", err, page.methods())
	}
}

// SetPermissions resets the origin's permissions and grants the allowed ones in one call.
func TestSetPermissions(t *testing.T) {
	cases := []struct {
		name  string
		perms map[string]string
		grant []string
	}{
		{"one", map[string]string{"camera": "allow", "microphone": "deny"}, []string{"videoCapture"}},
		{"case", map[string]string{"Location": "ALLOW"}, []string{"geolocation"}},
		{"group", map[string]string{"clipboard": "allow"}, []string{"clipboardReadWrite", "clipboardSanitizedWrite"}},
		{"none", map[string]string{"camera": "deny", "unknown": "allow"}, nil},
		{"all", map[string]string{"all": "allow"}, []string{"audioCapture", "clipboardReadWrite", "clipboardSanitizedWrite", "geolocation", "midi", "notifications", "sensors", "videoCapture"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, browser := newFakeCDP(t), newFakeCDP(t)
			d := newWebDriver(t, page, browser)
			if err := d.SetPermissions(ctxT(t), "https://shop.example.com/x", c.perms); err != nil {
				t.Fatal(err)
			}
			methods := browser.methods()
			if len(methods) == 0 || methods[0] != "Browser.resetPermissions" {
				t.Fatalf("browser commands %v", methods)
			}
			grants := browser.calls("Browser.grantPermissions")
			if c.grant == nil {
				if len(grants) != 0 {
					t.Errorf("granted %v", grants)
				}
				return
			}
			if len(grants) != 1 {
				t.Fatalf("grants %v", methods)
			}
			p := grants[0].params()
			var got []string
			for _, g := range p["permissions"].([]any) {
				got = append(got, g.(string))
			}
			sortStrings(got)
			if p["origin"] != "https://shop.example.com" || !reflect.DeepEqual(got, c.grant) {
				t.Errorf("grant %v", p)
			}
		})
	}
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// A grant Chrome rejects as a whole is retried one permission at a time; a failed reset is an error.
func TestSetPermissionsFallbacks(t *testing.T) {
	page, browser := newFakeCDP(t), newFakeCDP(t)
	browser.on("Browser.grantPermissions", func(m cdpMsg) any {
		if len(m.params()["permissions"].([]any)) > 1 {
			return errReply{cdpError{Message: "Unknown permission type"}}
		}
		return map[string]any{}
	})
	d := newWebDriver(t, page, browser)
	if err := d.SetPermissions(ctxT(t), "https://a.example/", map[string]string{"clipboard": "allow", "camera": "allow"}); err != nil {
		t.Fatal(err)
	}
	if n := len(browser.calls("Browser.grantPermissions")); n != 4 {
		t.Errorf("%d grant calls, want 1 + 3 one by one", n)
	}
	browser.on("Browser.resetPermissions", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if err := d.SetPermissions(ctxT(t), "https://a.example/", nil); err == nil {
		t.Error("reset failure not reported")
	}
	browser.reset()
	if err := d.SetPermissions(ctxT(t), "about:blank", map[string]string{"camera": "allow"}); err != nil || len(browser.calls("")) != 0 {
		t.Errorf("no origin: %v, %v", err, browser.methods())
	}
}

// Location, network and color scheme are emulated through CDP; airplane mode toggles offline.
func TestEmulation(t *testing.T) {
	page, d := inputPage(t)
	page.evaluator(func(expr, session string) any {
		switch expr {
		case "matchMedia('(prefers-color-scheme: dark)').matches":
			return value(true)
		case "location.href":
			return value("https://shop.example.com/cart")
		}
		return nil
	})
	ctx := ctxT(t)
	if err := d.SetLocation(ctx, 48.8584, 2.2945); err != nil {
		t.Fatal(err)
	}
	if p := page.calls("Emulation.setGeolocationOverride")[0].params(); p["latitude"] != 48.8584 || p["longitude"] != 2.2945 || p["accuracy"] != 10.0 {
		t.Errorf("geolocation %v", p)
	}
	d.SetAirplaneMode(ctx, true)
	d.ToggleAirplaneMode(ctx)
	d.ToggleAirplaneMode(ctx)
	var offline []any
	for _, m := range page.calls("Network.emulateNetworkConditions") {
		p := m.params()
		offline = append(offline, p["offline"])
		if p["latency"] != 0.0 || p["downloadThroughput"] != -1.0 || p["uploadThroughput"] != -1.0 {
			t.Errorf("network params %v", p)
		}
	}
	if !reflect.DeepEqual(offline, []any{true, false, true}) {
		t.Errorf("offline sequence %v", offline)
	}
	for _, on := range []bool{true, false} {
		if err := d.SetDarkMode(ctx, on); err != nil {
			t.Fatal(err)
		}
	}
	var schemes []string
	for _, m := range page.calls("Emulation.setEmulatedMedia") {
		f := m.params()["features"].([]any)[0].(map[string]any)
		if f["name"] != "prefers-color-scheme" {
			t.Errorf("feature %v", f)
		}
		schemes = append(schemes, f["value"].(string))
	}
	if !reflect.DeepEqual(schemes, []string{"dark", "light"}) {
		t.Errorf("schemes %v", schemes)
	}
	if dark, err := d.DarkMode(ctx); !dark || err != nil {
		t.Errorf("DarkMode = %v, %v", dark, err)
	}
	if href, err := d.Foreground(ctx); href != "https://shop.example.com/cart" || err != nil {
		t.Errorf("Foreground = %q, %v", href, err)
	}
}

// What the web cannot do says so; what has no web meaning is a no-op.
func TestUnsupportedAndNoops(t *testing.T) {
	_, d := inputPage(t)
	ctx := ctxT(t)
	for name, err := range map[string]error{
		"AddMedia":       d.AddMedia(ctx, []string{"a.png"}),
		"InstallApp":     d.InstallApp(ctx, "app.apk"),
		"StartRecording": d.StartRecording(ctx, "x.mp4"),
	} {
		if err == nil || !strings.Contains(err.Error(), "not supported on web") {
			t.Errorf("%s = %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"StopRecording":  d.StopRecording(ctx),
		"HideKeyboard":   d.HideKeyboard(ctx),
		"SetOrientation": d.SetOrientation(ctx, "LANDSCAPE_LEFT"),
		"ClearKeychain":  d.ClearKeychain(ctx),
	} {
		if err != nil {
			t.Errorf("%s = %v", name, err)
		}
	}
	if d.Platform() != "web" || d.Serial() != "web" || d.Name() != "Chrome" {
		t.Errorf("identity %q %q %q", d.Platform(), d.Serial(), d.Name())
	}
	if w, h := d.Size(); w != 1280 || h != 800 {
		t.Errorf("Size = %dx%d", w, h)
	}
}

// Close closes the browser (Browser.close), removes the profile, ends waits, and is idempotent.
func TestClose(t *testing.T) {
	page, browser := newFakeCDP(t), newFakeCDP(t)
	d := newWebDriver(t, page, browser)
	d.profile = filepath.Join(t.TempDir(), "profile")
	os.MkdirAll(filepath.Join(d.profile, "Default"), 0o755)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if len(browser.calls("Browser.close")) != 1 {
		t.Errorf("browser commands %v", browser.methods())
	}
	if _, err := os.Stat(d.profile); !os.IsNotExist(err) {
		t.Error("profile left behind")
	}
	if err := d.Close(); err != nil || len(browser.calls("Browser.close")) != 1 {
		t.Errorf("second Close: %v, %v", err, browser.methods())
	}
	<-d.page.done
}
