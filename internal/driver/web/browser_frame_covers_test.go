package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

func TestCrossOriginFrameUnderPageCoverInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	page := func(body string) []byte {
		return []byte(`<!doctype html><html><body style="margin:0">` + body + `</body></html>`)
	}
	frameSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(page(`<button style="margin:40px;width:200px;height:40px"
  onclick="document.getElementById('said').textContent = 'frame Continue'">Continue</button>
<button style="display:block;margin:0 40px;width:200px;height:40px"
  onclick="document.getElementById('said').textContent = 'frame Pay'">Pay</button>
<div id="said">Nothing in the frame</div>
<button style="display:block;margin:400px 40px 40px;width:200px;height:40px"
  onclick="parent.postMessage('frame Submit', '*')">Submit</button>`))
	}))
	defer frameSrv.Close()
	frameURL := strings.Replace(frameSrv.URL, "127.0.0.1", "localhost", 1) + "/"
	iframe := func(style string) string {
		return `<iframe src="` + frameURL + `" style="border:0;width:400px;height:240px;` + style + `"></iframe>`
	}
	say := `<div id="result" style="position:absolute;left:600px;top:20px">Nothing</div>
<script>function say(s) { document.getElementById('result').textContent = s; }
  window.addEventListener('message', (e) => say(e.data));</script>`
	pages := map[string]string{
		"/aria": `<div style="padding:60px 0 0 40px">` + iframe("") + `</div>
<div role="dialog" aria-modal="true" style="position:fixed;left:0;top:0;width:560px;height:400px;background:rgba(255,255,255,.9)">
  Before you pay <button style="position:absolute;left:300px;top:340px" onclick="say('dialog Continue')">Continue</button></div>` + say,
		"/dialog": iframe("margin:60px 0 0 40px") + `<dialog id="dlg" style="margin:420px auto 0 20px">Saved</dialog>` + say +
			`<script>document.getElementById('dlg').showModal();</script>`,
		"/glass": iframe("margin:60px 0 0 40px") + `<div style="position:fixed;inset:0;pointer-events:none;background:rgba(0,0,80,.1)"></div>` + say,
		"/long": iframe("display:block;margin:60px 0 0 40px") +
			`<div class="footer" style="height:2000px;background:#eee" onclick="say('footer')">Footer</div>` + say,
		"/longnested": `<iframe style="display:block;border:0;margin:60px 0 0 40px;width:480px;height:300px" srcdoc="<body style='margin:0'>` +
			strings.ReplaceAll(iframe("display:block"), `"`, `'`) + `</body>"></iframe>
<div class="footer" style="height:2000px;background:#eee" onclick="say('footer')">Footer</div>` + say,
		"/nested": `<iframe style="border:0;margin:60px 0 0 40px;width:480px;height:300px" srcdoc="<body style='margin:0'>` +
			strings.ReplaceAll(iframe(""), `"`, `'`) + `</body>"></iframe>
<div aria-modal="true" role="dialog" style="position:fixed;inset:0;background:rgba(0,0,0,.3)"></div>` + say,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write(page(body))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	dir := t.TempDir()
	var trace strings.Builder
	run := func(name, path, steps string) engine.FlowResult {
		t.Helper()
		f, err := flow.ParseBytes(filepath.Join(dir, name+".yaml"), []byte("url: "+srv.URL+path+
			"\n---\n- launchApp\n- extendedWaitUntil:\n    visible: Nothing in the frame\n    timeout: 10000\n"+steps))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		trace.Reset()
		o := engine.Options{LookupTimeout: 3 * time.Second, OptionalTimeout: 1500 * time.Millisecond, Trace: engine.NewTrace(&trace)}
		return engine.New(d, o).RunFlow(ctx, f)
	}
	pass := func(name, path, steps string) {
		t.Helper()
		if res := run(name, path, steps); res.Status != engine.Passed {
			t.Errorf("%s: %v\ntrace:\n%s", name, res.Err, trace.String())
		}
	}
	fail := func(name, path, steps, want string) {
		t.Helper()
		res := run(name, path, steps)
		if res.Status != engine.Failed || res.Err == nil || !strings.Contains(res.Err.Error(), want) {
			t.Errorf("%s: %s %v, want a failure with %q\ntrace:\n%s", name, res.Status, res.Err, want, trace.String())
		}
	}

	fail("aria", "/aria", "- tapOn: Pay\n", `"Pay" is under`)
	pass("aria rank", "/aria", "- tapOn: Continue\n- assertVisible: dialog Continue\n- assertVisible: Nothing in the frame\n")
	fail("dialog", "/dialog", "- tapOn: Pay\n", `"Pay" is under`)
	fail("nested", "/nested", "- tapOn: Pay\n", `"Pay" is under`)
	pass("glass", "/glass", "- tapOn: Pay\n- assertVisible: frame Pay\n")
	fail("long", "/long", "- tapOn: Submit\n", `not found: "Submit"`)
	if strings.Contains(trace.String(), "covered at") {
		t.Errorf("long: Submit, outside its frame, taken as covered:\n%s", trace.String())
	}
	pass("long scrolled", "/long", "- scrollUntilVisible:\n    element: Submit\n- tapOn: Submit\n- assertVisible: frame Submit\n")
	pageY := func() int {
		var y int
		d.eval(ctx, "Math.round(scrollY)", &y)
		return y
	}
	if y := pageY(); y != 0 {
		t.Errorf("long scrolled: the page scrolled to %d, not the frame", y)
	}
	pass("long nested scrolled", "/longnested", "- scrollUntilVisible:\n    element: Submit\n- assertVisible: Submit\n")
	if y := pageY(); y != 0 {
		t.Errorf("long nested scrolled: the page scrolled to %d, not the frame", y)
	}
	swipe := "- swipe:\n    start: 240, 280\n    end: 240, 100\n"
	pass("long swiped", "/long", swipe+swipe+"- tapOn: Submit\n- assertVisible: frame Submit\n")
	if y := pageY(); y != 0 {
		t.Errorf("long swiped: the page scrolled to %d, not the frame", y)
	}
	pass("long tapped point", "/long", "- tapOn:\n    point: 140, 200\n- assertVisible: frame Pay\n")
}
