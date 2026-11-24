package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/elforama/testbuddy/internal/ui"
)

const webViewApp = "com.example.web"

func webViewScreen(views ...ui.Rect) (*ui.Screen, *ui.Window) {
	w := &ui.Window{Type: "app", Package: webViewApp, Active: true}
	w.Nodes = append(w.Nodes, &ui.Node{Depth: 0, Class: "android.widget.FrameLayout", Visible: true,
		Bounds: ui.Rect{R: 1080, B: 2400}})
	for _, b := range views {
		w.Nodes = append(w.Nodes,
			&ui.Node{Depth: 1, Class: "android.webkit.WebView", Visible: true, Bounds: b},
			&ui.Node{Depth: 2, Class: "android.view.View", Visible: true, Bounds: b})
	}
	w.Nodes = append(w.Nodes, &ui.Node{Depth: 1, Class: "android.widget.TextView", Text: "Footer", Visible: true,
		Bounds: ui.Rect{T: 2200, R: 1080, B: 2400}})
	return &ui.Screen{Platform: "android", Width: 1080, Height: 2400, Windows: []*ui.Window{w}}, w
}

func classes(w *ui.Window) []string {
	var out []string
	for _, n := range w.Nodes {
		out = append(out, n.Class)
	}
	return out
}

type devToolsServer struct {
	srv         *httptest.Server
	pages       []map[string]string
	snapshot    string
	closeOnEval bool
}

func newDevToolsServer(t *testing.T, snapshot string) *devToolsServer {
	t.Helper()
	s := &devToolsServer{snapshot: snapshot}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(s.pages)
	})
	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(64 << 20)
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			json.Unmarshal(data, &req)
			if s.closeOnEval {
				return
			}
			reply := map[string]any{"id": req.ID, "result": map[string]any{
				"result": map[string]any{"type": "string", "value": s.snapshot}}}
			if req.Method != "Runtime.evaluate" {
				reply = map[string]any{"id": req.ID, "error": map[string]any{"code": -32601, "message": "unknown " + req.Method}}
			}
			b, _ := json.Marshal(reply)
			c.Write(r.Context(), websocket.MessageText, b)
		}
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *devToolsServer) port() int { return s.srv.Listener.Addr().(*net.TCPAddr).Port }

func TestDevToolsServerTakesLargeRequests(t *testing.T) {
	dt := newDevToolsServer(t, `{"W":360,"Nodes":[]}`)
	ctx := ctxT(t)
	c, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+strconv.Itoa(dt.port())+"/devtools/page/P1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	req, _ := json.Marshal(map[string]any{"id": 1, "method": "Runtime.evaluate",
		"params": map[string]any{"expression": strings.Repeat("x", 1<<20)}})
	if err := c.Write(ctx, websocket.MessageText, req); err != nil {
		t.Fatalf("issue #335: the stub DevTools server closed on a 1 MiB request: %v", err)
	}
	_, reply, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("issue #335: the stub DevTools server did not answer a 1 MiB request: %v", err)
	}
	if !strings.Contains(string(reply), `"id":1`) {
		t.Errorf("reply %s", reply)
	}
}

func TestAugmentWebViewSplicesThePage(t *testing.T) {
	snap, _ := json.Marshal(map[string]any{"W": 360, "Nodes": []map[string]any{
		{"d": 0, "b": [4]int{0, 0, 360, 600}, "c": "body"},
		{"d": 1, "b": [4]int{10, 20, 110, 60}, "c": "button", "t": "Buy", "id": "buy", "f": 1 | 2},
		{"d": 1, "b": [4]int{0, 700, 360, 800}, "c": "a", "t": "Below the WebView"},
	}})
	dt := newDevToolsServer(t, string(snap))
	dt.pages = []map[string]string{
		{"type": "service_worker", "webSocketDebuggerUrl": "ws://localhost/devtools/page/SW"},
		{"type": "page", "webSocketDebuggerUrl": "ws://localhost:9222/devtools/page/P1"},
	}
	a := shAgent(t, map[string]shAnswer{"pidof " + webViewApp: {"4242\n", 0}})
	s := stubADB(t, []stubRule{{match: "forward tcp:0", out: strconv.Itoa(dt.port())}})
	d := stubbedDriver(t, s, a)
	d.SetWebViewDevTools(webViewApp)

	screen, w := webViewScreen(ui.Rect{T: 200, R: 1080, B: 2200})
	d.augmentWebView(ctxT(t), screen)

	want := []string{"android.widget.FrameLayout", "android.webkit.WebView", "android.view.View",
		"body", "button", "a", "android.widget.TextView"}
	if got := classes(w); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("nodes %v\nwant  %v", got, want)
	}
	body, buy, below := w.Nodes[3], w.Nodes[4], w.Nodes[5]
	if body.Depth != 2 || buy.Depth != 3 {
		t.Errorf("depths body %d, button %d; want 2, 3 (under the WebView at depth 1)", body.Depth, buy.Depth)
	}
	if want := (ui.Rect{L: 30, T: 260, R: 330, B: 380}); buy.Bounds != want || !buy.Visible {
		t.Errorf("button bounds %v visible %v; want %v, visible", buy.Bounds, buy.Visible, want)
	}
	if buy.Text != "Buy" || buy.ID != "buy" || !buy.Clickable || !buy.Enabled {
		t.Errorf("button lost its fields: %+v", buy)
	}
	if want := (ui.Rect{T: 200, R: 1080, B: 2000}); body.Bounds != want {
		t.Errorf("body bounds %v, want %v", body.Bounds, want)
	}
	if below.Visible || !below.Bounds.Empty() {
		t.Errorf("an element below the WebView's end is clipped away: %v visible %v", below.Bounds, below.Visible)
	}
	var forwards []string
	for _, c := range s.joined() {
		if strings.Contains(c, "forward") {
			forwards = append(forwards, c)
		}
	}
	wantFwd := []string{
		"-s " + testSerial + " forward tcp:0 localabstract:webview_devtools_remote_4242",
		"-s " + testSerial + " forward --remove tcp:" + strconv.Itoa(dt.port()),
	}
	if strings.Join(forwards, "|") != strings.Join(wantFwd, "|") {
		t.Errorf("adb forwards %q\nwant %q", forwards, wantFwd)
	}
}

func TestAugmentWebViewKeepsTheNativeTree(t *testing.T) {
	snap := `{"W":360,"Nodes":[{"d":0,"b":[0,0,360,600],"c":"body"}]}`
	one := []ui.Rect{{T: 200, R: 1080, B: 2200}}
	page := map[string]string{"type": "page", "webSocketDebuggerUrl": "ws://localhost/devtools/page/P1"}
	cases := []struct {
		name     string
		app      string
		views    []ui.Rect
		pid      string
		pages    []map[string]string
		snapshot string
		inactive bool
		otherPkg bool
		wantPid  bool
		wantNote bool
	}{
		{name: "not asked for", app: "", views: one, pid: "4242", pages: []map[string]string{page}},
		{name: "invalid package", app: "com.example.web; reboot", views: one, pid: "4242", pages: []map[string]string{page}},
		{name: "no WebView", app: webViewApp, pid: "4242", pages: []map[string]string{page}},
		{name: "empty WebView", app: webViewApp, views: []ui.Rect{{}}, pid: "4242", pages: []map[string]string{page}},
		{name: "two WebViews", app: webViewApp, views: []ui.Rect{{T: 200, R: 1080, B: 1000}, {T: 1000, R: 1080, B: 2000}},
			pid: "4242", pages: []map[string]string{page}},
		{name: "window in the background", app: webViewApp, views: one, inactive: true, pid: "4242", pages: []map[string]string{page}},
		{name: "another app's window", app: webViewApp, views: one, otherPkg: true, pid: "4242", pages: []map[string]string{page}},
		{name: "app not running", app: webViewApp, views: one, pid: "", pages: []map[string]string{page}, wantPid: true},
		{name: "no page", app: webViewApp, views: one, pid: "4242", wantPid: true},
		{name: "two pages", app: webViewApp, views: one, pid: "4242", pages: []map[string]string{page, page}, wantPid: true},
		{name: "page without a page path", app: webViewApp, views: one, pid: "4242",
			pages: []map[string]string{{"type": "page", "webSocketDebuggerUrl": "ws://localhost/devtools/browser/B"}}, wantPid: true},
		{name: "snapshot not JSON", app: webViewApp, views: one, pid: "4242", pages: []map[string]string{page},
			snapshot: "not json", wantPid: true, wantNote: true},
		{name: "page with no width", app: webViewApp, views: one, pid: "4242", pages: []map[string]string{page},
			snapshot: `{"W":0,"Nodes":[{"d":0,"b":[0,0,1,1],"c":"body"}]}`, wantPid: true, wantNote: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := snap
			if c.snapshot != "" {
				s = c.snapshot
			}
			dt := newDevToolsServer(t, s)
			dt.pages = c.pages
			pidOut := ""
			if c.pid != "" {
				pidOut = c.pid + "\n"
			}
			code := 0
			if c.pid == "" {
				code = 1
			}
			a := shAgent(t, map[string]shAnswer{"pidof ": {pidOut, code}})
			st := stubADB(t, []stubRule{{match: "forward tcp:0", out: strconv.Itoa(dt.port())}})
			d := stubbedDriver(t, st, a)
			d.SetWebViewDevTools(c.app)
			screen, w := webViewScreen(c.views...)
			if c.inactive {
				w.Active, w.Focused = false, false
			}
			if c.otherPkg {
				w.Package = "com.example.other"
			}
			before := classes(w)
			d.augmentWebView(ctxT(t), screen)
			if got := classes(w); strings.Join(got, ",") != strings.Join(before, ",") {
				t.Errorf("tree changed: %v\nwas %v", got, before)
			}
			asked := false
			for _, cmd := range a.shCmds() {
				asked = asked || strings.HasPrefix(cmd, "pidof ")
			}
			if asked != c.wantPid {
				t.Errorf("asked for the pid: %v, want %v (commands %q)", asked, c.wantPid, a.shCmds())
			}
			if got := pageNotReadNotes(d); (len(got) == 1) != c.wantNote || len(got) > 1 {
				t.Errorf("issue #350: notes %q, want a \"WebView page not read\" note: %v", got, c.wantNote)
			}
		})
	}
}

func TestAugmentWebViewForwardFails(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"pidof ": {"4242\n", 0}})
	s := stubADB(t, []stubRule{{match: "forward tcp:0", out: "error: cannot bind", code: 1}})
	d := stubbedDriver(t, s, a)
	d.SetWebViewDevTools(webViewApp)
	screen, w := webViewScreen(ui.Rect{T: 200, R: 1080, B: 2200})
	before := len(w.Nodes)
	ctx, cancel := context.WithCancel(ctxT(t))
	defer cancel()
	d.augmentWebView(ctx, screen)
	if len(w.Nodes) != before {
		t.Errorf("nodes %d, want %d", len(w.Nodes), before)
	}
	if got := pageNotReadNotes(d); len(got) != 1 || !strings.Contains(got[0], "cannot bind") {
		t.Errorf("issue #350: notes %q, want one saying the forward failed", got)
	}
	for _, c := range s.joined() {
		if strings.Contains(c, "--remove") {
			t.Errorf("removed a forward that was never made: %q", c)
		}
	}
}

func pageNotReadNotes(d *Driver) []string {
	var out []string
	for _, n := range d.Notes() {
		if strings.HasPrefix(n, "WebView page not read: ") {
			out = append(out, n)
		}
	}
	return out
}

func TestAugmentWebViewSaysWhyThePageWasNotRead(t *testing.T) {
	dt := newDevToolsServer(t, `{"W":360,"Nodes":[]}`)
	dt.pages = []map[string]string{{"type": "page", "webSocketDebuggerUrl": "ws://localhost/devtools/page/P1"}}
	dt.closeOnEval = true
	a := shAgent(t, map[string]shAnswer{"pidof " + webViewApp: {"4242\n", 0}})
	s := stubADB(t, []stubRule{{match: "forward tcp:0", out: strconv.Itoa(dt.port())}})
	d := stubbedDriver(t, s, a)
	d.SetWebViewDevTools(webViewApp)
	d.Notes()
	var w *ui.Window
	for range 3 {
		var screen *ui.Screen
		screen, w = webViewScreen(ui.Rect{T: 200, R: 1080, B: 2200})
		d.augmentWebView(ctxT(t), screen)
	}
	want := []string{"android.widget.FrameLayout", "android.webkit.WebView", "android.view.View", "android.widget.TextView"}
	if got := classes(w); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("nodes %v, want the native tree %v", got, want)
	}
	if got := pageNotReadNotes(d); len(got) != 1 || !strings.Contains(got[0], "Runtime.evaluate") {
		t.Errorf("issue #350: notes %q, want one \"WebView page not read\" note naming the evaluate", got)
	}
	screen, _ := webViewScreen(ui.Rect{T: 200, R: 1080, B: 2200})
	d.augmentWebView(ctxT(t), screen)
	if got := pageNotReadNotes(d); len(got) != 1 {
		t.Errorf("issue #350: the next step's notes %q, want the note again", got)
	}
}

func TestAugmentWebViewWithoutAPageSaysNothing(t *testing.T) {
	dt := newDevToolsServer(t, `{"W":360,"Nodes":[]}`)
	a := shAgent(t, map[string]shAnswer{"pidof " + webViewApp: {"4242\n", 0}})
	s := stubADB(t, []stubRule{{match: "forward tcp:0", out: strconv.Itoa(dt.port())}})
	d := stubbedDriver(t, s, a)
	d.SetWebViewDevTools(webViewApp)
	d.Notes()
	screen, _ := webViewScreen(ui.Rect{T: 200, R: 1080, B: 2200})
	d.augmentWebView(ctxT(t), screen)
	if got := d.Notes(); len(got) != 0 {
		t.Errorf("issue #350: a WebView without a page noted %q", got)
	}
}

func socketAgent(t *testing.T, sockets ...string) *fakeAgent {
	t.Helper()
	list := filepath.Join(t.TempDir(), "unix")
	var b strings.Builder
	b.WriteString("Num       RefCount Protocol Flags    Type St Inode Path\n")
	for i, s := range sockets {
		fmt.Fprintf(&b, "0000000000000000: 00000002 00000000 00010000 0001 01 %d %s\n", 1000+i, s)
	}
	if err := os.WriteFile(list, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	a := shAgent(t, map[string]shAnswer{"pidof " + webViewApp: {"4242\n", 0}})
	pidof := a.handlers["sh"]
	a.on("sh", func(req map[string]any) map[string]any {
		cmd := fmt.Sprint(req["cmd"])
		if !strings.Contains(cmd, "/proc/net/unix") {
			return pidof(req)
		}
		c := exec.Command("/bin/sh", "-c", strings.ReplaceAll(cmd, "/proc/net/unix", list))
		c.Env = []string{"PATH=/usr/bin:/bin"}
		out, err := c.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Errorf("sh: %v", err)
		}
		return map[string]any{"code": code, "out": string(out)}
	})
	return a
}

func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestAugmentWebViewWithoutADevToolsSocket(t *testing.T) {
	for _, c := range []struct {
		name    string
		sockets []string
	}{
		{"no socket", nil},
		{"another pid's socket that starts the same", []string{"@webview_devtools_remote_42420"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := socketAgent(t, c.sockets...)
			s := stubADB(t, []stubRule{{match: "forward tcp:0", out: strconv.Itoa(closedPort(t))}})
			d := stubbedDriver(t, s, a)
			d.SetWebViewDevTools(webViewApp)
			d.Notes()
			screen, _ := webViewScreen(ui.Rect{T: 200, R: 1080, B: 2200})
			d.augmentWebView(ctxT(t), screen)
			if got := d.Notes(); len(got) != 0 {
				t.Errorf("issue #350: noted %q with no DevTools socket for the pid", got)
			}
			if containsAny(s.joined(), "forward") {
				t.Errorf("issue #350: forwarded with no DevTools socket: %q", s.joined())
			}
		})
	}
}

func TestAugmentWebViewUnansweredSocketNotedOncePerStep(t *testing.T) {
	a := socketAgent(t, "@webview_devtools_remote_4242")
	s := stubADB(t, nil)
	d := stubbedDriver(t, s, a)
	d.SetWebViewDevTools(webViewApp)
	d.Notes()
	for range 3 {
		s.rules([]stubRule{{match: "forward tcp:0", out: strconv.Itoa(closedPort(t))}})
		screen, _ := webViewScreen(ui.Rect{T: 200, R: 1080, B: 2200})
		d.augmentWebView(ctxT(t), screen)
	}
	got := pageNotReadNotes(d)
	if len(got) != 1 || !strings.Contains(got[0], "did not answer") {
		t.Errorf("issue #350: notes %q over 3 reads with 3 ports, want one", got)
	}
}

func TestWebViewReasonHasNoPort(t *testing.T) {
	ws := func(port int) error {
		return fmt.Errorf("failed to WebSocket dial: failed to send handshake request: %w", &url.Error{Op: "Get",
			URL: fmt.Sprintf("http://127.0.0.1:%d/devtools/page/P1", port),
			Err: &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port},
				Err: errors.New("connect: connection refused")}})
	}
	read := func(port int) error {
		return fmt.Errorf("Runtime.evaluate: read tcp 127.0.0.1:%d->127.0.0.1:%d: read: connection reset by peer", port+1, port)
	}
	for _, mk := range []func(int) error{ws, read} {
		a, b := webViewReason(mk(40001)), webViewReason(mk(51234))
		if a != b || strings.Contains(a, "40001") || strings.Contains(a, "127.0.0.1") {
			t.Errorf("issue #350: reasons %q and %q, want one without the host or port", a, b)
		}
	}
}
