package web

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// The driver keeps each JavaScript dialog it answered, with what it said and how tb answered
// it (assertDialog, issue #91): Notes hands each note out once, for the trace, while Dialogs
// lists those since a mark, the same to every caller. A frame's dialog (its own session) is
// kept with the frame's address.
func TestDialogsKept(t *testing.T) {
	page := newFakeCDP(t)
	d := newWebDriver(t, page, nil)
	open := func(typ, msg, def, url, session string) {
		t.Helper()
		page.reset()
		page.event("Page.javascriptDialogOpening", map[string]any{"type": typ, "message": msg, "defaultPrompt": def, "url": url}, session)
		eventually(t, typ+" answered", func() bool { return len(page.calls("Page.handleJavaScriptDialog")) == 1 })
	}
	if ds, mark := d.Dialogs(0); len(ds) != 0 || mark != 0 {
		t.Fatalf("before any dialog: %+v, mark %d", ds, mark)
	}
	open("confirm", "Delete it?", "", "http://x/", "")
	_, mark := d.Dialogs(0)
	if err := d.AnswerDialogs(context.Background(), driver.DialogAnswer{Cancel: true}); err != nil {
		t.Fatal(err)
	}
	open("prompt", "Your name?", "Ada", "http://frame.test/", "S1")
	open("alert", "Saved!", "", "http://x/", "")
	d.AnswerDialogs(context.Background(), driver.DialogAnswer{Text: "Grace", HasText: true})
	open("prompt", "Again?", "Ada", "http://x/", "")
	d.AnswerDialogs(context.Background(), driver.DialogAnswer{})
	open("prompt", "Default?", "Ada", "http://x/", "")
	open("beforeunload", "", "", "http://x/", "")

	all := []driver.Dialog{
		{Type: "confirm", Message: "Delete it?", URL: "http://x/", Answer: "OK"},
		{Type: "prompt", Message: "Your name?", Default: "Ada", URL: "http://frame.test/", Answer: "Cancel"},
		{Type: "alert", Message: "Saved!", URL: "http://x/", Answer: "OK"},
		{Type: "prompt", Message: "Again?", Default: "Ada", URL: "http://x/", Answer: `OK with "Grace"`},
		{Type: "prompt", Message: "Default?", Default: "Ada", URL: "http://x/", Answer: `OK with "Ada"`},
		{Type: "beforeunload", URL: "http://x/", Answer: "left the page"},
	}
	ds, next := d.Dialogs(0)
	// Each keeps the line the trace got for it (tb do and tb look print it).
	notes := d.Notes()
	for i := range ds {
		if i >= len(notes) || ds[i].Note != notes[i] {
			t.Errorf("dialog %d's note %q, want the trace's %q", i, ds[i].Note, notes)
		}
		ds[i].Note = ""
	}
	if !reflect.DeepEqual(ds, all) || next != len(all) {
		t.Errorf("Dialogs(0) = %+v, %d\nwant %+v, %d", ds, next, all, len(all))
	}
	if ds, _ := d.Dialogs(mark); len(ds) != len(all)-1 || ds[0].Message != "Your name?" {
		t.Errorf("Dialogs since the first = %+v\nwant the last %d", ds, len(all)-1)
	}
	if ds, again := d.Dialogs(next); len(ds) != 0 || again != next {
		t.Errorf("Dialogs since the last = %+v, %d", ds, again)
	}
	// Reading the trace's notes hands nothing out of the log.
	if ds, _ := d.Dialogs(0); len(ds) != len(all) {
		t.Errorf("Dialogs after Notes: %d, want %d", len(ds), len(all))
	}
}

// Only the last dialogLogMax dialogs are kept; a mark from before them lists what is left.
func TestDialogsKeptBounded(t *testing.T) {
	page := newFakeCDP(t)
	d := newWebDriver(t, page, nil)
	for i := 0; i < dialogLogMax+5; i++ {
		d.logDialog(driver.Dialog{Type: "alert", Message: fmt.Sprint(i)})
	}
	ds, next := d.Dialogs(0)
	if len(ds) != dialogLogMax || next != dialogLogMax+5 || ds[0].Message != "5" {
		t.Errorf("%d kept from %q, mark %d", len(ds), ds[0].Message, next)
	}
	if ds, _ := d.Dialogs(next - 2); len(ds) != 2 || ds[1].Message != fmt.Sprint(dialogLogMax+4) {
		t.Errorf("the last two: %+v", ds)
	}
}
