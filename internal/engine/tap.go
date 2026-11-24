package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

func (r *Runner) point(spec string, within ui.Rect, elementRelative bool) (int, int, error) {
	a, b, ok := strings.Cut(spec, ",")
	if !ok {
		return 0, 0, &invalidField{msg: fmt.Sprintf("invalid point %q", spec)}
	}
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if strings.HasSuffix(a, "%") || strings.HasSuffix(b, "%") {
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
	var tapped *ui.Node
	var tappedOn *ui.Screen
	var tappedSel *match.Selector
	var last driver.ActionResult
	var touchedAt time.Time
	settle := driver.ActionOpts{}
	if c.WaitToSettleTimeoutMs > 0 {
		settle.Timeout = time.Duration(c.WaitToSettleTimeoutMs) * time.Millisecond
	}
	held := r.held
	r.held = nil
	own, err := r.tapTimeout(c)
	if err != nil {
		return err
	}
	if c.Selector == nil {
		if r.unsettled > 0 {
			t := time.Now()
			r.Drv.WaitSettled(ctx, max(settle.Timeout, r.unsettled))
			r.unsettled = 0
			r.traceWait(t, "for the last action's change to settle")
		}
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
				n, s, away, err = r.atRest(ctx, sel, n, s, n.Bounds, n.Bounds, restSpanAfterAction)
				if err != nil {
					return err
				}
			}
		}
		if n == nil {
			if own >= 0 {
				n, s, away, err = r.findWithin(ctx, sel, own, settle.Timeout)
			} else {
				n, s, away, err = r.findUnderKeyboard(ctx, sel, r.lookupTimeout(c.Optional), settle.Timeout)
			}
		}
		if err != nil {
			return err
		}
		if away != "" {
			was, before := n, s
			if n, s, err = r.bringBack(ctx, c, sel, away, settle.Timeout, before, was); err != nil {
				return err
			}
			if !sameRecoveredTarget(was, before, n, s) {
				return failf("Element changed while recovering: %s", sel.Describe())
			}
		}
		if loadingCover(s, n) != nil {
			n, s = r.awaitUncovered(ctx, sel, n, s, r.lookupTimeout(c.Optional))
		}
		if cover, modal := occlusion.Covered(s, n); modal {
			wait := min(modalCoverWait, r.lookupTimeout(c.Optional))
			if occlusion.Veil(s, cover) || !occlusion.Lasting(s, n, cover) {
				wait = r.lookupTimeout(c.Optional)
			}
			if n, s, err = r.awaitModalGone(ctx, sel, n, s, wait); err != nil {
				return err
			}
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
	if c.RetriesTapIfNoChange() {
		attempts = 2
	}
	settleFor := settle.Timeout
	if settleFor == 0 {
		settleFor = 5 * time.Second
	}
	if !c.LongPress {
		settle.React = true
	}
	if d.Platform() == "android" && c.Name == "tapOn" && c.Point == "" && c.Selector != nil && !c.LongPress && c.Repeat <= 1 {
		settle.Aim = aimOf(tapped, tappedOn)
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
		if settle.Target == nil && c.Name == "tapOn" && c.Point == "" && c.Selector != nil && !c.LongPress && c.Repeat <= 1 &&
			tapped != nil && tapped.Class == "Button" && tapped.Enabled {
			b := tapped.Bounds
			settle.Watch = &b
		}
	}
	replays := 0
	for i := 0; i < attempts; i++ {
		r.invalidate()
		var res driver.ActionResult
		var err error
		switch {
		case c.LongPress:
			r.mark("longPress", [2]int{x, y})
			res, err = d.LongPress(ctx, x, y, 3*time.Second, settle)
		case c.Repeat > 1 && c.Name == "doubleTapOn":
			r.mark("doubleTap", [2]int{x, y})
			res, err = d.DoubleTap(ctx, x, y, settle)
		case c.Repeat > 1:
			for k := 0; k < c.Repeat && err == nil; k++ {
				o := settle
				if k < c.Repeat-1 {
					o.NoSettle = true
				}
				o.InRepeat = k > 0
				r.mark("tap", [2]int{x, y})
				res, err = d.Tap(ctx, x, y, o)
				if err == nil && k < c.Repeat-1 {
					if err = ctx.Err(); err == nil && c.Delay > 0 {
						select {
						case <-ctx.Done():
							err = ctx.Err()
						case <-time.After(time.Duration(c.Delay) * time.Millisecond):
						}
					}
				}
			}
		default:
			r.mark("tap", [2]int{x, y})
			touchedAt = time.Now()
			res, err = d.Tap(ctx, x, y, settle)
			if err == nil && res.NotInjected == "down" && !ci.On() {
				return failf("%s at (%d, %d): the platform refused the touch: no app saw it", tapWord(c), x, y)
			}
			if err == nil && res.NotInjected == "down" {
				r.tracef("tap at (%d, %d): the platform refused the touch: no app saw it; tapping again", x, y)
				r.invalidate()
				r.mark("tap", [2]int{x, y})
				res, err = d.Tap(ctx, x, y, settle)
			}
			if err == nil && res.NotInjected != "" {
				if res.NotInjected == "down" {
					return failf("%s at (%d, %d): the platform refused the touch twice: no app saw it", tapWord(c), x, y)
				}
				return failf("%s at (%d, %d): the touch's release was not delivered; the tap may or may not have taken effect", tapWord(c), x, y)
			}
		}
		if err != nil {
			r.tracef("%s at (%d, %d) failed: %v", tapWord(c), x, y, err)
			return err
		}
		r.tracef("%s at (%d, %d): %s", tapWord(c), x, y, actionDesc(res))
		if pc := res.PressCut; pc != nil {
			r.tracef("%s at (%d, %d): the app saw only %d ms of the %d ms press, after a %d ms stall in its down; a control that needs a longer press may have missed it",
				tapWord(c), x, y, pc.SeenMs, pc.PressMs, pc.StallMs)
		}
		withheldAgain, cancels := false, 0
		lost := func() bool {
			return res.Withheld == "" && !res.Cancelled && r.undelivered(ctx, c, res, tapped, tappedOn)
		}
		for {
			undelivered := false
			if res.Withheld == "" && !res.Cancelled {
				if undelivered = lost(); !undelivered {
					break
				}
			} else if settle.Target == nil {
				break
			}
			if res.Withheld != "" {
				if withheldAgain {
					if res.Withheld == "preflight" {
						return failf("tap on %s: %s, so the touch did not reach the app", tappedSel.Describe(), preflightDesc(res, tapped))
					}
					return failf("target still moving before tap: %s", tappedSel.Describe())
				}
				if res.Withheld == "moved" && !ci.On() {
					return failf("tap on %s: the target was moving when the touch went down, so the touch was withheld and did not reach the app", tappedSel.Describe())
				}
				withheldAgain = true
				r.tracef("resolving the target again")
			} else if res.Cancelled {
				if !ci.On() {
					return failf("tap on %s: the touch reached the app and was cancelled before it ended, so nothing took it as a tap", tappedSel.Describe())
				}
				if cancels == maxCancelledTouches {
					return failf("the app cancelled every touch on %s (%d): none was taken as a tap", tappedSel.Describe(), cancels+1)
				}
				cancels++
				r.tracef("tapping again")
			} else if undelivered {
				if replays >= maxTapReplays {
					r.tracef("the app never got the touch, and the tap was sent again %d times already: leaving it", replays)
					break
				}
				replays++
				r.tracef("the app never got the touch (SpringBoard reported it completed, the app went on without it): tapping again once the screen settles")
				r.unsettled = settleFor
			}
			r.invalidate()
			next, screen, dir, e := r.findUnderKeyboard(ctx, tappedSel, r.lookupTimeout(c.Optional), settle.Timeout)
			if e != nil {
				return e
			}
			if dir != "" {
				next, screen, e = r.bringBack(ctx, c, tappedSel, dir, settle.Timeout, screen, next)
				if e != nil {
					return e
				}
			}
			if !sameTapTarget(tapped, tappedOn, next, screen, settle.Target) {
				return failf("target changed before tap: %s", tappedSel.Describe())
			}
			x, y, e = r.clearAndPoint(ctx, tappedSel, screen, next)
			if e != nil {
				return e
			}
			if settle.Target != nil {
				replacement := identifiedAppTarget(next, screen)
				replacement.Moving = withheldAgain
				settle.Target = replacement
			}
			if settle.Watch != nil {
				b := next.Bounds
				settle.Watch = &b
			}
			r.invalidate()
			touchedAt = time.Now()
			if c.Name == "doubleTapOn" && c.Repeat > 1 {
				r.mark("doubleTap", [2]int{x, y})
				res, e = d.DoubleTap(ctx, x, y, settle)
			} else {
				r.mark("tap", [2]int{x, y})
				res, e = d.Tap(ctx, x, y, settle)
			}
			if e != nil {
				return e
			}
			r.tracef("%s at (%d, %d): %s", tapWord(c), x, y, actionDesc(res))
			tapped, tappedOn = next, screen
		}
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
		c.Point == "" && !c.LongPress && c.Repeat <= 1 && !stillUnderTouch(last, x, y) {
		if again, err := r.tapMovedTarget(ctx, tappedSel, tapped, tappedOn, x, y, last.ClicksBefore, settle); again || err != nil {
			return err
		}
	}
	if ci.On() && d.Platform() == "ios" && last.ChangedSinceRead && settle.Target != nil && last.Guarded && last.Probed && last.Clicked == nil &&
		!last.Cancelled && last.Withheld == "" && tapped != nil && c.Selector != nil && c.Point == "" &&
		!c.LongPress && c.Repeat <= 1 && !touchedAt.IsZero() {
		if err := r.tapShiftedButton(ctx, tappedSel, tapped, tappedOn, x, y, touchedAt, settle); err != nil {
			return err
		}
	}
	if ci.On() && d.Platform() == "ios" && isSwitch(tapped) && c.Selector != nil && c.Point == "" && !c.LongPress && c.Repeat <= 1 &&
		!c.RetriesTapIfNoChange() && !(last.Clicked != nil && *last.Clicked) &&
		!touchedAt.IsZero() && systemAlertOf(tappedOn, tapped) == nil {
		if err := r.confirmSwitchTap(ctx, tapped, tappedOn, x, y, touchedAt, settle); err != nil {
			return err
		}
	}
	if kind := swallowedTap(d.Platform(), c, last, tapped, tappedOn); kind != notSwallowed && replays == 0 {
		return r.retrySwallowedTap(ctx, kind, tappedSel, tapped, tappedOn, last, x, y, settle, r.lookupTimeout(c.Optional))
	}
	if alert := systemAlertOf(tappedOn, tapped); alert != nil && !c.LongPress && c.Repeat <= 1 {
		return r.confirmAlertTap(ctx, alert, tapped, x, y, settle)
	}
	return nil
}

const maxCancelledTouches = 2

func preflightDesc(res driver.ActionResult, tapped *ui.Node) string {
	why := res.WithheldWhy
	if why == "" {
		why = "the agent's check found no single enabled button there"
	}
	s := "withheld before the touch: " + why
	if res.LiveFrame != nil && (tapped == nil || *res.LiveFrame != tapped.Bounds) {
		s += " (its live frame " + res.LiveFrame.String()
		if tapped != nil {
			s += ", tb read it at " + tapped.Bounds.String()
		}
		s += ")"
	}
	return s
}

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

func (r *Runner) tapTimeout(c *flow.Command) (time.Duration, error) {
	if c.Timeout == "" || c.Selector == nil {
		return -1, nil
	}
	v, err := r.eval(c.Timeout)
	if err != nil {
		return 0, err
	}
	ms, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(v), "_", ""), 64)
	if err != nil || ms < 0 || math.IsNaN(ms) || math.IsInf(ms, 0) {
		return 0, &invalidField{msg: fmt.Sprintf("invalid timeout %q: a number of milliseconds, 0 or more", v)}
	}
	return time.Duration(ms * float64(time.Millisecond)), nil
}
