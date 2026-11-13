package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// answerDialogs runs an answerDialogs step (issue #91): the page's JavaScript confirm() and
// prompt() dialogs are answered as the flow chose from then on, by a driver that can (the web
// driver). The button and text are interpolated now.
func (r *Runner) answerDialogs(ctx context.Context, c *flow.Command) error {
	da, ok := r.Drv.(driver.DialogAnswerer)
	if !ok {
		return failf("answerDialogs works only in web flows (url:)")
	}
	ch := c.Dialogs
	if ch == nil {
		ch = &flow.DialogChoice{}
	}
	a := driver.DialogAnswer{Cancel: ch.Cancel, HasText: ch.HasText}
	if ch.Button != "" {
		b, err := r.eval(ch.Button)
		if err != nil {
			return err
		}
		cancel, ok := flow.DialogButton(b)
		if !ok {
			return failf("answerDialogs takes OK or CANCEL, got %q (from %s)", b, ch.Button)
		}
		if cancel && ch.HasText {
			return failf("answerDialogs with CANCEL types no text: drop text")
		}
		a.Cancel = cancel
	}
	if ch.HasText {
		t, err := r.eval(ch.Text)
		if err != nil {
			return err
		}
		a.Text = t
	}
	return da.AnswerDialogs(ctx, a)
}

// resetDialogs puts the default answer back, so a flow starts and ends with it (an agent
// session's commands keep their choice until they change it).
func (r *Runner) resetDialogs() {
	if da, ok := r.Drv.(driver.DialogAnswerer); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := da.AnswerDialogs(ctx, driver.DialogAnswer{}); err != nil {
			r.logLine("resetting the dialog answer: " + err.Error())
		}
	}
}

func describeDialogs(ch *flow.DialogChoice) string {
	switch {
	case ch == nil:
		return "Answer dialogs"
	case ch.Button != "":
		return "Answer dialogs with " + ch.Button
	case ch.Cancel:
		return "Answer dialogs with Cancel"
	case ch.HasText:
		return fmt.Sprintf("Answer dialogs with OK, prompt text %q", ch.Text)
	}
	return "Answer dialogs with OK"
}
