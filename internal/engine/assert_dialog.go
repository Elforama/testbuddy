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

func (r *Runner) markDialogs() {
	if dl, ok := r.Drv.(driver.DialogLog); ok {
		_, r.dialogMark = dl.Dialogs(int(^uint(0) >> 1))
	}
}

func actsForDialogs(c *flow.Command) bool {
	switch c.Name {
	case "waitForAnimationToEnd", "runScript", "evalScript", "addMedia":
		return false
	}
	return c.Mutating()
}

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
