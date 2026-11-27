package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

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
		return n, s, away, err
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

func (r *Runner) findWithin(ctx context.Context, sel *match.Selector, timeout, settle time.Duration) (*ui.Node, *ui.Screen, string, error) {
	r.tracef("looking for %s for up to %s (the step's timeout)", sel.Describe(), timeout)
	n, s, away, err := r.findUnderKeyboard(ctx, sel, timeout, settle)
	var nf *NotFound
	if timeout > keyboardGrace || !errors.As(err, &nf) || ctx.Err() != nil || s == nil || !s.Keyboard {
		return n, s, away, err
	}
	r.tracef("not found while the keyboard is up: hiding it (it may cover the element) and looking again")
	if herr := r.Drv.HideKeyboard(ctx); herr != nil {
		r.tracef("hiding the keyboard failed: %v", herr)
	}
	r.invalidate()
	return r.findStableAway(ctx, sel, timeout, settle)
}

const bringBackWait = 5 * time.Second

func (r *Runner) bringBack(ctx context.Context, c *flow.Command, sel *match.Selector, dir string, settle time.Duration, from *ui.Screen, was *ui.Node) (*ui.Node, *ui.Screen, error) {
	budget := r.opts.LookupTimeout
	if c.Optional {
		budget = r.opts.OptionalTimeout
	}
	budget = min(budget, bringBackWait)
	r.tracef("not tapping the last place it was seen: scrolling %s to bring it back into view (up to %s)", dir, budget)
	sc := &flow.Command{Name: "scrollUntilVisible", Direction: dir, VisibilityPct: 100,
		Timeout: strconv.FormatInt(budget.Milliseconds(), 10)}
	if ci.On() {
		r.scrollStop = screenLeftGuard(from, was)
	}
	defer func() { r.scrollStop = nil }()
	err := r.scrollTo(ctx, sc, sel)
	r.scrollStop = nil
	var left *screenLeft
	if errors.As(err, &left) {
		return nil, nil, &NotFound{FlowError: FlowError{Msg: fmt.Sprintf(
			"Element not found: %s (it went out of sight while moving, and %s while scrolling %s to bring it back)",
			sel.Describe(), left.why, dir)}, Screen: left.s, Query: sel.Text, ID: sel.ID, left: true}
	}
	if err != nil {
		var fe *FlowError
		if !errors.As(err, &fe) {
			return nil, nil, err
		}
		s, _ := r.read(ctx)
		return nil, nil, &NotFound{FlowError: FlowError{Msg: fmt.Sprintf(
			"Element not found: %s (it went out of sight while moving, and scrolling %s for %s did not bring it back)",
			sel.Describe(), dir, budget)}, Screen: s, Query: sel.Text, ID: sel.ID}
	}
	n, s, _, err := r.findStableAway(ctx, sel, time.Second, settle)
	return n, s, err
}

type screenLeft struct {
	why string
	s   *ui.Screen
}

func (e *screenLeft) Error() string { return "the screen changed while scrolling: " + e.why }

func screenLeftGuard(from *ui.Screen, was *ui.Node) func(*ui.Screen) string {
	if from == nil || was == nil {
		return nil
	}
	windows := func(s *ui.Screen) string {
		var b strings.Builder
		for _, w := range s.Windows {
			if w.Type != "app" {
				continue
			}
			pkg := w.Package
			if s.Platform == "web" {
				pkg, _, _ = strings.Cut(pkg, "#")
			}
			fmt.Fprintf(&b, "%d:%q:%q|", w.ID, pkg, w.Title)
		}
		return b.String()
	}
	wins, labels := windows(from), appLabels(from)
	list := ""
	for p := was.Parent; p != nil; p = p.Parent {
		if p.Scrollable {
			list = listKey(p)
			break
		}
	}
	replaced := func(s *ui.Screen) bool {
		now := appLabels(s)
		kept, added := false, false
		for l := range now {
			if labels[l] {
				kept = true
			} else {
				added = true
			}
		}
		return !kept && added
	}
	return func(s *ui.Screen) string {
		switch {
		case list != "" && !slices.ContainsFunc(s.Nodes, func(n *ui.Node) bool { return n.Scrollable && listKey(n) == list }):
			return "its list went"
		case windows(s) != wins:
			return "the app's window changed"
		case len(labels) > 0 && replaced(s):
			return "the screen changed"
		}
		return ""
	}
}

const keyboardGrace = 1500 * time.Millisecond

func busyIndicator(n *ui.Node) bool {
	return strings.HasSuffix(n.Class, "ProgressBar")
}

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
			continue
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

func drawnUnder(c, n *ui.Node) bool {
	under, known := drawOrder(c, n)
	return known && under
}

func drawOrder(c, n *ui.Node) (under, known bool) { return occlusion.DrawOrder(c, n) }

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

type hitChecker interface {
	CoveredAt(ctx context.Context, index, x, y int) (string, error)
}

func (r *Runner) awaitHitClear(ctx context.Context, hc hitChecker, sel *match.Selector, n *ui.Node, x, y int, center bool, timeout time.Duration) (*ui.Node, int, int, error) {
	what, err := hc.CoveredAt(ctx, n.Index, x, y)
	if err != nil || what == "" {
		return n, x, y, nil
	}
	start := time.Now()
	r.tracef("covered at (%d, %d) by %s: waiting up to %s for it to go", x, y, what, timeout.Round(time.Millisecond))
	events, stop := r.Drv.Events()
	defer stop()
	deadline := time.Now().Add(timeout)
	var gone error
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

const disabledWait = 3 * time.Second

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
