package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// JavaScript dialogs (issue #76). alert(), confirm(), prompt() and a beforeunload "Leave
// site?" stop the page's script until someone answers them: the input event that opened one
// does not return, and neither does any script, screen read or screenshot after it. tb
// answers each as it opens, the way a person clicking through would, and notes it for the
// run's trace (Notes):
//
//   - alert: OK;
//   - confirm: OK (confirm() returns true);
//   - prompt: OK with the dialog's own default text (what OK leaves in it);
//   - beforeunload: leave the page (tb navigates only when the flow asks it to).
//
// A flow can choose otherwise with answerDialogs (issue #91, AnswerDialogs below): Cancel
// for confirm() and prompt(), or a text for prompt(). The choice holds for every tab,
// background ones included; alert and beforeunload are answered as above whatever it is.
func (d *Driver) onDialog(params json.RawMessage, session string) {
	var e struct {
		Type          string `json:"type"`
		Message       string `json:"message"`
		DefaultPrompt string `json:"defaultPrompt"`
		URL           string `json:"url"`
	}
	json.Unmarshal(params, &e)
	d.mu.Lock()
	choice := d.dialogs // the flow's answerDialogs (issue #91), the same in every tab
	d.mu.Unlock()
	answer := map[string]any{"accept": true}
	var note string
	said := "OK" // how it was answered, for assertDialog
	switch {
	case e.Type == "alert":
		note = fmt.Sprintf("web: the page opened a JavaScript alert %q: tb answered OK", e.Message)
	case e.Type == "confirm" && choice.Cancel:
		answer["accept"] = false
		said = "Cancel"
		note = fmt.Sprintf("web: the page opened a JavaScript confirm %q: tb answered Cancel (confirm() returned false), as answerDialogs asked", e.Message)
	case e.Type == "confirm":
		note = fmt.Sprintf("web: the page opened a JavaScript confirm %q: tb answered OK (confirm() returned true)", e.Message)
	case e.Type == "prompt" && choice.Cancel:
		answer["accept"] = false
		said = "Cancel"
		note = fmt.Sprintf("web: the page opened a JavaScript prompt %q: tb answered Cancel (prompt() returned null), as answerDialogs asked", e.Message)
	case e.Type == "prompt" && choice.HasText:
		answer["promptText"] = choice.Text
		said = fmt.Sprintf("OK with %q", choice.Text)
		note = fmt.Sprintf("web: the page opened a JavaScript prompt %q: tb answered OK with %q, as answerDialogs asked", e.Message, choice.Text)
	case e.Type == "prompt":
		answer["promptText"] = e.DefaultPrompt
		said = fmt.Sprintf("OK with %q", e.DefaultPrompt)
		note = fmt.Sprintf("web: the page opened a JavaScript prompt %q: tb answered OK with its default text %q", e.Message, e.DefaultPrompt)
	case e.Type == "beforeunload":
		said = "left the page"
		note = "web: the page asked before it was left (a beforeunload dialog): tb left the page"
	default:
		note = fmt.Sprintf("web: the page opened a JavaScript %s dialog %q: tb accepted it", e.Type, e.Message)
	}
	// A tab tb does not drive (issue #78) is answered the same way; it does not change the page.
	bg := d.background(session)
	if bg {
		note = strings.Replace(note, "the page", "a background tab", 1)
	}
	d.note(note)
	// Logged as answered before Page.handleJavaScriptDialog below is sent: Answer is the answer
	// tb chose; a failure to send it is noted on its own.
	d.logDialog(driver.Dialog{Type: e.Type, Message: e.Message, Default: e.DefaultPrompt, URL: e.URL, Answer: said, Background: bg, Note: note})
	if !bg {
		d.changed("")
	}
	if d.page == nil {
		return
	}
	// Not on the read loop: the answer's reply arrives on it.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.page.callIn(ctx, session, "Page.handleJavaScriptDialog", answer, nil); err != nil {
			d.note(fmt.Sprintf("web: answering the JavaScript %s failed: %v", e.Type, err))
		}
	}()
}

var _ driver.DialogAnswerer = (*Driver)(nil)

// AnswerDialogs sets how the dialogs that open from now on are answered (answerDialogs,
// issue #91), in every tab; the zero answer is the default above.
func (d *Driver) AnswerDialogs(ctx context.Context, a driver.DialogAnswer) error {
	d.mu.Lock()
	d.dialogs = a
	d.mu.Unlock()
	return nil
}

// dialogLogMax is how many answered dialogs the driver keeps for assertDialog.
const dialogLogMax = 100

func (d *Driver) logDialog(dl driver.Dialog) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dialogLog = append(d.dialogLog, dl)
	if over := len(d.dialogLog) - dialogLogMax; over > 0 {
		d.dialogLog = append([]driver.Dialog(nil), d.dialogLog[over:]...)
	}
	d.dialogN++
}

var _ driver.DialogLog = (*Driver)(nil)

// Dialogs lists the JavaScript dialogs answered since mark (assertDialog, issue #91), in every
// tab and frame, oldest first, and the mark for the next call (driver.DialogLog). Unlike Notes,
// it hands nothing out: each caller keeps its own mark.
func (d *Driver) Dialogs(since int) ([]driver.Dialog, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	first := d.dialogN - len(d.dialogLog) // the mark of dialogLog[0]
	if since < first {
		since = first
	}
	if since >= d.dialogN {
		return nil, d.dialogN
	}
	return append([]driver.Dialog(nil), d.dialogLog[since-first:]...), d.dialogN
}

func (d *Driver) note(s string) {
	d.mu.Lock()
	d.notes = append(d.notes, s)
	d.mu.Unlock()
}

// Notes hands out what the driver did on its own since the last call (driver.Noter).
func (d *Driver) Notes() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.notes
	d.notes = nil
	return n
}
