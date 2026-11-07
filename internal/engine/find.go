package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// restlessAfter: when the UI keeps changing without ever going quiet (a ticker, a
// countdown), a waiting lookup reads anyway after this long, then again at most this often.
const restlessAfter = 400 * time.Millisecond

// silentAfter is the safety net for a device that sends no UI events at all during a wait
// (an app whose changes the platform does not report): after this long without a single
// event, the screen is read again, and the gap doubles each time. With events flowing it
// never fires. TB_SILENT_MS overrides; 0 disables.
var silentAfter = func() time.Duration {
	if v, err := strconv.Atoi(os.Getenv("TB_SILENT_MS")); err == nil {
		return time.Duration(v) * time.Millisecond
	}
	return 1500 * time.Millisecond
}()

// resolveSelector evaluates the selector's ${...} fields.
func (r *Runner) resolveSelector(s *flow.Selector) (*match.Selector, error) {
	if s == nil {
		return nil, nil
	}
	out := &match.Selector{Width: s.Width, Height: s.Height, Tolerance: s.Tolerance, Enabled: s.Enabled,
		Selected: s.Selected, Checked: s.Checked, Focused: s.Focused, Traits: s.Traits}
	var err error
	if out.Text, err = r.eval(s.Text); err != nil {
		return nil, err
	}
	if out.ID, err = r.eval(s.ID); err != nil {
		return nil, err
	}
	if out.Index, err = r.eval(s.Index); err != nil {
		return nil, err
	}
	if out.CSS, err = r.eval(s.CSS); err != nil {
		return nil, err
	}
	for _, rel := range []struct {
		src *flow.Selector
		dst **match.Selector
	}{{s.Below, &out.Below}, {s.Above, &out.Above}, {s.LeftOf, &out.LeftOf}, {s.RightOf, &out.RightOf},
		{s.ContainsChild, &out.ContainsChild}, {s.ChildOf, &out.ChildOf}} {
		if rel.src != nil {
			if *rel.dst, err = r.resolveSelector(rel.src); err != nil {
				return nil, err
			}
		}
	}
	for _, d := range s.ContainsDescendants {
		m, err := r.resolveSelector(d)
		if err != nil {
			return nil, err
		}
		out.ContainsDescendants = append(out.ContainsDescendants, m)
	}
	return out, nil
}

// adjusted is the format's timeout rule: the budget shrinks by the time since the last
// interaction with the device.
func (r *Runner) adjusted(t time.Duration) time.Duration {
	left := t - time.Since(r.lastInteraction)
	if left < 0 {
		return 0
	}
	return left
}

func (r *Runner) lookupTimeout(optional bool) time.Duration {
	if optional {
		return r.adjusted(r.opts.OptionalTimeout)
	}
	return r.adjusted(r.opts.LookupTimeout)
}

// find waits up to timeout for the selector to match, re-reading only when the device
// reports a change. It always checks at least once. waited reports whether the element
// only appeared after waiting (it may still be sliding into place).
func (r *Runner) find(ctx context.Context, sel *match.Selector, timeout time.Duration) (n *ui.Node, s *ui.Screen, waited bool, err error) {
	return r.findWithChangeRead(ctx, sel, timeout, false)
}

func (r *Runner) findWithChangeRead(ctx context.Context, sel *match.Selector, timeout time.Duration, firstChange bool) (n *ui.Node, s *ui.Screen, waited bool, err error) {
	deadline := time.Now().Add(timeout)
	events, stop := r.Drv.Events()
	defer stop()
	silence := silentAfter
	answered := 0
	start, reads := time.Now(), 0
	for {
		reads++
		s, err = r.read(ctx)
		if err != nil {
			return nil, nil, waited, err
		}
		if answered < maxDialogAnswers {
			if ok, err := r.answerDialog(ctx, s); err != nil {
				return nil, s, waited, err
			} else if ok {
				answered++
				continue
			}
		}
		n, _, err = r.match(ctx, s, sel)
		if err != nil {
			var ii *match.InvalidIndex
			if errors.As(err, &ii) {
				return nil, s, waited, &invalidField{msg: err.Error()}
			}
			return nil, s, waited, err
		}
		if n != nil {
			if waited {
				r.tracef("found %s: %s, after %s and %d reads", sel.Describe(), nodeDesc(n), since(start), reads)
			} else {
				r.tracef("found %s: %s", sel.Describe(), nodeDesc(n))
			}
			return n, s, waited, nil
		}
		if !time.Now().Before(deadline) {
			r.tracef("not found: %s, after %s and %d reads (screen: %s, %d elements, %s)",
				sel.Describe(), since(start), reads, s.Foreground(), len(s.Nodes), timeoutDesc(timeout))
			if sim := similarDesc(s, sel.Text); sim != "" {
				r.tracef("similar on screen: %s", sim)
			}
			return nil, s, waited, &NotFound{FlowError: FlowError{Msg: "Element not found: " + sel.Describe()}, Screen: s, Query: sel.Text}
		}
		waited = true
		quiet, err := r.waitChange(ctx, events, s.Seq, deadline, silence, firstChange)
		firstChange = false
		if err != nil {
			return nil, s, waited, err
		}
		if quiet {
			// No event for a while: read again, and warn if the screen did change without
			// the device saying so (the event source is missing something).
			r.tracef("no event from the device for %s while looking for %s: reading the screen again", silence, sel.Describe())
			before := s.Signature()
			r.invalidate()
			if s2, err := r.read(ctx); err == nil && s2.Signature() != before {
				r.silent(silence)
			}
			silence *= 2
		} else {
			silence = silentAfter
		}
	}
}

// silent notes (once per runner) that the device sent no events during a wait.
func (r *Runner) silent(after time.Duration) {
	if r.warnedSilent {
		return
	}
	r.warnedSilent = true
	r.logLine(fmt.Sprintf(
		"the screen changed without the device reporting it (found by a re-read after %s of silence); waits on this app may be slower", after))
}

// waitChange blocks until the screen may differ from the read at seq. A positive
// lookup may read once on the first change; later reads wait for settle or the
// restless interval to avoid taking a snapshot for every frame.
// quiet reports that no event arrived within the silence interval.
func (r *Runner) waitChange(ctx context.Context, events <-chan driver.Event, seq int64, deadline time.Time, silence time.Duration, early bool) (quiet bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dl := time.NewTimer(time.Until(deadline))
	defer dl.Stop()
	var restless <-chan time.Time
	if r.Drv.Seq() > seq {
		if early {
			return false, nil
		}
		restless = time.After(restlessAfter)
	}
	var hush <-chan time.Time
	if silence > 0 {
		hush = time.After(silence)
	}
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-dl.C:
			return false, nil
		case <-restless:
			return false, nil
		case <-hush:
			return true, nil
		case e, ok := <-events:
			if !ok || e.Kind == "disconnected" {
				return false, errors.New("lost the connection to the device agent")
			}
			hush = nil // the device is talking: no safety net needed
			switch e.Kind {
			case "settled":
				if e.Seq > seq {
					return false, nil
				}
			case "changed":
				if early && e.Seq > seq {
					return false, nil
				}
				if restless == nil {
					restless = time.After(restlessAfter)
				}
			}
		}
	}
}

// findStable finds the element, then makes sure the UI is settled before it is acted on.
// When nothing is pending that check answers at once (it is the device's event state); if
// the UI was still changing, or the element only appeared while waiting, it waits for the
// UI to settle and finds the element again, so a tap lands where it ends up.
func (r *Runner) findStable(ctx context.Context, sel *match.Selector, timeout time.Duration, settleTimeout time.Duration) (*ui.Node, *ui.Screen, error) {
	n, s, _, err := r.findStableAway(ctx, sel, timeout, settleTimeout)
	return n, s, err
}

// findStableAway is findStable that also says when the element moved out of sight as it
// settled (see atRest): away is the scrollUntilVisible direction that brings it back, "" when
// it is in sight (or nothing can scroll it back). n is then where it was last seen.
func (r *Runner) findStableAway(ctx context.Context, sel *match.Selector, timeout time.Duration, settleTimeout time.Duration) (n *ui.Node, s *ui.Screen, away string, err error) {
	if settleTimeout <= 0 {
		settleTimeout = 2 * time.Second
	}
	span := restGap // how long two reads must agree for the element to count as at rest
	if r.unsettled > 0 {
		// Just after an action, something it started may still be moving (a spring, which
		// pauses for a moment at each turn): reads must agree for longer.
		span = restSpanAfterAction
		// An action before this one left the UI reacting: let it settle (as long as that
		// action would have waited) before looking, so the element is read once, where it
		// ends up.
		t := time.Now()
		r.Drv.WaitSettled(ctx, max(settleTimeout, r.unsettled))
		r.unsettled = 0
		r.traceWait(t, "for the last action's change to settle")
	}
	n, s, waited, err := r.find(ctx, sel, timeout)
	if err != nil {
		return nil, s, "", err
	}
	// Every read from here on checks whether it still moves: past any cache.
	r.fresh = true
	defer func() { r.fresh = false }()
	found := time.Now()
	from := n.Bounds // where it was first found
	full := n.Bounds // the largest it was read at: smaller later means cut off at an edge
	t := time.Now()
	r.Drv.WaitSettled(ctx, settleTimeout)
	r.traceWait(t, "for a still screen")
	if waited || r.Drv.Seq() != s.Seq {
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err == nil {
			full = larger(full, n2.Bounds)
			if n2.Bounds != n.Bounds {
				r.tracef("it moved while the screen settled: %v -> %v", n.Bounds, n2.Bounds)
			} else if time.Since(found) >= span {
				return n2, s2, "", nil // two reads apart in time agree: it is at rest
			}
			n, s = n2, s2
		} else {
			r.tracef("it went while the screen settled (%v): acting on where it was", err)
		}
	}
	return r.atRest(ctx, sel, n, s, from, full, span)
}

// cutOff: r is smaller than the element was read at (full) and not around the same middle,
// so an edge clips it: it is sliding past the edge. An element that shrinks around its middle
// (a pulse) is not cut off.
func cutOff(r, full ui.Rect) bool {
	if r.W() >= full.W() && r.H() >= full.H() {
		return false
	}
	dx, dy := r.CX()-full.CX(), r.CY()-full.CY()
	return dx > 2 || dx < -2 || dy > 2 || dy < -2
}

// larger keeps the bigger width and height of two reads of an element.
func larger(a, b ui.Rect) ui.Rect {
	if b.W() > a.W() {
		a.L, a.R = b.L, b.R
	}
	if b.H() > a.H() {
		a.T, a.B = b.T, b.B
	}
	return a
}

// Motion that sends no events: a view sliding or springing in moves without telling the
// accessibility layer (Android reports no change for a new position; iOS animations play in
// the render server), so a "settled" screen can still have the target in motion. atRest
// reads it once more a moment later and, while it keeps moving, again, until two reads agree
// (up to restWait). It is the one bounded poll in a lookup: what moves sends nothing to wait on.
const (
	restGap  = 40 * time.Millisecond
	restWait = 2 * time.Second
	// restSpanAfterAction: right after an action, three reads must agree (two gaps), not
	// two. A spring turns: two reads 40 ms apart on either side of a turn can agree, a third
	// does not.
	restSpanAfterAction = 2 * restGap
)

// When it does not come back to rest in sight within restWait, and it had moved, it went out
// of sight as it moved (a banner growing above it pushes it past the bottom edge of its list:
// first a sliver shows, then nothing). away is then the scrollUntilVisible direction that
// brings it back, when a list it is in scrolls that way (see awayDir); else "" and the tap
// acts on where it was last seen, as there is nothing better to do.
func (r *Runner) atRest(ctx context.Context, sel *match.Selector, n *ui.Node, s *ui.Screen, from, full ui.Rect, span time.Duration) (*ui.Node, *ui.Screen, string, error) {
	start := time.Now()
	same := start // since when the reads have shown it where it is now
	missing, cut := false, false
	full = larger(full, n.Bounds)
	for {
		select {
		case <-ctx.Done():
			return n, s, "", ctx.Err()
		case <-time.After(restGap):
		}
		r.invalidate()
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err != nil {
			// AND-001: a target disappearing on another route is not a row
			// sliding out of its list. Return the current observation rather
			// than hiding navigation behind the last-seen node and screen.
			if s2 != nil && awayDir(s, n, from, s2) != "" && historyContext(s) != historyContext(s2) {
				r.tracef("the target disappeared as the screen context changed; stopping motion recovery")
				return nil, s2, "", err
			}
			// Out of sight for a moment: a spring that overshoots carries it past a clipping
			// edge and back. Keep reading (within restWait); only then act on where it was.
			if time.Since(start) > restWait {
				if dir := awayDir(s, n, from, s2); dir != "" {
					r.tracef("not seen again after %s: it went out of its list's view (last seen at %v)", since(start), n.Bounds)
					return n, s, dir, nil
				}
				r.tracef("not seen again after %s: acting on where it was, %v", since(start), n.Bounds)
				return n, s, "", nil
			}
			if !missing {
				r.tracef("it went out of sight while moving: looking again")
				missing = true
			}
			continue
		}
		if missing {
			missing = false
			if n2.Bounds != n.Bounds {
				n, s = n2, s2 // back in sight somewhere new: it needs two reads that agree
				same = time.Now()
				continue
			}
		}
		full = larger(full, n2.Bounds)
		if n2.Bounds == n.Bounds && cutOff(n2.Bounds, full) && time.Since(start) <= restWait {
			// Two reads agree, but it was bigger a moment ago: it is held at a clipping edge
			// while it moves past it (a spring overshooting), not at rest.
			if !cut {
				r.tracef("it is cut off at an edge while moving (%v, was %dx%d): looking again", n2.Bounds, full.W(), full.H())
				cut = true
			}
			continue
		}
		if n2.Bounds == n.Bounds && time.Since(same) < span && time.Since(start) <= restWait {
			continue // agreeing, but not for long enough yet
		}
		if n2.Bounds == n.Bounds && cutOff(n2.Bounds, full) {
			// Still cut off at an edge after restWait: only a sliver of it shows.
			if dir := awayDir(s2, n2, from, nil); dir != "" {
				r.tracef("only a part of it shows after %s, cut off at an edge (%v, was %dx%d)", since(start), n2.Bounds, full.W(), full.H())
				return n2, s2, dir, nil
			}
		}
		if n2.Bounds == n.Bounds {
			if time.Since(start) > span*2 {
				r.tracef("it was moving: at rest after %s at %v", since(start), n2.Bounds)
			}
			return n2, s2, "", nil
		}
		if time.Since(start) > restWait {
			if dir := awayDir(s2, n2, from, nil); dir != "" && cutOff(n2.Bounds, full) {
				r.tracef("still moving after %s, cut off at an edge (%v, was %dx%d)", since(start), n2.Bounds, full.W(), full.H())
				return n2, s2, dir, nil
			}
			r.tracef("still moving after %s: acting on where it is now, %v", since(start), n2.Bounds)
			return n2, s2, "", nil
		}
		n, s = n2, s2
		same = time.Now()
	}
}

// awayDir: n (last seen on s, first found at from) went out of sight as it moved. It is the
// scrollUntilVisible direction that brings it back (DOWN when it went down: the list must show
// what is below), or "" when nothing would. It needs a list holding n that scrolls along the
// way it went (not a lane that clips a spring), and, when it is gone (now is the read without
// it), that list still on screen: it left the list's view, the screen did not change. When no
// read caught it moving (a slow device: in view, then not listed), the way it went is towards
// the nearer end of its list's view (an insertion above pushes it down); an element that just
// vanishes outside any list has nothing to bring back.
func awayDir(s *ui.Screen, n *ui.Node, from ui.Rect, now *ui.Screen) string {
	screen := ui.Rect{R: s.Width, B: s.Height}
	dx, dy := n.Bounds.CX()-from.CX(), n.Bounds.CY()-from.CY()
	moved := abs(dx) > 2 || abs(dy) > 2
	if !moved && now == nil {
		return ""
	}
	vertical := !moved || abs(dy) >= abs(dx)
	var list *ui.Node
	for p := n.Parent; p != nil && list == nil; p = p.Parent {
		if p.Scrollable && sidewaysShaped(p, screen) != vertical {
			list = p
		}
	}
	if list == nil {
		return ""
	}
	if now != nil && !slices.ContainsFunc(now.Nodes, func(l *ui.Node) bool { return l.Scrollable && listKey(l) == listKey(list) }) {
		return "" // its list went too: the screen changed
	}
	if !moved {
		dy = n.Bounds.CY() - list.Bounds.Intersect(screen).CY()
	}
	switch {
	case vertical && dy >= 0:
		return "DOWN"
	case vertical:
		return "UP"
	case dx > 0:
		return "RIGHT"
	}
	return "LEFT"
}

// absent waits until the selector matches nothing on a settled screen. Restless elements
// (a ticker) may keep changing; what matters is that no real change is pending.
func (r *Runner) absent(ctx context.Context, sel *match.Selector, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	events, stop := r.Drv.Events()
	defer stop()
	answered := 0
	start := time.Now()
	for {
		s, err := r.read(ctx)
		if err != nil {
			return false, err
		}
		if answered < maxDialogAnswers {
			if ok, err := r.answerDialog(ctx, s); err != nil {
				return false, err
			} else if ok {
				answered++
				continue
			}
		}
		n, _, err := r.match(ctx, s, sel)
		if err != nil {
			return false, err
		}
		if n == nil {
			left := time.Until(deadline)
			r.Drv.WaitSettled(ctx, max(min(left, 2*time.Second), 0))
			if r.Drv.Seq() == s.Seq {
				r.tracef("gone: %s (checked on a settled screen, %s)", sel.Describe(), since(start))
				return true, nil
			}
			// Something changed while settling: confirm on a fresh read.
			s2, err := r.read(ctx)
			if err != nil {
				return false, err
			}
			if n2, _, err := r.match(ctx, s2, sel); err == nil && n2 == nil {
				r.tracef("gone: %s (%s)", sel.Describe(), since(start))
				return true, nil
			}
		}
		if !time.Now().Before(deadline) {
			r.tracef("still there after %s: %s", since(start), nodeDesc(n))
			return false, nil
		}
		if quiet, err := r.waitChange(ctx, events, s.Seq, deadline, silentAfter, false); err != nil {
			return false, err
		} else if quiet {
			r.invalidate()
		}
	}
}

// evalCondition evaluates a when/while/assert condition. timeout is the lookup budget for
// visible/notVisible (already adjusted by the caller unless noted).
func (r *Runner) evalCondition(ctx context.Context, c *flow.Condition, timeout time.Duration) (bool, error) {
	if c == nil {
		return true, nil
	}
	if c.Platform != "" && !strings.EqualFold(c.Platform, r.Drv.Platform()) {
		return false, nil
	}
	if c.Timeout != "" {
		v, err := r.eval(c.Timeout)
		if err != nil {
			return false, err
		}
		ms, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(v), "_", ""))
		if err != nil {
			return false, failf("invalid timeout %q", v)
		}
		timeout = time.Duration(ms) * time.Millisecond
	}
	if c.True != nil {
		v, err := r.eval(*c.True)
		if err != nil {
			return false, err
		}
		if !jsTruthy(v) {
			return false, nil
		}
	}
	if c.Visible != nil {
		sel, err := r.resolveSelector(c.Visible)
		if err != nil {
			return false, err
		}
		_, _, _, err = r.findWithChangeRead(ctx, sel, timeout, true)
		var nf *NotFound
		if errors.As(err, &nf) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	if c.NotVisible != nil {
		sel, err := r.resolveSelector(c.NotVisible)
		if err != nil {
			return false, err
		}
		ok, err := r.absent(ctx, sel, timeout)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func describeCondition(c *flow.Condition) string {
	if c == nil {
		return ""
	}
	if c.Label != "" {
		return c.Label
	}
	var parts []string
	if c.Platform != "" {
		parts = append(parts, "platform is "+c.Platform)
	}
	if c.Visible != nil {
		parts = append(parts, fmt.Sprintf("%s is visible", selDesc(c.Visible)))
	}
	if c.NotVisible != nil {
		parts = append(parts, fmt.Sprintf("%s is not visible", selDesc(c.NotVisible)))
	}
	if c.True != nil {
		parts = append(parts, fmt.Sprintf("%q is true", *c.True))
	}
	return strings.Join(parts, " and ")
}

// CSSQuerier is a driver that can evaluate CSS selectors (web).
type CSSQuerier interface {
	QueryCSS(ctx context.Context, css string) ([]int, error)
}

// match finds the selector on a screen, resolving CSS selectors on the page first.
func (r *Runner) match(ctx context.Context, s *ui.Screen, sel *match.Selector) (*ui.Node, []*ui.Node, error) {
	q, canCSS := r.Drv.(CSSQuerier)
	var cssErr error
	sel.Walk(func(x *match.Selector) {
		if x.CSS == "" || cssErr != nil {
			return
		}
		x.CSSNodes = nil
		if !canCSS {
			return // CSS selectors only exist on web; elsewhere they match nothing
		}
		idx, err := q.QueryCSS(ctx, x.CSS)
		if err != nil {
			cssErr = failf("css %q: %v", x.CSS, err)
			return
		}
		x.CSSNodes = make(map[int]bool, len(idx))
		for _, i := range idx {
			x.CSSNodes[i] = true
		}
	})
	if cssErr != nil {
		return nil, nil, cssErr
	}
	return match.Find(s, sel)
}

// timeoutDesc says how long a lookup was given, for the trace.
func timeoutDesc(t time.Duration) string {
	if t <= 0 {
		return "no wait: checked once"
	}
	return fmt.Sprintf("allowed %s", t.Round(time.Millisecond))
}
