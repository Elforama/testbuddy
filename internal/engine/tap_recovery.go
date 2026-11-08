package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// After a touch witnessed on its target, a fresh tree can show that the next
// button is still ready on the same screen. An unwitnessed touch takes the
// normal settle path so a pending overlay has time to appear.
func (r *Runner) unchangedGuardedButton(ctx context.Context, before *ui.Screen, sel *match.Selector) (*ui.Node, *ui.Screen) {
	if sel.ID == "" || sel.CSS != "" || sel.Index != "" || r.unsettled == 0 || time.Since(r.lastInteraction) > time.Second {
		return nil, nil
	}
	was, matches, err := match.Find(before, sel)
	if err != nil || len(matches) != 1 || was == nil || was.Class != "Button" || !was.Enabled {
		return nil, nil
	}
	r.invalidate()
	now, err := r.read(ctx)
	if err != nil || before.Keyboard || now.Keyboard || len(before.Nodes) != len(now.Nodes) || historyContext(before) != historyContext(now) {
		return nil, nil
	}
	// A replaced screen, a new overlay or a changed list must take the normal settle path.
	for i, n := range before.Nodes {
		other := now.Nodes[i]
		if n.Class != other.Class || n.ID != other.ID || n.Depth != other.Depth || n.Window != other.Window {
			return nil, nil
		}
	}
	n, matches, err := match.Find(now, sel)
	if err != nil || len(matches) != 1 || n == nil || !n.Enabled || was.ID != n.ID || was.Label() != n.Label() || was.Bounds != n.Bounds || identifiedAppTarget(n, now) == nil {
		return nil, nil
	}
	viewport := ui.Rect{R: now.Width, B: now.Height}
	if n.ClipRect(viewport).Intersect(n.Bounds) != n.Bounds || !sameRecoveredTarget(was, before, n, now) {
		return nil, nil
	}
	r.unsettled = 0
	r.tracef("%s was visible before the last tap and its live bounds still agree; using the guarded point", sel.Describe())
	return n, now
}

// Only uniquely identified app elements can be checked again by the iOS agent.
func identifiedAppTarget(n *ui.Node, s *ui.Screen) *driver.ActionTarget {
	if n == nil || n.ID == "" || s == nil || n.Window < 0 || n.Window >= len(s.Windows) {
		return nil
	}
	w := s.Windows[n.Window]
	if w.Type != "app" {
		return nil
	}
	for _, other := range w.Nodes {
		if other != n && other.ID == n.ID {
			return nil
		}
	}
	return &driver.ActionTarget{ID: n.ID, Window: w.ID, Seq: s.Seq, Bounds: n.Bounds}
}

// tapMovedTarget: the tap at (x, y) on was (read on before) reported no native
// click (Android) and was clickable. Some frameworks never emit that event, so
// state changes and declared outcomes must also guard against duplicate actions.
// A click can be reported after the
// tap's reply (the event comes once the view's handler has run), so the device's click count
// is read again just before a second tap: one click since clicksBefore, and there is none. If the same element (its type, id,
// size and window: only its place may change) is now somewhere else, it moved from under the
// tap as it went out (a banner pushing it down: a layout shift), so it is tapped again where
// it is, once it is still, scrolling it back if it left its list's view. Not when it is still
// under the point, gone with its list, or another element now
// matches (a dropdown's label showing the option just picked, which reports no click): then
// the tap went where it was meant to, and tapping again would act twice. again: it tapped.
func (r *Runner) tapMovedTarget(ctx context.Context, sel *match.Selector, was *ui.Node, before *ui.Screen, x, y int, clicksBefore *int64, o driver.ActionOpts) (again bool, err error) {
	cc, ok := r.Drv.(driver.ClickCounter)
	if !ok || clicksBefore == nil {
		return false, nil // no way to look again: a second tap could act twice
	}
	// A delayed click event already proves delivery. Check before expensive
	// motion recovery as well as immediately before any possible replay below.
	if now, err := cc.Clicks(ctx); err == nil && now != *clicksBefore {
		r.tracef("the view reported its click after the reply; skipping target recovery")
		return false, nil
	}
	r.invalidate()
	if was != nil && before != nil {
		if current, readErr := r.read(ctx); readErr == nil {
			if r.tapAssertionChanged(ctx, before, current) {
				return false, nil
			}
			if replacedAtPoint(before, current, x, y) {
				r.tracef("new content replaced the tapped control; leaving the result to the next flow step")
				return false, nil
			}
			// Compose can publish state/focus without TYPE_VIEW_CLICKED. A
			// unique, same-window target already showing the result needs no
			// recovery. A newly disabled control still covering the injected point
			// also reacted (e.g. Sign in -> loading). Do not chase it through the
			// navigation it starts. Displaced controls retain the motion checks.
			n, matches, matchErr := r.match(ctx, current, sel)
			if matchErr == nil && len(matches) == 1 && adjacentTapResultChanged(was, before, n, current) {
				r.tracef("the displaced control's adjacent result changed; leaving the outcome to the flow instead of replaying")
				return false, nil
			}
			if matchErr == nil && n == nil && historyContext(before) != historyContext(current) {
				r.tracef("the target disappeared on a different screen; leaving the result to the next flow step")
				return false, nil
			}
			if matchErr == nil && len(matches) == 1 && n != nil && was.ID != "" &&
				n.ID == was.ID && n.Class == was.Class &&
				was.Window >= 0 && was.Window < len(before.Windows) &&
				n.Window >= 0 && n.Window < len(current.Windows) {
				oldWindow, newWindow := before.Windows[was.Window], current.Windows[n.Window]
				if oldWindow.ID == newWindow.ID && oldWindow.Package == newWindow.Package && oldWindow.Type == newWindow.Type &&
					((was.Checkable && n.Checkable && n.Checked != was.Checked && n.Label() == was.Label()) ||
						(was.Editable && !was.Focused && n.Editable && n.Focused) ||
						(was.Enabled && !n.Enabled && n.Label() == was.Label() && n.Bounds.Contains(x, y) &&
							n.ClipRect(ui.Rect{R: current.Width, B: current.Height}).Contains(x, y))) {
					r.tracef("the target's state or focus confirms the tap; skipping motion recovery")
					return false, nil
				}
			}
		}
	}
	// This is recovery after an action, not a primary lookup. Once the
	// result settles, a removed button is left to the next flow assertion.
	// A target pushed outside its surviving list is still recovered below.
	n, s, away, err := r.findStableAway(ctx, sel, 0, o.Timeout)
	if r.tapAssertionChanged(ctx, before, s) {
		return false, nil
	}
	if adjacentTapResultChanged(was, before, n, s) {
		r.tracef("the displaced control's adjacent result changed during settling; skipping tap replay")
		return false, nil
	}
	if n == nil && s != nil && before != nil && historyContext(before) != historyContext(s) {
		r.tracef("the target disappeared after navigation settled; leaving the result to the next flow step")
		return false, nil
	}
	// An onClick can replace its own view before Android reports TYPE_VIEW_CLICKED.
	// A newly visible control at that point makes a replay ambiguous even when
	// the original footer is still visible farther down the list.
	if s != nil && before != nil && replacedAtPoint(before, s, x, y) {
		r.tracef("new content replaced the tapped control at (%d, %d); leaving the result to the next flow step", x, y)
		return false, nil
	}
	if err != nil {
		var nf *NotFound
		if !errors.As(err, &nf) || s == nil || before == nil {
			return false, nil
		}
		// The banner can push the target entirely out of view between injection and
		// this read. Its original list must still exist before we try scrolling it back.
		away = awayDir(before, was, was.Bounds, s)
		if away == "" {
			return false, nil
		}
	}
	broughtBack := away != ""
	if broughtBack {
		if now, err := cc.Clicks(ctx); err != nil || now != *clicksBefore {
			return false, nil // a late click may have navigated: do not scroll or tap again
		}
		n, s, err = r.bringBack(ctx, &flow.Command{}, sel, away, o.Timeout)
		if err != nil {
			return false, err
		}
	}
	if (!broughtBack && n.Bounds.Contains(x, y)) || !sameElementMoved(was, before, n, s) {
		return false, nil
	}
	if now, err := cc.Clicks(ctx); err != nil || now != *clicksBefore {
		r.tracef("the target moved, but a view reported the click after the tap's reply (or the count is unknown): not tapping again")
		return false, nil
	}
	r.tracef("no view took the tap and the target moved from under it, to %s (a layout shift): tapping it where it is", n.Bounds)
	x2, y2, err := r.clearAndPoint(ctx, sel, s, n)
	if err != nil {
		return false, err
	}
	r.invalidate()
	res, err := r.Drv.Tap(ctx, x2, y2, o)
	if err != nil {
		return true, err
	}
	r.tracef("tap at (%d, %d) again: %s", x2, y2, actionDesc(res))
	if res.Settle != driver.NoChange {
		r.unsettled = 5 * time.Second
	}
	return true, nil
}

// replacedAtPoint recognizes a new action or loading indicator at the old tap
// point. A plain banner is not an action: it can still displace a missed tap.
func replacedAtPoint(before, now *ui.Screen, x, y int) bool {
	eligible := func(n *ui.Node, s *ui.Screen) bool {
		return !n.Scrollable && n.Bounds.Contains(x, y) && n.ClipRect(ui.Rect{R: s.Width, B: s.Height}).Contains(x, y) &&
			(n.Clickable || n.ShortClass() == "ProgressBar")
	}
	for _, n := range match.Visible(now) {
		if !eligible(n, now) {
			continue
		}
		existed := false
		for _, old := range before.Nodes {
			if eligible(old, before) && old.Class == n.Class && old.ID == n.ID && old.Label() == n.Label() {
				existed = true
				break
			}
		}
		if !existed {
			return true
		}
	}
	return false
}

// sameElementMoved: n (on s) is was (on before) in another place: the same type, id and size
// (within 2 px), in a window of the same type and app.
func sameElementMoved(was *ui.Node, before *ui.Screen, n *ui.Node, s *ui.Screen) bool {
	if was.Class != n.Class || was.ID != n.ID || abs(was.Bounds.W()-n.Bounds.W()) > 2 || abs(was.Bounds.H()-n.Bounds.H()) > 2 {
		return false
	}
	win := func(sc *ui.Screen, k *ui.Node) (string, string) {
		if sc == nil || k.Window < 0 || k.Window >= len(sc.Windows) {
			return "", ""
		}
		return sc.Windows[k.Window].Type, sc.Windows[k.Window].Package
	}
	t1, p1 := win(before, was)
	t2, p2 := win(s, n)
	return t1 == t2 && p1 == p2
}

// retrySwallowedTap: an iOS button shows every touch it gets (UIKit highlights it, SwiftUI
// dims it), which the device reports as a change. A tap on one that changed nothing at all
// may never have reached it: something tb cannot see took it, such as a loading veil hidden
// from accessibility. Then tap again once the screen changes (the veil going), until a tap
// gets a reaction or timeout runs out. A second tap is only made while what the screen lists
// (every element's text, value and place) is exactly as it was before the first: if anything
// changed the tap may have landed (a screen that never stops moving filters out its reaction),
// and tapping again would act twice.
func (r *Runner) retrySwallowedTap(ctx context.Context, sel *match.Selector, o driver.ActionOpts, timeout time.Duration, before *ui.Screen) error {
	if before == nil {
		return nil
	}
	sig := screenSig(before)
	unchanged := func() bool {
		r.invalidate()
		s, err := r.read(ctx)
		if err != nil {
			return false
		}
		if screenSig(s) != sig {
			r.tracef("what the screen lists changed (%s): the tap may have landed, so not tapping again", screenDiff(before, s))
			return false
		}
		return true
	}
	if !unchanged() {
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
		if !unchanged() {
			return nil
		}
		n, _, err := r.findStable(ctx, sel, 0, o.Timeout)
		if err != nil {
			r.tracef("it went (%v): the last tap may have landed after all", err)
			return nil
		}
		x, y := n.Bounds.CX(), n.Bounds.CY()
		r.invalidate()
		res, err := r.Drv.Tap(ctx, x, y, o)
		if err != nil {
			return err
		}
		r.tracef("tap at (%d, %d) again: %s", x, y, actionDesc(res))
		if res.Settle != driver.NoChange {
			r.unsettled = 5 * time.Second
			return nil
		}
	}
}

// screenDiff names the first element that differs between two reads, for the trace.
func screenDiff(a, b *ui.Screen) string {
	for i := 0; i < len(a.Nodes) && i < len(b.Nodes); i++ {
		if nodeSig(a.Nodes[i]) != nodeSig(b.Nodes[i]) {
			return fmt.Sprintf("%s became %s", nodeDesc(a.Nodes[i]), nodeDesc(b.Nodes[i]))
		}
	}
	return fmt.Sprintf("%d elements, then %d", len(a.Nodes), len(b.Nodes))
}

func nodeSig(n *ui.Node) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%v|%v|%v|%v", n.Class, n.ID, n.Text, n.Desc, n.Value, n.Bounds, n.Enabled, n.Checked, n.Selected)
}

// screenSig sums up what a screen lists: each element's kind, id, text, value, place and state.
func screenSig(s *ui.Screen) uint64 {
	h := fnv.New64a()
	for _, n := range s.Nodes {
		fmt.Fprintln(h, nodeSig(n))
	}
	return h.Sum64()
}

// swallowedGap: the least time between taps on a button that did not react.
const swallowedGap = 300 * time.Millisecond
