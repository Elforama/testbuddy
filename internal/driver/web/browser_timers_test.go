package web

import (
	"testing"
	"time"

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
