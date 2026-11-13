package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Choosing the answer to a JavaScript dialog (issue #91): since #76, tb answers every dialog
// with OK (a prompt with its own default text), so a flow could not take the Cancel branch of
// a confirm() or type its own text into a prompt(). With answerDialogs (AnswerDialogs here)
// the page sees the answer the flow chose, in the driven tab and in a background tab alike,
// until the flow chooses again; the zero answer is the old default. Needs Chrome: runs with
// TB_BROWSER_TESTS=1.
func TestDialogAnswersInChrome(t *testing.T) {
	if os.Getenv("TB_BROWSER_TESTS") != "1" {
		t.Skip("needs Chrome: run with TB_BROWSER_TESTS=1")
	}
	t.Setenv("TB_WEB_HEADLESS", "1")
	btn := func(id, label, onclick string) string {
		return `<button id="` + id + `" style="display:block;width:220px;height:40px" onclick="` + onclick + `">` + label + `</button>`
	}
	pages := map[string]string{
		"/": btn("confirm", "Confirm", "this.textContent = confirm('Delete it?') ? 'Confirmed' : 'Cancelled'") +
			btn("prompt", "Prompt", "const v = prompt('Your name?', 'Ada'); this.textContent = v === null ? 'No name' : 'Name: [' + v + ']'") +
			// A confirm that opens in this tab once a popup has taken the flow's attention.
			btn("later", "Ask later", "setTimeout(() => { document.getElementById('bg').textContent = confirm('From behind?') ? 'bg OK' : 'bg Cancel' }, 300); window.open('/popup')") +
			`<div id="bg">bg waiting</div>
<script>window.addEventListener('beforeunload', e => { e.preventDefault(); e.returnValue = ''; });</script>`,
		"/popup": `<h1 id="popup">Popup</h1>` + btn("closepopup", "Close popup", "window.close()"),
		"/next":  `<h1 id="next">Next</h1>`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><body style="margin:0">` + pages[r.URL.Path] + `</body></html>`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	d, err := Open(ctx, device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	var da driver.DialogAnswerer = d // the engine's answerDialogs step finds it so
	answer := func(a driver.DialogAnswer) {
		t.Helper()
		if err := da.AnswerDialogs(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	var said []string
	saw := func(s string) bool {
		said = append(said, d.Notes()...)
		return strings.Contains(strings.Join(said, "\n"), s)
	}
	// text reads the screen until #id shows want (a flow's assertion retries the same way).
	text := func(id, want string) {
		t.Helper()
		var n *ui.Node
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			s, err := d.Screen(cctx)
			cancel()
			if err != nil {
				t.Fatalf("screen read waiting for #%s: %v (a JavaScript dialog left open blocks the page)", id, err)
			}
			if n = find(s, id); n != nil && n.Text == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("#%s is %+v, want %q", id, n, want)
			}
		}
	}
	tap := func(id string) {
		t.Helper()
		n := find(screenWith(t, d, ctx, id), id)
		if n == nil {
			t.Fatalf("no #%s", id)
		}
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		if _, err := d.Tap(cctx, n.Bounds.CX(), n.Bounds.CY(), driver.ActionOpts{}); err != nil {
			t.Fatalf("tap on #%s: %v", id, err)
		}
	}
	reset := func() {
		t.Helper()
		if err := d.LaunchApp(ctx, srv.URL+"/", driver.LaunchOpts{}); err != nil {
			t.Fatal(err)
		}
	}

	// Cancel: confirm() returns false, prompt() null.
	answer(driver.DialogAnswer{Cancel: true})
	tap("confirm")
	text("confirm", "Cancelled")
	tap("prompt")
	text("prompt", "No name")
	if !saw(`confirm "Delete it?": tb answered Cancel`) || !saw(`prompt "Your name?": tb answered Cancel`) {
		t.Errorf("the trace does not say the dialogs were cancelled: %q", said)
	}

	// The choice holds for a dialog in a tab the flow is not driving (issue #78).
	tap("later")
	screenWith(t, d, ctx, "popup")
	for deadline := time.Now().Add(5 * time.Second); !saw(`background tab opened a JavaScript confirm`); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no note of the background tab's confirm: %q", said)
		}
	}
	tap("closepopup")
	text("bg", "bg Cancel")
	if !saw(`confirm "From behind?": tb answered Cancel`) {
		t.Errorf("the background confirm was not cancelled: %q", said)
	}

	// Text for a prompt: OK with that text (confirm() gets OK).
	reset()
	answer(driver.DialogAnswer{Text: "Grace", HasText: true})
	tap("prompt")
	text("prompt", "Name: [Grace]")
	tap("confirm")
	text("confirm", "Confirmed")
	if !saw(`prompt "Your name?": tb answered OK with "Grace"`) {
		t.Errorf("the trace does not name the prompt's text: %q", said)
	}
	// An empty text is a text too.
	answer(driver.DialogAnswer{HasText: true})
	tap("prompt")
	text("prompt", "Name: []")

	// The zero answer is the default again: OK, with the prompt's own text.
	reset()
	answer(driver.DialogAnswer{})
	tap("prompt")
	text("prompt", "Name: [Ada]")
	tap("confirm")
	text("confirm", "Confirmed")

	// Whatever the choice, a beforeunload "Leave site?" is left (tb navigates only when the
	// flow asks it to).
	answer(driver.DialogAnswer{Cancel: true})
	if err := d.LaunchApp(ctx, srv.URL+"/next", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	if href, err := d.Foreground(ctx); err != nil || href != srv.URL+"/next" {
		t.Errorf("after leaving a beforeunload page with Cancel chosen: at %s (%v), want /next", href, err)
	}
}
