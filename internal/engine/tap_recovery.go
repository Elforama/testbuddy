package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
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
	// A count that cannot be read, or that moved, may be a late click: never tap twice then.
	clickedSince := func() bool {
		now, err := cc.Clicks(ctx)
		return err != nil || now != *clicksBefore
	}
	return r.recoverMovedTarget(ctx, sel, was, before, x, y, clickedSince, o,
		"no view took the tap and the target moved from under it")
}

// recoverMovedTarget: the tap at (x, y) on was (read on before) has no proof that it took. When
// the same element is now at rest elsewhere, off the point, and nothing shows the tap took
// effect (the next assertion's outcome, the result beside it, new content at the point, a new
// screen, its own state or focus), it is tapped once more where it is. clickedSince says whether
// a click may have been reported since the tap (true: do not tap again); why starts the trace
// line before the second tap. again: it tapped.
func (r *Runner) recoverMovedTarget(ctx context.Context, sel *match.Selector, was *ui.Node, before *ui.Screen, x, y int, clickedSince func() bool, o driver.ActionOpts, why string) (again bool, err error) {
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
		if wentInPlace(before, was, s) {
			// Issue #141: an app behind on a slow runner takes the touch late. The row it
			// went to is no longer listed, but nothing else in its list moved or came: it
			// did not leave the list's view, the tap is taking effect (its screen opens next).
			// A row missing for a moment only (a rebind, a node the agent could not read) is
			// looked for once more after a short settle. One the app really removed (the last
			// row of a list deleted by the tap) stays gone: the tap passes, and the next step
			// judges what it did.
			r.Drv.WaitSettled(ctx, inPlaceSettle)
			r.invalidate()
			n2, s2, _, ferr := r.find(ctx, sel, 0)
			if ferr != nil {
				r.tracef("it is no longer listed, but nothing else in its list moved: the tap is taking effect, not scrolling to bring it back")
				return false, nil
			}
			r.tracef("it is listed again after a moment")
			n, s, away = n2, s2, ""
		}
	}
	broughtBack := away != ""
	if broughtBack {
		if clickedSince() {
			return false, nil // a late click may have navigated: do not scroll or tap again
		}
		n, s, err = r.bringBack(ctx, &flow.Command{}, sel, away, o.Timeout, before, was)
		if err != nil {
			var nf *NotFound
			if errors.As(err, &nf) && nf.left {
				// Issue #141: the screen the tap opened came while scrolling: the tap took.
				r.tracef("the screen changed while bringing it back: the tap took effect; leaving the result to the next flow step")
				return false, nil
			}
			return false, err
		}
	}
	if (!broughtBack && n.Bounds.Contains(x, y)) || !sameElementMoved(was, before, n, s) {
		return false, nil
	}
	if clickedSince() {
		r.tracef("the target moved, but a view reported the click after the tap's reply (or the count is unknown): not tapping again")
		return false, nil
	}
	r.tracef("%s, to %s (a layout shift): tapping it where it is", why, n.Bounds)
	x2, y2, err := r.clearAndPoint(ctx, sel, s, n)
	if err != nil {
		return false, err
	}
	if o.Target != nil {
		// iOS: the agent checks the target where it is now, not where the first tap aimed.
		o.Target = identifiedAppTarget(n, s)
	}
	r.invalidate()
	r.mark("tap", [2]int{x2, y2})
	res, err := r.Drv.Tap(ctx, x2, y2, o)
	if err != nil {
		return true, err
	}
	r.tracef("tap at (%d, %d) again: %s", x2, y2, actionDesc(res))
	if res.Withheld != "" || res.Cancelled {
		// iOS: the second touch did not take either. No third: the step fails, saying so.
		how := "withheld"
		if res.Cancelled {
			how = "cancelled"
		}
		return true, failf("tap on %s: the target moved from under the first touch, and the second one was %s", sel.Describe(), how)
	}
	if res.Settle != driver.NoChange {
		r.unsettled = 5 * time.Second
	}
	return true, nil
}

// wentInPlace: was (on before) is not on now, but the list holding it is, where it was, and
// lists the very same elements as before (kind, id, label and place), was and what holds it
// aside. A target pushed out of its list's view moves its neighbours with it, or something new
// (a banner, a spacer) takes its place: then this is false. With no neighbour to compare,
// there is no evidence either way: false.
func wentInPlace(before *ui.Screen, was *ui.Node, now *ui.Screen) bool {
	var list *ui.Node
	for p := was.Parent; p != nil && list == nil; p = p.Parent {
		if p.Scrollable {
			list = p
		}
	}
	if list == nil {
		return false
	}
	var nowList *ui.Node
	for _, n := range now.Nodes {
		if n.Scrollable && listKey(n) == listKey(list) && n.Bounds == list.Bounds {
			nowList = n
			break
		}
	}
	if nowList == nil {
		return false
	}
	// What holds was inside the list (a column of rows) can change size as it goes: it is
	// left out on both sides. Only was's own chain of holders: on now, the nodes at the same
	// places among their siblings, from the list down, of the same kind and id.
	var chain []*ui.Node // was's holders, from the list down
	for p := was.Parent; p != nil && p != list; p = p.Parent {
		chain = append([]*ui.Node{p}, chain...)
	}
	holders := map[*ui.Node]bool{}
	for _, h := range chain {
		holders[h] = true
	}
	at := nowList
	for _, h := range chain {
		i := slices.Index(h.Parent.Children, h)
		if i < 0 || i >= len(at.Children) || at.Children[i].Class != h.Class || at.Children[i].ID != h.ID {
			return false // its holders are not where they were: no evidence that it went in place
		}
		at = at.Children[i]
		holders[at] = true
	}
	items := func(s *ui.Screen, l *ui.Node, skip *ui.Node) map[string]int {
		out := map[string]int{}
		for _, n := range match.Visible(s) {
			if n == l || !n.IsDescendantOf(l) || holders[n] || (skip != nil && (n == skip || n.IsDescendantOf(skip))) {
				continue
			}
			out[fmt.Sprintf("%s|%s|%s|%v", n.Class, n.ID, n.Label(), n.Bounds)]++
		}
		return out
	}
	was1, now1 := items(before, list, was), items(now, nowList, nil)
	return len(was1) > 0 && maps.Equal(was1, now1)
}

// inPlaceSettle: how long recovery lets a target that went in place settle before looking for
// it once more (issue #141).
const inPlaceSettle = 500 * time.Millisecond

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
		r.mark("tap", [2]int{x, y})
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

// iosLayoutLag: how long after a SwiftUI layout change its accessibility frames can still show
// the old layout (issue #149: measured 37–280 ms on an iOS 27 simulator, while touches already
// go to the new layout). A look at a target sooner after its touch may see it where it was.
const iosLayoutLag = 300 * time.Millisecond

// tapShiftedButton (iOS, issue #149): a guarded tap on a button whose touch no control
// confirmed (a SwiftUI button reports no action). The target can have moved just before the
// touch went down: SwiftUI routes touches to the new layout at once, but the frames the agent
// and the probe read stay at the old one for up to iosLayoutLag, so the touch is reported on
// the target while it went to whatever now lies there (a banner pushed in above it). Once the
// frames are current, the target is looked for again: still under the point, or anything shows
// the tap took effect (recoverMovedTarget's checks), and the tap stands; moved off the point
// with no such sign, it is tapped once more where it is. A next assertion already true ends the
// check at once, with no wait. touchedAt: when the touch was sent.
func (r *Runner) tapShiftedButton(ctx context.Context, sel *match.Selector, was *ui.Node, before *ui.Screen, x, y int, touchedAt time.Time, o driver.ActionOpts) (again bool, err error) {
	if before == nil || was == nil {
		return false, nil
	}
	if wait := iosLayoutLag - time.Since(touchedAt); wait > 0 {
		r.invalidate()
		if now, err := r.read(ctx); err == nil && r.tapAssertionChanged(ctx, before, now) {
			return false, nil
		}
		if wait = iosLayoutLag - time.Since(touchedAt); wait > 0 {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(wait):
			}
		}
	}
	// One look with current frames. The target still under the point, or gone with the screen
	// it was on: the tap stands, with no wait for a still screen (the next step waits as it
	// always does).
	r.invalidate()
	now, err := r.read(ctx)
	if err != nil {
		return false, nil
	}
	n, matches, err := r.match(ctx, now, sel)
	if err != nil {
		return false, nil
	}
	if len(matches) == 1 && n != nil && n.Bounds.Contains(x, y) && n.ClipRect(ui.Rect{R: now.Width, B: now.Height}).Contains(x, y) {
		return false, nil
	}
	if n == nil && historyContext(before) != historyContext(now) {
		return false, nil
	}
	return r.recoverMovedTarget(ctx, sel, was, before, x, y, func() bool { return false }, o,
		"the touch went down where the target was and no control confirmed it, but the target is now elsewhere")
}
