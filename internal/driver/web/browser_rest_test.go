package web

import (
	"context"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// restProver is driver.RestProver (issue #369), named here so the test builds without it.
type restProver interface {
	AtRest(ctx context.Context, s *ui.Screen, n *ui.Node) bool
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

// An endless spinner, a pulse and a scripted slide: none of them moves #still.
const restPage = `<!doctype html><html><body style="margin:0">` + restStyle + `
<div id="spinner"></div>
<button id="still">Still</button>
<button id="pulse">Pulse</button>
<div><button id="jsmove" style="position: relative; left: 0">Scripted</button></div>
<script>
  // A slide in a script, one step a frame, with no CSS animation: only the frames show it.
  const js = document.getElementById('jsmove');
  let x = 0;
  (function step() { x = (x + 1) % 200; js.style.left = x + 'px'; requestAnimationFrame(step); })();
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

func TestAtRestInChrome(t *testing.T) {
	for _, p := range []struct {
		name, html, wait string
		cases            map[string]bool
	}{
		{"spinner, pulse, script", restPage, "jsmove", map[string]bool{"still": true, "pulse": false, "jsmove": false}},
		{"drawer", drawerPage, "indrawer", map[string]bool{"indrawer": false, "still": false}},
		{"banner", bannerPage, "pushed", map[string]bool{"pushed": false}},
	} {
		t.Run(p.name, func(t *testing.T) {
			d, ctx := openPage(t, p.html)
			rp, ok := any(d).(restProver)
			if !ok {
				t.Fatal("issue #369: the web driver cannot prove an element at rest")
			}
			screenWith(t, d, ctx, p.wait)
			for id, rest := range p.cases {
				s, err := d.Screen(ctx)
				if err != nil {
					t.Fatal(err)
				}
				n := find(s, id)
				if n == nil {
					t.Fatalf("no #%s", id)
				}
				if got := rp.AtRest(ctx, s, n); got != rest {
					t.Errorf("issue #369: AtRest of #%s = %v, want %v", id, got, rest)
				}
			}
			if !p.cases["still"] {
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
			if rp.AtRest(ctx, old, find(old, "still")) {
				t.Error("issue #369: AtRest proved a node of a read older than the last")
			}
		})
	}
}
