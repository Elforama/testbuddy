package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Location, network and color scheme are emulated through CDP; airplane mode toggles offline.
func TestEmulation(t *testing.T) {
	page, d := inputPage(t)
	page.evaluator(func(expr, session string) any {
		switch expr {
		case "matchMedia('(prefers-color-scheme: dark)').matches":
			return value(true)
		case "location.href":
			return value("https://shop.example.com/cart")
		}
		return nil
	})
	ctx := ctxT(t)
	if err := d.SetLocation(ctx, 48.8584, 2.2945); err != nil {
		t.Fatal(err)
	}
	if p := page.calls("Emulation.setGeolocationOverride")[0].params(); p["latitude"] != 48.8584 || p["longitude"] != 2.2945 || p["accuracy"] != 10.0 {
		t.Errorf("geolocation %v", p)
	}
	d.SetAirplaneMode(ctx, true)
	d.ToggleAirplaneMode(ctx)
	d.ToggleAirplaneMode(ctx)
	var offline []any
	for _, m := range page.calls("Network.emulateNetworkConditions") {
		p := m.params()
		offline = append(offline, p["offline"])
		if p["latency"] != 0.0 || p["downloadThroughput"] != -1.0 || p["uploadThroughput"] != -1.0 {
			t.Errorf("network params %v", p)
		}
	}
	if !reflect.DeepEqual(offline, []any{true, false, true}) {
		t.Errorf("offline sequence %v", offline)
	}
	for _, on := range []bool{true, false} {
		if err := d.SetDarkMode(ctx, on); err != nil {
			t.Fatal(err)
		}
	}
	var schemes []string
	for _, m := range page.calls("Emulation.setEmulatedMedia") {
		f := m.params()["features"].([]any)[0].(map[string]any)
		if f["name"] != "prefers-color-scheme" {
			t.Errorf("feature %v", f)
		}
		schemes = append(schemes, f["value"].(string))
	}
	if !reflect.DeepEqual(schemes, []string{"dark", "light"}) {
		t.Errorf("schemes %v", schemes)
	}
	if dark, err := d.DarkMode(ctx); !dark || err != nil {
		t.Errorf("DarkMode = %v, %v", dark, err)
	}
	if href, err := d.Foreground(ctx); href != "https://shop.example.com/cart" || err != nil {
		t.Errorf("Foreground = %q, %v", href, err)
	}
}

// What the web cannot do says so; what has no web meaning is a no-op.
func TestUnsupportedAndNoops(t *testing.T) {
	_, d := inputPage(t)
	ctx := ctxT(t)
	for name, err := range map[string]error{
		"AddMedia":   d.AddMedia(ctx, []string{"a.png"}),
		"InstallApp": d.InstallApp(ctx, "app.apk"),
	} {
		if err == nil || !strings.Contains(err.Error(), "not supported on web") {
			t.Errorf("%s = %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"StopRecording":  d.StopRecording(ctx),
		"HideKeyboard":   d.HideKeyboard(ctx),
		"SetOrientation": d.SetOrientation(ctx, "LANDSCAPE_LEFT"),
		"ClearKeychain":  d.ClearKeychain(ctx),
	} {
		if err != nil {
			t.Errorf("%s = %v", name, err)
		}
	}
	// issue #115: a no-op still reports a cancelled run, not success.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := d.SetOrientation(cctx, "LANDSCAPE_LEFT"); !errors.Is(err, context.Canceled) {
		t.Errorf("issue #115: SetOrientation on a cancelled run = %v, want context.Canceled", err)
	}
	if d.Platform() != "web" || d.Serial() != "web" || d.Name() != "Chrome" {
		t.Errorf("identity %q %q %q", d.Platform(), d.Serial(), d.Name())
	}
	if w, h := d.Size(); w != 1280 || h != 800 {
		t.Errorf("Size = %dx%d", w, h)
	}
}

// Close closes the browser (Browser.close), removes the profile, ends waits, and is idempotent.
func TestClose(t *testing.T) {
	page, browser := newFakeCDP(t), newFakeCDP(t)
	d := newWebDriver(t, page, browser)
	d.profile = filepath.Join(t.TempDir(), "profile")
	os.MkdirAll(filepath.Join(d.profile, "Default"), 0o755)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if len(browser.calls("Browser.close")) != 1 {
		t.Errorf("browser commands %v", browser.methods())
	}
	if _, err := os.Stat(d.profile); !os.IsNotExist(err) {
		t.Error("profile left behind")
	}
	if err := d.Close(); err != nil || len(browser.calls("Browser.close")) != 1 {
		t.Errorf("second Close: %v, %v", err, browser.methods())
	}
	<-d.page.done
}
