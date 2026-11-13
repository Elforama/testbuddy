package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func isURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "about:") || strings.HasPrefix(s, "data:")
}

// navigate opens the URL and returns once the page has loaded (a load event), once its DOM
// is ready and has been quiet for loadGraceNow while a subresource never finishes (issue
// #79: the load event would not come), or after loadTimeout with whatever loaded.
func (d *Driver) navigate(ctx context.Context, link string) error {
	d.mu.Lock()
	before, readyBefore := d.loads, d.readies
	d.mu.Unlock()
	var r struct {
		ErrorText string `json:"errorText"`
		LoaderID  string `json:"loaderId"`
	}
	if err := d.page.call(ctx, "Page.navigate", map[string]any{"url": link}, &r); err != nil {
		return err
	}
	if r.ErrorText != "" {
		return fmt.Errorf("could not open %s: %s", link, r.ErrorText)
	}
	if r.LoaderID == "" { // same-document navigation: no load event
		return nil
	}
	d.waitUntil(ctx, loadTimeout, func() bool {
		return d.loads > before || (d.readies > readyBefore && d.ready && d.stale)
	})
	return nil
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
