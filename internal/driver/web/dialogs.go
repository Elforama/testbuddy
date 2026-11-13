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
	}
	json.Unmarshal(params, &e)
	d.mu.Lock()
	choice := d.dialogs // the flow's answerDialogs (issue #91), the same in every tab
	d.mu.Unlock()
	answer := map[string]any{"accept": true}
	var note string
	switch {
	case e.Type == "alert":
		note = fmt.Sprintf("web: the page opened a JavaScript alert %q: tb answered OK", e.Message)
	case e.Type == "confirm" && choice.Cancel:
		answer["accept"] = false
		note = fmt.Sprintf("web: the page opened a JavaScript confirm %q: tb answered Cancel (confirm() returned false), as answerDialogs asked", e.Message)
	case e.Type == "confirm":
		note = fmt.Sprintf("web: the page opened a JavaScript confirm %q: tb answered OK (confirm() returned true)", e.Message)
	case e.Type == "prompt" && choice.Cancel:
		answer["accept"] = false
		note = fmt.Sprintf("web: the page opened a JavaScript prompt %q: tb answered Cancel (prompt() returned null), as answerDialogs asked", e.Message)
	case e.Type == "prompt" && choice.HasText:
		answer["promptText"] = choice.Text
		note = fmt.Sprintf("web: the page opened a JavaScript prompt %q: tb answered OK with %q, as answerDialogs asked", e.Message, choice.Text)
	case e.Type == "prompt":
		answer["promptText"] = e.DefaultPrompt
		note = fmt.Sprintf("web: the page opened a JavaScript prompt %q: tb answered OK with its default text %q", e.Message, e.DefaultPrompt)
	case e.Type == "beforeunload":
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
