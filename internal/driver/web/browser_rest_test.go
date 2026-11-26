package web

import (
	"testing"
	"time"
)

const restPage = `<!doctype html><html><body style="margin:0">
<style>
  button { display: block; width: 200px; height: 40px; margin: 8px; }
  @keyframes pulse { from { transform: scale(1); } to { transform: scale(1.2); } }
  @keyframes glow { from { background-color: #fff; } to { background-color: #fc0; } }
  @keyframes drift { from { transform: translateX(0); } to { transform: translateX(60px); } }
  #pulsing { animation: pulse 400ms ease-in-out infinite alternate; }
  #glowing { animation: glow 400ms linear infinite alternate; }
  #drifting { animation: drift 600ms linear infinite alternate; }
  #ball { position: absolute; right: 20px; top: 20px; width: 20px; height: 20px; background: #36f; }
  @keyframes bounce { from { left: 300px; } to { left: 500px; } }
  #floater { position: absolute; top: 400px; width: 20px; height: 20px; animation: bounce 700ms linear infinite alternate; }
  #spacer { height: 10px; }
</style>
<button id="plain">Plain</button>
<button id="pulsing">Pulsing</button>
<button id="glowing">Glowing</button>
<div id="drifting"><button id="carried">Carried</button></div>
<div id="jittery"><button id="shaken">Shaken</button></div>
<div id="spacer"></div>
<button id="pushed">Pushed</button>
<slide-panel><button id="slotted">Slotted</button></slide-panel>
<div id="ball"></div>
<div id="floater"></div>
<span id="clock">0</span>
<script>
  let n = 0;
  const frame = () => {
    n++;
    document.getElementById('clock').textContent = String(n);
    document.getElementById('ball').style.top = (20 + n % 50) + 'px';
    if (window.shake) document.getElementById('jittery').style.marginLeft = (n % 30) + 'px';
    requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
  window.push = () => { document.getElementById('spacer').style.height = '90px'; };
  customElements.define('slide-panel', class extends HTMLElement {
    constructor() {
      super();
      const root = this.attachShadow({ mode: 'open' });
      root.innerHTML = '<div id="wrap"><slot></slot></div>';
      this.wrap = root.getElementById('wrap');
    }
  });
  window.slide = () => document.querySelector('slide-panel').wrap.animate(
    [{ transform: 'translateX(0)' }, { transform: 'translateX(80px)' }], { duration: 60000 });
  window.grow = () => document.getElementById('spacer').animate(
    [{ height: '10px' }, { height: '12px' }], { duration: 60000, iterations: Infinity });
  window.reason = (id) => {
    const el = document.getElementById(id), box = el.getBoundingClientRect();
    return window.__tb.unrest(window.__tb.nodes.indexOf(el), Math.round(box.left), Math.round(box.top), Math.round(box.right), Math.round(box.bottom));
  };
  window.reasonAfterPush = () => new Promise((done) => {
    window.push();
    requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(() => done(window.reason('pushed')), 30)));
  });
</script>
</body></html>`

func TestPageVouchesForRestInChrome(t *testing.T) {
	d, ctx := openPage(t, restPage)
	screenWith(t, d, ctx, "pushed")
	time.Sleep(1200 * time.Millisecond)
	d.WaitSettled(ctx, 5*time.Second)
	vouched := func(id string) bool {
		t.Helper()
		s, err := d.Screen(ctx)
		if err != nil {
			t.Fatal(err)
		}
		n := find(s, id)
		if n == nil {
			t.Fatalf("no #%s", id)
		}
		return d.AtRest(ctx, n.Index, n.Bounds)
	}
	for _, c := range []struct {
		id   string
		want bool
		why  string
	}{
		{"plain", true, "nothing moves it, though a clock ticks, an out-of-flow ball moves every frame and an absolutely positioned element animates its left"},
		{"glowing", true, "its endless animation changes only a colour"},
		{"pulsing", false, "its own endless animation scales it"},
		{"carried", false, "an ancestor's endless animation carries it"},
		{"pushed", true, "nothing has pushed it yet"},
	} {
		if got := vouched(c.id); got != c.want {
			t.Errorf("#%s: vouched %v, want %v: %s", c.id, got, c.want, c.why)
		}
	}

	if !vouched("slotted") {
		t.Error("#slotted: not vouched while its shadow wrapper is still")
	}
	var why string
	if err := d.eval(ctx, "window.reasonAfterPush()", &why); err != nil {
		t.Fatal(err)
	}
	if why != "layout shifted" && why != "much of the layout shifted" {
		t.Errorf("#pushed two frames after the layout above it grew: %q, want a layout shift named", why)
	}
	time.Sleep(400 * time.Millisecond)
	d.WaitSettled(ctx, 5*time.Second)
	if !vouched("pushed") {
		t.Error("#pushed: not vouched 400 ms after its one shift")
	}

	if err := d.eval(ctx, "window.slide(), window.reason('slotted')", &why); err != nil {
		t.Fatal(err)
	}
	if why != "animated" {
		t.Errorf("#slotted while its shadow wrapper slides: %q, want animated", why)
	}
	if err := d.eval(ctx, "window.grow(), window.reason('plain')", &why); err != nil {
		t.Fatal(err)
	}
	if why != "an animation changes the layout" {
		t.Errorf("#plain while an endless animation grows an element by under 3 px: %q, want the layout animation named", why)
	}
	if err := d.eval(ctx, "document.getElementById('spacer').getAnimations().forEach((a) => a.cancel()), 1", nil); err != nil {
		t.Fatal(err)
	}

	if err := d.eval(ctx, "window.shake = true, 1", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	d.WaitSettled(ctx, 5*time.Second)
	if vouched("shaken") {
		t.Error("#shaken: vouched while a script moves its ancestor every frame")
	}
	if vouched("plain") {
		t.Error("#plain: vouched while a restless in-flow element changes the layout every frame")
	}
}

func TestPageDoesNotVouchForMovedBoundsInChrome(t *testing.T) {
	d, ctx := openPage(t, restPage)
	s := screenWith(t, d, ctx, "plain")
	time.Sleep(1200 * time.Millisecond)
	d.WaitSettled(ctx, 5*time.Second)
	s, err := d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := find(s, "plain")
	if !d.AtRest(ctx, n.Index, n.Bounds) {
		t.Fatal("#plain: not vouched at the bounds just read")
	}
	stale := n.Bounds
	stale.T += 12
	stale.B += 12
	if d.AtRest(ctx, n.Index, stale) {
		t.Error("#plain: vouched at bounds 12 px from where it is")
	}
}
