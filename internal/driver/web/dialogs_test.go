package web

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// A flow's answerDialogs (issue #91) is how confirm() and prompt() are answered from then on;
// alert and beforeunload are answered as before, and the zero answer is the default again.
func TestDialogAnswerChosen(t *testing.T) {
	page := newFakeCDP(t)
	d := newWebDriver(t, page, nil)
	for _, c := range []struct {
		choice              driver.DialogAnswer
		typ, msg, def, note string
		want                map[string]any
	}{
		{driver.DialogAnswer{Cancel: true}, "confirm", "Delete it?", "", `confirm "Delete it?": tb answered Cancel (confirm() returned false)`, map[string]any{"accept": false}},
		{driver.DialogAnswer{Cancel: true}, "prompt", "Your name?", "Ada", `prompt "Your name?": tb answered Cancel (prompt() returned null)`, map[string]any{"accept": false}},
		{driver.DialogAnswer{Cancel: true}, "alert", "Saved!", "", `alert "Saved!": tb answered OK`, map[string]any{"accept": true}},
		{driver.DialogAnswer{Cancel: true}, "beforeunload", "", "", "tb left the page", map[string]any{"accept": true}},
		{driver.DialogAnswer{Text: "Grace", HasText: true}, "prompt", "Your name?", "Ada", `prompt "Your name?": tb answered OK with "Grace"`, map[string]any{"accept": true, "promptText": "Grace"}},
		{driver.DialogAnswer{Text: "Grace", HasText: true}, "confirm", "Delete it?", "", `tb answered OK (confirm() returned true)`, map[string]any{"accept": true}},
		{driver.DialogAnswer{HasText: true}, "prompt", "Your name?", "Ada", `tb answered OK with ""`, map[string]any{"accept": true, "promptText": ""}},
		{driver.DialogAnswer{}, "prompt", "Your name?", "Ada", `tb answered OK with its default text "Ada"`, map[string]any{"accept": true, "promptText": "Ada"}},
	} {
		page.reset()
		if err := d.AnswerDialogs(context.Background(), c.choice); err != nil {
			t.Fatal(err)
		}
		page.event("Page.javascriptDialogOpening", map[string]any{"type": c.typ, "message": c.msg, "defaultPrompt": c.def, "url": "http://x/"}, "")
		eventually(t, c.typ+" answered", func() bool { return len(page.calls("Page.handleJavaScriptDialog")) == 1 })
		if got := page.calls("Page.handleJavaScriptDialog")[0].params(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v, %s: answered %v, want %v", c.choice, c.typ, got, c.want)
		}
		if notes := d.Notes(); len(notes) != 1 || !strings.Contains(notes[0], c.note) {
			t.Errorf("%+v, %s: notes %q, want one with %q", c.choice, c.typ, notes, c.note)
		}
	}
}
