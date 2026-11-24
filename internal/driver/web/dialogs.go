package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func (d *Driver) onDialog(params json.RawMessage, session string) {
	var e struct {
		Type          string `json:"type"`
		Message       string `json:"message"`
		DefaultPrompt string `json:"defaultPrompt"`
		URL           string `json:"url"`
	}
	json.Unmarshal(params, &e)
	d.mu.Lock()
	choice := d.dialogs
	d.mu.Unlock()
	answer := map[string]any{"accept": true}
	var note string
	said := "OK"
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
	bg := d.background(session)
	if bg {
		note = strings.Replace(note, "the page", "a background tab", 1)
	}
	d.note(note)
	d.logDialog(driver.Dialog{Type: e.Type, Message: e.Message, Default: e.DefaultPrompt, URL: e.URL, Answer: said, Background: bg, Note: note})
	if !bg {
		d.changed("")
	}
	if d.page == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.page.callIn(ctx, session, "Page.handleJavaScriptDialog", answer, nil); err != nil {
			d.note(fmt.Sprintf("web: answering the JavaScript %s failed: %v", e.Type, err))
		}
	}()
}

var _ driver.DialogAnswerer = (*Driver)(nil)

func (d *Driver) AnswerDialogs(ctx context.Context, a driver.DialogAnswer) error {
	d.mu.Lock()
	d.dialogs = a
	d.mu.Unlock()
	return nil
}

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

func (d *Driver) Dialogs(since int) ([]driver.Dialog, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	first := d.dialogN - len(d.dialogLog)
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

func (d *Driver) Notes() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.notes
	d.notes = nil
	return n
}
