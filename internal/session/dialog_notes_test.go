package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// webDialogFake is a browser that keeps the JavaScript dialogs it answered (driver.DialogLog),
// as the web driver does: tapping "Delete" opens a confirm. It notes other things it does on
// its own too (driver.Noter), which are for a run's trace only.
type webDialogFake struct {
	*fakeDriver
	nmu   sync.Mutex
	log   []driver.Dialog
	notes []string
}

func (f *webDialogFake) dialog(typ, msg string) {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	note := "web: the page opened a JavaScript " + typ + " \"" + msg + "\": tb answered OK"
	f.log = append(f.log, driver.Dialog{Type: typ, Message: msg, Answer: "OK", Note: note})
	f.notes = append(f.notes, note)
}

func (f *webDialogFake) Dialogs(since int) ([]driver.Dialog, int) {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	if since >= len(f.log) {
		return nil, len(f.log)
	}
	return append([]driver.Dialog(nil), f.log[since:]...), len(f.log)
}

func (f *webDialogFake) Notes() []string {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	n := f.notes
	f.notes = nil
	return n
}

func (f *webDialogFake) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	f.nmu.Lock()
	f.notes = append(f.notes, "web: the tap opened a new tab")
	f.nmu.Unlock()
	if y < 150 {
		f.dialog("confirm", "Delete it?")
	}
	return f.fakeDriver.Tap(ctx, x, y, o)
}

// openWith opens a session on drv the way tb open does (Service.connect).
func openWith(t *testing.T, s *Service, info device.Info, drv driver.Driver) {
	t.Helper()
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) { return drv, nil }
	if _, err := s.connect(context.Background(), info, ""); err != nil {
		t.Fatal(err)
	}
}

// issue #91: the line tb writes for each JavaScript dialog it answers went only to a run's
// trace, which tb do and tb look have none of, so an agent driving a page never learnt that a
// dialog had opened, or what it said. do prints it under the step it opened in (and in
// --json), and look prints, after the listing, those since the last command (and in --json).
// Dialogs from before the session opened are not printed, and nor are the driver's other notes.
func TestDoAndLookShowDialogNotes(t *testing.T) {
	s := New()
	f := &webDialogFake{fakeDriver: newFake("chrome", "Chrome", device.Web)}
	f.show(button("Delete", 100), label("Items", 200))
	f.dialog("alert", "Before the session")
	openWith(t, s, f.info, f)

	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Delete\n- assertVisible: Items\n", "json": true}, "")
	if !resp.OK {
		t.Fatalf("resp %+v", resp)
	}
	var res struct {
		Steps []struct {
			Notes []string `json:"notes"`
		} `json:"steps"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.JSON, &res); err != nil {
		t.Fatal(err)
	}
	want := `web: the page opened a JavaScript confirm "Delete it?": tb answered OK`
	if len(res.Steps) != 2 || len(res.Steps[0].Notes) != 1 || res.Steps[0].Notes[0] != want ||
		len(res.Steps[1].Notes) != 0 || len(res.Notes) != 0 {
		t.Errorf("--json notes: %+v", res)
	}
	resp, _ = call(t, s.do, map[string]any{"commands": "- tapOn: Delete\n"}, "")
	lines := strings.Split(resp.Text, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], `ok Tap on "Delete"`) || lines[1] != "  note: "+want {
		t.Errorf("do does not show the dialog under its step:\n%s", resp.Text)
	}
	for _, other := range []string{"Before the session", "new tab"} {
		if strings.Contains(resp.Text, other) {
			t.Errorf("do shows %q:\n%s", other, resp.Text)
		}
	}

	// A dialog that opens between commands (a timer) is shown by the next look, after the
	// listing, once.
	f.dialog("alert", "Saved!")
	resp, _ = call(t, s.look, nil, "")
	if !strings.HasPrefix(resp.Text, "web 400x800") || !strings.HasSuffix(resp.Text, "\nnote: web: the page opened a JavaScript alert \"Saved!\": tb answered OK\n") {
		t.Errorf("look does not show the dialog after the listing:\n%s", resp.Text)
	}
	resp, _ = call(t, s.look, nil, "")
	if strings.Contains(resp.Text, "note:") {
		t.Errorf("look shows a note twice:\n%s", resp.Text)
	}
	// look --json has them too, next to the listing's fields.
	f.dialog("prompt", "Name?")
	resp, _ = call(t, s.look, map[string]any{"json": true}, "")
	var l struct {
		Platform string   `json:"platform"`
		Notes    []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.JSON, &l); err != nil {
		t.Fatal(err)
	}
	if l.Platform != "web" || len(l.Notes) != 1 || !strings.Contains(l.Notes[0], `prompt "Name?"`) {
		t.Errorf("look --json: %s", resp.JSON)
	}
	resp, _ = call(t, s.look, map[string]any{"json": true}, "")
	if strings.Contains(string(resp.JSON), `"notes"`) {
		t.Errorf("look --json without new dialogs: %s", resp.JSON)
	}
}

// noterFake is a phone whose driver notes what it did on its own (driver.Noter), as the
// Android and iOS drivers do (a keyboard hidden, a launch retried): those are for a run's trace,
// and tb do and tb look print none of them.
type noterFake struct {
	*fakeDriver
	nmu   sync.Mutex
	notes []string
}

func (f *noterFake) Notes() []string {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	n := f.notes
	f.notes = nil
	return n
}

func (f *noterFake) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	f.nmu.Lock()
	f.notes = append(f.notes, "android: hid the keyboard first")
	f.nmu.Unlock()
	return f.fakeDriver.Tap(ctx, x, y, o)
}

func TestDoAndLookLeaveOtherNotes(t *testing.T) {
	s := New()
	f := &noterFake{fakeDriver: newFake("emulator-5554", "Pixel 7", device.Android)}
	f.show(button("Delete", 100))
	openWith(t, s, f.info, f)
	resp, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Delete\n", "json": true}, "")
	if strings.Contains(resp.Text, "note") || strings.Contains(string(resp.JSON), "notes") {
		t.Errorf("do shows the driver's notes:\n%s\n%s", resp.Text, resp.JSON)
	}
	f.nmu.Lock()
	f.notes = append(f.notes, "android: restarted the agent")
	f.nmu.Unlock()
	resp, _ = call(t, s.look, map[string]any{"json": true}, "")
	if strings.Contains(resp.Text, "note") || strings.Contains(string(resp.JSON), "notes") {
		t.Errorf("look shows the driver's notes:\n%s\n%s", resp.Text, resp.JSON)
	}
}
