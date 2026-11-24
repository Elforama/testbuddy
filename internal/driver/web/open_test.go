package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

var stubChrome string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tb-web-stubs-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stubChrome = filepath.Join(dir, "chrome")
	script := "#!/bin/sh\n" +
		"[ -n \"$TB_STUB_DIR\" ] || exit 97\n" +
		"rec=''; for a in \"$@\"; do rec=\"$rec$a\x1f\"; done; printf '%s\x1e' \"$rec\" >> \"$TB_STUB_DIR/chrome.log\"\n" +
		"if [ -f \"$TB_STUB_DIR/chrome.rules\" ]; then . \"$TB_STUB_DIR/chrome.rules\"; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(stubChrome, []byte(script), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type fakeBrowser struct {
	*fakeCDP
	dir string
}

func newFakeBrowser(t *testing.T, announce bool, pages []map[string]string) *fakeBrowser {
	f := &fakeBrowser{fakeCDP: newFakeCDP(t), dir: t.TempDir()}
	host := f.srv.Listener.Addr().String()
	f.mu.Lock()
	f.onHTTP = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/json/list" {
			return false
		}
		list := []map[string]string{}
		for _, p := range pages {
			list = append(list, map[string]string{"id": strings.TrimPrefix(p["path"], "/devtools/page/"), "type": p["type"],
				"webSocketDebuggerUrl": "ws://" + host + p["path"]})
		}
		json.NewEncoder(w).Encode(list)
		return true
	}
	f.mu.Unlock()
	quit := filepath.Join(f.dir, "quit")
	f.on("Browser.close", func(cdpMsg) any {
		os.WriteFile(quit, nil, 0o644)
		return map[string]any{}
	})
	f.on("Page.getFrameTree", func(cdpMsg) any {
		return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "MAIN-FRAME", "url": "about:blank"}}}
	})
	rules := "exit 0\n"
	if announce {
		rules = fmt.Sprintf("printf 'starting\\nDevTools listening on ws://%s/devtools/browser/b1\\n' >&2\n", host) +
			"while [ ! -f " + sq(quit) + " ] && [ -d " + sq(f.dir) + " ]; do /bin/sleep 0.02; done\nexit 0\n"
	}
	if err := os.WriteFile(filepath.Join(f.dir, "chrome.rules"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TB_STUB_DIR", f.dir)
	t.Setenv("TB_CHROME", stubChrome)
	t.Setenv("TMPDIR", t.TempDir())
	if ChromePath() != stubChrome {
		t.Fatal("TB_CHROME is not the stub: refusing to start a browser")
	}
	t.Cleanup(func() { os.WriteFile(quit, nil, 0o644) })
	return f
}

func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (f *fakeBrowser) args(t *testing.T) []string {
	b, err := os.ReadFile(filepath.Join(f.dir, "chrome.log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(strings.TrimSuffix(string(b), "\x1e"), "\x1f"), "\x1f")
}

var onePage = []map[string]string{{"type": "service_worker", "path": "/devtools/page/sw"}, {"type": "page", "path": "/devtools/page/p1"}}

func TestOpen(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	t.Setenv("TB_WEB_SIZE", "800x600")
	t.Setenv("TB_WEB_HEADLESS", "1")
	d, err := Open(ctxT(t), device.WebDevice(2))
	if err != nil {
		t.Fatal(err)
	}
	if d.Serial() != "web-2" || d.Name() != "Chrome 2" {
		t.Errorf("identity %q %q", d.Serial(), d.Name())
	}
	if w, h := d.Size(); w != 800 || h != 600 {
		t.Errorf("Size = %dx%d", w, h)
	}
	if d.mainFrame != "MAIN-FRAME" {
		t.Errorf("mainFrame = %q", d.mainFrame)
	}
	args := f.args(t)
	for _, want := range []string{"--remote-debugging-port=0", "--no-first-run", "--disable-save-password-bubble",
		"--password-store=basic", "--window-size=800,600", "--headless=new", "--user-data-dir=" + d.profile} {
		if !contains(args, want) {
			t.Errorf("no %s in %q", want, args)
		}
	}
	if args[len(args)-1] != "about:blank" {
		t.Errorf("last argument %q", args[len(args)-1])
	}
	prefs, err := os.ReadFile(filepath.Join(d.profile, "Default", "Preferences"))
	var p map[string]any
	if err != nil || json.Unmarshal(prefs, &p) != nil || p["credentials_enable_service"] != false {
		t.Errorf("Preferences %s (%v)", prefs, err)
	}
	var setup []string
	for _, m := range f.calls("") {
		setup = append(setup, m.Method)
	}
	want := []string{"Target.attachToTarget", "Page.enable", "Page.setLifecycleEventsEnabled", "Runtime.enable", "Network.enable", "Runtime.addBinding",
		"Page.addScriptToEvaluateOnNewDocument", "Target.setAutoAttach", "Target.setDiscoverTargets", "Target.setAutoAttach",
		"Page.getFrameTree", "Runtime.evaluate", "Page.getLayoutMetrics"}
	if !reflect.DeepEqual(setup, want) {
		t.Errorf("setup %v\nwant %v", setup, want)
	}
	if a := f.calls("Target.attachToTarget")[0].params(); a["targetId"] != "p1" || a["flatten"] != true {
		t.Errorf("attach %v", a)
	}
	for _, m := range f.calls("Target.setAutoAttach") {
		if a := m.params(); a["autoAttach"] != true || a["waitForDebuggerOnStart"] != true || a["flatten"] != true {
			t.Errorf("auto-attach %v", a)
		}
	}
	if a := f.calls("Target.setAutoAttach")[1].params(); !reflect.DeepEqual(a["filter"], []any{map[string]any{"type": "page"}}) {
		t.Errorf("the browser auto-attaches %v, want new tabs", a["filter"])
	}
	if e := f.calls("Runtime.evaluate")[0].params()["expression"]; e != pageJS {
		t.Error("page.js was not evaluated in the open page")
	}
	profile := d.profile
	t0 := time.Now()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) > 2*time.Second {
		t.Errorf("Close took %s (the browser did not exit on Browser.close)", time.Since(t0))
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Error("profile left behind")
	}
}

func TestOpenDefaults(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	t.Setenv("TB_WEB_SIZE", "")
	t.Setenv("TB_WEB_HEADLESS", "")
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	args := f.args(t)
	if !contains(args, "--window-size=1280,800") || contains(args, "--headless=new") {
		t.Errorf("args %q", args)
	}
}

func TestOpenBadSize(t *testing.T) {
	for _, v := range []string{"big", "1280", "1280x", "x800", "0x800", "1280x0", "-1x800", "1280x800x2",
		"1280.5x800", "1280 800", "1280xx800", "+1280x800", "1280,800", "10001x800", "1280x10001", "199x800", "1280x199", "1x1", "99999999999999999999x1"} {
		t.Run(v, func(t *testing.T) {
			f := newFakeBrowser(t, true, onePage)
			t.Setenv("TB_WEB_SIZE", v)
			d, err := Open(ctxT(t), device.WebDevice(1))
			if d != nil {
				d.Close()
			}
			want := fmt.Sprintf("TB_WEB_SIZE: want WIDTHxHEIGHT, each a whole number from 200 to 10000, such as 1280x800, not %q", v)
			if err == nil || err.Error() != want {
				t.Errorf("err = %v\nwant %s", err, want)
			}
			if _, err := os.Stat(filepath.Join(f.dir, "chrome.log")); !os.IsNotExist(err) {
				t.Error("a browser was started")
			}
		})
	}
	for v, want := range map[string]string{"200x200": "200,200", "10000x10000": "10000,10000",
		"1280 x 800": "1280,800", "1280x800\n": "1280,800", " 1280X800 ": "1280,800"} {
		t.Run("accepts "+v, func(t *testing.T) {
			f := newFakeBrowser(t, true, onePage)
			t.Setenv("TB_WEB_SIZE", v)
			d, err := Open(ctxT(t), device.WebDevice(1))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if !contains(f.args(t), "--window-size="+want) {
				t.Errorf("args %q", f.args(t))
			}
		})
	}
}

func TestOpenSizeChromeChanged(t *testing.T) {
	for _, c := range []struct {
		size     string
		pageW    int
		pageH    int
		wantNote string
	}{
		{"390x844", 500, 757, "web: asked for a 390x844 window, Chrome made it wider: the page is 500x757"},
		{"1280x800", 1280, 713, ""},
		{"1280x800", 1264, 713, ""},
	} {
		t.Run(fmt.Sprintf("%s gives %dx%d", c.size, c.pageW, c.pageH), func(t *testing.T) {
			f := newFakeBrowser(t, true, onePage)
			f.on("Page.getLayoutMetrics", func(cdpMsg) any {
				return map[string]any{"cssLayoutViewport": map[string]any{"pageX": 0, "pageY": 0,
					"clientWidth": c.pageW, "clientHeight": c.pageH}}
			})
			t.Setenv("TB_WEB_SIZE", c.size)
			d, err := Open(ctxT(t), device.WebDevice(1))
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			var want []string
			if c.wantNote != "" {
				want = []string{c.wantNote}
			}
			if got := d.Notes(); !reflect.DeepEqual(got, want) {
				t.Errorf("notes %q, want %q", got, want)
			}
			if again := d.Notes(); len(again) != 0 {
				t.Errorf("said again: %q", again)
			}
		})
	}
}

func TestOpenNoDevTools(t *testing.T) {
	f := newFakeBrowser(t, false, onePage)
	quit := filepath.Join(f.dir, "quit")
	if err := os.WriteFile(filepath.Join(f.dir, "chrome.rules"),
		[]byte("while [ ! -f "+sq(quit)+" ] && [ -d "+sq(f.dir)+" ]; do /bin/sleep 0.02; done\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Open(ctx, device.WebDevice(1)); err != context.DeadlineExceeded {
		t.Errorf("err = %v", err)
	}
	if entries, _ := os.ReadDir(os.Getenv("TMPDIR")); len(entries) != 0 {
		t.Errorf("left %d entries in the temp folder", len(entries))
	}
}

func TestOpenFailures(t *testing.T) {
	t.Run("no page", func(t *testing.T) {
		f := newFakeBrowser(t, true, []map[string]string{{"type": "service_worker", "path": "/x"}})
		if _, err := Open(ctxT(t), device.WebDevice(1)); err == nil || err.Error() != "Chrome opened no page" {
			t.Errorf("err = %v", err)
		}
		if len(f.calls("Browser.close")) != 1 {
			t.Error("the browser was not closed")
		}
	})
	t.Run("setup step", func(t *testing.T) {
		f := newFakeBrowser(t, true, onePage)
		f.on("Network.enable", func(cdpMsg) any { return errReply{cdpError{Code: -32601, Message: "'Network.enable' wasn't found"}} })
		if _, err := Open(ctxT(t), device.WebDevice(1)); err == nil || !strings.Contains(err.Error(), "wasn't found") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		newFakeBrowser(t, true, onePage)
		t.Setenv("TB_CHROME", filepath.Join(t.TempDir(), "no-chrome"))
		if _, err := Open(ctxT(t), device.WebDevice(1)); err == nil {
			t.Error("started a browser that does not exist")
		}
		if entries, _ := os.ReadDir(os.Getenv("TMPDIR")); len(entries) != 0 {
			t.Errorf("profile left behind: %v", entries)
		}
	})
	t.Run("unreachable endpoint", func(t *testing.T) {
		f := newFakeBrowser(t, true, onePage)
		f.mu.Lock()
		f.onHTTP = func(w http.ResponseWriter, r *http.Request) bool {
			http.Error(w, "no", http.StatusForbidden)
			return true
		}
		f.mu.Unlock()
		os.WriteFile(filepath.Join(f.dir, "quit"), nil, 0o644)
		if _, err := Open(ctxT(t), device.WebDevice(1)); err == nil {
			t.Error("connected to an endpoint that refuses websockets")
		}
	})
}

func TestKill(t *testing.T) {
	f := newFakeBrowser(t, true, onePage)
	d, err := Open(ctxT(t), device.WebDevice(1))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	profile := d.profile
	d.Kill()
	exited := make(chan error, 1)
	go func() { exited <- d.cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("the browser still runs after Kill")
	}
	if len(f.calls("Browser.close")) != 0 {
		t.Error("Kill asked the browser to close instead of killing it")
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Error("profile left behind")
	}
}
