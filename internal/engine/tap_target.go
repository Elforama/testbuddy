package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// findUnderKeyboard finds a tap's target. One the keyboard hides is often not listed at all
// (Android leaves out views it covers): when it is not found within keyboardGrace while the
// keyboard is up, the keyboard is hidden, as a person would, and the lookup goes on. The grace
// lets something about to appear above the keyboard (a suggestion) be found first.
// away is findStableAway's: where the target went when it moved out of sight.
func (r *Runner) findUnderKeyboard(ctx context.Context, sel *match.Selector, timeout, settle time.Duration) (*ui.Node, *ui.Screen, string, error) {
	if timeout <= keyboardGrace {
		n, s, away, err := r.findStableAway(ctx, sel, timeout, settle)
		var nf *NotFound
		if errors.As(err, &nf) && ctx.Err() == nil {
			if old, before, dir := r.previouslySeen(sel, s); old != nil {
				return old, before, dir, nil
			}
		}
		return n, s, away, err
	}
	start := time.Now()
	n, s, away, err := r.findStableAway(ctx, sel, keyboardGrace, settle)
	var nf *NotFound
	if err == nil || !errors.As(err, &nf) || ctx.Err() != nil {
		return n, s, away, err // found, or a failure that looking longer would not mend (a lost device)
	}
	if s != nil && !s.Keyboard {
		if old, before, dir := r.previouslySeen(sel, s); old != nil {
			return old, before, dir, nil
		}
	}
	if s != nil && s.Keyboard {
		r.tracef("not found while the keyboard is up: hiding it (it may cover the element) and looking again")
		if herr := r.Drv.HideKeyboard(ctx); herr != nil {
			r.tracef("hiding the keyboard failed: %v", herr)
		}
		r.invalidate()
	}
	return r.findStableAway(ctx, sel, max(timeout-time.Since(start), 0), settle)
}

// bringBackWait bounds the scrolling that brings back a tap's target gone out of sight.
const bringBackWait = 5 * time.Second

// bringBack: the tap's target moved out of sight while the tap waited for it to rest (on a
// slow device a banner growing above it pushed it past the bottom edge of its list: a sliver
// showed, then nothing, bench/flows/scenarios/animations.yaml on CI). A tap where it was last
// seen would reach nothing there. As a person would, scroll its list the way it went
// (scrollUntilVisible, dir) until all of it shows, then find it again at rest. Bounded (the
// tap's lookup timeout, at most bringBackWait): a target that does not come back fails the
// tap as not found.
func (r *Runner) bringBack(ctx context.Context, c *flow.Command, sel *match.Selector, dir string, settle time.Duration) (*ui.Node, *ui.Screen, error) {
	budget := r.opts.LookupTimeout
	if c.Optional {
		budget = r.opts.OptionalTimeout
	}
	budget = min(budget, bringBackWait)
	r.tracef("not tapping the last place it was seen: scrolling %s to bring it back into view (up to %s)", dir, budget)
	sc := &flow.Command{Name: "scrollUntilVisible", Direction: dir, VisibilityPct: 100,
		Timeout: strconv.FormatInt(budget.Milliseconds(), 10)}
	if err := r.scrollTo(ctx, sc, sel); err != nil {
		var fe *FlowError
		if !errors.As(err, &fe) {
			return nil, nil, err // a lost device, a cancelled run
		}
		s, _ := r.read(ctx)
		return nil, nil, &NotFound{FlowError: FlowError{Msg: fmt.Sprintf(
			"Element not found: %s (it went out of sight while moving, and scrolling %s for %s did not bring it back)",
			sel.Describe(), dir, budget)}, Screen: s, Query: sel.Text}
	}
	n, s, _, err := r.findStableAway(ctx, sel, time.Second, settle)
	return n, s, err
}

// keyboardGrace: how long a tap looks for its target with the keyboard up before hiding it.
const keyboardGrace = 1500 * time.Millisecond

// busyIndicator: a spinner or a progress bar (Android's ProgressBar, which Compose's progress
// indicators report as too).
func busyIndicator(n *ui.Node) bool {
	return strings.HasSuffix(n.Class, "ProgressBar")
}

// loadingCover is a loading overlay over n, or nil (Android, iOS). Apps lay one over a screen while
// it loads, often see-through: n shows through it and stays in the tree, but the overlay takes
// the touches, and a tap on n is lost. It is a node at n's center, outside n and not one of
// its containers, that is a spinner or holds one. Android lists views by place on screen, not
// as drawn, so the order says nothing; screens hidden under the current one are not listed.
//
// iOS can report the whole veil as the spinner's frame, or expose a compact spinner
// inside a separate overlay container. Do not use a common ancestor of the target
// and spinner as evidence of a cover.
func loadingCover(s *ui.Screen, n *ui.Node) *ui.Node {
	if s.Platform == "ios" {
		for _, c := range s.Nodes {
			if c.Class != "ActivityIndicator" || c.Window != n.Window || c.IsDescendantOf(n) {
				continue
			}
			for cover := c; cover != nil && !n.IsDescendantOf(cover); cover = cover.Parent {
				if cover != c && (cover.Class != "Other" || cover.Scrollable || len(cover.Children) != 1) {
					break
				}
				b := cover.Bounds
				if b.L <= n.Bounds.L && b.T <= n.Bounds.T && b.R >= n.Bounds.R && b.B >= n.Bounds.B {
					return cover
				}
			}
		}
		return nil
	}
	if s.Platform != "android" {
		return nil
	}
	x, y := n.Bounds.CX(), n.Bounds.CY()
	for _, c := range s.Nodes {
		if c == n || c.Window != n.Window || !c.Bounds.Contains(x, y) || c.IsDescendantOf(n) || n.IsDescendantOf(c) {
			continue
		}
		if drawnUnder(c, n) {
			continue // a layer under n (a floating bar's content area): it cannot take n's taps
		}
		if busyIndicator(c) {
			return c
		}
		found := false
		c.Walk(func(d *ui.Node) bool {
			if !found && d != c && busyIndicator(d) {
				found = true
			}
			return !found
		})
		if found {
			return c
		}
	}
	return nil
}

// drawnUnder: c is drawn under n, as far as the drawing order tells. Below the nearest node
// holding both, the branch holding n is drawn after the one holding c (Android reports each
// node's place among its siblings as drawn: elevation and custom drawing order count). An
// order not reported, or the same for both (drawn at once), tells nothing: false.
func drawnUnder(c, n *ui.Node) bool {
	under, known := drawOrder(c, n)
	return known && under
}

// drawOrder: whether c is drawn under n (under), and whether the drawing order tells (known),
// as drawnUnder reads it.
func drawOrder(c, n *ui.Node) (under, known bool) {
	bc, bn := c, n
	for bc.Parent != nil && !n.IsDescendantOf(bc.Parent) {
		bc = bc.Parent
	}
	for bn.Parent != nil && bn.Parent != bc.Parent {
		bn = bn.Parent
	}
	if bc.Parent != bn.Parent || bc == bn || !bc.DrawOrderKnown || !bn.DrawOrderKnown || bn.DrawOrder == bc.DrawOrder {
		return false, false // no common container found, or no order to tell
	}
	return bn.DrawOrder > bc.DrawOrder, true
}

// awaitUncovered waits, on change events, for a loading overlay over n to go (up to timeout;
// then n is tapped anyway), lets the screen settle, and finds n again where it rests.
func (r *Runner) awaitUncovered(ctx context.Context, sel *match.Selector, n *ui.Node, s *ui.Screen, timeout time.Duration) (*ui.Node, *ui.Screen) {
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	start := time.Now()
	r.tracef("under a loading overlay (%s): waiting up to %s for it to go", nodeDesc(loadingCover(s, n)), timeout.Round(time.Millisecond))
	for loadingCover(s, n) != nil && time.Now().Before(deadline) {
		r.waitAny(ctx, events, s.Seq, deadline)
		if ctx.Err() != nil {
			break
		}
		r.invalidate()
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err != nil {
			continue
		}
		n, s = n2, s2
	}
	if loadingCover(s, n) != nil {
		r.tracef("the overlay is still there after %s: tapping anyway", since(start))
	} else {
		r.tracef("the overlay went after %s", since(start))
	}
	r.Drv.WaitSettled(ctx, 2*time.Second)
	r.invalidate()
	if n2, s2, _, err := r.find(ctx, sel, 0); err == nil {
		n, s = n2, s2
	}
	return n, s
}

// hitChecker is a driver that can say what a tap at a point would reach instead of a node
// (the web: the browser's hit test).
type hitChecker interface {
	CoveredAt(ctx context.Context, index, x, y int) (string, error)
}

// awaitHitClear waits, on change events, until a tap at (x, y) would reach n (up to timeout;
// then n is tapped anyway), and finds n again where it rests. center: the point is n's middle,
// so it follows n if it moved.
func (r *Runner) awaitHitClear(ctx context.Context, hc hitChecker, sel *match.Selector, n *ui.Node, x, y int, center bool, timeout time.Duration) (*ui.Node, int, int, error) {
	what, err := hc.CoveredAt(ctx, n.Index, x, y)
	if err != nil || what == "" {
		return n, x, y, nil // a failed check does not fail the tap
	}
	start := time.Now()
	r.tracef("covered at (%d, %d) by %s: waiting up to %s for it to go", x, y, what, timeout.Round(time.Millisecond))
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	var gone error // the latest lookup's error: the target is not on screen
	r.invalidate()
	if _, _, _, err := r.find(ctx, sel, 0); err != nil {
		gone = err
	}
	for what != "" && time.Now().Before(deadline) {
		r.waitAny(ctx, events, r.Drv.Seq(), deadline)
		if ctx.Err() != nil {
			return n, x, y, ctx.Err()
		}
		r.invalidate()
		n2, _, _, err := r.find(ctx, sel, 0)
		if gone = err; err != nil {
			continue
		}
		dx, dy := x-n.Bounds.L, y-n.Bounds.T
		n = n2
		if center {
			x, y = n.Bounds.CX(), n.Bounds.CY()
		} else {
			x, y = n.Bounds.L+dx, n.Bounds.T+dy
		}
		if what, err = hc.CoveredAt(ctx, n.Index, x, y); err != nil {
			what = ""
		}
	}
	if what != "" && gone != nil {
		// The target left (the page moved: a banner pushed it out of view) and something else
		// is where it was. Tapping there would tap that instead: fail on the target.
		r.tracef("it is gone, and %s is where it was: not tapping that", what)
		return n, x, y, gone
	}
	if what != "" {
		r.tracef("still covered by %s after %s: tapping anyway", what, since(start))
	} else {
		r.tracef("uncovered after %s", since(start))
	}
	return n, x, y, nil
}

// disabledWait: how long a tap gives a disabled target to turn on.
const disabledWait = 3 * time.Second

// awaitEnabled gives a disabled element up to timeout to turn on, on change events: a button
// that waits for its form to validate takes a tap and does nothing, and the flow would fail
// later for no visible reason. A selector that names `enabled` itself is never waited for.
// It returns the element as found last (still disabled after the timeout, it is tapped
// anyway, as the flow format does).
func (r *Runner) awaitEnabled(ctx context.Context, sel *match.Selector, n *ui.Node, s *ui.Screen, timeout time.Duration) (*ui.Node, *ui.Screen) {
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	seq := s.Seq
	for !n.Enabled && time.Now().Before(deadline) {
		r.waitAny(ctx, events, seq, deadline)
		if ctx.Err() != nil {
			break
		}
		seq = r.Drv.Seq()
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err != nil {
			continue
		}
		n, s = n2, s2
	}
	if n.Enabled {
		r.Drv.WaitSettled(ctx, 2*time.Second)
		if n2, s2, _, err := r.find(ctx, sel, 0); err == nil {
			n, s = n2, s2
		}
	}
	return n, s
}
