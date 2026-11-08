package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

func (r *Runner) point(spec string, within ui.Rect, elementRelative bool) (int, int, error) {
	a, b, ok := strings.Cut(spec, ",")
	if !ok {
		return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q", spec)}
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if strings.HasSuffix(a, "%") || strings.HasSuffix(b, "%") {
		// Fractions are fine ("12.5%"): Maestro takes them too.
		px, e1 := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(a, "%")), 64)
		py, e2 := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(b, "%")), 64)
		if e1 != nil || e2 != nil || px < 0 || px > 100 || py < 0 || py > 100 || math.IsNaN(px) || math.IsNaN(py) {
			return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q: percentages are 0-100", spec)}
		}
		return within.L + int(float64(within.W())*px/100), within.T + int(float64(within.H())*py/100), nil
	}
	x, e1 := strconv.Atoi(a)
	y, e2 := strconv.Atoi(b)
	if e1 != nil || e2 != nil || x < 0 || y < 0 {
		return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q", spec)}
	}
	if elementRelative {
		if x >= within.W() || y >= within.H() {
			return 0, 0, &invalidField{msg: fmt.Sprintf("point %q is outside the element", spec)}
		}
		return within.L + x, within.T + y, nil
	}
	return x, y, nil
}

func (r *Runner) tap(ctx context.Context, c *flow.Command) error {
	d := r.Drv
	previous := r.previousGuardedTap
	previousClick := r.previousClick
	r.previousGuardedTap = nil
	w, h := d.Size()
	var x, y int
	var tapped *ui.Node // the element tapped (nil: a point on the screen)
	var tappedOn *ui.Screen
	var tappedSel *match.Selector
	var last driver.ActionResult
	settle := driver.ActionOpts{}
	if c.WaitToSettleTimeoutMs > 0 {
		settle.Timeout = time.Duration(c.WaitToSettleTimeoutMs) * time.Millisecond
	}
	held := r.held // the last tap's cover, for this tap only
	r.held = nil
	if c.Selector == nil { // a point on the screen
		p, err := r.eval(c.Point)
		if err != nil {
			return err
		}
		if x, y, err = r.point(p, ui.Rect{R: w, B: h}, false); err != nil {
			return err
		}
	} else {
		sel, err := r.resolveSelector(c.Selector)
		if err != nil {
			return err
		}
		var n *ui.Node
		var s *ui.Screen
		var away string
		if previous != nil && c.Name == "tapOn" && c.Point == "" && c.Repeat <= 1 {
			n, s = r.unchangedGuardedButton(ctx, previous, sel)
			if n != nil && !previousClick {
				// The prior touch finished on its intended button, but the app didn't
				// report a control action (e.g. SwiftUI). Check the next button's own
				// motion without waiting for unrelated changes elsewhere on screen.
				n, s, away, err = r.atRest(ctx, sel, n, s, n.Bounds, n.Bounds, restSpanAfterAction)
				if err != nil {
					return err
				}
			}
		}
		if n == nil {
			n, s, away, err = r.findUnderKeyboard(ctx, sel, r.lookupTimeout(c.Optional), settle.Timeout)
		}
		if err != nil {
			return err
		}
		if away != "" {
			was, before := n, s
			if n, s, err = r.bringBack(ctx, c, sel, away, settle.Timeout); err != nil {
				return err
			}
			if !sameRecoveredTarget(was, before, n, s) {
				return failf("Element changed while recovering: %s", sel.Describe())
			}
		}
		if loadingCover(s, n) != nil {
			n, s = r.awaitUncovered(ctx, sel, n, s, r.lookupTimeout(c.Optional))
		}
		if !n.Enabled && sel.Enabled == nil {
			t := time.Now()
			r.tracef("it is disabled: waiting up to %s for it to turn on", disabledWait)
			n, s = r.awaitEnabled(ctx, sel, n, s, disabledWait)
			if n.Enabled {
				r.tracef("it turned on after %s", since(t))
			} else {
				r.tracef("still disabled after %s: tapping anyway", since(t))
			}
		}
		tapped, tappedOn, tappedSel = n, s, sel
		pointOn := func(n *ui.Node, s *ui.Screen) (int, int, error) {
			if c.Point != "" {
				p, err := r.eval(c.Point)
				if err != nil {
					return 0, 0, err
				}
				return r.point(p, n.Bounds, true)
			}
			return r.clearAndPoint(ctx, sel, s, n)
		}
		if x, y, err = pointOn(n, s); err != nil {
			return err
		}
		mounted, err := r.awaitMountedCover(ctx, held, s, n, x, y, min(heldCoverWait, r.lookupTimeout(c.Optional)))
		if err != nil {
			return err
		}
		if mounted || r.awaitHeldCover(ctx, held, s, n, x, y, min(heldCoverWait, r.lookupTimeout(c.Optional))) {
			// The last tap's cover is still drawn over it, out of the tree (cover.go): waited
			// for it to go. Find the target again where it is now.
			r.invalidate()
			if n2, s2, _, ferr := r.find(ctx, sel, time.Second); ferr == nil {
				n, s = n2, s2
				tapped, tappedOn = n, s
				if x, y, err = pointOn(n, s); err != nil {
					return err
				}
			}
		}
		if hc, ok := d.(hitChecker); ok {
			// The web: what the browser's own hit test reaches at the point. Something else
			// there (a veil, a dialog's backdrop, a toast) would take the tap: wait for it to go.
			if n2, x2, y2, err := r.awaitHitClear(ctx, hc, sel, n, x, y, c.Point == "", r.lookupTimeout(c.Optional)); err != nil {
				return err
			} else {
				n, x, y = n2, x2, y2
				tapped = n
			}
		}
		r.held = r.noteCover(ctx, tappedOn, tapped)
	}
	attempts := 1
	if c.RetryTapIfNoChange {
		attempts = 2
	}
	// The tap waits for the UI to start reacting (so the next command cannot act on the screen
	// it left), not for it to finish: see Runner.unsettled.
	settleFor := settle.Timeout
	if settleFor == 0 {
		settleFor = 5 * time.Second
	}
	if !c.LongPress {
		settle.React = true
	}
	if d.Platform() == "android" && c.Name == "tapOn" && c.Point == "" && c.Repeat <= 1 && tapped != nil && tapped.Editable {
		r.inputTarget = identifiedAppTarget(tapped, tappedOn)
	}
	if d.Platform() == "ios" {
		settle.Hold = iosTapHold(tapped, c.Point != "" || c.Selector == nil)
		if c.Name == "tapOn" && c.Point == "" && c.Repeat <= 1 && tappedSel != nil && tappedSel.ID != "" {
			if target := identifiedAppTarget(tapped, tappedOn); target != nil {
				if tapped.Editable {
					r.inputTarget = target
				}
				if tapped.Class == "Button" && tapped.Enabled {
					settle.Target = target
				}
			}
		}
	}
	for i := 0; i < attempts; i++ {
		r.invalidate()
		var res driver.ActionResult
		var err error
		switch {
		case c.LongPress:
			res, err = d.LongPress(ctx, x, y, 3*time.Second, settle)
		case c.Repeat > 1 && c.Name == "doubleTapOn":
			res, err = d.DoubleTap(ctx, x, y, settle)
		case c.Repeat > 1:
			for k := 0; k < c.Repeat && err == nil; k++ {
				o := settle
				if k < c.Repeat-1 {
					o.NoSettle = true
				}
				res, err = d.Tap(ctx, x, y, o)
				if k < c.Repeat-1 && c.Delay > 0 {
					time.Sleep(time.Duration(c.Delay) * time.Millisecond) // the flow asked for this gap
				}
			}
		default:
			res, err = d.Tap(ctx, x, y, settle)
		}
		if err != nil {
			r.tracef("%s at (%d, %d) failed: %v", tapWord(c), x, y, err)
			return err
		}
		if res.Withheld != "" && settle.Target != nil {
			r.tracef("stale touch withheld at %s; resolving the target again", res.Withheld)
			r.invalidate()
			next, screen, dir, e := r.findUnderKeyboard(ctx, tappedSel, r.lookupTimeout(c.Optional), settle.Timeout)
			if e != nil {
				return e
			}
			if dir != "" {
				next, screen, e = r.bringBack(ctx, c, tappedSel, dir, settle.Timeout)
				if e != nil {
					return e
				}
			}
			replacement := identifiedAppTarget(next, screen)
			if replacement == nil || replacement.ID != settle.Target.ID || replacement.Window != settle.Target.Window || next.Class != tapped.Class || next.Label() != tapped.Label() {
				return failf("target changed before tap: %s", tappedSel.Describe())
			}
			x, y, e = r.clearAndPoint(ctx, tappedSel, screen, next)
			if e != nil {
				return e
			}
			settle.Target = replacement
			r.invalidate()
			res, e = d.Tap(ctx, x, y, settle)
			if e != nil {
				return e
			}
			if res.Withheld != "" {
				return failf("target still moving before tap: %s", tappedSel.Describe())
			}
			tapped, tappedOn = next, screen
		}
		r.tracef("%s at (%d, %d): %s", tapWord(c), x, y, actionDesc(res))
		last = res
		confirmed := res.Guarded && res.Clicked != nil && *res.Clicked
		if res.Probed && res.Guarded && (confirmed || res.Targeted) {
			r.previousGuardedTap = tappedOn
			r.previousClick = confirmed
		}
		if confirmed {
			r.tracef("the target reported its control action")
		}
		if res.Settle != driver.NoChange || confirmed {
			if settle.React {
				r.unsettled = settleFor
			}
			break
		}
		if i+1 < attempts {
			r.tracef("retrying: nothing changed (retryTapIfNoChange)")
		}
	}
	if last.Clicked != nil && !*last.Clicked && tapped != nil && tapped.Clickable && c.Selector != nil &&
		c.Point == "" && !c.LongPress && c.Repeat <= 1 {
		if again, err := r.tapMovedTarget(ctx, tappedSel, tapped, tappedOn, x, y, last.ClicksBefore, settle); again || err != nil {
			return err
		}
	}
	if d.Platform() == "ios" && !last.Guarded && last.Settle == driver.NoChange && tapped != nil && tapped.Class == "Button" &&
		tapped.Enabled && c.Point == "" && !c.LongPress && c.Repeat <= 1 && systemAlertOf(tappedOn, tapped) == nil {
		return r.retrySwallowedTap(ctx, tappedSel, settle, r.lookupTimeout(c.Optional), tappedOn)
	}
	if alert := systemAlertOf(tappedOn, tapped); alert != nil && !c.LongPress && c.Repeat <= 1 {
		return r.confirmAlertTap(ctx, alert, tapped, x, y, settle)
	}
	return nil
}

// tapWord names a tap command's gesture for the trace.
func tapWord(c *flow.Command) string {
	switch {
	case c.LongPress:
		return "long press"
	case c.Repeat > 1 && c.Name == "doubleTapOn":
		return "double tap"
	case c.Repeat > 1:
		return fmt.Sprintf("%d taps", c.Repeat)
	}
	return "tap"
}

// iOS tap holds. Most taps are held 10 ms: React Native cancels a press when a scroll view
// near it (a carousel paging itself) moves during the touch, which a longer hold makes likelier
// (measured: 1 lost tap in 30 at 20 ms, none in 576 at 10 ms). UIKit's switches and
// other tracking controls need the touch to span a display frame (a UISwitch toggles reliably
// from 17 ms), and system alerts' and sheets' buttons are UIKit: 20 ms for those, and for taps
// at a point, which may land on anything.
const iosShortHold, iosFrameHold = 10 * time.Millisecond, 20 * time.Millisecond

var iosFrameHoldTypes = map[string]bool{
	"Switch": true, "Toggle": true, "CheckBox": true, "RadioButton": true, "Slider": true,
	"Stepper": true, "SegmentedControl": true, "PickerWheel": true, "Picker": true, "DatePicker": true,
	"PopUpButton": true, "ComboBox": true, "MenuButton": true,
}

func iosTapHold(n *ui.Node, atPoint bool) time.Duration {
	if n == nil || atPoint || iosFrameHoldTypes[n.Class] {
		return iosFrameHold
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Class == "Alert" || p.Class == "Sheet" || iosFrameHoldTypes[p.Class] {
			return iosFrameHold
		}
	}
	return iosShortHold
}
