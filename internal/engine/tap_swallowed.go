package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// A tap that provably changed nothing may be sent again (issue #280). One rule on every
// platform: a touch can be swallowed before it reaches what it aimed at, and the app then shows
// nothing of it. Sending it again is safe only when nothing could have taken it as a tap, which
// noOpTap checks, again before each replay:
//   - the settle was NoChange, and no view reported a click in the reply;
//   - the agent's click count (Android, ClickCounter) has not moved since the tap: a view's
//     click can be reported after the reply;
//   - what the screen lists (screenSig) is exactly as the tap found it;
//   - the target is found again at the same bounds.
//
// That alone is not enough: a press that acts late and draws nothing (a request sent) passes
// every check, and tapping it again would act twice. So a platform signal must also say why the
// touch could have been lost (swallowedTap):
//   - the touch was the first input a window just added ever got (Android firstInWindow, a
//     window younger than freshWindowAge), or went to an app still on its way to the
//     foreground (iOS appActive: false, issue #290). Such a window may take no input yet: a
//     dialog's guard against early taps, an enter transition, Android's permission dialogs.
//     A window that already took input is never "first", the app's own window back after a
//     modal closed included: "the window changed since the last touch" would retry that one.
//     Sent again after swallowedGap, at most maxSwallowedReplays times.
//   - iOS: a button shows every touch it gets (UIKit highlights it, SwiftUI dims it), which the
//     device reports as a change. One that showed nothing was not reached: something tb cannot
//     see took the touch, such as a loading veil hidden from accessibility. Sent again each time
//     the screen changes (the veil going), until a tap gets a reaction or the lookup timeout
//     runs out.
//
// retryTapIfNoChange: false turns both off.

// swallowKind: why a tap that changed nothing may never have reached its target.
type swallowKind int

const (
	notSwallowed swallowKind = iota
	swallowedByHidden
	swallowedByNewWindow
)

const (
	// freshWindowAge: a window older than this when its first input came is not one coming in.
	freshWindowAge = 5 * time.Second
	// maxSwallowedReplays: how many more times a tap into a window coming in is sent.
	maxSwallowedReplays = 2
	// swallowedGap: the least time between a tap that did nothing and its replay.
	swallowedGap = 300 * time.Millisecond
)

// swallowedTap says whether, and why, the tap whose last reply is last may have been swallowed.
func swallowedTap(platform string, c *flow.Command, last driver.ActionResult, tapped *ui.Node, on *ui.Screen) swallowKind {
	if c.ForbidsTapReplay() || tapped == nil || c.Selector == nil || c.Point != "" || c.LongPress || c.Repeat > 1 ||
		last.Settle != driver.NoChange || (last.Clicked != nil && *last.Clicked) || last.NotInjected != "" ||
		last.Withheld != "" || last.Cancelled || systemAlertOf(on, tapped) != nil {
		return notSwallowed
	}
	switch {
	case last.FirstInWindow && last.WindowAge > 0 && last.WindowAge < freshWindowAge:
		return swallowedByNewWindow
	case last.AppEntering && !last.Targeted: // not when the probe saw the touch end on the target
		return swallowedByNewWindow
	case platform == "ios" && !last.Guarded && tapped.Class == "Button" && tapped.Enabled:
		return swallowedByHidden
	}
	return notSwallowed
}

// noOpTap holds what a tap found, to tell whether it still provably did nothing.
type noOpTap struct {
	sel          *match.Selector
	was          *ui.Node
	before       *ui.Screen
	sig          uint64
	clicksBefore *int64 // the agent's click count before the first tap (nil: none)
	settle       time.Duration
}

// holds re-reads the screen and the click count: true while the tap provably did nothing, the
// click count read last, just before a replay.
func (t *noOpTap) holds(ctx context.Context, r *Runner) bool {
	r.invalidate()
	s, err := r.read(ctx)
	if err != nil {
		return false
	}
	if screenSig(s) != t.sig {
		r.tracef("what the screen lists changed (%s): the tap may have landed, so not tapping again", screenDiff(t.before, s))
		return false
	}
	n, _, err := r.findStable(ctx, t.sel, 0, t.settle)
	if err != nil {
		r.tracef("it went (%v): the last tap may have landed after all", err)
		return false
	}
	if n.Bounds != t.was.Bounds {
		r.tracef("it moved (%s, was %s): the tap may have landed, so not tapping again", n.Bounds, t.was.Bounds)
		return false
	}
	if cc, ok := r.Drv.(driver.ClickCounter); ok && t.clicksBefore != nil {
		if now, err := cc.Clicks(ctx); err != nil || now != *t.clicksBefore {
			r.tracef("a view reported a click after the reply (or the count could not be read): not tapping again")
			return false
		}
	}
	return true
}

// retrySwallowedTap sends again, at (x, y), a tap that provably did nothing (see above).
func (r *Runner) retrySwallowedTap(ctx context.Context, kind swallowKind, sel *match.Selector, was *ui.Node, before *ui.Screen,
	first driver.ActionResult, x, y int, o driver.ActionOpts, timeout time.Duration) error {
	if before == nil || was == nil {
		return nil
	}
	t := &noOpTap{sel: sel, was: was, before: before, sig: screenSig(before), clicksBefore: first.ClicksBefore, settle: o.Timeout}
	if !t.holds(ctx, r) {
		return nil
	}
	again := func() (done bool, err error) {
		r.invalidate()
		r.mark("tap", [2]int{x, y})
		res, err := r.Drv.Tap(ctx, x, y, o)
		if err != nil {
			return true, err
		}
		r.tracef("tap at (%d, %d) again: %s", x, y, actionDesc(res))
		if res.Settle != driver.NoChange || (res.Clicked != nil && *res.Clicked) {
			r.unsettled = 5 * time.Second
			return true, nil
		}
		return res.NotInjected != "" || res.Withheld != "" || res.Cancelled, nil
	}
	if kind == swallowedByNewWindow {
		if first.AppEntering {
			r.tracef("nothing changed and the app was still coming to the foreground when the touch went: it may have taken no input yet; tapping again")
		} else {
			r.tracef("nothing changed, no view took a click, and the touch was the first input to a window %d ms old: it may have taken no input yet; tapping again", first.WindowAge.Milliseconds())
		}
		for i := 1; i <= maxSwallowedReplays; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(swallowedGap):
			}
			if !t.holds(ctx, r) {
				return nil
			}
			if done, err := again(); done || err != nil {
				return err
			}
		}
		r.tracef("the tap changed nothing %d times: leaving it", maxSwallowedReplays+1)
		return nil
	}
	r.tracef("nothing reacted to the tap (a button shows every touch) and nothing on screen changed: something hidden may have taken it; tapping again when the screen changes")
	start := time.Now()
	deadline := start.Add(timeout)
	events, stop := r.Drv.Events()
	defer stop()
	for tries := 1; ; tries++ {
		seq := r.Drv.Seq()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(swallowedGap):
		}
		r.waitAny(ctx, events, seq, deadline)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(deadline) {
			r.tracef("no tap reached it in %s (%d taps)", since(start), tries)
			return nil
		}
		if !t.holds(ctx, r) {
			return nil
		}
		if done, err := again(); done || err != nil {
			return err
		}
	}
}
