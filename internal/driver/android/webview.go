package android

import (
	"context"
	"encoding/json"
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
	width, nodes := d.webViewNodes(ctx, app)
	if width <= 0 {
		return
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

func (d *Driver) webViewNodes(ctx context.Context, app string) (int, []*ui.Node) {
	out, err := d.shOK(ctx, "pidof "+app)
	pid, parseErr := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || parseErr != nil || pid <= 0 {
		return 0, nil
	}
	port, err := d.adb.Forward(ctx, "localabstract:webview_devtools_remote_"+strconv.Itoa(pid))
	if err != nil {
		return 0, nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = d.adb.Run(cleanup, "forward", "--remove", "tcp:"+strconv.Itoa(port))
	}()
	host := "127.0.0.1:" + strconv.Itoa(port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/json/list", nil)
	if err != nil {
		return 0, nil
	}
	req.Close = true
	resp, err := webViewHTTP.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	var pages []struct {
		Type                 string
		WebSocketDebuggerURL string
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&pages) != nil {
		return 0, nil
	}
	var socket *url.URL
	for _, p := range pages {
		u, err := url.Parse(p.WebSocketDebuggerURL)
		if p.Type != "page" || err != nil || !strings.HasPrefix(u.Path, "/devtools/page/") {
			continue
		}
		if socket != nil {
			return 0, nil
		}
		socket = &url.URL{Scheme: "ws", Host: host, Path: u.Path}
	}
	if socket == nil {
		return 0, nil
	}
	width, nodes, err := web.ReadDevToolsPage(ctx, socket.String())
	if err != nil {
		return 0, nil
	}
	return width, nodes
}
