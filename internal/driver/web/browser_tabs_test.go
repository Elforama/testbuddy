package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// New tabs (issue #78): a tap on a target=_blank link, or on a button that calls window.open,
// opens a new tab, but the driver kept driving the first one: the screen read still showed
// the first page, Foreground returned its URL, and the new page's elements were not found.
// Later steps must act on the new tab, as a person's attention would follow it, and when
// that tab closes the driver goes back to the tab that opened it. Network mocks cover the new
// tab. Needs Chrome: runs with TB_BROWSER_TESTS=1.
func TestNewTabsInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	btn := func(id, label, onclick string) string {
		return `<button id="` + id + `" style="display:block;width:220px;height:40px" onclick="` + onclick + `">` + label + `</button>`
	}
	pages := map[string]string{
		"/": `<h1 id="home">Home</h1>
<a id="blank" target="_blank" href="/other" style="display:block;width:220px;height:40px">Open other</a>` +
			btn("open", "Open popup", "window.open('/popup')") +
			btn("selfclose", "Open a popup that closes", "window.open('/selfclose')") +
			btn("mockdoc", "Open a mocked page", "window.open('/mockdoc')"),
		// A ticker: the page repaints, so a recording gets frames while it is driven.
		"/other": `<h1 id="other">Other page</h1><div id="tick">0</div>` + btn("closeother", "Close", "window.close()") +
			`<script>let n = 0; setInterval(() => { document.getElementById('tick').textContent = ++n; }, 100);</script>`,
		// The popup fetches /api/data, which a network mock answers, and can open a third tab.
		"/popup": `<h1 id="popup">Popup</h1><div id="data">waiting</div>` +
			btn("deeper", "Open deeper", "window.open('/deeper')") +
			btn("closepopup", "Close popup", "window.close()") +
			`<script>fetch('/api/data').then(r => r.text()).then(t => { document.getElementById('data').textContent = t; });</script>`,
		"/deeper":    `<h1 id="deeper">Deeper</h1>` + btn("closedeeper", "Close deeper", "window.close()"),
		"/selfclose": `<h1 id="brief">Brief</h1><script>setTimeout(() => window.close(), 50);</script>`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := pages[r.URL.Path]; ok {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<!doctype html><html><body style="margin:0">` + body + `</body></html>`))
			return
		}
		w.Write([]byte("real data"))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}

	// on reads the screen until it shows id (a flow's lookup retries the same way), and fails
	// the test with what is on screen instead.
	on := func(id string) *ui.Screen {
		t.Helper()
		var s *ui.Screen
		for deadline := time.Now().Add(5 * time.Second); ; {
			if s, err = d.Screen(ctx); err != nil {
				t.Fatalf("screen read, waiting for #%s: %v", id, err)
			}
			if find(s, id) != nil {
				return s
			}
			if time.Now().After(deadline) {
				var ids []string
				for _, n := range s.Nodes {
					if n.ID != "" {
						ids = append(ids, n.ID)
					}
				}
				t.Fatalf("#%s not on screen within 5 s; the screen shows %v", id, ids)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	tap := func(id string) {
		t.Helper()
		n := find(on(id), id)
		if _, err := d.Tap(ctx, n.Bounds.CX(), n.Bounds.CY(), driver.ActionOpts{}); err != nil {
			t.Fatalf("tap on #%s: %v", id, err)
		}
	}
	at := func(path string) {
		t.Helper()
		href, err := d.Foreground(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if href != srv.URL+path {
			t.Errorf("Foreground is %s, want %s", href, srv.URL+path)
		}
	}

	// A target=_blank link: the new tab is driven, and closing it goes back to the opener. A
	// recording follows: it gets the new tab's frames, and ends as a video.
	_, ffmpegErr := ffmpegPath()
	recording := ffmpegErr == nil
	frames := func() int {
		d.recMu.Lock()
		defer d.recMu.Unlock()
		if d.rec == nil {
			return 0
		}
		d.rec.mu.Lock()
		defer d.rec.mu.Unlock()
		return len(d.rec.frames)
	}
	video := filepath.Join(t.TempDir(), "tabs.mp4")
	if recording {
		if err := d.StartRecording(ctx, video); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Log("no ffmpeg: the recording across tabs is not checked")
	}
	tap("blank")
	on("other")
	at("/other")
	if recording {
		before := frames()
		for deadline := time.Now().Add(3 * time.Second); frames() < before+3; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the recording got %d frames in 3 s from the new tab, which repaints every 100 ms", frames()-before)
			}
		}
	}
	tap("closeother")
	on("home")
	at("/")
	if recording {
		if err := d.StopRecording(ctx); err != nil {
			t.Fatal(err)
		}
		if st, err := os.Stat(video); err != nil || st.Size() < 2000 {
			t.Fatalf("video across tabs: %v", err)
		}
	}

	// A new tab whose own document is mocked: the mock is on before its first request.
	if err := d.MockNetwork(ctx, driver.NetworkMock{URL: "*/mockdoc", Status: 200, Headers: [][2]string{{"Content-Type", "text/html"}},
		Body: []byte(`<!doctype html><html><body><h1 id="mocked">Mocked page</h1>` + btn("closemocked", "Close", "window.close()") + `</body></html>`)}); err != nil {
		t.Fatal(err)
	}
	tap("mockdoc")
	on("mocked")
	at("/mockdoc")
	tap("closemocked")
	on("home")

	// window.open, with a network mock that covers the new tab from its first request.
	if err := d.MockNetwork(ctx, driver.NetworkMock{URL: "*/api/data", Status: 200, Body: []byte("mocked data")}); err != nil {
		t.Fatal(err)
	}
	tap("open")
	s := on("popup")
	at("/popup")
	for deadline := time.Now().Add(5 * time.Second); find(s, "data") == nil || find(s, "data").Text != "mocked data"; {
		if time.Now().After(deadline) {
			t.Fatalf("the popup's fetch: %+v, want the mocked data", find(s, "data"))
		}
		time.Sleep(100 * time.Millisecond)
		s = on("popup")
	}

	// A tab opened from the new tab: driven in turn, and each close goes back one tab.
	tap("deeper")
	on("deeper")
	at("/deeper")
	tap("closedeeper")
	on("popup")
	at("/popup")
	tap("closepopup")
	on("home")
	at("/")

	// A popup that closes itself at once leaves the driver on the opener.
	tap("selfclose")
	time.Sleep(300 * time.Millisecond)
	if err := d.WaitSettled(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	on("home")
	at("/")
	if s, _ := d.Screen(ctx); s != nil && find(s, "brief") != nil {
		t.Errorf("the closed popup is still on screen")
	}
}
