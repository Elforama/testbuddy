package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// maxUndeliveredReplays: how many more times a tap or double tap the app never got is sent.
const maxUndeliveredReplays = 2

// resendUndelivered sends again a tap or double tap whose touches the app never got (issue
// #334, driver.ActionResult.NotDelivered). The iOS agent says so only on proof: the system
// reported the touches completed, and the app went on after that without them (UIKit drops
// the touches an app gets while it presents a screen; a full-screen cover still sliding in
// took none). No app took them as a tap, so a second one cannot act twice. The screen is
// usually still changing (the presentation), which kept #280's replay from it (that one needs
// "nothing changed"): each replay waits for the screen to settle first, then finds the target
// again (a named one must still be the same button) and aims at where it is now. Sent again
// at most maxUndeliveredReplays times; still not delivered, it is left to the next step.
func (r *Runner) resendUndelivered(ctx context.Context, c *flow.Command, sel *match.Selector, tapped *ui.Node, on *ui.Screen,
	res driver.ActionResult, x, y int, o driver.ActionOpts, settleFor time.Duration) (driver.ActionResult, int, int, *ui.Node, *ui.Screen, error) {
	d := r.Drv
	for i := 1; res.NotDelivered; i++ {
		if i > maxUndeliveredReplays {
			r.tracef("the app never got the touch %d times: leaving it", i)
			break
		}
		r.tracef("the app never got the touch (the system reported it completed, the app went on without it): tapping again once the screen settles")
		r.invalidate()
		if sel != nil && tapped != nil {
			// findStable waits for the change to settle (unsettled), then for the target to rest.
			r.unsettled = settleFor
			next, screen, err := r.findStable(ctx, sel, r.lookupTimeout(c.Optional), o.Timeout)
			if err != nil {
				return res, x, y, tapped, on, err
			}
			if o.Target != nil {
				if t := identifiedAppTarget(next, screen); t == nil || t.ID != o.Target.ID || t.Window != o.Target.Window {
					return res, x, y, tapped, on, failf("target changed before tap: %s", sel.Describe())
				} else {
					o.Target = t
				}
			}
			if o.Watch != nil {
				b := next.Bounds
				o.Watch = &b
			}
			if c.Point != "" {
				p, err := r.eval(c.Point)
				if err != nil {
					return res, x, y, tapped, on, err
				}
				if x, y, err = r.point(p, next.Bounds, true); err != nil {
					return res, x, y, tapped, on, err
				}
			} else if x, y, err = r.clearAndPoint(ctx, sel, screen, next); err != nil {
				return res, x, y, tapped, on, err
			}
			tapped, on = next, screen
		}
		r.invalidate()
		var err error
		if c.Repeat > 1 {
			r.mark("doubleTap", [2]int{x, y})
			res, err = d.DoubleTap(ctx, x, y, o)
		} else {
			r.mark("tap", [2]int{x, y})
			res, err = d.Tap(ctx, x, y, o)
		}
		if err != nil {
			return res, x, y, tapped, on, err
		}
		r.tracef("%s at (%d, %d) again: %s", tapWord(c), x, y, actionDesc(res))
	}
	return res, x, y, tapped, on, nil
}

// inAppWindow: n is an element of an app window (not the keyboard's, nor a system one).
func inAppWindow(s *ui.Screen, n *ui.Node) bool {
	if s == nil || n == nil {
		return false
	}
	if n.Window >= 0 && n.Window < len(s.Windows) {
		w := s.Windows[n.Window]
		return w.Type != "keyboard" && w.Type != "system" && w.Package != "com.apple.springboard"
	}
	return true
}
