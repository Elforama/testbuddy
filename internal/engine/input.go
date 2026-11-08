package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func (r *Runner) typeText(ctx context.Context, text string) error {
	if err := r.ensureRequestedInputTarget(ctx); err != nil {
		return err
	}
	// The focused field from the last read, when it is still current (no extra read: typing
	// should not wait for one).
	if s := r.LastScreen(); s != nil && s.Seq == r.Drv.Seq() {
		if f := focusedField(s); f != nil {
			r.tracef("typing %d characters into %s", len([]rune(text)), nodeDesc(f))
		} else {
			r.tracef("typing %d characters, but no field has focus (tap the field first)", len([]rune(text)))
		}
	} else {
		r.tracef("typing %d characters", len([]rune(text)))
	}
	r.invalidate()
	if typer, ok := r.Drv.(driver.CheckedTextTyper); ok && r.Drv.Platform() == "android" {
		target := r.inputTarget
		res, err := typer.TypeTextChecked(ctx, text, driver.ActionOpts{NoChangeMs: 300, Target: target})
		if errors.Is(err, driver.ErrTypingVerificationUnavailable) {
			r.tracef("the focused field is unavailable through the device focus API: using screen verification")
		} else {
			r.inputTarget = nil
			if err != nil {
				r.tracef("the device failed to type: %v", err)
			} else {
				r.tracef("typed with device first-key verification: %s", actionDesc(res))
			}
			return err
		}
	}
	if r.Drv.Platform() == "android" && len([]rune(text)) > 1 {
		// The first key alone, checked: a field that has just taken focus (a screen that
		// opens with its keyboard up) can drop a key that arrives before the keyboard is
		// connected to it. The rest follows once the field shows the first.
		rest, err := r.typeFirstKey(ctx, text)
		if err != nil || rest == "" {
			return err
		}
		text = rest
	}
	target := r.inputTarget
	r.inputTarget = nil
	// The fallback first-key guard read the field after that character. The rest
	// changes it again, even when a WebView/provider emits no accessibility event.
	r.invalidate()
	res, err := r.Drv.TypeText(ctx, text, driver.ActionOpts{NoChangeMs: 300, Target: target})
	if err != nil {
		r.tracef("the device failed to type: %v", err)
	} else {
		r.tracef("typed: %s", actionDesc(res))
	}
	return err
}

// typeFirstKey types text's first character and waits (on change events, up to 1 s) for the
// focused field's text to change; if it never does, the key was lost, and it is typed again
// once. It returns the rest of the text to type.
func (r *Runner) typeFirstKey(ctx context.Context, text string) (string, error) {
	first := string([]rune(text)[0])
	rest := string([]rune(text)[1:])
	before, ok := r.focusedState(ctx)
	if !ok {
		return text, nil // no field to watch: type as before
	}
	for attempt := 0; attempt < 2; attempt++ {
		r.invalidate()
		if _, err := r.Drv.TypeText(ctx, first, driver.ActionOpts{NoChangeMs: 300}); err != nil {
			return "", err
		}
		if r.awaitFieldChange(ctx, before, time.Second) {
			return rest, nil
		}
		if attempt == 0 {
			r.tracef("the first key did not reach the field (it was still connecting to the keyboard): typing it again")
		}
	}
	return rest, nil // still unchanged: a field that shows nothing (a hidden or formatted one)
}

// focusedState names the field with input focus and its text, read now: a key that lands
// changes it (its text, or which field has focus: a one-digit code box moves to the next).
func (r *Runner) focusedState(ctx context.Context) (string, bool) {
	r.invalidate()
	s, err := r.read(ctx)
	if err != nil {
		return "", false
	}
	f := focusedField(s)
	if f == nil || !f.Editable {
		return "", false
	}
	text := f.Text
	if f.ShowingHint {
		text = ""
	}
	return fmt.Sprintf("%s#%s@%v:%s", f.Class, f.ID, f.Bounds, text), true
}

// awaitFieldChange waits on change events until the focused field (or its text) is no longer
// before.
func (r *Runner) awaitFieldChange(ctx context.Context, before string, timeout time.Duration) bool {
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	for {
		if t, ok := r.focusedState(ctx); !ok || t != before {
			return true // a different field, its text, or no field at all: the key went somewhere
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return false
		}
		r.waitAny(ctx, events, r.Drv.Seq(), deadline)
	}
}

// focusedField is the field with input focus on s, if any.
func focusedField(s *ui.Screen) *ui.Node {
	for _, n := range s.Nodes {
		if n.Focused && n.Editable {
			return n
		}
	}
	for _, n := range s.Nodes {
		if n.Focused {
			return n
		}
	}
	return nil
}
