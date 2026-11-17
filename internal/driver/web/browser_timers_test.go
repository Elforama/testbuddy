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
)

// Issue #312: short page timers count as busy, so a page that renders in two phases (a
// change, then a timer that renders the rest) is settled only after the second; pollers,
// intervals and long timers do not hold it.
const timersPage = `<!doctype html><html><body>
<button id="two">Two phases</button>
<button id="chain">Chained timers</button>
<button id="toast">Save</button>
<button id="micro">Microtasks</button>
<button id="plain">Plain</button>
<input id="q" placeholder="Search">
<div id="out">idle</div>
<script>
  const out = document.getElementById('out');
  const show = (t) => { out.textContent = t; };
  document.getElementById('two').onclick = () => { show('loading'); setTimeout(() => show('phase two'), 200); };
  document.getElementById('chain').onclick = () => {
    show('step 0');
    setTimeout(() => { show('step 1'); setTimeout(() => show('step 2'), 150); }, 150);
  };
  document.getElementById('toast').onclick = (e) => {
    e.target.textContent = 'Saving';
    setTimeout(() => {
      const t = document.createElement('div'); t.id = 'saved'; t.textContent = 'Saved'; document.body.append(t);
    }, 100);
  };
  document.getElementById('micro').onclick = () => {
    show('micro start');
    let p = Promise.resolve();
    for (let i = 0; i < 20; i++) p = p.then(() => null);
    p.then(() => show('micro done'));
  };
  let clicks = 0;
  document.getElementById('plain').onclick = () => show('plain ' + (++clicks));
  let h;
  document.getElementById('q').addEventListener('input', (e) => {
    clearTimeout(h);
    const v = e.target.value;
    h = setTimeout(() => show('results for ' + v), 150);
  });
  window.startPollers = () => {
    setInterval(() => {}, 50);                                  // an interval: never counted
    const poll = () => setTimeout(poll, 100); poll();           // a setTimeout poller
    (async () => { for (;;) await new Promise((r) => setTimeout(r, 80)); })(); // an await-sleep loop
    const rearm = () => setTimeout(rearm, 5000); rearm();       // a long timer
    return 1;
  };
</script>
</body></html>`

func TestShortTimersInChrome(t *testing.T) {
	d, ctx := openPage(t, timersPage)
	s := screenWith(t, d, ctx, "plain")
	text := func(id string) string {
		t.Helper()
		s, err := d.Screen(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n := find(s, id); n != nil {
			return n.Text
		}
		return "(none)"
	}
	// tap taps the element and waits for the page to settle, as the next action would.
	tap := func(id string) time.Duration {
		t.Helper()
		n := find(s, id)
		if n == nil {
			t.Fatalf("no #%s", id)
		}
		start := time.Now()
		res, err := d.Tap(ctx, n.Bounds.CX(), n.Bounds.CY(), driver.ActionOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Settle != driver.Changed {
			t.Errorf("tap on #%s: settle %v, want changed", id, res.Settle)
		}
		d.WaitSettled(ctx, 5*time.Second)
		return time.Since(start)
	}

	tap("two")
	if got := text("out"); got != "phase two" {
		t.Errorf("settled after a two-phase update with %q showing, want %q", got, "phase two")
	}
	tap("chain")
	if got := text("out"); got != "step 2" {
		t.Errorf("settled after chained timers with %q showing, want %q", got, "step 2")
	}
	tap("toast")
	if text("saved") != "Saved" {
		t.Error("settled before the toast a 100 ms timer shows")
	}
	tap("micro")
	if got := text("out"); got != "micro done" {
		t.Errorf("settled after microtasks with %q showing, want %q", got, "micro done")
	}
	tap("q")
	if _, err := d.TypeText(ctx, "ada", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	d.WaitSettled(ctx, 5*time.Second)
	if got := text("out"); got != "results for ada" {
		t.Errorf("settled after a debounced search with %q showing, want %q", got, "results for ada")
	}

	// Pollers, an interval and a long timer run for good: they do not hold the page.
	if err := d.eval(ctx, "window.startPollers()", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // past the pollers' first second
	for i := 1; i <= 3; i++ {
		if took := tap("plain"); took > 900*time.Millisecond {
			t.Errorf("tap %d with pollers running took %v: they hold the page", i, took)
		}
	}
}

// timerPage is a page open in Chrome, with what the tests below do on it.
type timerPage struct {
	t   *testing.T
	d   *Driver
	ctx context.Context
}

func openTimerPage(t *testing.T, html, first string) *timerPage {
	d, ctx := openPage(t, html)
	screenWith(t, d, ctx, first)
	return &timerPage{t, d, ctx}
}

func (p *timerPage) text(id string) string {
	p.t.Helper()
	s, err := p.d.Screen(p.ctx)
	if err != nil {
		p.t.Fatal(err)
	}
	if n := find(s, id); n != nil {
		return n.Text
	}
	return "(none)"
}

// tap taps the element and waits for the page to settle, as the next action would: how long
// that took.
func (p *timerPage) tap(id string) time.Duration {
	p.t.Helper()
	s, err := p.d.Screen(p.ctx)
	if err != nil {
		p.t.Fatal(err)
	}
	n := find(s, id)
	if n == nil {
		p.t.Fatalf("no #%s", id)
	}
	start := time.Now()
	res, err := p.d.Tap(p.ctx, n.Bounds.CX(), n.Bounds.CY(), driver.ActionOpts{})
	if err != nil {
		p.t.Fatal(err)
	}
	if res.Settle != driver.Changed {
		p.t.Errorf("tap on #%s: settle %v, want changed", id, res.Settle)
	}
	p.d.WaitSettled(p.ctx, 15*time.Second)
	return time.Since(start)
}

func (p *timerPage) eval(expr string) {
	p.t.Helper()
	if err := p.d.eval(p.ctx, expr, nil); err != nil {
		p.t.Fatal(err)
	}
}

// Issue #312 (review): a 1 s countdown that changes the page every tick and re-arms its timer
// from somewhere page.js cannot follow (a MessageChannel, an effect run after a render, a
// requestAnimationFrame, a fetch's then, an interval) must not hold a tap: it took 13 s.
func TestCountdownDoesNotHoldInChrome(t *testing.T) {
	p := openTimerPage(t, `<!doctype html><html><body>
<button id="plain">Plain</button><div id="out">idle</div>
<div id="c1"></div><div id="c2"></div><div id="c3"></div><div id="c4"></div><div id="c5"></div>
<script>
  let clicks = 0;
  document.getElementById('plain').onclick = () => { document.getElementById('out').textContent = 'plain ' + (++clicks); };
  // Each countdown ticks every second and re-arms its next tick from elsewhere.
  const countdown = (id, rearm) => {
    let n = 1000;
    const tick = () => { document.getElementById(id).textContent = String(--n); rearm(() => setTimeout(tick, 1000)); };
    setTimeout(tick, 1000);
  };
  const mc = new MessageChannel(); let mcNext = null;
  mc.port1.onmessage = () => { const f = mcNext; mcNext = null; f && f(); };
  countdown('c1', (f) => { mcNext = f; mc.port2.postMessage(0); });           // a MessageChannel
  countdown('c2', (f) => { mcNext2.push(f); mc2.port2.postMessage(0); });     // an effect after a render
  const mc2 = new MessageChannel(); const mcNext2 = [];
  mc2.port1.onmessage = () => { const f = mcNext2.shift(); Promise.resolve().then(() => queueMicrotask(f)); };
  countdown('c3', (f) => requestAnimationFrame(f));                          // a requestAnimationFrame
  countdown('c4', (f) => fetch('/ping').then(() => f()));                    // a fetch's then
  const later = []; setInterval(() => { while (later.length) later.shift()(); }, 100);
  countdown('c5', (f) => later.push(f));                                     // an interval
</script></body></html>`, "plain")
	time.Sleep(3500 * time.Millisecond) // the countdowns are well under way
	for i := 1; i <= 3; i++ {
		if took := p.tap("plain"); took > 700*time.Millisecond {
			t.Errorf("tap %d with countdowns running took %v: they hold the page", i, took)
		}
	}
}

// Issue #312 (review): clearInterval clears a setTimeout id too (one pool): the cleared timer
// must stop counting, or every tap after it waits out the cap.
func TestClearIntervalOfTimeoutInChrome(t *testing.T) {
	p := openTimerPage(t, `<!doctype html><html><body>
<button id="plain">Plain</button><div id="out">idle</div>
<script>
  let clicks = 0;
  document.getElementById('plain').onclick = () => {
    document.getElementById('out').textContent = 'plain ' + (++clicks);
    clearInterval(setTimeout(() => {}, 900)); // cleared with the other function
  };
</script></body></html>`, "plain")
	for i := 1; i <= 3; i++ {
		if took := p.tap("plain"); took > 700*time.Millisecond {
			t.Errorf("tap %d after a timeout cleared with clearInterval took %v: it still counts", i, took)
		}
	}
}

// Issue #312 (review): the page and a same-process iframe share a CDP session: one's timers
// must neither erase nor stand in for the other's.
func TestFrameTimersInChrome(t *testing.T) {
	p := openTimerPage(t, `<!doctype html><html><body>
<button id="a">Page timer</button><button id="b">Frame timer</button><div id="out">idle</div>
<iframe id="f" srcdoc="<p>frame</p>"></iframe>
<script>
  const out = document.getElementById('out');
  const fw = () => document.getElementById('f').contentWindow;
  // The page's timer renders; the frame's quick one ends meanwhile.
  document.getElementById('a').onclick = () => {
    out.textContent = 'a waiting';
    setTimeout(() => { out.textContent = 'a done'; }, 250);
    fw().setTimeout(() => {}, 0);
  };
  // The frame's timer renders; the page's quick one ends meanwhile.
  document.getElementById('b').onclick = () => {
    out.textContent = 'b waiting';
    fw().setTimeout(() => { out.textContent = 'b done'; }, 250);
    setTimeout(() => {}, 0);
  };
</script></body></html>`, "a")
	p.tap("a")
	if got := p.text("out"); got != "a done" {
		t.Errorf("a frame's timer ending let the page settle with %q showing, want %q", got, "a done")
	}
	p.tap("b")
	if got := p.text("out"); got != "b done" {
		t.Errorf("the page's timer ending let the page settle with %q showing (the frame's pending), want %q", got, "b done")
	}
}

// Issue #312 (review): renders a 40 ms quiet window would miss unless their cause is tracked:
// a cross-site API answering in 80 ms, and a one-shot setInterval 150 ms on (70 ms in the
// review): the reviewer saw both settle with the old text on screen.
func TestDelayedRendersInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte("items"))
	}))
	defer api.Close()
	// Another site than the page's 127.0.0.1.
	apiURL := strings.Replace(api.URL, "127.0.0.1", "localhost", 1) + "/items"
	p := openTimerPage(t, `<!doctype html><html><body>
<button id="api">Load</button><button id="interval">Interval</button>
<div id="out">idle</div>
<script>
  const out = document.getElementById('out');
  document.getElementById('api').onclick = () => {
    out.textContent = 'loading';
    fetch('`+apiURL+`').then((r) => r.text()).then((t) => { out.textContent = 'got ' + t; });
  };
  document.getElementById('interval').onclick = () => {
    out.textContent = 'interval waiting';
    const id = setInterval(() => { out.textContent = 'interval done'; clearInterval(id); }, 150);
  };
</script></body></html>`, "api")
	for _, c := range []struct{ id, want string }{
		{"api", "got items"}, {"interval", "interval done"},
	} {
		p.tap(c.id)
		if got := p.text("out"); got != c.want {
			t.Errorf("tap on #%s settled with %q showing, want %q", c.id, got, c.want)
		}
	}
}

// Issue #312 (review): the wrapped timer functions look native to the page.
func TestTimerWrappersLookNativeInChrome(t *testing.T) {
	p := openTimerPage(t, `<!doctype html><html><body><p id="x">x</p></body></html>`, "x")
	var bad []string
	err := p.d.eval(p.ctx, `(() => {
  const bad = [];
  for (const n of ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval']) {
    const f = window[n];
    if (f.name !== n) bad.push(n + '.name ' + f.name);
    if (String(f) !== 'function ' + n + '() { [native code] }') bad.push(n + ' toString ' + String(f));
    if (Function.prototype.toString.call(f) !== 'function ' + n + '() { [native code] }') bad.push(n + ' Function.prototype.toString');
  }
  const ts = Function.prototype.toString;
  if (String(ts) !== 'function toString() { [native code] }') bad.push('toString itself ' + String(ts));
  if (setTimeout.length !== 0 && setTimeout.length !== 1) bad.push('setTimeout.length ' + setTimeout.length);
  return bad;
})()`, &bad)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Error("not native-looking: " + b)
	}
}

// A Sentry/Datadog/Zone.js-style wrapper, installed by the page after tb's: every app timer
// reaches tb's setTimeout through the same line of the wrapper.
const sentryWrapper = `
  const origSetTimeout = window.setTimeout;
  window.setTimeout = function (fn, ms, ...rest) {
    const wrapped = function () { return fn.apply(this, arguments); };
    return origSetTimeout.call(window, wrapped, ms, ...rest);
  };`

// Issue #312 (review 2): timers armed through a page's own setTimeout wrapper share its call
// site: a background loop re-arming through it made the site look recurring, and the tap's
// one-shot timer, armed through the same line, went uncounted or held the tap for the cap
// (taps took 1.07 to 1.09 s, 216 ms on main). The one-shot must be caught, and taps quick.
func TestWrappedTimersInChrome(t *testing.T) {
	p := openTimerPage(t, `<!doctype html><html><body>
<button id="go">Go</button><div id="out">idle</div>
<script>`+sentryWrapper+`
  const loop = () => setTimeout(loop, 30); loop();             // a background re-arm
  const mc = new MessageChannel(); mc.port1.onmessage = () => setTimeout(() => {}, 30);
  setInterval(() => mc.port2.postMessage(0), 30);             // and one re-armed from elsewhere
  const out = document.getElementById('out');
  let n = 0;
  document.getElementById('go').onclick = () => {
    const k = ++n;
    requestAnimationFrame(() => {
      out.textContent = 'waiting ' + k;
      setTimeout(() => { out.textContent = 'done ' + k; }, 80);
    });
  };
</script></body></html>`, "go")
	time.Sleep(2500 * time.Millisecond) // the loops are learned
	for i := 1; i <= 4; i++ {
		took := p.tap("go")
		if want := fmt.Sprintf("done %d", i); p.text("out") != want {
			t.Errorf("tap %d settled with %q showing, want %q: the one-shot timer was not counted", i, p.text("out"), want)
		}
		if took > 700*time.Millisecond {
			t.Errorf("tap %d through a timer wrapper took %v", i, took)
		}
	}
}

// Issue #312 (review 2): a plain wrapper with nothing else going on: a two-phase update
// through it is still caught.
func TestWrappedTwoPhaseInChrome(t *testing.T) {
	p := openTimerPage(t, `<!doctype html><html><body>
<button id="go">Go</button><div id="out">idle</div>
<script>`+sentryWrapper+`
  const out = document.getElementById('out');
  let n = 0;
  document.getElementById('go').onclick = () => {
    const k = ++n;
    out.textContent = 'waiting ' + k;
    setTimeout(() => { out.textContent = 'done ' + k; }, 200);
  };
</script></body></html>`, "go")
	for i := 1; i <= 3; i++ {
		p.tap("go")
		if want := fmt.Sprintf("done %d", i); p.text("out") != want {
			t.Errorf("tap %d settled with %q showing, want %q", i, p.text("out"), want)
		}
	}
}

// Issue #312 (review 2): a same-site request answered after more than softCap, whose answer
// renders through a short timer: the timer still counts, past the cap.
func TestTimerAfterSlowRequestInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	page := `<!doctype html><html><body>
<button id="go">Go</button><div id="out">idle</div>
<script>
  const out = document.getElementById('out');
  document.getElementById('go').onclick = () => {
    out.textContent = 'loading';
    fetch('/slow').then((r) => r.text()).then((t) => {
      setTimeout(() => { out.textContent = 'got ' + t; }, 60);
    });
  };
</script></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(1600 * time.Millisecond)
			w.Write([]byte("items"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(page))
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
	screenWith(t, d, ctx, "go")
	p := &timerPage{t, d, ctx}
	p.tap("go")
	if got := p.text("out"); got != "got items" {
		t.Errorf("settled with %q showing, want %q: the timer after a 1.6 s request was missed", got, "got items")
	}
}

// Issue #312 (review 3): the same component code arms a short timer in the background (a
// banner it rotates every 300 ms) and after a tap (it mounts the tap's result, which renders
// 80 ms later). Every frame is the same, so the site is learned as recurring; the tap's
// one-shot must still be caught, as main's 120 ms window caught it, and a plain tap on the
// page must stay quick.
func TestRecurringSiteAfterTapInChrome(t *testing.T) {
	p := openTimerPage(t, `<!doctype html><html><body>
<button id="go">Go</button><button id="plain">Plain</button>
<div id="out">idle</div><div id="banner"></div><div id="plainout"></div>
<script>
  const effects = [];
  const mc = new MessageChannel();
  mc.port1.onmessage = () => { while (effects.length) effects.shift()(); };
  const useEffect = (f) => { effects.push(f); mc.port2.postMessage(0); };
  function mountDelayed(el, text) {
    el.textContent = 'loading';
    useEffect(() => { setTimeout(() => { el.textContent = text; }, 80); });
  }
  let b = 0;
  setInterval(() => mountDelayed(document.getElementById('banner'), 'banner ' + (++b)), 300);
  let n = 0;
  document.getElementById('go').onclick = () => mountDelayed(document.getElementById('out'), 'done ' + (++n));
  let m = 0;
  document.getElementById('plain').onclick = () => { document.getElementById('plainout').textContent = 'plain ' + (++m); };
</script></body></html>`, "go")
	time.Sleep(2500 * time.Millisecond) // the banner's site is learned as recurring
	for i := 1; i <= 3; i++ {
		p.tap("go")
		if want := fmt.Sprintf("done %d", i); p.text("out") != want {
			t.Errorf("tap %d settled with %q showing, want %q: the tap's one-shot was not counted", i, p.text("out"), want)
		}
	}
	for i := 1; i <= 3; i++ {
		if took := p.tap("plain"); took > 700*time.Millisecond {
			t.Errorf("plain tap %d with the banner rotating took %v", i, took)
		}
	}
}
