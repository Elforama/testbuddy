package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
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
		d.noteOncePerStep("WebView page not read: " + webViewReason(err))
		return
	}
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

func (d *Driver) webViewNodes(ctx context.Context, app string) (int, []*ui.Node, error) {
	out, code, err := d.Sh(ctx, "pidof "+app)
	if err != nil {
		return 0, nil, fmt.Errorf("asking for the app's pid: %w", err)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(out))
	if code != 0 || parseErr != nil || pid <= 0 {
		return 0, nil, nil
	}
	_, code, err = d.Sh(ctx, "grep -q '@webview_devtools_remote_"+strconv.Itoa(pid)+"$' /proc/net/unix")
	if err != nil {
		return 0, nil, fmt.Errorf("listing its sockets: %w", err)
	}
	if code != 0 {
		return 0, nil, nil
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
			return 0, nil, nil
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

var loopbackAddr = regexp.MustCompile(`(127\.0\.0\.1|\[::1\]|localhost):\d+(->(127\.0\.0\.1|\[::1\]|localhost):\d+)?`)

func webViewReason(err error) string {
	msg := err.Error()
	var ue *url.Error
	if errors.As(err, &ue) && strings.HasSuffix(msg, ue.Error()) {
		return loopbackAddr.ReplaceAllString(strings.TrimSuffix(msg, ue.Error()), "the forward") +
			ue.Op + ": " + webViewReason(ue.Err)
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil && strings.HasSuffix(msg, oe.Error()) {
		return loopbackAddr.ReplaceAllString(strings.TrimSuffix(msg, oe.Error()), "the forward") +
			oe.Op + ": " + loopbackAddr.ReplaceAllString(oe.Err.Error(), "the forward")
	}
	return loopbackAddr.ReplaceAllString(msg, "the forward")
}
