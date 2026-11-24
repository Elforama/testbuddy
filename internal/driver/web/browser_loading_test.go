package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

func loadServer(t *testing.T, page string) string {
	t.Helper()
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wait := func(d time.Duration) bool {
			select {
			case <-time.After(d):
				return true
			case <-hang:
			case <-r.Context().Done():
			}
			return false
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/hang."):
			wait(time.Hour)
		case r.URL.Path == "/slow.js":
			if wait(3 * time.Second) {
				w.Header().Set("Content-Type", "text/javascript")
				w.Write([]byte(`document.getElementById('app').textContent = 'Ready'`))
			}
		case r.URL.Path == "/slow.html":
			if wait(3 * time.Second) {
				w.Header().Set("Content-Type", "text/html")
				w.Write([]byte(`<button>In the frame</button>`))
			}
		case r.URL.Path == "/slow.woff2":
			if wait(3500 * time.Millisecond) {
				w.Header().Set("Content-Type", "font/woff2")
				w.Write([]byte("not really a font"))
			}
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(page))
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hang) })
	return srv.URL + "/"
}

func openChrome(t *testing.T) (*Driver, context.Context) {
	t.Helper()
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, ctx
}

func timed(f func()) time.Duration {
	t0 := time.Now()
	f()
	return time.Since(t0)
}

const hangingImagePage = `<!doctype html><html><body style="margin:0">
<button id="buy" style="display:block;width:200px;height:60px" onclick="document.getElementById('out').textContent='Bought'">Buy</button>
<button id="push" style="display:block;width:200px;height:60px" onclick="history.pushState({}, '', '#two'); document.getElementById('out').textContent='Pushed'">Push</button>
<a id="frag" href="#frag" style="display:block;width:200px;height:60px">Frag</a>
<div id="out"></div>
<img src="/hang.png">
</body></html>`

func TestHangingSubresourceInChrome(t *testing.T) {
	d, ctx := openChrome(t)
	link := loadServer(t, hangingImagePage)

	launch := timed(func() {
		if err := d.LaunchApp(ctx, link, driver.LaunchOpts{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("launchApp: %s", launch.Round(time.Millisecond))
	if launch > loadGraceNow()+3*time.Second {
		t.Errorf("launchApp took %s: a stalled image held it to the load timeout", launch.Round(time.Millisecond))
	}

	settle := timed(func() { d.WaitSettled(ctx, 5*time.Second) })
	t.Logf("WaitSettled: %s", settle.Round(time.Millisecond))
	if settle > 2*time.Second {
		t.Errorf("WaitSettled took %s on a page that is ready but for a stalled image", settle.Round(time.Millisecond))
	}

	var r driver.ActionResult
	tap := timed(func() {
		var err error
		if r, err = d.Tap(ctx, 100, 30, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("tap: %s, settle=%s", tap.Round(time.Millisecond), r.Settle)
	if r.Settle != driver.Changed || tap > 2*time.Second {
		t.Errorf("tap: settle=%s after %s, want changed in well under 2 s", r.Settle, tap.Round(time.Millisecond))
	}
	var out string
	if err := d.eval(ctx, "document.getElementById('out').textContent", &out); err != nil || out != "Bought" {
		t.Errorf("the tap did not land: %q, %v", out, err)
	}
	if notes := strings.Join(d.Notes(), "\n"); !strings.Contains(notes, "still loading") {
		t.Errorf("the trace does not say the page is still loading: %q", notes)
	}
}

func TestHangingSubresourceSameDocumentBackInChrome(t *testing.T) {
	d, ctx := openChrome(t)
	if err := d.LaunchApp(ctx, loadServer(t, hangingImagePage), driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	d.WaitSettled(ctx, 5*time.Second)
	for _, y := range []int{150, 90} {
		if _, err := d.Tap(ctx, 100, y, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
		d.WaitSettled(ctx, 6*time.Second)
	}
	if err := d.back(ctx); err != nil {
		t.Fatal(err)
	}
	settle := timed(func() { d.WaitSettled(ctx, 6*time.Second) })
	t.Logf("WaitSettled after back: %s", settle.Round(time.Millisecond))
	if settle > loadGraceNow()+2*time.Second {
		t.Errorf("WaitSettled after a same-document back took %s", settle.Round(time.Millisecond))
	}
	var hash string
	if err := d.eval(ctx, "location.hash", &hash); err != nil || hash != "#frag" {
		t.Errorf("back did not go back to #frag: %q, %v", hash, err)
	}
}

func TestSlowFrameInChrome(t *testing.T) {
	d, ctx := openChrome(t)
	link := loadServer(t, `<!doctype html><html><body><img src="/hang.png"><iframe src="/slow.html"></iframe></body></html>`)
	launch := timed(func() {
		if err := d.LaunchApp(ctx, link, driver.LaunchOpts{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("launchApp: %s", launch.Round(time.Millisecond))
	if launch < 2900*time.Millisecond {
		t.Errorf("launchApp returned after %s, before the frame (3 s)", launch.Round(time.Millisecond))
	}
}

func TestSlowAppBundleInChrome(t *testing.T) {
	d, ctx := openChrome(t)
	link := loadServer(t, `<!doctype html><html><body><div id="app"></div><script async src="/slow.js"></script></body></html>`)
	launch := timed(func() {
		if err := d.LaunchApp(ctx, link, driver.LaunchOpts{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("launchApp: %s", launch.Round(time.Millisecond))
	var app string
	if err := d.eval(ctx, "document.getElementById('app').textContent", &app); err != nil || app != "Ready" {
		t.Errorf("launchApp returned after %s on an empty shell: #app = %q, %v", launch.Round(time.Millisecond), app, err)
	}
}

func TestSlowFontInChrome(t *testing.T) {
	d, ctx := openChrome(t)
	link := loadServer(t, `<!doctype html><html><head><style>
@font-face { font-family: Slow; src: url(/slow.woff2) format("woff2"); }
body { font-family: Slow, sans-serif; }
</style></head><body><button id="go">Go</button><img src="/hang.png"></body></html>`)
	launch := timed(func() {
		if err := d.LaunchApp(ctx, link, driver.LaunchOpts{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("launchApp: %s", launch.Round(time.Millisecond))
	if launch < 3400*time.Millisecond {
		t.Errorf("launchApp returned after %s, before the font (3.5 s)", launch.Round(time.Millisecond))
	}
}

func TestHangingScriptInChrome(t *testing.T) {
	d, ctx := openChrome(t)
	link := loadServer(t, `<!doctype html><html><body><div id="app">Shell</div><script async src="/hang.js"></script></body></html>`)
	limit := loadGraceNow() + 3*time.Second
	lctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	launch := timed(func() { d.LaunchApp(lctx, link, driver.LaunchOpts{}) })
	t.Logf("launchApp: %s (cut off at %s)", launch.Round(time.Millisecond), limit)
	if launch < limit-500*time.Millisecond {
		t.Errorf("launchApp gave up on a pending same-site script after %s", launch.Round(time.Millisecond))
	}
	settle := timed(func() { d.WaitSettled(ctx, time.Second) })
	if settle < 900*time.Millisecond {
		t.Errorf("a page waiting on its own script counted as settled (%s)", settle.Round(time.Millisecond))
	}
}
