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
