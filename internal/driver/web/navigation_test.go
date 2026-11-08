package web

import (
	"reflect"
	"strings"
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
