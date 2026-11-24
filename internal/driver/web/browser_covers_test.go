package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

var coverPages = map[string]string{
	"/drawer": `<style>
  .row { height: 48px; border-bottom: 1px solid #ddd; padding: 0 16px; line-height: 48px; cursor: pointer; }
  #backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.4); }
  #drawer { position: fixed; top: 0; bottom: 0; left: 0; width: 280px; background: #fff; }
  #drawer div { height: 48px; line-height: 48px; padding: 0 16px; cursor: pointer; }
</style>
<div id="list"></div>
<div id="backdrop"></div>
<nav id="drawer"><div onclick="pick('drawer Recent')">Recent</div><div onclick="pick('drawer Images')">Images</div>
  <div id="result">Nothing picked</div></nav>
<script>
  function pick(s) { document.getElementById('result').textContent = s; }
  const names = ['Documents', 'Downloads', 'Images', 'Audio', 'Videos'];
  for (let i = 0; i < 40; i++) {
    const r = document.createElement('div');
    r.className = 'row';
    r.textContent = i < names.length ? names[i] : 'File ' + i;
    r.onclick = () => pick('list ' + r.textContent);
    document.getElementById('list').appendChild(r);
  }
</script>`,
	"/sticky": `<style>
  header { position: sticky; top: 0; height: 80px; background: #eef; z-index: 1; }
  .row { height: 40px; border-bottom: 1px solid #ddd; }
</style>
<header>Header <span id="result">Nothing tapped</span></header>
<div id="rows"></div>
<script>
  for (let i = 0; i < 60; i++) {
    const r = document.createElement('div');
    r.className = 'row';
    r.textContent = 'Row ' + i;
    r.onclick = () => { document.getElementById('result').textContent = r.textContent + ' tapped'; };
    document.getElementById('rows').appendChild(r);
  }
  // Row 5's top 30 px (its middle too) under the header, 11 px of it below.
  window.addEventListener('load', () => window.scrollTo(0, document.getElementById('rows').children[5].offsetTop - 50));
</script>`,
	"/aria": `<style>
  #dlg { position: absolute; left: 40px; top: 120px; width: 480px; height: 260px; background: #fff; border: 2px solid #333; }
  #dlg button { position: absolute; right: 16px; bottom: 16px; }
  .page button { display: block; margin: 8px 0; width: 200px; height: 40px; }
</style>
<div class="page" style="padding: 140px 0 0 80px">
  <button onclick="say('page Delete')">Delete</button>
  <button onclick="say('page Archive')">Archive</button>
</div>
<div id="dlg" role="dialog" aria-modal="true">Remove this file?
  <button onclick="say('dialog Delete')">Delete</button></div>
<div id="result" style="position:absolute; top: 420px; left: 40px">Nothing</div>
<script>function say(s) { document.getElementById('result').textContent = s; }</script>`,
	"/dialog": `<style>
  .page button { display: block; margin: 8px 0; width: 200px; height: 40px; }
</style>
<div class="page" style="padding: 40px">
  <button onclick="say('page OK')">OK</button>
  <button onclick="say('page Settings')">Settings</button>
  <div id="result">Nothing</div>
</div>
<dialog id="dlg" style="margin: 20px auto 0 20px; width: 400px; height: 200px">Saved.
  <button onclick="say('dialog OK'); this.closest('dialog').close()">OK</button></dialog>
<script>
  function say(s) { document.getElementById('result').textContent = s; }
  document.getElementById('dlg').showModal();
</script>`,
	"/passthrough": `<style>
  button { display: block; margin: 8px 0; width: 200px; height: 40px; }
  #glass { position: fixed; inset: 0; background: rgba(0,0,80,.1); pointer-events: none; }
  #ghost { position: fixed; left: 0; top: 150px; width: 400px; height: 60px; opacity: 0; background: red; }
</style>
<div style="padding: 40px 40px 0">
  <button onclick="say('first Save')">Save</button>
  <button onclick="say('second Save')">Save</button>
</div>
<div style="padding: 0 40px; position: absolute; top: 150px">
  <button onclick="say('hidden Send')">Send</button>
</div>
<div style="padding: 0 40px; position: absolute; top: 260px">
  <button onclick="say('clear Send')">Send</button>
  <div id="result">Nothing</div>
</div>
<div id="glass"></div>
<div id="ghost" onclick="say('ghost')"></div>
<script>function say(s) { document.getElementById('result').textContent = s; }</script>`,
	"/toast": `<button id="go" style="position:absolute; left:40px; top:100px; width:200px; height:40px"
  onclick="document.getElementById('result').textContent = 'Go tapped'">Go</button>
<div id="result" style="position:absolute; top:300px">Nothing</div>
<div id="t" style="position:fixed; left:20px; top:60px; width:300px; height:140px; background:#333; color:#fff">Saved
  <button onclick="this.parentNode.remove()">Undo</button></div>
<script>setTimeout(() => document.getElementById('t').remove(), 3500);</script>`,
	"/stretched": `<div style="position:relative; margin:100px 40px; width:300px; padding:16px; border:1px solid #ccc">
  <h5 style="margin:0">Card title</h5><p>Some text.</p>
  <a href="#go" style="position:absolute; inset:0" onclick="document.getElementById('result').textContent = 'Link tapped'"></a>
</div>
<div id="result">Nothing</div>
<div style="height:3000px"></div>`,
}

func coverServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := coverPages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><body style="margin:0">` + body + `</body></html>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCoversInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	srv := coverServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	dir := t.TempDir()
	var trace strings.Builder
	run := func(name, page, steps string, lookup time.Duration) (engine.FlowResult, time.Duration) {
		t.Helper()
		f, err := flow.ParseBytes(filepath.Join(dir, name+".yaml"), []byte("url: "+srv.URL+page+"\n---\n- launchApp\n"+steps))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		trace.Reset()
		o := engine.Options{LookupTimeout: lookup, OptionalTimeout: lookup / 2, Trace: engine.NewTrace(&trace)}
		start := time.Now()
		res := engine.New(d, o).RunFlow(ctx, f)
		return res, time.Since(start)
	}
	scrollY := func() int {
		var y int
		d.eval(ctx, "Math.round(window.scrollY)", &y)
		return y
	}
	pass := func(name, page, steps string) time.Duration {
		t.Helper()
		res, took := run(name, page, steps, 5*time.Second)
		if res.Status != engine.Passed {
			t.Errorf("%s: %v\ntrace:\n%s", name, res.Err, trace.String())
		}
		return took
	}
	fail := func(name, page, steps, want string) {
		t.Helper()
		res, _ := run(name, page, steps, 3*time.Second)
		if res.Status != engine.Failed || res.Err == nil || !strings.Contains(res.Err.Error(), want) {
			t.Errorf("%s: %s %v, want a failure with %q\ntrace:\n%s", name, res.Status, res.Err, want, trace.String())
		}
	}
	noDrag := func(name string) {
		t.Helper()
		if strings.Contains(trace.String(), "dragged its list") {
			t.Errorf("%s: dragged content under a modal layer:\n%s", name, trace.String())
		}
	}

	took := pass("drawer", "/drawer", "- tapOn: Images\n- assertVisible: drawer Images\n")
	noDrag("drawer")
	if y := scrollY(); y != 0 {
		t.Errorf("drawer: the page scrolled to %d under the drawer", y)
	}
	if took > 3*time.Second {
		t.Errorf("drawer: took %s: waited on the list's row under the backdrop", took)
	}
	fail("drawer stays", "/drawer", "- tapOn: Audio\n", `"Audio" is under`)
	noDrag("drawer stays")
	if y := scrollY(); y != 0 {
		t.Errorf("drawer stays: the page scrolled to %d under the drawer", y)
	}

	if took := pass("sticky", "/sticky", "- tapOn: Row 5\n- assertVisible: Row 5 tapped\n"); took > 3*time.Second {
		t.Errorf("sticky: took %s: waited on the header", took)
	}
	if !strings.Contains(trace.String(), "dragged its list") {
		t.Errorf("sticky: the page was not scrolled to bring the row clear of the header:\n%s", trace.String())
	}
	var loaded int
	d.eval(ctx, "document.getElementById('rows').children[5].offsetTop - 50", &loaded)
	if y := scrollY(); y == loaded {
		t.Errorf("sticky: the page is still at %d, where it loaded: the drag scrolled nothing", y)
	}

	if res, took := run("toast", "/toast", "- tapOn: Go\n- assertVisible: Go tapped\n", 6*time.Second); res.Status != engine.Passed {
		t.Errorf("toast: %v after %s\ntrace:\n%s", res.Err, took, trace.String())
	}

	run("stretched", "/stretched", "- tapOn: Card title\n", 2*time.Second)
	if strings.Contains(trace.String(), "dragged its list") {
		t.Errorf("stretched: dragged the page for a cover that scrolls with it:\n%s", trace.String())
	}
	if y := scrollY(); y != 0 {
		t.Errorf("stretched: the page scrolled to %d", y)
	}

	pass("aria", "/aria", "- tapOn: Delete\n- assertVisible: dialog Delete\n")
	fail("aria stays", "/aria", "- tapOn: Archive\n", `"Archive" is under`)
	noDrag("aria stays")

	pass("dialog", "/dialog", "- tapOn: OK\n- assertVisible: dialog OK\n")
	fail("dialog stays", "/dialog", "- tapOn: Settings\n", `"Settings" is under`)
	noDrag("dialog stays")

	if took := pass("glass", "/passthrough", "- tapOn: Save\n- assertVisible: first Save\n"); took > 3*time.Second {
		t.Errorf("glass: took %s: a pointer-events: none overlay was waited on", took)
	}
	pass("ghost", "/passthrough", "- tapOn: Send\n- assertVisible: clear Send\n")
}

func TestHitTestAnnotationsInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	page := func(body string) []byte {
		return []byte(`<!doctype html><html><body style="margin:0">` + body + `</body></html>`)
	}
	frameSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(page(`<button id="xbtn" style="width:200px;height:40px">X</button>
<div id="xcover" style="position:fixed;left:0;top:0;width:250px;height:60px;background:#ddd"></div>`))
	}))
	defer frameSrv.Close()
	frameURL := strings.Replace(frameSrv.URL, "127.0.0.1", "localhost", 1) + "/"
	abs := func(id string, l, t, w, h int) string {
		px := func(v int) string { return strconv.Itoa(v) + "px" }
		return `<button id="` + id + `" style="position:absolute;left:` + px(l) + `;top:` + px(t) + `;width:` + px(w) + `;height:` + px(h) + `">` + id + `</button>`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(page(abs("under", 20, 20, 200, 40) +
			`<div id="host"></div>` +
			`<iframe id="same" style="position:absolute;left:20px;top:200px;width:300px;height:150px;border:0" srcdoc="<body style='margin:0'><button id='inframe' style='width:200px;height:40px'>In frame</button><button id='clearframe' style='display:block;margin-top:60px;width:200px;height:40px'>Clear</button></body>"></iframe>` +
			`<div id="toast" style="position:fixed;left:0;top:215px;width:400px;height:60px;background:#eee"></div>` +
			abs("behind", 610, 50, 80, 30) +
			`<div id="wrap" style="position:fixed;inset:0;pointer-events:none"><button id="fab" style="pointer-events:auto;position:absolute;left:600px;top:20px;width:100px;height:100px">+</button></div>` +
			`<iframe id="cross" src="` + frameURL + `" style="position:absolute;left:800px;top:200px;width:300px;height:200px;border:0"></iframe>` +
			abs("plain", 20, 450, 200, 40) +
			`<div id="glass" style="position:fixed;inset:0;pointer-events:none;background:rgba(0,0,80,.05)"></div>` +
			`<dialog id="dlg">Saved <button id="close">Close</button></dialog>` +
			`<script>document.getElementById('host').attachShadow({mode:'open'}).innerHTML =
			  '<div id="shade" style="position:fixed;left:0;top:0;width:300px;height:100px;background:#ccc"></div>';</script>`))
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
	s := screenWith(t, d, ctx, "xbtn")
	get := func(s *ui.Screen, id string) *ui.Node {
		t.Helper()
		n := find(s, id)
		if n == nil {
			t.Fatalf("#%s not listed", id)
		}
		return n
	}
	cover := func(s *ui.Screen, id string) (*ui.Node, bool) {
		t.Helper()
		return occlusion.Covered(s, get(s, id))
	}
	if c, modal := cover(s, "under"); c == nil || c.ID != "shade" || !modal {
		t.Errorf("#under: cover %v modal %v, want the shadow root's #shade, modal (%+v)", c, modal, get(s, "under").Covers)
	}
	if c, modal := cover(s, "inframe"); c == nil || c.ID != "toast" || modal {
		t.Errorf("#inframe: cover %v modal %v, want the page's #toast, a bar (%+v)", c, modal, get(s, "inframe").Covers)
	}
	if c, _ := cover(s, "clearframe"); c != nil {
		t.Errorf("#clearframe: covered by %v", c)
	}
	if c, modal := cover(s, "behind"); c == nil || c.ID != "fab" || modal {
		t.Errorf("#behind: cover %v modal %v, want #fab, a bar (%+v)", c, modal, get(s, "behind").Covers)
	}
	if c, _ := cover(s, "xbtn"); c == nil || c.ID != "xcover" || c.Bounds.L != 800 || c.Bounds.T != 200 {
		t.Errorf("#xbtn: cover %v, want the frame's #xcover at (800, 200) (%+v)", c, get(s, "xbtn").Covers)
	}
	if n := get(s, "plain"); len(n.Covers) != 0 {
		t.Errorf("#plain under a pointer-events: none overlay: covers %+v", n.Covers)
	}
	if err := d.eval(ctx, "document.getElementById('dlg').showModal()", nil); err != nil {
		t.Fatal(err)
	}
	s = screenWith(t, d, ctx, "close")
	if c, modal := cover(s, "plain"); c == nil || c.ShortClass() != "dialog" || !modal {
		t.Errorf("#plain under a modal dialog: cover %v modal %v (%+v)", c, modal, get(s, "plain").Covers)
	}
	if c, _ := cover(s, "close"); c != nil {
		t.Errorf("the dialog's own button: covered by %v", c)
	}
}
