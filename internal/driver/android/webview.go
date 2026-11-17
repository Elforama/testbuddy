package android

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver/web"
	"github.com/elforama/testbuddy/internal/ui"
)

var webViewHTTP = http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (d *Driver) SetWebViewDevTools(appID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.webViewApp = appID
}

func (d *Driver) augmentWebView(ctx context.Context, screen *ui.Screen) {
	d.mu.Lock()
	app := d.webViewApp
	d.mu.Unlock()
	if app == "" || validPkg(app) != nil {
		return
	}
	// Match one visible WebView in the flow's app with one attached DevTools page.
	// Keep the native tree when the mapping would be ambiguous.
	var window *ui.Window
	var container *ui.Node
	var insertAt int
	for _, w := range screen.Windows {
		if w.Type != "app" || w.Package != app || (!w.Active && !w.Focused) {
			continue
		}
		for i := 0; i < len(w.Nodes); i++ {
			n := w.Nodes[i]
			if n.Class != "android.webkit.WebView" || !n.Visible || n.Bounds.Empty() {
				continue
			}
			if container != nil {
				return
			}
			for i+1 < len(w.Nodes) && w.Nodes[i+1].Depth > n.Depth {
				i++
			}
			window, container, insertAt = w, n, i+1
		}
	}
	if container == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	width, nodes, err := d.webViewNodes(ctx, app)
	if err != nil {
		// The native tree stays; the trace says why (issue #350): a page script the socket
		// would not take (issue #335) showed only as missing nodes.
		d.noteOncePerStep("WebView page not read: " + err.Error())
		return
	}
	if width <= 0 {
		return // no debuggable page, or not one page: the normal case, nothing to say
	}
	bounds := container.Bounds
	scale := float64(bounds.W()) / float64(width)
	toPixels := func(v int) int { return int(math.Round(float64(v) * scale)) }
	for _, n := range nodes {
		b := n.Bounds
		n.Bounds = (ui.Rect{
			L: bounds.L + toPixels(b.L), T: bounds.T + toPixels(b.T),
			R: bounds.L + toPixels(b.R), B: bounds.T + toPixels(b.B),
		}).Intersect(bounds)
		n.Depth += container.Depth + 1
		n.Visible = !n.Bounds.Empty()
	}
	window.Nodes = slices.Insert(window.Nodes, insertAt, nodes...)
}

// webViewNodes reads the page in app's WebView over its DevTools socket. No error and no width:
// nothing to read (the app not running, no debuggable page, several pages), which is not
// news. An error: the page was there to read and could not be (issue #350).
func (d *Driver) webViewNodes(ctx context.Context, app string) (int, []*ui.Node, error) {
	out, code, err := d.Sh(ctx, "pidof "+app)
	if err != nil {
		return 0, nil, fmt.Errorf("asking for the app's pid: %w", err)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(out))
	if code != 0 || parseErr != nil || pid <= 0 {
		return 0, nil, nil // the app is not running
	}
	port, err := d.adb.Forward(ctx, "localabstract:webview_devtools_remote_"+strconv.Itoa(pid))
	if err != nil {
		return 0, nil, fmt.Errorf("forwarding its DevTools socket: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = d.adb.Run(cleanup, "forward", "--remove", "tcp:"+strconv.Itoa(port))
	}()
	host := "127.0.0.1:" + strconv.Itoa(port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/json/list", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Close = true
	resp, err := webViewHTTP.Do(req)
	if err != nil {
		// adb accepts the forward even with nothing behind it. No DevTools socket at all: the
		// app does not enable WebView debugging, the normal case. A socket that is there but
		// does not answer is news.
		sock := "@webview_devtools_remote_" + strconv.Itoa(pid)
		if _, code, serr := d.Sh(ctx, "grep -q "+sock+" /proc/net/unix"); serr == nil && code != 0 {
			return 0, nil, nil
		}
		return 0, nil, fmt.Errorf("its DevTools socket did not answer: %w", err)
	}
	defer resp.Body.Close()
	var pages []struct {
		Type                 string
		WebSocketDebuggerURL string
	}
	if resp.StatusCode != http.StatusOK {
		return 0, nil, fmt.Errorf("its DevTools page list: %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&pages); err != nil {
		return 0, nil, fmt.Errorf("its DevTools page list: %w", err)
	}
	var socket *url.URL
	for _, p := range pages {
		u, err := url.Parse(p.WebSocketDebuggerURL)
		if p.Type != "page" || err != nil || !strings.HasPrefix(u.Path, "/devtools/page/") {
			continue
		}
		if socket != nil {
			return 0, nil, nil // several pages: which one is in the WebView is not known
		}
		socket = &url.URL{Scheme: "ws", Host: host, Path: u.Path}
	}
	if socket == nil {
		return 0, nil, nil
	}
	width, nodes, err := web.ReadDevToolsPage(ctx, socket.String())
	if err != nil {
		return 0, nil, err
	}
	if width <= 0 {
		return 0, nil, fmt.Errorf("the page script gave no page width")
	}
	return width, nodes, nil
}
