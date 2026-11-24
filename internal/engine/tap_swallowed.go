package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

type swallowKind int

const (
	notSwallowed swallowKind = iota
	swallowedByHidden
	swallowedByNewWindow
)

const (
	freshWindowAge      = 1500 * time.Millisecond
	maxSwallowedReplays = 2
	swallowedGap        = 300 * time.Millisecond
	stillRetapAfter     = time.Second
	maxStillRetaps      = 2
)

func swallowedTap(platform string, c *flow.Command, last driver.ActionResult, tapped *ui.Node, on *ui.Screen) swallowKind {
	if c.ForbidsTapReplay() || tapped == nil || c.Selector == nil || c.Point != "" || c.LongPress || c.Repeat > 1 ||
		last.Settle != driver.NoChange || (last.Clicked != nil && *last.Clicked) || last.NotInjected != "" ||
		last.Withheld != "" || last.Cancelled || systemAlertOf(on, tapped) != nil {
		return notSwallowed
	}
	switch {
	case last.FirstInWindow && last.WindowOver && last.WindowAge > 0 && last.WindowAge < freshWindowAge:
		return swallowedByNewWindow
	case last.AppEntering && !last.Targeted && (!last.Guarded || last.Touch == "none"):
		return swallowedByNewWindow
	case platform == "ios" && !last.Guarded && !last.Reached && tapped.Class == "Button" && tapped.Enabled:
		return swallowedByHidden
	}
	return notSwallowed
}

type noOpTap struct {
	sel          *match.Selector
	was          *ui.Node
	before       *ui.Screen
	sig          uint64
	clicksBefore *int64
	settle       time.Duration
}

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
		return res.NotInjected != "" || res.Withheld != "" || res.Cancelled || res.Reached, nil
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
	shows := showsEveryTouch(was)
	stillMax := 0
	if shows {
		stillMax = maxStillRetaps
		r.tracef("nothing reacted to the tap (a whole list row, whose cell shows every touch) and nothing on screen changed: something hidden may have taken it; tapping again when the screen changes, or once it has stayed still for %s (at most %d times)", stillRetapAfter, maxStillRetaps)
	} else {
		r.tracef("nothing reacted to the tap (a button shows every touch) and nothing on screen changed: something hidden may have taken it; tapping again when the screen changes")
	}
	start := time.Now()
	deadline := start.Add(timeout)
	events, stop := r.Drv.Events()
	defer stop()
	stillTaps := 0
	for tries := 1; ; tries++ {
		seq := r.Drv.Seq()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(swallowedGap):
		}
		until := deadline
		if stillTaps < stillMax {
			until = minTime(deadline, time.Now().Add(stillRetapAfter-swallowedGap))
		}
		r.waitAny(ctx, events, seq, until)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(deadline) {
			r.tracef("no tap reached it in %s (%d taps)", since(start), tries)
			if !shows {
				return nil
			}
			return failf("no tap reached %s: %d taps changed nothing and none went to its view (something drawn over it may take the touches)", sel.Describe(), tries)
		}
		if r.Drv.Seq() <= seq {
			stillTaps++
			r.tracef("the screen stayed still for %s: tapping again (%d of %d)", stillRetapAfter, stillTaps, stillMax)
		}
		if !t.holds(ctx, r) {
			return nil
		}
		if done, err := again(); done || err != nil {
			return err
		}
	}
}

func showsEveryTouch(n *ui.Node) bool {
	cell := n
	for cell != nil && cell.Class != "Cell" {
		cell = cell.Parent
	}
	if cell == nil || cell.Parent == nil || !cell.Parent.Scrollable {
		return false
	}
	a, b := n.Bounds, cell.Bounds
	if abs(a.L-b.L) > rowSlack || abs(a.T-b.T) > rowSlack || abs(a.R-b.R) > rowSlack || abs(a.B-b.B) > rowSlack {
		return false
	}
	only := true
	cell.Walk(func(d *ui.Node) bool {
		if d != cell && d != n && touchable("ios", d) {
			only = false
		}
		return only
	})
	return only
}

const rowSlack = 2
