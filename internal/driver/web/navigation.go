package web

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// navRetryFor bounds the retry of a refused connection to a loopback host (issue #283): a
// dev server started just before the flow may not be listening yet.
var navRetryFor = 3 * time.Second

const (
	navBackoffFirst = 100 * time.Millisecond
	navBackoffMax   = 500 * time.Millisecond
)

func isURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "about:") || strings.HasPrefix(s, "data:")
}

// isLoopback reports whether the URL's host is this machine: localhost, *.localhost (which
// Chrome resolves to loopback itself), or a loopback address (127.0.0.0/8, ::1).
func isLoopback(u *url.URL) bool {
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// hostPort is the URL's host:port, with the scheme's default port when it names none.
func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443", "ws": "80", "wss": "443"}[u.Scheme]
	}
	if port == "" {
		return u.Hostname()
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// navFailure classifies the error Chrome's Page.navigate gave for link. It is the one place
// navigation errors are interpreted (issue #283). retry says whether a new attempt can
// succeed: only a refused connection to a loopback host, a dev server that is still starting.
// A refused connection never reached a server, so retrying it cannot repeat a request. Other
// errors (a reset or empty response may have reached the server, so a retry could repeat a
// request; a DNS or certificate error will not change) and every remote host fail at once.
// hint, for the error once retries are over, points at the cause.
func navFailure(link, errorText string) (retry bool, hint string) {
	if errorText != "net::ERR_CONNECTION_REFUSED" {
		return false, ""
	}
	u, err := url.Parse(link)
	if err != nil || !isLoopback(u) {
		return false, ""
	}
	return true, fmt.Sprintf("nothing is listening on %s; start the app's dev server before the flow", hostPort(u))
}

// navigate opens the URL and returns once the page has loaded (a load event), once its DOM
// is ready and has been quiet for loadGraceNow while a subresource never finishes (issue
// #79: the load event would not come), or after loadTimeout with whatever loaded. Every
// navigation tb makes goes through here. A failure navFailure says to retry is retried with
// backoff for navRetryFor, then reported with its hint; others are reported at once.
func (d *Driver) navigate(ctx context.Context, link string) error {
	deadline := time.Now().Add(navRetryFor)
	backoff := navBackoffFirst
	for {
		errorText, err := d.navigateOnce(ctx, link)
		if err != nil || errorText == "" {
			return err
		}
		retry, hint := navFailure(link, errorText)
		if !retry {
			return fmt.Errorf("could not open %s: %s", link, errorText)
		}
		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("could not open %s: %s for %s: %s", link, errorText, navRetryFor, hint)
		}
		t := time.NewTimer(min(backoff, left))
		select {
		case <-ctx.Done():
			t.Stop() // a cancellation, not a failure: no dev-server hint
			return fmt.Errorf("could not open %s: %s: %w", link, errorText, ctx.Err())
		case <-t.C:
		}
		backoff = min(2*backoff, navBackoffMax)
	}
}

// navigateOnce makes one Page.navigate (a GET of link) and waits for its load as navigate
// describes. It returns Chrome's error text for a navigation that failed.
func (d *Driver) navigateOnce(ctx context.Context, link string) (string, error) {
	d.mu.Lock() // counted per attempt: a failed attempt's error page loads too
	before, readyBefore := d.loads, d.readies
	d.mu.Unlock()
	var r struct {
		ErrorText string `json:"errorText"`
		LoaderID  string `json:"loaderId"`
	}
	if err := d.page.call(ctx, "Page.navigate", map[string]any{"url": link}, &r); err != nil {
		return "", err
	}
	if r.ErrorText != "" {
		return r.ErrorText, nil
	}
	if r.LoaderID == "" { // same-document navigation: no load event
		return "", nil
	}
	d.waitUntil(ctx, loadTimeout, func() bool {
		return d.loads > before || (d.readies > readyBefore && d.ready && d.stale)
	})
	return "", nil
}

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if !isURL(appID) {
		return fmt.Errorf("%q is not a URL the browser can open (a web flow needs `url: https://...`)", appID)
	}
	d.closeOtherTabs(ctx) // the app starts in one tab, whatever the last flow opened (issue #78)
	if o.ClearState {
		if err := d.ClearState(ctx, appID); err != nil {
			return err
		}
	}
	return d.navigate(ctx, appID)
}

func (d *Driver) StopApp(ctx context.Context, appID string) error {
	return d.navigate(ctx, "about:blank")
}

func (d *Driver) KillApp(ctx context.Context, appID string) error {
	return d.navigate(ctx, "about:blank")
}

func origin(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (d *Driver) ClearState(ctx context.Context, appID string) error {
	o := origin(appID)
	if o == "" {
		return nil
	}
	return d.page.call(ctx, "Storage.clearDataForOrigin", map[string]any{"origin": o, "storageTypes": "all"}, nil)
}

func (d *Driver) ClearKeychain(ctx context.Context) error { return nil }

func (d *Driver) OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error {
	return d.navigate(ctx, link)
}

var webPermissions = map[string][]string{
	"location": {"geolocation"}, "camera": {"videoCapture"}, "microphone": {"audioCapture"},
	"notifications": {"notifications"}, "clipboard": {"clipboardReadWrite", "clipboardSanitizedWrite"},
	"sensors": {"sensors"}, "midi": {"midi"},
}

func (d *Driver) SetPermissions(ctx context.Context, appID string, perms map[string]string) error {
	o := origin(appID)
	if o == "" {
		return nil
	}
	var grant []string
	for name, v := range perms {
		list := webPermissions[strings.ToLower(name)]
		if strings.EqualFold(name, "all") {
			for _, l := range webPermissions {
				list = append(list, l...)
			}
		}
		if strings.EqualFold(v, "allow") {
			grant = append(grant, list...)
		}
	}
	if err := d.browser.call(ctx, "Browser.resetPermissions", map[string]any{}, nil); err != nil {
		return err
	}
	if len(grant) == 0 {
		return nil
	}
	if err := d.browser.call(ctx, "Browser.grantPermissions", map[string]any{"origin": o, "permissions": grant}, nil); err != nil {
		// some permission names differ across Chrome versions: grant one by one
		for _, p := range grant {
			d.browser.call(ctx, "Browser.grantPermissions", map[string]any{"origin": o, "permissions": []string{p}}, nil)
		}
	}
	return nil
}

func (d *Driver) Foreground(ctx context.Context) (string, error) {
	var href string
	err := d.eval(ctx, "location.href", &href)
	return href, err
}

func (d *Driver) WaitForApp(ctx context.Context, appID string, timeout time.Duration) error {
	return d.WaitSettled(ctx, timeout)
}
