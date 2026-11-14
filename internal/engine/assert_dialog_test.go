package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// dialogLogFake is a web-like device that keeps the JavaScript dialogs it answered
// (driver.DialogLog) and notes each (driver.Noter): tapping "Delete" opens a confirm, "Later"
// an alert a moment after the tap, "Plain" nothing.
type dialogLogFake struct {
	*fakeDriver
	lmu   sync.Mutex
	log   []driver.Dialog
	notes []string
}

func (d *dialogLogFake) open(dl driver.Dialog) {
	d.lmu.Lock()
	defer d.lmu.Unlock()
	d.log = append(d.log, dl)
	d.notes = append(d.notes, "web: the page opened a JavaScript "+dl.Type+" "+dl.Message)
}

func (d *dialogLogFake) Dialogs(since int) ([]driver.Dialog, int) {
	d.lmu.Lock()
	defer d.lmu.Unlock()
	if since >= len(d.log) {
		return nil, len(d.log)
	}
	return append([]driver.Dialog(nil), d.log[since:]...), len(d.log)
}

func (d *dialogLogFake) Notes() []string {
	d.lmu.Lock()
	defer d.lmu.Unlock()
	n := d.notes
	d.notes = nil
	return n
}

func newDialogLogFake() *dialogLogFake {
	d := &dialogLogFake{fakeDriver: &fakeDriver{plat: "web", screen: static(btn("Delete", "", 0), btn("Later", "", 100), btn("Plain", "", 200))}}
	d.onTap = func(x, y int) {
		switch {
		case y < 50:
			d.open(driver.Dialog{Type: "confirm", Message: "Delete it?", Answer: "OK"})
		case y < 150:
			go func() {
				time.Sleep(150 * time.Millisecond)
				d.open(driver.Dialog{Type: "alert", Message: "Saved later", Answer: "OK"})
			}()
		}
	}
	return d
}

// assertDialog (issue #91) checks the dialogs opened since the last step that acted on the app:
// by text (the format's text rule, interpolated) and type; a check in between does not move
// that window, an action does; one that opens late is waited for.
func TestAssertDialogStep(t *testing.T) {
	o := quick
	o.LookupTimeout = 600 * time.Millisecond
	o.Env = []flow.KV{{Key: "WHAT", Value: "it"}}
	run := func(src string) FlowResult { return runWith(t, newDialogLogFake(), o, src) }

	mustPass(t, run(`- tapOn: Delete
- assertVisible: Plain
- assertDialog: Delete it?
- assertDialog: {text: "Delete ${WHAT}\\?", type: confirm}
- assertDialog: {type: confirm}
- assertDialog
- tapOn: Later
- waitForAnimationToEnd
- assertDialog: {text: Saved later, type: alert}
`))
	mustFail(t, run("- tapOn: Plain\n- assertDialog: Delete it?\n"),
		`no JavaScript dialog opened since the last step that acted on the page (waited`)
	mustFail(t, run("- tapOn: Delete\n- tapOn: Plain\n- assertDialog\n"), "no JavaScript dialog opened")
	mustFail(t, run("- tapOn: Delete\n- assertDialog: {text: \"Delete it?\", type: alert}\n"),
		`no JavaScript alert "Delete it?" opened since the last step that acted on the page; these did: confirm "Delete it?" (tb answered OK)`)
	mustFail(t, run("- tapOn: Delete\n- assertDialog: Delete all\n"), `these did: confirm "Delete it?"`)

	// optional: a warning.
	res := run("- tapOn: Plain\n- assertDialog: {text: Hi, optional: true}\n")
	mustPass(t, res)
	if res.Steps[1].Status != Warned {
		t.Errorf("optional assertDialog without a dialog: %s, want warned", res.Steps[1].Status)
	}

	// A dialog from before the flow started does not count.
	d := newDialogLogFake()
	d.open(driver.Dialog{Type: "alert", Message: "Old"})
	mustFail(t, runWith(t, d, o, "- assertDialog: Old\n"), "no JavaScript dialog opened")
}

// A tb do session keeps the window between calls: a tap in one call, the check in the next.
// Its window starts when the runner is made (the session opens): a dialog from before then
// does not count.
func TestAssertDialogAcrossDoCalls(t *testing.T) {
	d := newDialogLogFake()
	d.open(driver.Dialog{Type: "confirm", Message: "Delete it?", Answer: "OK"})
	r := New(d, quick)
	do := func(src string) error {
		t.Helper()
		cmds, err := flow.ParseCommands(t.TempDir(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return r.RunCommands(context.Background(), cmds, "app", nil)
	}
	if err := do("- assertDialog: Delete it?\n"); err == nil || !strings.Contains(err.Error(), "no JavaScript dialog opened") {
		t.Errorf("a dialog from before the session counted: %v", err)
	}
	for _, src := range []string{"- tapOn: Delete\n", "- assertDialog: Delete it?\n"} {
		if err := do(src); err != nil {
			t.Fatalf("%q: %v", src, err)
		}
	}
}

// A script runs in tb's JavaScript engine, not the page: like a wait, it does not start a new
// window, so the dialog the tap before it opened still counts.
func TestAssertDialogAfterScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "s.js", "output.y = 2")
	mustPass(t, runSrc(t, newDialogLogFake(), quick, dir, `appId: app
---
- tapOn: Delete
- evalScript: ${output.x = 1}
- runScript: s.js
- assertDialog: Delete it?
`))
}

// Android and iOS keep no JavaScript dialogs: the step fails, or warns when optional.
func TestAssertDialogUnsupported(t *testing.T) {
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- assertDialog: Hi"),
		"assertDialog works only in web flows (url:)")
}

func TestDescribeAssertDialog(t *testing.T) {
	for src, want := range map[string]string{
		"- assertDialog: Delete it?\n":                 `Assert that a JavaScript dialog "Delete it?" opened`,
		"- assertDialog: {text: Saved, type: alert}\n": `Assert that a JavaScript alert "Saved" opened`,
		"- assertDialog\n":                             "Assert that a JavaScript dialog opened",
	} {
		cmds, err := flow.ParseCommands(t.TempDir(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if got := Describe(cmds[0]); got != want {
			t.Errorf("%q: %q, want %q", src, got, want)
		}
	}
}
