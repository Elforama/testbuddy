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
)

// Issue #311: taps whose only effect is focus. A field that already has the focus, a focusable
// element tapped again, and a field focused from another in the same shadow root (focusin
// does not leave a shadow root when focus moves within it) change nothing page.js reports,
// and waited out the 500 ms no-change window.
const focusPage311 = `<!doctype html><html><body style="margin:0">
<input id="first" style="position:absolute;left:10px;top:10px;width:200px;height:30px">
<div id="host" style="position:absolute;left:10px;top:60px;width:300px;height:30px"></div>
<div id="fd" tabindex="0" style="position:absolute;left:10px;top:110px;width:200px;height:30px">Focusable</div>
<div id="plain" style="position:absolute;left:10px;top:160px;width:200px;height:30px">Plain</div>
<input id="combo" style="position:absolute;left:10px;top:210px;width:200px;height:30px">
<ul id="opts" hidden style="position:absolute;left:10px;top:250px;margin:0"><li>Option</li></ul>
<button id="send" style="position:absolute;left:300px;top:10px;width:100px;height:30px">Send</button>
<div id="sent" style="position:absolute;left:300px;top:60px">0</div>
<script>
  const root = document.getElementById('host').attachShadow({mode: 'open'});
  root.innerHTML = '<input id="sw" type="checkbox" style="position:absolute;left:0;top:0;width:30px;height:30px;margin:0">' +
    '<input id="sf" style="position:absolute;left:50px;top:0;width:200px;height:30px">';
  // A combo box whose list opens (and closes) a moment after each tap: a late reaction.
  const opts = document.getElementById('opts');
  document.getElementById('combo').addEventListener('click', () => setTimeout(() => { opts.hidden = !opts.hidden; }, 200));
  // A button whose action shows on a worker's reply, 150 ms later: nothing tb tracks.
  const worker = new Worker(URL.createObjectURL(new Blob(['onmessage = () => setTimeout(() => postMessage(1), 150);'])));
  const sent = document.getElementById('sent');
  worker.onmessage = () => { sent.textContent = String(Number(sent.textContent) + 1); };
  document.getElementById('send').addEventListener('click', () => worker.postMessage(1));
</script>
</body></html>`

func TestFocusOnlyTapsInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(focusPage311))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	d.WaitSettled(ctx, 5*time.Second)
	tap := func(x, y int) driver.ActionResult {
		t.Helper()
		d.WaitSettled(ctx, 3*time.Second)
		r, err := d.Tap(ctx, x, y, driver.ActionOpts{React: true})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	active := func() string {
		var id string
		d.eval(ctx, `(() => { let a = document.activeElement; while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement; return a ? a.id : ''; })()`, &id)
		return id
	}
	fast := func(what string, r driver.ActionResult, want string) {
		t.Helper()
		if r.Settle != driver.Changed || r.SettleMs > 100 {
			t.Errorf("%s: %s after %d ms; want changed within 100 ms", what, r.Settle, r.SettleMs)
		}
		if a := active(); a != want {
			t.Errorf("%s: focus on %q, want %q", what, a, want)
		}
	}

	tap(110, 25)
	fast("a field that already has the focus", tap(110, 25), "first")
	tap(25, 75) // the switch in the shadow root
	fast("a field focused from another in its shadow root", tap(160, 75), "sf")
	fast("the shadow root's field tapped again", tap(160, 75), "sf")
	tap(110, 125)
	fast("a focusable element tapped again", tap(110, 125), "fd")

	// A tap that changes nothing, focus included, is silent: it still waits out the window.
	tap(110, 175)
	if r := tap(110, 175); r.Settle != driver.NoChange || r.SettleMs < 450 {
		t.Errorf("a silent tap: %s after %d ms; want no change after the window", r.Settle, r.SettleMs)
	}

	// Reliability: the focused combo box's tap ends at once, and its list, which closes 200 ms
	// later, is waited for before the next step reads the page.
	tap(110, 225)
	d.WaitSettled(ctx, 3*time.Second)
	var open bool
	if d.eval(ctx, `!document.getElementById('opts').hidden`, &open); !open {
		t.Fatal("the combo box's list did not open")
	}
	fast("the focused combo box", tap(110, 225), "combo")
	d.WaitSettled(ctx, 3*time.Second)
	if d.eval(ctx, `!document.getElementById('opts').hidden`, &open); open {
		t.Error("the next step would read the page before the combo box's list closed")
	}

	// A focused button tapped again is tapped for its action, not for focus: its late
	// reaction is still waited for, as before.
	tap(350, 25)
	var sent string
	for deadline := time.Now().Add(3 * time.Second); sent != "1" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		d.eval(ctx, `document.getElementById('sent').textContent`, &sent)
	}
	d.WaitSettled(ctx, 3*time.Second)
	r := tap(350, 25)
	d.WaitSettled(ctx, 3*time.Second)
	d.eval(ctx, `document.getElementById('sent').textContent`, &sent)
	if r.Settle != driver.Changed || sent != "2" {
		t.Errorf("a focused button tapped again: %s after %d ms, sent %q; want its late reaction (2) waited for", r.Settle, r.SettleMs, sent)
	}
}
