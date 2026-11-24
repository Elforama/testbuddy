package engine

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

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

func (r *Runner) tapMovedTarget(ctx context.Context, sel *match.Selector, was *ui.Node, before *ui.Screen, x, y int, clicksBefore *int64, o driver.ActionOpts) (again bool, err error) {
	cc, ok := r.Drv.(driver.ClickCounter)
	if !ok || clicksBefore == nil {
		return false, nil
	}
	if now, err := cc.Clicks(ctx); err == nil && now != *clicksBefore {
		r.tracef("the view reported its click after the reply; skipping target recovery")
		return false, nil
	}
	clickedSince := func() bool {
		now, err := cc.Clicks(ctx)
		return err != nil || now != *clicksBefore
	}
	return r.recoverMovedTarget(ctx, sel, was, before, x, y, o, movedRecovery{clickedSince: clickedSince})
}

func stillUnderTouch(last driver.ActionResult, x, y int) bool {
	return last.AimAt != nil && last.AimAt.Contains(x, y)
}

func aimOf(n *ui.Node, s *ui.Screen) *driver.Aim {
	if n == nil || s == nil || s.Read == 0 || n.Ref == 0 || n.Window < 0 || n.Window >= len(s.Windows) {
		return nil
	}
	return &driver.Aim{Read: s.Read, Window: s.Windows[n.Window].ID, Ref: n.Ref}
}

type movedRecovery struct {
	clickedSince func() bool
	now          *ui.Screen
	ios          bool
}

func (r *Runner) recoverMovedTarget(ctx context.Context, sel *match.Selector, was *ui.Node, before *ui.Screen, x, y int, o driver.ActionOpts, m movedRecovery) (again bool, err error) {
	clickedSince := func() bool { return m.clickedSince != nil && m.clickedSince() }
	ownChange := func(n *ui.Node) bool {
		if !m.ios || n == nil || n.ID != was.ID || n.Class != was.Class || (n.Label() == was.Label() && n.Value == was.Value) {
			return false
		}
		r.tracef("the target's own label or value changed (%q, then %q): the tap took; the tap stands", was.Label()+was.Value, n.Label()+n.Value)
		return true
	}
	if m.now == nil {
		r.invalidate()
	}
	if was != nil && before != nil {
		current, readErr := m.now, error(nil)
		if current == nil {
			current, readErr = r.read(ctx)
		}
		if readErr == nil {
			if r.tapAssertionChanged(ctx, before, current) {
				return false, nil
			}
			if replacedAtPoint(before, current, x, y) {
				r.tracef("new content replaced the tapped control; leaving the result to the next flow step")
				return false, nil
			}
			if m.ios && r.newContentWhereItWas(before, current, was) {
				return false, nil
			}
			n, matches, matchErr := r.match(ctx, current, sel)
			if matchErr == nil && len(matches) == 1 && adjacentTapResultChanged(was, before, n, current) {
				r.tracef("the displaced control's adjacent result changed; leaving the outcome to the flow instead of replaying")
				return false, nil
			}
			if matchErr == nil && n == nil && historyContext(before) != historyContext(current) {
				r.tracef("the target disappeared on a different screen; leaving the result to the next flow step")
				return false, nil
			}
			if matchErr == nil && len(matches) == 1 && ownChange(n) {
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
	if s != nil && before != nil && replacedAtPoint(before, s, x, y) {
		r.tracef("new content replaced the tapped control at (%d, %d); leaving the result to the next flow step", x, y)
		return false, nil
	}
	if m.ios && r.newContentWhereItWas(before, s, was) {
		return false, nil
	}
	if m.ios && (err != nil || away != "" || n == nil) {
		r.tracef("the target is no longer in view: the tap stands; leaving the result to the next flow step")
		return false, nil
	}
	if ownChange(n) {
		return false, nil
	}
	if err != nil {
		var nf *NotFound
		if !errors.As(err, &nf) || s == nil || before == nil {
			return false, nil
		}
		away = awayDir(before, was, was.Bounds, s)
		if away == "" {
			return false, nil
		}
		if ci.On() && wentInPlace(before, was, s) {
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
			return false, nil
		}
		n, s, err = r.bringBack(ctx, &flow.Command{}, sel, away, o.Timeout, before, was)
		if err != nil {
			var nf *NotFound
			if errors.As(err, &nf) && nf.left {
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
	if m.ios {
		r.tracef("the touch went down where the target was and no control confirmed it, but the target is now elsewhere, at %s, and nothing shows the tap took", n.Bounds)
		return false, failf("tap at (%d, %d): the target moved from under the tap (now at %s); the app may not have taken it", x, y, n.Bounds)
	}
	r.tracef("no view took the tap and the target moved from under it, to %s (a layout shift): tapping it where it is", n.Bounds)
	x2, y2, err := r.clearAndPoint(ctx, sel, s, n)
	if err != nil {
		return false, err
	}
	r.invalidate()
	r.mark("tap", [2]int{x2, y2})
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
	var chain []*ui.Node
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
			return false
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

const inPlaceSettle = 500 * time.Millisecond

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

func (r *Runner) newContentWhereItWas(before, now *ui.Screen, was *ui.Node) bool {
	if before == nil || now == nil || was == nil {
		return false
	}
	area := was.Bounds.Intersect(ui.Rect{R: before.Width, B: before.Height})
	if area.Empty() {
		return false
	}
	key := func(n *ui.Node) string { return n.Class + "|" + n.ID + "|" + n.Label() }
	more := map[string]int{}
	for _, n := range now.Nodes {
		more[key(n)]++
	}
	for _, n := range before.Nodes {
		more[key(n)]--
	}
	for _, n := range match.Visible(now) {
		if n.Scrollable || (was.ID != "" && n.ID == was.ID) || more[key(n)] <= 0 {
			continue
		}
		if n.ClipRect(ui.Rect{R: now.Width, B: now.Height}).Intersect(n.Bounds).Intersect(area).Empty() {
			continue
		}
		r.tracef("new content where the target was (%s): the tap took; the tap stands", nodeDesc(n))
		return true
	}
	return false
}

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

func screenDiff(a, b *ui.Screen) string {
	an, bn := listed(a), listed(b)
	for i := 0; i < len(an) && i < len(bn); i++ {
		if nodeSig(an[i]) != nodeSig(bn[i]) {
			return fmt.Sprintf("%s became %s", nodeDesc(an[i]), nodeDesc(bn[i]))
		}
	}
	return fmt.Sprintf("%d elements, then %d", len(an), len(bn))
}

func nodeSig(n *ui.Node) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%v|%v|%v|%v", n.Class, n.ID, n.Text, n.Desc, n.Value, n.Bounds, n.Enabled, n.Checked, n.Selected)
}

func screenSig(s *ui.Screen) uint64 {
	h := fnv.New64a()
	for _, n := range listed(s) {
		fmt.Fprintln(h, nodeSig(n))
	}
	return h.Sum64()
}

func listed(s *ui.Screen) []*ui.Node {
	if s.Platform != "android" {
		return s.Nodes
	}
	out := make([]*ui.Node, 0, len(s.Nodes))
	for _, n := range s.Nodes {
		if !systemTicker(s, n) {
			out = append(out, n)
		}
	}
	return out
}

var clockText = regexp.MustCompile(`^\d{1,2}:\d{2}([\s\x{00a0}\x{202f}]?[AaPp]\.?[Mm]\.?)?$`)

var systemTickers = map[string]bool{
	"com.android.systemui:id/mobile_combo": true, "com.android.systemui:id/mobile_signal": true,
	"com.android.systemui:id/wifi_combo": true, "com.android.systemui:id/wifi_signal": true,
	"com.android.systemui:id/battery": true,
}

func systemTicker(s *ui.Screen, n *ui.Node) bool {
	if n.Window < 0 || n.Window >= len(s.Windows) || s.Windows[n.Window].Type != "system" {
		return false
	}
	if systemTickers[n.ID] {
		return true
	}
	t, d := strings.TrimSpace(n.Text), strings.TrimSpace(n.Desc)
	return (t != "" || d != "") && (t == "" || clockText.MatchString(t)) && (d == "" || clockText.MatchString(d))
}

const iosLayoutLag = 500 * time.Millisecond

func (r *Runner) tapShiftedButton(ctx context.Context, sel *match.Selector, was *ui.Node, before *ui.Screen, x, y int, touchedAt time.Time, o driver.ActionOpts) error {
	if before == nil || was == nil {
		return nil
	}
	if wait := iosLayoutLag - time.Since(touchedAt); wait > 0 {
		if next, _ := ctx.Value(tapAssertionKey{}).(*flow.Command); next != nil {
			r.invalidate()
			if now, err := r.read(ctx); err == nil && r.tapAssertionChanged(ctx, before, now) {
				return nil
			}
		}
		if wait = iosLayoutLag - time.Since(touchedAt); wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
	}
	r.invalidate()
	now, err := r.read(ctx)
	if err != nil {
		return nil
	}
	n, matches, err := r.match(ctx, now, sel)
	if err != nil || n == nil || len(matches) != 1 {
		r.tracef("the target is gone after the tap (or no longer unique): the tap stands")
		return nil
	}
	if n.Bounds.Contains(x, y) && n.ClipRect(ui.Rect{R: now.Width, B: now.Height}).Contains(x, y) {
		return nil
	}
	_, err = r.recoverMovedTarget(ctx, sel, was, before, x, y, o, movedRecovery{now: now, ios: true})
	return err
}
