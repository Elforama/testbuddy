package web

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
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
func (d *Driver) onDialog(params json.RawMessage, session string) {
	var e struct {
		Type          string `json:"type"`
		Message       string `json:"message"`
		DefaultPrompt string `json:"defaultPrompt"`
	}
	json.Unmarshal(params, &e)
	answer := map[string]any{"accept": true}
	var note string
	switch e.Type {
	case "alert":
		note = fmt.Sprintf("web: the page opened a JavaScript alert %q: tb answered OK", e.Message)
	case "confirm":
		note = fmt.Sprintf("web: the page opened a JavaScript confirm %q: tb answered OK (confirm() returned true)", e.Message)
	case "prompt":
		answer["promptText"] = e.DefaultPrompt
		note = fmt.Sprintf("web: the page opened a JavaScript prompt %q: tb answered OK with its default text %q", e.Message, e.DefaultPrompt)
	case "beforeunload":
		note = "web: the page asked before it was left (a beforeunload dialog): tb left the page"
	default:
		note = fmt.Sprintf("web: the page opened a JavaScript %s dialog %q: tb accepted it", e.Type, e.Message)
	}
	d.note(note)
	d.changed("")
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
