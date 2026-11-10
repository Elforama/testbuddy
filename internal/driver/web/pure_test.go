package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/elforama/testbuddy/internal/driver"
)

// sameSite compares the last two host labels, so an API subdomain counts as the page's own.
func TestSameSite(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://example.com/api", "https://example.com/", true},
		{"https://api.example.com/v1", "https://www.example.com/page", true},
		{"https://a.b.example.com/", "http://example.com:8080/", true},
		{"https://example.com/", "https://example.org/", false},
		{"https://cdn.analytics.net/beacon", "https://shop.example.com/", false},
		{"http://localhost:3000/api", "http://localhost:5173/", true},
		{"%zz", "https://example.com/", false},
		{"https://example.com/", "::bad", false},
	}
	for _, c := range cases {
		if got := sameSite(c.a, c.b); got != c.want {
			t.Errorf("sameSite(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Different IP hosts, and siblings under a two-label public suffix, are different sites.
func TestSameSiteIPsAndPublicSuffixes(t *testing.T) {
	for _, c := range [][2]string{{"http://10.0.0.1/api", "http://127.0.0.1/"}, {"https://tracker.co.uk/", "https://shop.co.uk/"}} {
		if sameSite(c[0], c[1]) {
			t.Errorf("sameSite(%q, %q) = true", c[0], c[1])
		}
	}
}

// origin is scheme://host[:port] of an absolute URL, else "".
func TestOrigin(t *testing.T) {
	cases := map[string]string{
		"https://example.com/path?q=1#x":   "https://example.com",
		"http://localhost:3000/a":          "http://localhost:3000",
		"https://user:pw@example.com/":     "https://example.com",
		"about:blank":                      "",
		"data:text/html,<p>hi</p>":         "",
		"example.com/path":                 "",
		"%zz":                              "",
		"file:///Users/me/index.html":      "",
		"chrome-extension://abcdef/x.html": "chrome-extension://abcdef",
	}
	for in, want := range cases {
		if got := origin(in); got != want {
			t.Errorf("origin(%q) = %q, want %q", in, got, want)
		}
	}
}

// isURL accepts anything with a scheme separator, about: and data: URLs.
func TestIsURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://example.com": true, "http://localhost:3000": true, "file:///x.html": true, "about:blank": true,
		"data:text/html,hi": true, "com.example.app": false, "example.com": false, "": false, "mailto:x@y": false,
	} {
		if got := isURL(in); got != want {
			t.Errorf("isURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// defaults: 500 ms to start reacting and a 5 s settle timeout, unless the caller says otherwise.
func TestDefaults(t *testing.T) {
	if o := defaults(driver.ActionOpts{}); o.NoChangeMs != 500 || o.Timeout != 5*time.Second {
		t.Errorf("defaults = %+v", o)
	}
	in := driver.ActionOpts{NoChangeMs: 40, Timeout: time.Second, React: true, NoSettle: true, Drag: true}
	if o := defaults(in); o != in {
		t.Errorf("defaults(%+v) = %+v, want it unchanged", in, o)
	}
}

// The key table: DOM key/code names and Windows virtual key codes; only Enter produces text.
func TestKeys(t *testing.T) {
	want := map[string]keyDef{
		"enter":     {"Enter", "Enter", 13, "\r"},
		"backspace": {"Backspace", "Backspace", 8, ""},
		"tab":       {"Tab", "Tab", 9, ""},
		"escape":    {"Escape", "Escape", 27, ""},
		"home":      {"Home", "Home", 36, ""},
	}
	if len(keys) != len(want) {
		t.Errorf("%d keys, want %d", len(keys), len(want))
	}
	for name, k := range want {
		if keys[name] != k {
			t.Errorf("keys[%q] = %+v, want %+v", name, keys[name], k)
		}
	}
}

// The embedded page helper defines window.__tb with snapshot and css, and reports through __tbNotify.
func TestPageJS(t *testing.T) {
	for _, want := range []string{"window.__tb", "tb.snapshot", "tb.css", "__tbNotify", "MutationObserver"} {
		if !strings.Contains(pageJS, want) {
			t.Errorf("page.js lacks %q", want)
		}
	}
	if _, err := goja.Compile("page.js", pageJS, false); err != nil {
		t.Errorf("page.js does not parse: %v", err)
	}
}

// TB_CHROME overrides the browser search; a found browser is a real file.
func TestChromePath(t *testing.T) {
	t.Setenv("TB_CHROME", "/opt/custom/chrome")
	if got := ChromePath(); got != "/opt/custom/chrome" {
		t.Errorf("ChromePath = %q", got)
	}
	t.Setenv("TB_CHROME", "")
	t.Setenv("PATH", t.TempDir())
	if p := ChromePath(); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("ChromePath = %q, which does not exist", p)
		}
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "chromium"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin)
	if p := ChromePath(); p == "" {
		t.Error("a chromium on PATH was not found")
	}
}

// scrollByJS's JavaScript, run against a small fake DOM: it scrolls the nearest scrollable
// ancestor of the point by (dx, dy), else the page, and says whether anything moved.
func TestScrollByJS(t *testing.T) {
	const dom = `
function el(tag, o) {
  return Object.assign({tagName: tag, parentElement: null, scrollLeft: 0, scrollTop: 0, scrollHeight: 100, clientHeight: 100,
    scrollWidth: 100, clientWidth: 100, ox: 'visible', oy: 'visible', maxTop: 0, maxLeft: 0, calls: [],
    scrollBy(o) { this.calls.push(o); this.scrollTop = Math.max(0, Math.min(this.maxTop, this.scrollTop + o.top));
      this.scrollLeft = Math.max(0, Math.min(this.maxLeft, this.scrollLeft + o.left)); }}, o || {});
}
var html = el('HTML', {maxTop: 5000, scrollHeight: 6000});
var body = el('BODY', {parentElement: html});
var list = el('DIV', {parentElement: body, oy: 'auto', scrollHeight: 2000, clientHeight: 400, maxTop: 1600});
var hidden = el('DIV', {parentElement: list, oy: 'hidden', scrollHeight: 900, clientHeight: 100, maxTop: 800});
var row = el('LI', {parentElement: hidden});
var carousel = el('DIV', {parentElement: body, ox: 'scroll', scrollWidth: 3000, clientWidth: 300, maxLeft: 2700});
var slide = el('DIV', {parentElement: carousel});
var frame = el('IFRAME', {parentElement: body});
var loose = el('P', {parentElement: body});
var hit = null;
var document = {body: body, documentElement: html, scrollingElement: html, elementFromPoint: function (x, y) { return hit; }};
function getComputedStyle(e) { return {overflowY: e.oy, overflowX: e.ox}; }
`
	cases := []struct {
		name           string
		hit            string
		dx, dy         int
		want           string
		scrolled, prop string // which element's property moved
		amount         int64
	}{
		{"nothing there", "null", 0, 300, "still", "", "", 0},
		{"over a frame", "frame", 0, 300, "frame", "", "", 0},
		{"nearest scrollable ancestor", "row", 0, 300, "moved", "list", "scrollTop", 300},
		{"clamped at the end", "row", 0, 99999, "moved", "list", "scrollTop", 1600},
		{"back past the top", "row", 0, -50, "still", "list", "scrollTop", 0},
		{"sideways", "slide", 250, 0, "moved", "carousel", "scrollLeft", 250},
		{"sideways ignores vertical-only scrollers", "row", 250, 0, "still", "html", "scrollLeft", 0},
		{"no scrollable ancestor: the page", "loose", 0, 400, "moved", "html", "scrollTop", 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vm := goja.New()
			if _, err := vm.RunString(dom + "hit = " + c.hit + ";"); err != nil {
				t.Fatal(err)
			}
			expr := fmt.Sprintf(scrollByJS, 10, 20, c.dx, c.dy)
			if strings.Contains(expr, "%!") {
				t.Fatalf("format error in %s", expr)
			}
			v, err := vm.RunString(expr)
			if err != nil {
				t.Fatal(err)
			}
			if v.String() != c.want {
				t.Errorf("result %q, want %q", v, c.want)
			}
			if c.scrolled != "" {
				got := vm.Get(c.scrolled).ToObject(vm).Get(c.prop).ToInteger()
				if got != c.amount {
					t.Errorf("%s.%s = %d, want %d", c.scrolled, c.prop, got, c.amount)
				}
			}
			if c.want == "moved" {
				call := vm.Get(c.scrolled).ToObject(vm).Get("calls").Export().([]any)[0].(map[string]any)
				if call["behavior"] != "instant" || call["left"] != int64(c.dx) || call["top"] != int64(c.dy) {
					t.Errorf("scrollBy(%v)", call)
				}
			}
		})
	}
}

// pageEnv is a fake window for page.js's change reporting: listeners by event name, the
// MutationObserver's callback, a settable clock, and every __tbNotify payload.
const pageEnv = `
var listeners = {}, notes = [], now = 0, observed = null;
var window = {
  addEventListener: function (ev, fn) { (listeners[ev] = listeners[ev] || []).push(fn); },
  __tbNotify: function (p) { notes.push(JSON.parse(p)); },
};
var performance = { now: function () { return now; } };
function MutationObserver(cb) { observed = cb; this.observe = function () {}; }
var document = { documentElement: {}, addEventListener: function () {} };
function ShadowRoot() {}
function getComputedStyle() { return { animationIterationCount: '1' }; }
// An event's target is the list unless the test names another, as a browser always names one.
function fire(ev, e) { if (!e.target) e.target = list; (listeners[ev] || []).forEach(function (fn) { fn(e); }); }
function last() { return notes[notes.length - 1]; }
var list = { nodeType: 1, isConnected: true };
`

func runPage(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	if _, err := vm.RunString(pageEnv); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(pageJS); err != nil {
		t.Fatalf("page.js: %v", err)
	}
	return vm
}

func jsInt(t *testing.T, vm *goja.Runtime, expr string) int64 {
	t.Helper()
	v, err := vm.RunString(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return v.ToInteger()
}

// An element that keeps changing on its own for 500 ms is restless (a ticker); one that changes
// right after each of the test's inputs (a list growing with every Enter) stays solid.
func TestPageJSRestless(t *testing.T) {
	vm := runPage(t)
	// On its own: a change every 100 ms. From 500 ms on its changes are restless.
	for ms := 0; ms <= 700; ms += 100 {
		vm.RunString(fmt.Sprintf("now = %d; observed([{ target: list }]);", ms))
	}
	if s := jsInt(t, vm, "last().s"); s != 0 {
		t.Error("a ticker's change after 700 ms is still solid")
	}

	vm = runPage(t)
	// The same pace, each change following a key press: never restless.
	for ms := 0; ms <= 1500; ms += 100 {
		vm.RunString(fmt.Sprintf("now = %d; fire('keydown', { isTrusted: true }); now += 5; observed([{ target: list }]);", ms))
	}
	if s := jsInt(t, vm, "last().s"); s != 1 {
		t.Error("a list changing after each key press was taken for restless")
	}
	// A page's own synthetic events are not the test's input.
	vm = runPage(t)
	for ms := 0; ms <= 700; ms += 100 {
		vm.RunString(fmt.Sprintf("now = %d; fire('keydown', { isTrusted: false }); observed([{ target: list }]);", ms))
	}
	if s := jsInt(t, vm, "last().s"); s != 0 {
		t.Error("untrusted events kept a ticker solid")
	}
	// A drag (scrollByJS) marks input too, through window.__tb.acted.
	vm = runPage(t)
	for ms := 0; ms <= 1000; ms += 100 {
		vm.RunString(fmt.Sprintf("now = %d; window.__tb.acted(); observed([{ target: list }]);", ms))
	}
	if s := jsInt(t, vm, "last().s"); s != 1 {
		t.Error("changes during drags were taken for restless")
	}
}

// A transition holds the page (its running count goes up) unless it only changes a color.
func TestPageJSTransitions(t *testing.T) {
	vm := runPage(t)
	for _, prop := range []string{"color", "background-color", "border-top-color", "fill", "stroke", "box-shadow", "text-shadow"} {
		vm.RunString(fmt.Sprintf("fire('transitionrun', { propertyName: %q });", prop))
		if a := jsInt(t, vm, "notes.length ? last().a : 0"); a != 0 {
			t.Errorf("%s transition holds the page (a = %d)", prop, a)
		}
	}
	for _, prop := range []string{"opacity", "transform", "left", "height"} {
		vm.RunString(fmt.Sprintf("fire('transitionrun', { propertyName: %q });", prop))
		if a := jsInt(t, vm, "last().a"); a != 1 {
			t.Errorf("%s transition: a = %d, want 1", prop, a)
		}
		vm.RunString(fmt.Sprintf("fire('transitionend', { propertyName: %q });", prop))
		if a := jsInt(t, vm, "last().a"); a != 0 {
			t.Errorf("%s transition ended: a = %d", prop, a)
		}
	}
	// A color transition's end does not cancel another transition's count.
	vm.RunString("fire('transitionrun', { propertyName: 'transform' }); fire('transitionend', { propertyName: 'color' });")
	if a := jsInt(t, vm, "last().a"); a != 1 {
		t.Errorf("a color end ended a transform: a = %d", a)
	}
}

// An element removed while it transitions or animates (a toast an app unmounts mid-fade) gets
// its cancel event while detached, where it never reaches the window: it stops being counted
// once it is out of the page, while one still in the page keeps holding it (issue #75).
func TestPageJSRemovedMidAnimation(t *testing.T) {
	vm := runPage(t)
	vm.RunString(`var toast = { nodeType: 1, isConnected: true }, modal = { nodeType: 1, isConnected: true };
fire('transitionrun', { target: toast, propertyName: 'opacity' });
fire('animationstart', { target: modal, animationName: 'slide' });
fire('transitionrun', { propertyName: 'transform' });`)
	if a := jsInt(t, vm, "last().a"); a != 3 {
		t.Fatalf("three running: a = %d", a)
	}
	vm.RunString("toast.isConnected = false; modal.isConnected = false; observed([{ target: list }]);")
	if a := jsInt(t, vm, "last().a"); a != 1 {
		t.Errorf("toast and modal removed mid-run, the list still moving: a = %d, want 1", a)
	}
	vm.RunString("fire('transitionend', { propertyName: 'transform' });")
	if a := jsInt(t, vm, "last().a"); a != 0 {
		t.Errorf("all done: a = %d", a)
	}
}

// covered: ” when a click at the point reaches the node, something inside it or something
// it is inside of; else a name for what is on top. Shadow roots are followed down.
func TestPageJSCovered(t *testing.T) {
	vm := runPage(t)
	_, err := vm.RunString(`
function el(tag, o) { return Object.assign({ tagName: tag, nodeType: 1, parentNode: null, isConnected: true, id: '', className: '' }, o || {}); }
document.elementFromPoint = function (x, y) { return hit; };
var body = el('BODY');
var button = el('BUTTON', { parentNode: body, id: 'under_button' });
var label = el('SPAN', { parentNode: button });
var veil = el('DIV', { parentNode: body, id: 'veil' });
var toast = el('DIV', { parentNode: body, className: 'toast big' });
var other = { ownerDocument: {} };
button.ownerDocument = document; label.ownerDocument = document;
window.__tb.nodes = [button, label, other];
var hit = null;
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ hit, want string }{
		{"button", ""}, {"label", ""}, {"body", ""}, {"veil", "#veil"}, {"toast", "div.toast"}, {"null", ""},
	} {
		v, err := vm.RunString("hit = " + c.hit + "; window.__tb.covered(0, 10, 10)")
		if err != nil {
			t.Fatal(err)
		}
		if v.String() != c.want {
			t.Errorf("hit %s: covered = %q, want %q", c.hit, v, c.want)
		}
	}
	// A node in another (frame) document is not checked.
	if v, _ := vm.RunString("hit = veil; window.__tb.covered(2, 10, 10)"); v.String() != "" {
		t.Errorf("frame node: %q", v)
	}
	// A shadow host's inner element is followed down.
	if v, _ := vm.RunString(`var inner = el('SPAN', { parentNode: null }); var root = { elementFromPoint: function () { return inner; } };
		inner.parentNode = root; Object.setPrototypeOf(root, ShadowRoot.prototype); root.host = button;
		var host = el('X-HOST', { parentNode: body, shadowRoot: root }); hit = host; window.__tb.covered(0, 10, 10)`); v.String() != "" {
		t.Errorf("inside the target's shadow root: %q", v)
	}
}

func TestActivePortURL(t *testing.T) {
	dir := t.TempDir()
	if got := activePortURL(dir); got != "" {
		t.Errorf("no file: %q", got)
	}
	file := filepath.Join(dir, "DevToolsActivePort")
	os.WriteFile(file, []byte("9222\n"), 0o644) // Chrome still writing it
	if got := activePortURL(dir); got != "" {
		t.Errorf("half-written file: %q", got)
	}
	os.WriteFile(file, []byte("9222\r\n/devtools/browser/abc-123\r\n"), 0o644)
	if got := activePortURL(dir); got != "ws://127.0.0.1:9222/devtools/browser/abc-123" {
		t.Errorf("got %q", got)
	}
}
