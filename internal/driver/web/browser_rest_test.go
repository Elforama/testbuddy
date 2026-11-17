package web

import (
	"context"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// restProver is driver.RestProver (issue #369), named here so the test builds without it.
type restProver interface {
	AtRest(ctx context.Context, s *ui.Screen, n *ui.Node, span time.Duration) bool
}

// Issue #369: what moves on a page, and what does not. Needs Chrome: runs with
// TB_BROWSER_TESTS=1.
const restStyle = `<style>
  button { display: block; width: 160px; height: 40px; margin: 8px; }
  @keyframes pulse { from { transform: scale(1); } to { transform: scale(1.2); } }
  @keyframes spin { to { transform: rotate(360deg); } }
  @keyframes grow { from { height: 0; } to { height: 200px; } }
  #pulse { animation: pulse 0.6s ease-in-out infinite alternate; }
  #spinner { position: fixed; right: 8px; top: 8px; width: 24px; height: 24px; border: 3px solid #999; border-top-color: #000; border-radius: 50%; animation: spin 1s linear infinite; }
  #drawer { transition: transform 20s linear; }
  #banner { animation: grow 20s linear; overflow: hidden; background: #eef; }
</style>`

// An endless spinner, a pulse and a scripted slide: none of them moves #still. The slide steps
// 1 px every third frame, so only frames compared across the span see it.
const restPage = `<!doctype html><html><body style="margin:0">` + restStyle + `
<div id="spinner"></div>
<button id="still">Still</button>
<button id="pulse">Pulse</button>
<div><button id="jsmove" style="position: relative; left: 0">Scripted</button></div>
<script>
  const js = document.getElementById('jsmove');
  let x = 0, f = 0;
  (function step() { if (++f % 3 === 0) { x = (x + 1) % 200; js.style.left = x + 'px'; } requestAnimationFrame(step); })();
</script>
</body></html>`

// A drawer sliding for 20 s: a transition on what holds #indrawer, and a finite one elsewhere
// for #still (a drawer that comes over it).
const drawerPage = `<!doctype html><html><body style="margin:0">` + restStyle + `
<button id="still">Still</button>
<div id="drawer"><button id="indrawer">In drawer</button></div>
<script>
  requestAnimationFrame(() => requestAnimationFrame(() => {
    document.getElementById('drawer').style.transform = 'translateX(300px)';
  }));
</script>
</body></html>`

// A banner growing for 20 s above #pushed: a layout animation elsewhere.
const bannerPage = `<!doctype html><html><body style="margin:0">` + restStyle + `
<div id="banner">Banner</div>
<button id="pushed">Pushed</button>
</body></html>`

// A layout shift with no animation (issue #262's kind): a timer moves #shifted 1 px every 34 ms.
const shiftPage = `<!doctype html><html><body style="margin:0">` + restStyle + `
<div id="gap" style="height: 0"></div>
<button id="shifted">Shifted</button>
<script>
  let h = 0;
  setInterval(() => { h = (h + 1) % 100; document.getElementById('gap').style.height = h + 'px'; }, 34);
</script>
</body></html>`

// A banner in another element's shadow root, about to grow (its animation waits out a delay:
// nothing has moved yet), above #below.
const shadowPage = `<!doctype html><html><body style="margin:0">` + restStyle + `
<div id="host"></div>
<button id="below">Below</button>
<script>
  const root = document.getElementById('host').attachShadow({ mode: 'open' });
  root.innerHTML = '<style>@keyframes g { from { height: 0 } to { height: 200px } } div { overflow: hidden; animation: g 2s linear 30s; }</style><div>Banner</div>';
</script>
</body></html>`

// An SVG animated with SMIL, which getAnimations does not list.
const smilPage = `<!doctype html><html><body style="margin:0">` + restStyle + `
<svg width="40" height="40"><rect width="10" height="10"><animate attributeName="x" from="0" to="30" dur="1s" repeatCount="indefinite"/></rect></svg>
<button id="still">Still</button>
</body></html>`

func TestAtRestInChrome(t *testing.T) {
	type check struct {
		id   string
		span time.Duration
		rest bool
	}
	for _, p := range []struct {
		name, html, wait string
		checks           []check
	}{
		{"spinner, pulse, script", restPage, "jsmove", []check{
			{"still", 40 * time.Millisecond, true}, {"still", 80 * time.Millisecond, true},
			{"pulse", 40 * time.Millisecond, false}, {"jsmove", 80 * time.Millisecond, false}}},
		{"drawer", drawerPage, "indrawer", []check{{"indrawer", 40 * time.Millisecond, false}, {"still", 40 * time.Millisecond, false}}},
		{"banner", bannerPage, "pushed", []check{{"pushed", 40 * time.Millisecond, false}}},
		{"layout shift", shiftPage, "shifted", []check{{"shifted", 40 * time.Millisecond, false}}},
		{"shadow root", shadowPage, "below", []check{{"below", 40 * time.Millisecond, false}}},
		{"smil", smilPage, "still", []check{{"still", 40 * time.Millisecond, false}}},
	} {
		t.Run(p.name, func(t *testing.T) {
			d, ctx := openPage(t, p.html)
			rp, ok := any(d).(restProver)
			if !ok {
				t.Fatal("issue #369: the web driver cannot prove an element at rest for a span")
			}
			screenWith(t, d, ctx, p.wait)
			for _, c := range p.checks {
				s, err := d.Screen(ctx)
				if err != nil {
					t.Fatal(err)
				}
				n := find(s, c.id)
				if n == nil {
					t.Fatalf("no #%s", c.id)
				}
				start := time.Now()
				got := rp.AtRest(ctx, s, n, c.span)
				if got != c.rest {
					t.Errorf("issue #369: AtRest of #%s over %v = %v, want %v", c.id, c.span, got, c.rest)
				}
				if got && time.Since(start) < c.span {
					t.Errorf("issue #369: #%s proven at rest after %v, before its span %v", c.id, time.Since(start), c.span)
				}
			}
			if p.name != "spinner, pulse, script" {
				return
			}
			// A read that is not the last one: its nodes may not be the page's snapshot any more.
			old, err := d.Screen(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.Screen(ctx); err != nil {
				t.Fatal(err)
			}
			if rp.AtRest(ctx, old, find(old, "still"), 40*time.Millisecond) {
				t.Error("issue #369: AtRest proved a node of a read older than the last")
			}
		})
	}
}

// Issue #369 review: the frame comparison itself, with no animation to give motion away: a box
// that steps 1 px every third frame looks the same over two frames; over 80 ms it does not.
func TestStillComparesFramesOverTheSpanInChrome(t *testing.T) {
	d, ctx := openPage(t, restPage)
	screenWith(t, d, ctx, "jsmove")
	for i := 0; i < 5; i++ {
		s, err := d.Screen(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n := find(s, "jsmove")
		if n == nil {
			t.Fatal("no #jsmove")
		}
		if rp, ok := any(d).(restProver); ok && rp.AtRest(ctx, s, n, 80*time.Millisecond) {
			t.Fatalf("issue #369: a box stepping every third frame proven at rest over 80 ms (try %d)", i+1)
		}
	}
}
