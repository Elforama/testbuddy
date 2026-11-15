package web

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// navPage answers Page.navigate with a loader id and fires the load event a little later.
func navPage(t *testing.T) (*fakeCDP, *Driver) {
	page, d := inputPage(t)
	page.on("Page.navigate", func(m cdpMsg) any {
		go func() {
			time.Sleep(30 * time.Millisecond)
			page.event("Page.loadEventFired", map[string]any{"timestamp": 1}, "")
		}()
		return map[string]any{"frameId": "F", "loaderId": "L1"}
	})
	return page, d
}

// LaunchApp opens the URL and returns once it has loaded; ClearState clears the origin's storage first.
func TestLaunchApp(t *testing.T) {
	page, d := navPage(t)
	t0 := time.Now()
	if err := d.LaunchApp(ctxT(t), "https://shop.example.com/start?x=1", driver.LaunchOpts{ClearState: true}); err != nil {
		t.Fatal(err)
	}
	if time.Since(t0) < 25*time.Millisecond {
		t.Error("returned before the load event")
	}
	got := page.methods()
	if !reflect.DeepEqual(got, []string{"Storage.clearDataForOrigin", "Page.navigate"}) {
		t.Errorf("commands %v", got)
	}
	if p := page.calls("Storage.clearDataForOrigin")[0].params(); p["origin"] != "https://shop.example.com" || p["storageTypes"] != "all" {
		t.Errorf("clear params %v", p)
	}
	if p := page.calls("Page.navigate")[0].params(); p["url"] != "https://shop.example.com/start?x=1" {
		t.Errorf("navigate params %v", p)
	}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{}); err == nil || !strings.Contains(err.Error(), "is not a URL") {
		t.Errorf("non-URL app: %v", err)
	}
	page.on("Storage.clearDataForOrigin", func(cdpMsg) any { return errReply{cdpError{Message: "denied"}} })
	if err := d.LaunchApp(ctxT(t), "https://x.example/", driver.LaunchOpts{ClearState: true}); err == nil {
		t.Error("clear failure not reported")
	}
}

// Navigation errors are reported; a same-document navigation (no loader) returns at once.
func TestNavigate(t *testing.T) {
	page, d := inputPage(t)
	page.on("Page.navigate", func(cdpMsg) any { return map[string]any{"errorText": "net::ERR_NAME_NOT_RESOLVED"} })
	if err := d.OpenLink(ctxT(t), "https://nope.invalid/", "", false, false); err == nil ||
		err.Error() != "could not open https://nope.invalid/: net::ERR_NAME_NOT_RESOLVED" {
		t.Errorf("err = %v", err)
	}
	page.on("Page.navigate", func(cdpMsg) any { return map[string]any{"frameId": "F"} })
	t0 := time.Now()
	if err := d.OpenLink(ctxT(t), "https://shop.example.com/#reviews", "", false, false); err != nil || time.Since(t0) > time.Second {
		t.Errorf("same-document navigation: %v after %s", err, time.Since(t0))
	}
	page.on("Page.navigate", func(cdpMsg) any { return errReply{cdpError{Message: "Cannot navigate to invalid URL"}} })
	if err := d.StopApp(ctxT(t), "https://x/"); err == nil {
		t.Error("protocol error not reported")
	}

	// Issue #283: every navigation (LaunchApp, OpenLink) classifies Chrome's errors the same
	// way. A refused connection to a loopback host is a dev server that may still be starting:
	// it is retried for navRetryFor, then fails with a hint. Remote hosts and other errors
	// fail at once, as before.
	defer func(v time.Duration) { navRetryFor = v }(navRetryFor)
	navRetryFor = 250 * time.Millisecond
	const hint = "nothing is listening on"
	callers := map[string]func(link string) error{
		"launchApp": func(link string) error { return d.LaunchApp(ctxT(t), link, driver.LaunchOpts{}) },
		"openLink":  func(link string) error { return d.OpenLink(ctxT(t), link, "", false, false) },
	}
	// refusing answers Page.navigate with a refused connection n times (n < 0: always), then
	// with a same-document navigation; it counts the calls.
	refusing := func(n int32) *atomic.Int32 {
		var calls atomic.Int32
		page.on("Page.navigate", func(cdpMsg) any {
			if c := calls.Add(1); n < 0 || c <= n {
				return map[string]any{"frameId": "F", "loaderId": "L", "errorText": "net::ERR_CONNECTION_REFUSED"}
			}
			return map[string]any{"frameId": "F"}
		})
		return &calls
	}
	for name, open := range callers {
		t.Run(name, func(t *testing.T) {
			calls := refusing(2) // a server that starts listening after two refusals
			if err := open("http://127.0.0.1:7357/x"); err != nil || calls.Load() != 3 {
				t.Errorf("starting server: %v after %d calls, want success after 3", err, calls.Load())
			}
			for link, at := range map[string]string{"http://127.0.0.1:7357/x": "127.0.0.1:7357", "http://localhost:7357/": "localhost:7357",
				"http://[::1]:7357/": "[::1]:7357", "http://app.localhost/": "app.localhost:80"} {
				calls = refusing(-1) // nothing ever listens
				t0 := time.Now()
				err := open(link)
				took := time.Since(t0)
				if err == nil || !strings.Contains(err.Error(), hint+" "+at+";") || !strings.Contains(err.Error(), "ERR_CONNECTION_REFUSED") {
					t.Errorf("%s never listening: %v, want the hint for %s", link, err, at)
				}
				if took < navRetryFor || took > navRetryFor+500*time.Millisecond || calls.Load() < 3 {
					t.Errorf("%s never listening: %d calls in %s, want retries for %s", link, calls.Load(), took, navRetryFor)
				}
			}
			for _, link := range []string{"https://shop.example.com/", "http://10.0.0.5:7357/", "http://localhost.example.com/"} {
				calls = refusing(-1) // a remote host: one call, no hint, at once
				t0 := time.Now()
				err := open(link)
				if err == nil || err.Error() != "could not open "+link+": net::ERR_CONNECTION_REFUSED" || calls.Load() != 1 || time.Since(t0) > 200*time.Millisecond {
					t.Errorf("%s refused: %v after %d calls in %s, want one immediate bare failure", link, err, calls.Load(), time.Since(t0))
				}
			}
			calls.Store(0) // another error on loopback fails at once too
			page.on("Page.navigate", func(cdpMsg) any {
				calls.Add(1)
				return map[string]any{"frameId": "F", "errorText": "net::ERR_EMPTY_RESPONSE"}
			})
			if err := open("http://127.0.0.1:7357/x"); err == nil || strings.Contains(err.Error(), hint) || calls.Load() != 1 {
				t.Errorf("loopback empty response: %v after %d calls, want one bare failure", err, calls.Load())
			}
		})
	}
	// A cancelled step stops the retry.
	refusing(-1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	t0 = time.Now()
	if err := d.OpenLink(ctx, "http://127.0.0.1:7357/x", "", false, false); err == nil || time.Since(t0) > 300*time.Millisecond {
		t.Errorf("cancelled retry: %v after %s", err, time.Since(t0))
	}
}

// Stopping or killing the "app" opens about:blank; ClearState of a non-origin does nothing.
func TestStopKillClear(t *testing.T) {
	page, d := navPage(t)
	for _, run := range []func() error{
		func() error { return d.StopApp(ctxT(t), "https://shop.example.com") },
		func() error { return d.KillApp(ctxT(t), "https://shop.example.com") },
	} {
		page.reset()
		if err := run(); err != nil {
			t.Fatal(err)
		}
		if n := page.calls("Page.navigate"); len(n) != 1 || n[0].params()["url"] != "about:blank" {
			t.Errorf("navigations %v", page.methods())
		}
	}
	page.reset()
	if err := d.ClearState(ctxT(t), "about:blank"); err != nil || len(page.calls("")) != 0 {
		t.Errorf("ClearState(about:blank): %v, %v", err, page.methods())
	}
}

// SetPermissions resets the origin's permissions and grants the allowed ones in one call.
func TestSetPermissions(t *testing.T) {
	cases := []struct {
		name  string
		perms map[string]string
		grant []string
	}{
		{"one", map[string]string{"camera": "allow", "microphone": "deny"}, []string{"videoCapture"}},
		{"case", map[string]string{"Location": "ALLOW"}, []string{"geolocation"}},
		{"group", map[string]string{"clipboard": "allow"}, []string{"clipboardReadWrite", "clipboardSanitizedWrite"}},
		{"none", map[string]string{"camera": "deny", "unknown": "allow"}, nil},
		{"all", map[string]string{"all": "allow"}, []string{"audioCapture", "clipboardReadWrite", "clipboardSanitizedWrite", "geolocation", "midi", "notifications", "sensors", "videoCapture"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page, browser := newFakeCDP(t), newFakeCDP(t)
			d := newWebDriver(t, page, browser)
			if err := d.SetPermissions(ctxT(t), "https://shop.example.com/x", c.perms); err != nil {
				t.Fatal(err)
			}
			methods := browser.methods()
			if len(methods) == 0 || methods[0] != "Browser.resetPermissions" {
				t.Fatalf("browser commands %v", methods)
			}
			grants := browser.calls("Browser.grantPermissions")
			if c.grant == nil {
				if len(grants) != 0 {
					t.Errorf("granted %v", grants)
				}
				return
			}
			if len(grants) != 1 {
				t.Fatalf("grants %v", methods)
			}
			p := grants[0].params()
			var got []string
			for _, g := range p["permissions"].([]any) {
				got = append(got, g.(string))
			}
			sortStrings(got)
			if p["origin"] != "https://shop.example.com" || !reflect.DeepEqual(got, c.grant) {
				t.Errorf("grant %v", p)
			}
		})
	}
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// A grant Chrome rejects as a whole is retried one permission at a time; a failed reset is an error.
func TestSetPermissionsFallbacks(t *testing.T) {
	page, browser := newFakeCDP(t), newFakeCDP(t)
	browser.on("Browser.grantPermissions", func(m cdpMsg) any {
		if len(m.params()["permissions"].([]any)) > 1 {
			return errReply{cdpError{Message: "Unknown permission type"}}
		}
		return map[string]any{}
	})
	d := newWebDriver(t, page, browser)
	if err := d.SetPermissions(ctxT(t), "https://a.example/", map[string]string{"clipboard": "allow", "camera": "allow"}); err != nil {
		t.Fatal(err)
	}
	if n := len(browser.calls("Browser.grantPermissions")); n != 4 {
		t.Errorf("%d grant calls, want 1 + 3 one by one", n)
	}
	browser.on("Browser.resetPermissions", func(cdpMsg) any { return errReply{cdpError{Message: "x"}} })
	if err := d.SetPermissions(ctxT(t), "https://a.example/", nil); err == nil {
		t.Error("reset failure not reported")
	}
	browser.reset()
	if err := d.SetPermissions(ctxT(t), "about:blank", map[string]string{"camera": "allow"}); err != nil || len(browser.calls("")) != 0 {
		t.Errorf("no origin: %v, %v", err, browser.methods())
	}
}
