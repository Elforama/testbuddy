package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Network mocks in a real browser: a page's fetch() gets the mocked response, in the page and
// in a cross-origin frame (its own CDP session); a failing mock rejects the fetch; a delay
// holds the response back. Needs Chrome: runs with TB_BROWSER_TESTS=1.
func TestMockNetworkInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	// fetchPage fetches path when loaded and shows what came back in #out.
	fetchPage := func(path, extra string) string {
		return `<!doctype html><html><body><div id="out">waiting</div>` + extra + `<script>
  const t0 = Date.now();
  fetch('` + path + `').then(r => r.text().then(b => r.status + ' ' + (r.headers.get('content-type') || '') + ' ' + b))
    .catch(e => 'failed')
    .then(s => { document.getElementById('out').textContent = s + ' in ' + (Date.now() - t0) + 'ms'; });
</script></body></html>`
	}
	// The frame is served from another site (localhost, not 127.0.0.1): Chrome runs it in its
	// own process, attached through its own session.
	frameSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/frame" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(fetchPage("/api/frame", "")))
			return
		}
		w.Write([]byte("real frame response"))
	}))
	defer frameSrv.Close()
	frameURL := strings.Replace(frameSrv.URL, "127.0.0.1", "localhost", 1) + "/frame"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/ads", "/slow":
			path := map[string]string{"/": "/api/items", "/ads": "/ads/banner.js", "/slow": "/api/slow"}[r.URL.Path]
			extra := ""
			if r.URL.Path == "/" {
				extra = `<iframe src="` + frameURL + `" width="600" height="100"></iframe>`
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(fetchPage(path, extra)))
		default:
			w.Write([]byte("real response"))
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// out waits for every #out on the page (and in frames) to stop saying "waiting".
	out := func(want int) []string {
		t.Helper()
		var got []string
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			s, err := d.Screen(ctx)
			if err != nil {
				t.Fatal(err)
			}
			got = outs(s)
			done := len(got) == want
			for _, g := range got {
				done = done && g != "waiting"
			}
			if done {
				return got
			}
		}
		t.Fatalf("page outputs %q, want %d answered", got, want)
		return nil
	}

	if err := d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/items", Status: 200, Body: []byte(`{"items":[1,2,3]}`),
		Headers: [][2]string{{"Content-Type", "application/json"}}}); err != nil {
		t.Fatal(err)
	}
	if err := d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/frame", Status: 202, Body: []byte("framed"),
		Headers: [][2]string{{"Content-Type", "text/plain"}}}); err != nil {
		t.Fatal(err)
	}
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	got := out(2)
	if n := len(d.frameSessions()); n != 1 {
		t.Fatalf("%d cross-origin frame sessions, want 1: the frame's mock was not tested in its own session", n)
	}
	wantPrefix := []string{`200 application/json {"items":[1,2,3]}`, "202 text/plain framed"}
	for i, w := range wantPrefix {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("output %d: %q, want %q", i, got[i], w)
		}
	}

	// A failing mock rejects the fetch.
	if err := d.MockNetwork(ctx, driver.NetworkMock{URL: "*/ads/*", Fail: true}); err != nil {
		t.Fatal(err)
	}
	if err := d.LaunchApp(ctx, srv.URL+"/ads", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := out(1); !strings.HasPrefix(got[0], "failed") {
		t.Errorf("failing mock: %q", got[0])
	}

	// A delay holds the response back.
	if err := d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/slow", Status: 200, Body: []byte("late"),
		Headers: [][2]string{{"Content-Type", "text/plain"}}, Delay: 700 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if err := d.LaunchApp(ctx, srv.URL+"/slow", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	got = out(1)
	var ms int
	if _, err := fmt.Sscanf(got[0][strings.LastIndex(got[0], " in ")+4:], "%dms", &ms); err != nil || !strings.HasPrefix(got[0], "200 text/plain late") || ms < 650 {
		t.Errorf("delayed mock: %q", got[0])
	}

	// Cleared: requests reach the server again.
	if err := d.ClearNetworkMocks(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	got = out(2)
	for i, w := range []string{"200 text/plain; charset=utf-8 real response", "200 text/plain; charset=utf-8 real frame response"} {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("after clearing, output %d: %q, want %q", i, got[i], w)
		}
	}
}

// outs lists the texts of the #out elements on screen, page first.
func outs(s *ui.Screen) []string {
	var out []string
	for _, n := range s.Nodes {
		if n.ID == "out" {
			out = append(out, n.Text)
		}
	}
	return out
}
