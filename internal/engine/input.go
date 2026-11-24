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
				r.tracef("typed with device verification%s: %s", typingDesc(res.Typing), actionDesc(res))
			}
			return err
		}
	}
	if r.Drv.Platform() == "android" && len([]rune(text)) > 1 {
		rest, err := r.typeFirstKey(ctx, text)
		if err != nil || rest == "" {
			return err
		}
		text = rest
	}
	target := r.inputTarget
	r.inputTarget = nil
	r.invalidate()
	res, err := r.Drv.TypeText(ctx, text, driver.ActionOpts{NoChangeMs: 300, Target: target})
	if err != nil {
		r.tracef("the device failed to type: %v", err)
	} else {
		r.tracef("typed: %s", actionDesc(res))
	}
	return err
}

func typingDesc(how string) string {
	switch how {
	case "acknowledged":
		return " (the field rewrites what is typed: keys went in one at a time, each once the field showed the one before)"
	case "repaired":
		return " (the field lost keys: they were typed again, one at a time, from the first one missing)"
	}
	return ""
}

func (r *Runner) typeFirstKey(ctx context.Context, text string) (string, error) {
	first := string([]rune(text)[0])
	rest := string([]rune(text)[1:])
	before, ok := r.focusedState(ctx)
	if !ok {
		return text, nil
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
	return rest, nil
}

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

func (r *Runner) awaitFieldChange(ctx context.Context, before string, timeout time.Duration) bool {
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	for {
		if t, ok := r.focusedState(ctx); !ok || t != before {
			return true
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return false
		}
		r.waitAny(ctx, events, r.Drv.Seq(), deadline)
	}
}

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
