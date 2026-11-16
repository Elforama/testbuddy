package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #334: a tap or double tap whose touches the app never got. testmanagerd, and the iOS
// agent before this, took any process's "touches completed" as the tap's, the system's too;
// UIKit drops the touches an app gets while it presents a screen, so a tap during a
// full-screen cover's presentation was reported done and did nothing. The presentation still
// moving made the settle "the screen changed", which kept #280's replay from it.
//
// The agent judges, from the app's own posts, whether it got the touches (Delivery in its
// Gestures.swift: "notDelivered" only on proof that the app ran on past them; a stalled app,
// which reports late, is "unknown"). Asking costs nothing on the tap itself: the runner asks
// after the tap's settle (driver.DeliveryChecker), and only for a tap that could have been lost
// unseen: no receipt from the probe that the touch reached the app, no control reporting its
// action, on an element of the app's own window (a point, a keyboard key or a system window's
// element may take touches the app never gets, as it should).
//
// In CI mode (a CI allowance, as #103's and #131's) such a tap is found again once the screen
// settles, with #131's checks (the same element, findUnderKeyboard, bringBack), and sent again
// the same way, at most maxTapReplays times; a replay withheld or cancelled is handled as a
// first touch would be. Elsewhere the trace says the touch may not have been delivered, and
// the tap stands.

// maxTapReplays: how many more times a tap the app never got is sent (issue #334).
const maxTapReplays = 2

// deliveryWait: how long the runner waits for the agent's delivery verdict, after the settle.
const deliveryWait = 2500 * time.Millisecond

// undelivered says whether the app never got the touches of the tap whose reply is res, asking
// the device (iOS) when the tap could have been lost unseen.
func (r *Runner) undelivered(ctx context.Context, c *flow.Command, res driver.ActionResult, tapped *ui.Node, on *ui.Screen) bool {
	dc, ok := r.Drv.(driver.DeliveryChecker)
	if !ok || res.DeliveryToken == 0 || c.LongPress || (c.Repeat > 1 && c.Name != "doubleTapOn") || c.Point != "" ||
		res.Targeted || res.Reached || res.Touch == "ended" || res.Touch == "cancelled" || (res.Clicked != nil && *res.Clicked) ||
		res.NotInjected != "" || !inAppWindow(on, tapped) {
		return false
	}
	t := time.Now()
	v, err := dc.Delivery(ctx, res.DeliveryToken, deliveryWait)
	if err != nil {
		r.tracef("asking whether the app got the touch failed: %v", err)
		return false
	}
	if time.Since(t) >= 50*time.Millisecond {
		r.traceWait(t, "for the app's word on the touch")
	}
	return v == driver.NotDelivered
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

// sameTapTarget: next, found again on now, is the element was on before (issue #131's checks,
// and #334's): a named target the same id in the same window; any the same class and label,
// in the same window, at the same place in the tree (sameElement: not a twin with the same
// text on a screen presented since), of the app's own window.
func sameTapTarget(was *ui.Node, before *ui.Screen, next *ui.Node, now *ui.Screen, target *driver.ActionTarget) bool {
	if was == nil || next == nil || next.Class != was.Class || next.Label() != was.Label() {
		return false
	}
	if target != nil {
		t := identifiedAppTarget(next, now)
		return t != nil && t.ID == target.ID && t.Window == target.Window
	}
	if !inAppWindow(now, next) || !sameElement(was, next) {
		return false
	}
	if before != nil && was.Window >= 0 && was.Window < len(before.Windows) && next.Window >= 0 && next.Window < len(now.Windows) {
		a, b := before.Windows[was.Window], now.Windows[next.Window]
		return a.ID == b.ID && a.Type == b.Type && a.Package == b.Package
	}
	return true
}
