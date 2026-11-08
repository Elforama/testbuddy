package web

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
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
