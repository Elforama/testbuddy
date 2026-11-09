package android

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/elforama/testbuddy/internal/ui"
)

// A flow with androidWebViewHierarchy: devtools reads the page in the app's one WebView
// through the WebView's DevTools socket and splices its elements into the native tree.

const webViewApp = "com.example.web"

// webViewScreen is the app's window holding the given WebViews (by bounds), each followed by
// one native child, and a native "Footer" after them all.
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

// devToolsServer is a WebView's DevTools endpoint: /json/list names the pages, and each page's
// websocket answers Runtime.evaluate with the page script's snapshot.
type devToolsServer struct {
	srv      *httptest.Server
	pages    []map[string]string
	snapshot string // the snapshot JSON the page script returns
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

// The page's elements land right after the WebView's own subtree, scaled from CSS pixels to
// the WebView's device pixels, moved to its position, clipped to it and one level deeper.
// The forwarded DevTools port is removed afterwards.
func TestAugmentWebViewSplicesThePage(t *testing.T) {
	// The page is 360 CSS px wide in a 1080 px WebView: scale 3, origin (0, 200).
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

// Whenever the WebView-to-page mapping is not certain, or anything on the way fails, the
// native tree stays exactly as it was.
func TestAugmentWebViewKeepsTheNativeTree(t *testing.T) {
	snap := `{"W":360,"Nodes":[{"d":0,"b":[0,0,360,600],"c":"body"}]}`
	one := []ui.Rect{{T: 200, R: 1080, B: 2200}}
	page := map[string]string{"type": "page", "webSocketDebuggerUrl": "ws://localhost/devtools/page/P1"}
	cases := []struct {
		name     string
		app      string // SetWebViewDevTools
		views    []ui.Rect
		pid      string
		pages    []map[string]string
		snapshot string
		inactive bool
		otherPkg bool
		wantPid  bool // the device is asked for the app's pid
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
			snapshot: "not json", wantPid: true},
		{name: "page with no width", app: webViewApp, views: one, pid: "4242", pages: []map[string]string{page},
			snapshot: `{"W":0,"Nodes":[{"d":0,"b":[0,0,1,1],"c":"body"}]}`, wantPid: true},
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
				code = 1 // pidof finds nothing
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
		})
	}
}

// A WebView's DevTools socket the forward cannot reach (adb has no port for it) leaves the
// tree alone and does not hang the screen read.
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
	for _, c := range s.joined() {
		if strings.Contains(c, "--remove") {
			t.Errorf("removed a forward that was never made: %q", c)
		}
	}
}
