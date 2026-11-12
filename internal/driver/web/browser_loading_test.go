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

// A page whose image never answers (a stalled ad pixel or analytics call) holds the load
// event back for good. Issue #79: opening it took the full 30 s load timeout, WaitSettled
// its whole 5 s, and every tap 5 s with settle=timeout. Once the DOM is ready and has been
// quiet for loadGrace, the stalled load no longer counts as busy, and the trace says the
// page is still loading. Needs Chrome: runs with TB_BROWSER_TESTS=1.
func TestHangingSubresourceInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hang.png" {
			select { // never answers while the test runs
			case <-hang:
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><body style="margin:0">
<button id="buy" style="width:200px;height:60px" onclick="document.getElementById('out').textContent='Bought'">Buy</button>
<div id="out"></div>
<img src="/hang.png">
</body></html>`))
	}))
	defer srv.Close()
	defer close(hang) // before srv.Close: lets the handler return
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	t0 := time.Now()
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	launch := time.Since(t0)
	t.Logf("launchApp: %s", launch.Round(time.Millisecond))
	if launch > loadGraceNow()+3*time.Second {
		t.Errorf("launchApp took %s: a stalled image held it to the load timeout", launch.Round(time.Millisecond))
	}

	t0 = time.Now()
	d.WaitSettled(ctx, 5*time.Second)
	settle := time.Since(t0)
	t.Logf("WaitSettled: %s", settle.Round(time.Millisecond))
	if settle > time.Second {
		t.Errorf("WaitSettled took %s on a page that is ready but for a stalled image", settle.Round(time.Millisecond))
	}

	t0 = time.Now()
	r, err := d.Tap(ctx, 100, 30, driver.ActionOpts{})
	tap := time.Since(t0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tap: %s, settle=%s", tap.Round(time.Millisecond), r.Settle)
	if r.Settle != driver.Changed || tap > time.Second {
		t.Errorf("tap: settle=%s after %s, want changed in well under a second", r.Settle, tap.Round(time.Millisecond))
	}
	var out string
	if err := d.eval(ctx, "document.getElementById('out').textContent", &out); err != nil || out != "Bought" {
		t.Errorf("the tap did not land: %q, %v", out, err)
	}
	if notes := strings.Join(d.Notes(), "\n"); !strings.Contains(notes, "still loading") {
		t.Errorf("the trace does not say the page is still loading: %q", notes)
	}
}
