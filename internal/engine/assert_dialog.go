package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
)

// markDialogs starts a new window for assertDialog (issue #91): the dialogs that count are
// those opened since the last step that acted on the app started. A check (assertVisible,
// assertDialog itself) leaves the window where it is, so a flow can check the page and the
// dialog in either order, and several dialogs one after the other.
func (r *Runner) markDialogs() {
	if dl, ok := r.Drv.(driver.DialogLog); ok {
		_, r.dialogMark = dl.Dialogs(int(^uint(0) >> 1))
	}
}

// actsForDialogs reports whether c starts a new window for assertDialog: a command that acts
// on the app (flow.Command.Mutating), but not a wait for the screen to be still.
func actsForDialogs(c *flow.Command) bool {
	return c.Mutating() && c.Name != "waitForAnimationToEnd"
}

// assertDialog runs an assertDialog step (issue #91): a JavaScript dialog whose message and
// type are the step's opened in the window above, by the time the lookup timeout runs out
// (one that a timer opens after the tap counts too). tb answered it as it opened (dialogs.go
// in the web driver), so this checks what the driver kept, not the screen.
func (r *Runner) assertDialog(ctx context.Context, c *flow.Command) error {
	dl, ok := r.Drv.(driver.DialogLog)
	if !ok {
		return failf("assertDialog works only in web flows (url:)")
	}
	ch := c.DialogCheck
	if ch == nil {
		ch = &flow.DialogCheck{}
	}
	text := ch.Text
	if ch.HasText {
		var err error
		if text, err = r.eval(ch.Text); err != nil {
			return err
		}
	}
	matches := func(d driver.Dialog) bool {
		return (ch.Type == "" || d.Type == ch.Type) && (!ch.HasText || match.TextMatches(text, d.Message))
	}
	start := time.Now()
	deadline := start.Add(r.lookupTimeout(c.Optional))
	for {
		seen, _ := dl.Dialogs(r.dialogMark)
		for _, d := range seen {
			if matches(d) {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			want := wantedDialog(ch.Type, text, ch.HasText)
			if len(seen) == 0 {
				return failf("no JavaScript dialog opened since the last step that acted on the page (waited %s for a %s)", since(start), want)
			}
			var parts []string
			for _, d := range seen {
				parts = append(parts, dialogDesc(d))
			}
			return failf("no %s opened since the last step that acted on the page; these did: %s", want, strings.Join(parts, "; "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// wantedDialog names the dialog an assertDialog looks for: `confirm "Delete it?"`.
func wantedDialog(typ, text string, hasText bool) string {
	what := "JavaScript dialog"
	if typ != "" {
		what = "JavaScript " + typ
	}
	if hasText {
		what += fmt.Sprintf(" %q", text)
	}
	return what
}

// dialogDesc names a dialog the driver answered, for a failure: `confirm "Delete it?" (tb
// answered OK)`.
func dialogDesc(d driver.Dialog) string {
	s := fmt.Sprintf("%s %q (tb answered %s", d.Type, d.Message, d.Answer)
	if d.Background {
		s += ", in a background tab"
	}
	return s + ")"
}

func describeAssertDialog(ch *flow.DialogCheck) string {
	if ch == nil {
		ch = &flow.DialogCheck{}
	}
	return "Assert that a " + wantedDialog(ch.Type, ch.Text, ch.HasText) + " opened"
}
