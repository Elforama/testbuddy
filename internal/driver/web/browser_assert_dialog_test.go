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
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

func TestAssertDialogInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	btn := func(id, label, onclick string) string {
		return `<button id="` + id + `" style="display:block;width:220px;height:40px" onclick="` + onclick + `">` + label + `</button>`
	}
	page := func(body string) []byte {
		return []byte(`<!doctype html><html><body style="margin:0">` + body + `</body></html>`)
	}
	frameSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(page(btn("framed", "Framed", "this.textContent = confirm('From the frame?') ? 'Frame OK' : 'Frame Cancel'")))
	}))
	defer frameSrv.Close()
	frameURL := strings.Replace(frameSrv.URL, "127.0.0.1", "localhost", 1) + "/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(page(btn("confirm", "Confirm", "this.textContent = confirm('Delete it?') ? 'Confirmed' : 'Cancelled'") +
			btn("later", "Later", "setTimeout(() => alert('Saved later'), 500); this.textContent = 'Waiting'") +
			btn("plain", "Plain", "this.textContent = 'Clicked'") +
			`<iframe src="` + frameURL + `" width="300" height="80"></iframe>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	screenWith(t, d, ctx, "framed")
	if n := len(d.frameSessions()); n != 1 {
		t.Fatalf("%d cross-origin frame sessions, want 1: the frame's dialog would not be tested in its own process", n)
	}

	dir := t.TempDir()
	var trace strings.Builder
	run := func(name, steps string, lookup time.Duration) engine.FlowResult {
		t.Helper()
		f, err := flow.ParseBytes(filepath.Join(dir, name+".yaml"), []byte("url: "+srv.URL+"/\n---\n"+steps))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		trace.Reset()
		o := engine.Options{LookupTimeout: lookup, OptionalTimeout: lookup / 2, Trace: engine.NewTrace(&trace)}
		return engine.New(d, o).RunFlow(ctx, f)
	}
	pass := func(name, steps string) {
		t.Helper()
		if res := run(name, steps, 5*time.Second); res.Status != engine.Passed {
			t.Errorf("%s: %v\ntrace:\n%s", name, res.Err, trace.String())
		}
	}
	fail := func(name, steps, want string) {
		t.Helper()
		res := run(name, steps, 1500*time.Millisecond)
		if res.Status != engine.Failed || res.Err == nil || !strings.Contains(res.Err.Error(), want) {
			t.Errorf("%s: %s %v, want a failure with %q\ntrace:\n%s", name, res.Status, res.Err, want, trace.String())
		}
	}

	pass("confirm", `- tapOn: {id: confirm}
- assertVisible: Confirmed
- assertDialog: "Delete it?"
- assertDialog: {text: "Delete .*", type: confirm}
- assertDialog: {type: confirm}
- assertDialog: {}
`)
	pass("later", `- tapOn: {id: later}
- assertDialog: {text: Saved later, type: alert}
`)
	pass("frame", `- tapOn: {id: framed}
- assertVisible: Frame OK
- assertDialog: {text: "From the frame?", type: confirm}
`)
	pass("cancelled", `- answerDialogs: CANCEL
- tapOn: {id: framed}
- assertVisible: Frame Cancel
- assertDialog: From the frame?
`)
	if strings.Count(trace.String(), `confirm "From the frame?"`) != 1 {
		t.Errorf("the frame's confirm should be noted once:\n%s", trace.String())
	}

	fail("none", "- tapOn: {id: plain}\n- assertDialog: Delete it?\n", "no JavaScript dialog opened")
	fail("stale", "- tapOn: {id: confirm}\n- tapOn: {id: plain}\n- assertDialog: Delete it?\n", "no JavaScript dialog opened")
	fail("other text", "- tapOn: {id: confirm}\n- assertDialog: Delete everything?\n", `confirm "Delete it?"`)
	fail("other type", "- tapOn: {id: confirm}\n- assertDialog: {text: \"Delete it?\", type: alert}\n", `confirm "Delete it?"`)

	ds, _ := d.Dialogs(0)
	framed := 0
	for _, dl := range ds {
		if dl.Message == "From the frame?" {
			framed++
			if !strings.HasPrefix(dl.URL, frameURL) || dl.Type != "confirm" || dl.Background {
				t.Errorf("the frame's confirm was kept as %+v, want type confirm from %s, not in a background tab", dl, frameURL)
			}
		}
	}
	if framed != 2 {
		t.Errorf("%d of the frame's confirms kept, want 2 (one per flow that tapped it): %+v", framed, ds)
	}
}
