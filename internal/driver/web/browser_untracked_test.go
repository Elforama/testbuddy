package web

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #80 review: ways a box moves after a read while nothing reaches seq. A tap skips its
// re-reads of a target when the read was Tracked, seq has not moved and the page is settled;
// in each case below #t moves after the read, so one of those must be false once it has moved.
const movesPage = `<!doctype html><html><head><style>
body { margin: 0 }
#t { height: 30px }
#hov { height: 20px }
@keyframes grow { from { height: 20px } to { height: 220px } }
#hov:hover { animation: grow 2s linear infinite alternate }
</style></head><body>
<div id="hov">hover me</div>
<div id="slot"></div>
<div id="host"></div>
<div id="t">target</div>
</body></html>`

func TestScreenUntrackedMovesInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	var pngBytes bytes.Buffer
	png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 40, 150)))
	slow := 700 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow.png":
			time.Sleep(slow)
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngBytes.Bytes())
		case "/slow.css":
			time.Sleep(slow)
			w.Header().Set("Content-Type", "text/css")
			w.Write([]byte("#t { margin-top: 150px }"))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(movesPage))
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	cases := []struct {
		name    string
		setup   string // before the read
		trigger string // after it ("" : what setup started lands by itself)
		hover   bool   // the trigger is the mouse moving onto #hov
	}{
		{name: "image loads", setup: `const i = new Image(); i.src = '/slow.png'; slot.append(i)`},
		{name: "stylesheet loads", setup: `const l = document.createElement('link'); l.rel = 'stylesheet'; l.href = '/slow.css'; document.head.append(l)`},
		{name: "insertRule", setup: `document.head.append(document.createElement('style'))`,
			trigger: `const ss = document.styleSheets; ss[ss.length - 1].insertRule('#t { margin-top: 120px }')`},
		{name: "deleteRule", setup: `const s = document.createElement('style'); s.textContent = '#t { margin-top: 120px }'; document.head.append(s)`,
			trigger: `const ss = document.styleSheets; ss[ss.length - 1].deleteRule(0)`},
		{name: "replaceSync", setup: `window.sh = new CSSStyleSheet(); document.adoptedStyleSheets = [sh]`,
			trigger: `sh.replaceSync('#t { margin-top: 120px }')`},
		{name: "replace", setup: `window.sh = new CSSStyleSheet(); document.adoptedStyleSheets = [sh]`,
			trigger: `sh.replace('#t { margin-top: 120px }')`},
		{name: "adoptedStyleSheets", setup: `window.sh = new CSSStyleSheet(); sh.replaceSync('#t { margin-top: 120px }')`,
			trigger: `document.adoptedStyleSheets = [sh]`},
		{name: "shadow adoptedStyleSheets", setup: `window.sh = new CSSStyleSheet(); sh.replaceSync('div { height: 150px }'); window.sr = host.attachShadow({mode: 'open'}); sr.innerHTML = '<div>in</div>'`,
			trigger: `sr.adoptedStyleSheets = [sh]`},
		{name: "new Animation play", trigger: `new Animation(new KeyframeEffect(t, [{transform: 'translateY(200px)'}, {transform: 'translateY(200px)'}], {duration: 5000}), document.timeline).play()`},
		{name: "Animation currentTime", setup: `window.an = t.animate([{transform: 'none'}, {transform: 'translateY(300px)'}], {duration: 3000}); an.pause()`,
			trigger: `an.currentTime = 2000`},
		{name: "Animation reverse", setup: `window.an = t.animate([{transform: 'none'}, {transform: 'translateY(200px)'}], {duration: 4000, fill: 'both'}); an.pause(); an.currentTime = 0`,
			trigger: `an.reverse()`},
		{name: "Animation cancel", setup: `window.an = t.animate([{transform: 'translateY(200px)'}, {transform: 'translateY(200px)'}], {duration: 4000, fill: 'both'}); an.pause()`,
			trigger: `an.cancel()`},
		{name: "setKeyframes", setup: `window.an = t.animate([{transform: 'none'}, {transform: 'none'}], {duration: 1, fill: 'forwards'})`,
			trigger: `an.effect.setKeyframes([{transform: 'translateY(200px)'}, {transform: 'translateY(200px)'}])`},
		{name: "closed shadow root", setup: `window.sr = host.attachShadow({mode: 'closed'}); sr.innerHTML = '<div id=sp style="height: 10px">in</div>'`,
			trigger: `sr.getElementById('sp').style.height = '200px'`},
		{name: "open shadow root attached late", trigger: `host.attachShadow({mode: 'open'}).innerHTML = '<div style="height: 200px">in</div>'`},
		{name: "infinite animation on hover", hover: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
			if c.setup != "" {
				if err := d.eval(ctx, "{"+c.setup+"}", nil); err != nil {
					t.Fatal(err)
				}
			}
			s := read()
			n := find(s, "t")
			if n == nil {
				t.Fatal("#t not listed")
			}
			switch {
			case c.hover:
				h := find(s, "hov")
				for _, typ := range []string{"mouseMoved"} {
					if err := d.page.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": typ, "x": h.Bounds.CX(), "y": h.Bounds.CY()}, nil); err != nil {
						t.Fatal(err)
					}
				}
			case c.trigger != "":
				if err := d.eval(ctx, "{"+c.trigger+"}", nil); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(slow + 300*time.Millisecond)
			s2, err := d.Screen(ctx)
			if err != nil {
				t.Fatal(err)
			}
			n2 := find(s2, "t")
			if n2 == nil || n2.Bounds == n.Bounds {
				t.Fatalf("#t did not move (%v -> %v): the case does not test anything", n.Bounds, n2)
			}
			if s.Tracked && d.Seq() == s.Seq && d.SettledNow() {
				t.Errorf("#t moved %v -> %v with the read tracked, seq unchanged (%d) and the page settled: a tap would skip its re-reads and land on the old place", n.Bounds, n2.Bounds, s.Seq)
			}
		})
	}
	// Remove the mouse from #hov for later cases (none now; kept explicit).
	d.page.call(ctx, "Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": 0, "y": 0}, nil)
}
