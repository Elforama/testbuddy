package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// Tap confirmation (issue #204). A tap's reply saying "the screen changed" does not prove the tap
// took: an app redrawing on its own (a spinner, a start-up animation) changes the screen whether
// or not the touch reached it. And Android drops a touch on a window just added that has no input
// channel yet (just after launch). No view reporting a click proves nothing either: Compose,
// React Native and bare gesture detectors never report one, so a re-tap on that alone would act
// twice on a tap whose effect comes late.
//
// So a tap is sent again only on evidence that its touch was dropped, and none that it took. A
// CI allowance (package ci), like the other speculative re-taps (#103, #131):
//
//   - Android: a window came, went or changed just before the touch (droppedTouchSuspect), the
//     reply claimed a change, and no view reported a click, even after the reply;
//   - the target is still under the point, and the app's tree shows no trace of the tap
//     (tapTrace) once settled, nor tapConfirmGrace later.
//
// It is then sent once more on its target found again, and only if it is the step's first
// touch: a step taps at most twice. iOS queues touches and its probe receipt can simply be late,
// and the web driver has no click signal yet: neither is re-tapped here.

// windowJustChanged: how recent a window change before a touch makes it suspect of being
// dropped (a window just added, its input channel not up yet).
const windowJustChanged = time.Second

// droppedTouchSuspect: res, a tap's reply, gives reason to think its touch was dropped (issue
// #204): in CI mode, a window changed just before it, the reply claimed a change (perhaps the
// app's own), and no view reported a click.
func droppedTouchSuspect(res driver.ActionResult) bool {
	return ci.On() && res.WindowAge != nil && *res.WindowAge >= 0 && *res.WindowAge <= windowJustChanged &&
		res.Settle != driver.NoChange && res.Clicked != nil && !*res.Clicked
}

// tapConfirmGrace: how long a suspect tap's settled screen is given for a late effect (a screen
// opening after the app's own work) before the tap is sent again. CI runners are slow.
const tapConfirmGrace = time.Second

// tapTrace names what on now, read after a tap at (x, y), shows that the tap took (before is the
// screen it was made on), "" when nothing does. Changes in place only (a node moving, away from
// the tap point) and a progress indicator's progress are an app's own redraws: not a trace.
// Anything else is, so that a tap that did land is not sent twice.
func tapTrace(before, now *ui.Screen, x, y int) string {
	if before == nil || now == nil {
		return ""
	}
	if historyContext(before) != historyContext(now) {
		return "the screen or its windows changed"
	}
	a, b := appContent(before), appContent(now)
	if len(a) != len(b) {
		return fmt.Sprintf("the app lists %d elements, then %d", len(a), len(b))
	}
	for i := range a {
		if a[i].sig != b[i].sig {
			return fmt.Sprintf("%s became %s", nodeDesc(a[i].n), nodeDesc(b[i].n))
		}
	}
	p, q := underPoint(before, x, y), underPoint(now, x, y)
	if len(p) != len(q) {
		return "what is under the tap point changed"
	}
	for i := range p {
		if p[i] != q[i] {
			return "what is under the tap point changed"
		}
	}
	return ""
}

type contentEntry struct {
	n   *ui.Node
	sig string
}

// appContent: what the app's windows list, node by node, without their places.
func appContent(s *ui.Screen) []contentEntry {
	var out []contentEntry
	for _, n := range s.Nodes {
		if n.Window < 0 || n.Window >= len(s.Windows) || s.Windows[n.Window].Type != "app" {
			continue
		}
		text, desc, value, state := n.Text, n.Desc, n.Value, n.State
		if progressClass(n) {
			text, desc, value, state = "", "", "", "" // its progress: the app's own redraw
		}
		out = append(out, contentEntry{n, fmt.Sprintf("%s|%s|%d|%d|%q|%q|%q|%q|%q|%q|%v|%v|%v|%v|%v|%v",
			n.Class, n.ID, n.Window, n.Depth, text, desc, value, state, n.Hint, n.Error,
			n.Enabled, n.Checked, n.Selected, n.Focused, n.Clickable, n.Editable)})
	}
	return out
}

func progressClass(n *ui.Node) bool {
	switch n.ShortClass() {
	case "ProgressBar", "ActivityIndicator", "ProgressIndicator", "CircularProgressIndicator", "LinearProgressIndicator":
		return true
	}
	return false
}

// underPoint: the nodes whose bounds hold (x, y), with their places, in tree order.
func underPoint(s *ui.Screen, x, y int) []string {
	var out []string
	for _, n := range s.Nodes {
		if n.Bounds.Contains(x, y) {
			out = append(out, fmt.Sprintf("%d|%s|%s|%v", n.Window, n.Class, n.ID, n.Bounds))
		}
	}
	return out
}

// retryUnconfirmedTap: the tap at (x, y) on sel's element, made on before, is suspect of a dropped
// touch (droppedTouchSuspect). seen are
// the screens read since, settled is the last of them. When none shows the tap's trace (tapTrace),
// nor a fresh read a moment later, and no click came late, the tap is sent once more, on the
// element found again. again: it tapped.
func (r *Runner) retryUnconfirmedTap(ctx context.Context, sel *match.Selector, before *ui.Screen, seen []*ui.Screen, x, y int, clicksBefore *int64, o driver.ActionOpts) (again bool, err error) {
	if before == nil || len(seen) == 0 {
		return false, nil
	}
	for _, s := range seen {
		if why := tapTrace(before, s, x, y); why != "" {
			return false, nil
		}
	}
	lateClick := func() bool {
		cc, ok := r.Drv.(driver.ClickCounter)
		if !ok || clicksBefore == nil {
			return false
		}
		now, err := cc.Clicks(ctx)
		return err != nil || now != *clicksBefore // an unknown count may hide a click
	}
	if lateClick() {
		r.tracef("a view reported the click after the tap's reply")
		return false, nil
	}
	// A late effect (a screen the app opens after its own work) is given a moment to show.
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(tapConfirmGrace):
	}
	r.invalidate()
	now, err := r.read(ctx)
	if err != nil {
		return false, nil
	}
	if why := tapTrace(before, now, x, y); why != "" {
		r.tracef("the tap took effect a moment later: %s", why)
		return false, nil
	}
	if r.tapAssertionChanged(ctx, before, now) || lateClick() {
		return false, nil
	}
	n, matches, err := r.match(ctx, now, sel)
	if err != nil || n == nil || len(matches) != 1 {
		return false, nil
	}
	r.tracef("unconfirmed tap: a window changed just before the touch, no view took it and nothing it would change did (the screen's change was the app's own): tapping once more (CI mode)")
	x2, y2, err := r.clearAndPoint(ctx, sel, now, n)
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
