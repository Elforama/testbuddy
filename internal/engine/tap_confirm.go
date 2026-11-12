package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// Tap confirmation (issue #204). A tap's reply saying "the screen changed" does not prove the tap
// took: an app redrawing on its own (a spinner, a start-up animation) changes the screen whether
// or not the touch reached it, and Android drops a touch now and then (a window with no input
// channel yet, just after launch). A tap counts as taken on the platform's own evidence:
//
//   - Android: a view reported its click (the agent's click count, ClickCounter), even after the
//     reply;
//   - iOS: the in-app probe's receipt says the touch completed on the target (Targeted), or its
//     control reported its action (Clicked);
//   - on any platform: the app's tree shows the tap's trace (tapTrace): a new screen or window,
//     anything the app lists reading differently, or anything under the tap point changed.
//
// A tap whose reply claimed a change but that has none of these, once the screen settled and a
// moment after, is unconfirmed: it is sent once more, on its target found again. Only once: a
// second unconfirmed tap is left to the next step. A reply that says nothing changed keeps its
// own rules (retryTapIfNoChange; iOS's swallowed-tap retry). The web has no click signal from
// its driver yet: its taps are not checked here.

// tapConfirmGrace: how long an unconfirmed tap's screen is given, after it settled, for a late
// effect (a screen opening after the app's own work) before the tap is sent again.
const tapConfirmGrace = 300 * time.Millisecond

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

// retryUnconfirmedTap: the tap at (x, y) on sel's element, made on before, claimed a change but
// no view reported its click (Android) or the probe saw no touch complete on it (iOS). seen are
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
	if o.Target != nil {
		// iOS: the agent checks the touch against the target found again, as for the first.
		target := identifiedAppTarget(n, now)
		if target == nil || target.ID != o.Target.ID || target.Window != o.Target.Window {
			return false, nil
		}
		o.Target = target
	}
	r.tracef("unconfirmed tap: the reply said the screen changed, but no view took the tap and nothing the tap would change did (the app's own redraw); tapping once more")
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

// confirmProbedTap: iOS, a guarded tap on an app with the in-app probe, whose reply claimed a
// change while the probe saw no touch complete on the target and its control reported no action
// (issue #204). Once the screen settled, it is confirmed as retryUnconfirmedTap does.
func (r *Runner) confirmProbedTap(ctx context.Context, sel *match.Selector, before *ui.Screen, x, y int, o driver.ActionOpts) error {
	n, s, away, err := r.findStableAway(ctx, sel, 0, o.Timeout)
	if err != nil || n == nil || s == nil || away != "" || !n.Bounds.Contains(x, y) {
		return nil // gone, moved or out of sight: the tap may have taken; the next step judges
	}
	_, err = r.retryUnconfirmedTap(ctx, sel, before, []*ui.Screen{s}, x, y, nil, o)
	return err
}
