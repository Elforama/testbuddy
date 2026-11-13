package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #80: a screen read is Tracked (every later change that can move a box bumps seq) only
// when no animation that can move a box is running. A tap skips its stability re-reads on a
// tracked, unchanged, settled screen, so motion that sends no event must leave it untracked.
const trackedPage = `<!doctype html><html><head><style>
@keyframes spin { to { transform: rotate(360deg); } }
@keyframes glow { from { background-color: #fff; } to { background-color: #ff0; } }
.spin { animation: spin 1s linear infinite; }
.glow { animation: glow 1s linear infinite alternate; }
</style></head><body>
<button id="go">Go</button>
<div id="box" style="width:40px;height:40px">box</div>
</body></html>`

func TestScreenTrackedInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(trackedPage))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// The blank page the browser opens on got the helper late (its scripts could hold the
	// originals of what the helper wraps): its reads are never tracked.
	if s, err := d.Screen(ctx); err != nil || s.Tracked {
		t.Errorf("the blank page, helper installed late: tracked %v (%v), want untracked", s != nil && s.Tracked, err)
	}
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	read := func() *ui.Screen {
		t.Helper()
		d.WaitSettled(ctx, 3*time.Second)
		s, err := d.Screen(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	run := func(js string) {
		t.Helper()
		if err := d.eval(ctx, js, nil); err != nil {
			t.Fatal(err)
		}
	}

	if s := read(); !s.Tracked || !d.SettledNow() {
		t.Fatalf("a still page: tracked %v, settled now %v; want both", s.Tracked, d.SettledNow())
	}
	// A color that keeps changing moves nothing.
	run(`document.getElementById('box').className = 'glow'`)
	if s := read(); !s.Tracked {
		t.Errorf("an endless color animation left the page untracked")
	}
	// An endless transform (a spinner) sends no events as it turns.
	run(`document.getElementById('box').className = 'spin'`)
	if s := read(); s.Tracked {
		t.Errorf("an endless rotation left the page tracked")
	}
	run(`document.getElementById('box').className = ''`)
	if s := read(); !s.Tracked {
		t.Errorf("the rotation removed: the page is still untracked")
	}
	// A script animation, started with no DOM mutation: it bumps seq, and the read while it
	// runs is untracked.
	s := read()
	run(`document.getElementById('box').animate([{transform: 'translateX(0)'}, {transform: 'translateX(100px)'}], {duration: 3000})`)
	if d.Seq() == s.Seq {
		t.Errorf("element.animate() did not bump seq")
	}
	if s := read(); s.Tracked {
		t.Errorf("a running element.animate() movement left the page tracked")
	}
}
