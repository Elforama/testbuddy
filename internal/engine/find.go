package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/changes"
	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

func (r *Runner) waitAny(ctx context.Context, events <-chan driver.Event, seq int64, deadline time.Time) {
	if r.Drv.Seq() > seq {
		return
	}
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			return
		case e, ok := <-events:
			if !ok || e.Kind == "disconnected" || changes.News(e, seq) {
				return
			}
		}
	}
}

const restlessAfter = changes.RestlessAfter

var silentAfter = changes.SilentAfter()

func (r *Runner) resolveSelector(s *flow.Selector) (*match.Selector, error) {
	return resolveSelector(s, r.eval)
}

func resolveSelector(s *flow.Selector, eval func(string) (string, error)) (*match.Selector, error) {
	if s == nil {
		return nil, nil
	}
	out := &match.Selector{Width: s.Width, Height: s.Height, Tolerance: s.Tolerance, Enabled: s.Enabled,
		Selected: s.Selected, Checked: s.Checked, Focused: s.Focused, Traits: s.Traits}
	var err error
	if out.Text, err = eval(s.Text); err != nil {
		return nil, err
	}
	if out.ID, err = eval(s.ID); err != nil {
		return nil, err
	}
	if out.Index, err = eval(s.Index); err != nil {
		return nil, err
	}
	if out.CSS, err = eval(s.CSS); err != nil {
		return nil, err
	}
	for _, rel := range []struct {
		src *flow.Selector
		dst **match.Selector
	}{{s.Below, &out.Below}, {s.Above, &out.Above}, {s.LeftOf, &out.LeftOf}, {s.RightOf, &out.RightOf},
		{s.ContainsChild, &out.ContainsChild}, {s.ChildOf, &out.ChildOf}} {
		if rel.src != nil {
			if *rel.dst, err = resolveSelector(rel.src, eval); err != nil {
				return nil, err
			}
		}
	}
	for _, d := range s.ContainsDescendants {
		m, err := resolveSelector(d, eval)
		if err != nil {
			return nil, err
		}
		out.ContainsDescendants = append(out.ContainsDescendants, m)
	}
	return out, nil
}

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

func (r *Runner) find(ctx context.Context, sel *match.Selector, timeout time.Duration) (n *ui.Node, s *ui.Screen, waited bool, err error) {
	return r.findWithChangeRead(ctx, sel, timeout, false)
}

func (r *Runner) findWithChangeRead(ctx context.Context, sel *match.Selector, timeout time.Duration, firstChange bool) (n *ui.Node, s *ui.Screen, waited bool, err error) {
	deadline := time.Now().Add(timeout)
	w := r.watchChanges(true)
	defer w.Stop()
	answered := 0
	start, reads := time.Now(), 0
	final := false
	away := r.awayWatch()
	for {
		reads++
		s, err = r.readUntil(ctx, deadline)
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
		if away.seen(s) && time.Now().Before(deadline) {
			r.tracef("not found: %s, after %s and %d reads: %s for %s", sel.Describe(), since(start), reads, away.what, since(away.since))
			return nil, s, waited, &NotFound{FlowError: FlowError{Msg: "Element not found: " + sel.Describe()}, Screen: s, Query: sel.Text, away: away.message()}
		}
		if !time.Now().Before(deadline) {
			if r.stallGrace(&deadline, timeout, start) {
				r.invalidate()
				continue
			}
			if age := r.screenAge(); !final && age > restlessAfter && ci.On() {
				final = true
				r.tracef("deadline reached %s after the last read: reading the screen once more", age.Round(time.Millisecond))
				r.rereadUntil(ctx, deadline)
				continue
			}
			r.tracef("not found: %s, after %s and %d reads (screen: %s, %d elements, %s)",
				sel.Describe(), since(start), reads, s.Foreground(), len(s.Nodes), timeoutDesc(timeout))
			if sim := similarDesc(s, sel.Text); sim != "" {
				r.tracef("similar on screen: %s", sim)
			}
			return nil, s, waited, &NotFound{FlowError: FlowError{Msg: "Element not found: " + sel.Describe()}, Screen: s, Query: sel.Text, away: away.message()}
		}
		waited = true
		silence, err := w.Wait(ctx, s.Seq, away.wake(deadline), firstChange)
		firstChange = false
		if err != nil {
			return nil, s, waited, err
		}
		if away.due() && time.Now().Before(deadline) {
			r.invalidate()
			continue
		}
		if silence > 0 {
			r.tracef("no change reported by the device for %s while looking for %s: reading the screen again", silence, sel.Describe())
			before := s.Signature()
			if s2, err := r.rereadUntil(ctx, deadline); err == nil && s2.Signature() != before {
				r.silent(silence)
			}
		}
	}
}

func (r *Runner) silent(after time.Duration) {
	if r.warnedSilent {
		return
	}
	r.warnedSilent = true
	r.logLine(fmt.Sprintf(
		"the screen changed without the device reporting it (found by a re-read after %s of silence); waits on this app may be slower", after))
}

func (r *Runner) watchChanges(backoff bool) *changes.Watch {
	return changes.Subscribe(r.Drv, silentAfter, backoff)
}

func (r *Runner) findStable(ctx context.Context, sel *match.Selector, timeout time.Duration, settleTimeout time.Duration) (*ui.Node, *ui.Screen, error) {
	n, s, _, err := r.findStableAway(ctx, sel, timeout, settleTimeout)
	return n, s, err
}

func (r *Runner) findStableAway(ctx context.Context, sel *match.Selector, timeout time.Duration, settleTimeout time.Duration) (n *ui.Node, s *ui.Screen, away string, err error) {
	if settleTimeout <= 0 {
		settleTimeout = 2 * time.Second
	}
	span := restGap
	if r.unsettled > 0 {
		span = restSpanAfterAction
		t := time.Now()
		r.Drv.WaitSettled(ctx, max(settleTimeout, r.unsettled))
		r.unsettled = 0
		r.traceWait(t, "for the last action's change to settle")
	}
	n, s, waited, err := r.find(ctx, sel, timeout)
	if err != nil {
		return nil, s, "", err
	}
	r.fresh = true
	defer func() { r.fresh = false }()
	found := time.Now()
	from := n.Bounds
	full := n.Bounds
	t := time.Now()
	r.Drv.WaitSettled(ctx, settleTimeout)
	r.traceWait(t, "for a still screen")
	if waited || r.Drv.Seq() != s.Seq {
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err == nil {
			full = larger(full, n2.Bounds)
			if !sameElement(n, n2) {
				r.tracef("another match is picked now that the screen settled: %s (was %s)", nodeDesc(n2), nodeDesc(n))
				from, full = n2.Bounds, n2.Bounds
			} else if n2.Bounds != n.Bounds {
				r.tracef("it moved while the screen settled: %v -> %v", n.Bounds, n2.Bounds)
			} else if time.Since(found) >= span {
				return n2, s2, "", nil
			}
			n, s = n2, s2
		} else {
			r.tracef("it went while the screen settled (%v): acting on where it was", err)
		}
	}
	return r.atRest(ctx, sel, n, s, from, full, span)
}

func sameElement(a, b *ui.Node) bool {
	inList := false
	for p := a.Parent; p != nil; p = p.Parent {
		if p.Scrollable {
			inList = true
			break
		}
	}
	for first := true; a != nil && b != nil; a, b, first = a.Parent, b.Parent, false {
		if !first && a.Scrollable {
			inList = false
		}
		if a.Class != b.Class || a.ID != b.ID || (!inList && childIndex(a) != childIndex(b)) {
			return false
		}
	}
	return a == nil && b == nil
}

func childIndex(n *ui.Node) int {
	if n.Parent == nil {
		return -1
	}
	for i, c := range n.Parent.Children {
		if c == n {
			return i
		}
	}
	return -1
}

func cutOff(r, full ui.Rect) bool {
	if r.W() >= full.W() && r.H() >= full.H() {
		return false
	}
	dx, dy := r.CX()-full.CX(), r.CY()-full.CY()
	return dx > 2 || dx < -2 || dy > 2 || dy < -2
}

func larger(a, b ui.Rect) ui.Rect {
	if b.W() > a.W() {
		a.L, a.R = b.L, b.R
	}
	if b.H() > a.H() {
		a.T, a.B = b.T, b.B
	}
	return a
}

const (
	restGap             = 40 * time.Millisecond
	restWait            = 2 * time.Second
	restSpanAfterAction = 2 * restGap
)

type pulseWatch struct {
	slack      int
	first      ui.Rect
	dir        int
	ext        int
	turns      int
	maxW, maxH int
	minW, minH int
}

func pulseSlack(screen ui.Rect) int { return max(1, max(screen.W(), screen.H())/800) }

func (p *pulseWatch) turn() int { return 2 + 2*p.slack }

func (p *pulseWatch) reset(b ui.Rect) {
	*p = pulseWatch{slack: p.slack, first: b, ext: b.W() + b.H(), maxW: b.W(), maxH: b.H(), minW: b.W(), minH: b.H()}
}

func (p *pulseWatch) see(b ui.Rect) bool {
	off := func(a, c int) bool { return abs(a-c) > p.slack }
	if off(b.L+b.R, p.first.L+p.first.R) || off(b.T+b.B, p.first.T+p.first.B) {
		p.reset(b)
		return false
	}
	size := b.W() + b.H()
	switch {
	case p.dir == 0 && size-p.ext > p.turn():
		p.dir, p.ext = 1, size
	case p.dir == 0 && p.ext-size > p.turn():
		p.dir, p.ext = -1, size
	case p.dir != 0 && (size-p.ext)*p.dir > 0:
		p.ext = size
	case p.dir != 0 && (p.ext-size)*p.dir > p.turn():
		p.dir, p.ext = -p.dir, size
		p.turns++
	}
	p.maxW, p.maxH = max(p.maxW, b.W()), max(p.maxH, b.H())
	p.minW, p.minH = min(p.minW, b.W()), min(p.minH, b.H())
	return p.turns >= 2 && 2*p.minW >= p.maxW && 2*p.minH >= p.maxH
}

func (r *Runner) atRest(ctx context.Context, sel *match.Selector, n *ui.Node, s *ui.Screen, from, full ui.Rect, span time.Duration) (*ui.Node, *ui.Screen, string, error) {
	start := time.Now()
	same := start
	missing, cut := false, false
	full = larger(full, n.Bounds)
	w, h := r.Drv.Size()
	swing := pulseWatch{slack: pulseSlack(ui.Rect{R: w, B: h})}
	swing.reset(n.Bounds)
	for {
		select {
		case <-ctx.Done():
			return n, s, "", ctx.Err()
		case <-time.After(restGap):
		}
		r.invalidate()
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err != nil {
			if s2 != nil && awayDir(s, n, from, s2) != "" && historyContext(s) != historyContext(s2) {
				r.tracef("the target disappeared as the screen context changed; stopping motion recovery")
				return nil, s2, "", err
			}
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
				n, s = n2, s2
				same = time.Now()
				swing.reset(n2.Bounds)
				continue
			}
		}
		if !sameElement(n, n2) && time.Since(start) <= restWait {
			r.tracef("another match is picked now: %s (was %s)", nodeDesc(n2), nodeDesc(n))
			from, full = n2.Bounds, n2.Bounds
			n, s = n2, s2
			same = time.Now()
			swing.reset(n2.Bounds)
			continue
		}
		if n2.Bounds != n.Bounds && swing.see(n2.Bounds) {
			r.tracef("it pulses in place around (%d, %d): acting on its centre after %s, %v", n2.Bounds.CX(), n2.Bounds.CY(), since(start), n2.Bounds)
			return n2, s2, "", nil
		}
		full = larger(full, n2.Bounds)
		if n2.Bounds == n.Bounds && cutOff(n2.Bounds, full) && time.Since(start) <= restWait {
			if !cut {
				r.tracef("it is cut off at an edge while moving (%v, was %dx%d): looking again", n2.Bounds, full.W(), full.H())
				cut = true
			}
			continue
		}
		if n2.Bounds == n.Bounds && time.Since(same) < span && time.Since(start) <= restWait {
			continue
		}
		if n2.Bounds == n.Bounds && cutOff(n2.Bounds, full) {
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
		return ""
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

func appUnread(s *ui.Screen) bool {
	return len(s.Windows) == 0 || s.Unread > 0 && screenWindow(s) == nil
}

func (r *Runner) absent(ctx context.Context, sel *match.Selector, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	w := r.watchChanges(false)
	defer w.Stop()
	answered := 0
	start := time.Now()
	final := false
	var reloadSince time.Time
	reloadSaid := false
	blindSaid := false
	away := r.awayWatch()
	cannotCheck := func(s *ui.Screen) error {
		return &NotFound{FlowError: FlowError{Msg: "Could not check that " + sel.Describe() + " is not visible"},
			Screen: s, Query: sel.Text, away: away.message()}
	}
	for {
		s, err := r.readUntil(ctx, deadline)
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
		if away.seen(s) && time.Now().Before(deadline) {
			r.tracef("not checked: %s, after %s: %s for %s", sel.Describe(), since(start), away.what, since(away.since))
			return false, cannotCheck(s)
		}
		blind := n == nil && appUnread(s)
		if blind && !blindSaid {
			blindSaid = true
			r.tracef("not judged: %s is not visible on a read with no app window (%d unread): waiting for the app's window", sel.Describe(), s.Unread)
		}
		loading := ""
		if n == nil && !away.active() && !blind && time.Now().Before(deadline) {
			if loading = r.reloading(s); loading != "" {
				if reloadSince.IsZero() {
					reloadSince = time.Now()
					r.tracef("not judged yet: %s is not visible on a new screen that %s; waiting up to %s for its content",
						sel.Describe(), strings.TrimPrefix(loading, "it "), reloadWait)
				} else if time.Since(reloadSince) >= reloadWait {
					r.tracef("still no content after %s (%s): judging the screen as it is", since(reloadSince), loading)
					r.reloadJudged()
					loading = ""
				}
			} else if !reloadSince.IsZero() && !reloadSaid {
				reloadSaid = true
				r.tracef("the new screen showed its content after %s", since(reloadSince))
			}
		}
		if n == nil && (away.active() || blind) {
		} else if loading != "" {
		} else if n == nil {
			left := time.Until(deadline)
			r.Drv.WaitSettled(ctx, max(min(left, 2*time.Second), 0))
			if r.Drv.Seq() == s.Seq {
				r.tracef("gone: %s (checked on a settled screen, %s)", sel.Describe(), since(start))
				return true, nil
			}
			s2, err := r.readUntil(ctx, deadline)
			if err != nil {
				return false, err
			}
			if r.reloading(s2) != "" && time.Now().Before(deadline) {
				continue
			}
			if n2, _, err := r.match(ctx, s2, sel); err == nil && n2 == nil && !r.isAway(s2) && !appUnread(s2) {
				r.tracef("gone: %s (%s)", sel.Describe(), since(start))
				return true, nil
			}
		}
		if !time.Now().Before(deadline) {
			if r.stallGrace(&deadline, timeout, start) {
				r.invalidate()
				continue
			}
			if age := r.screenAge(); !final && age > restlessAfter && ci.On() {
				final = true
				r.tracef("deadline reached %s after the last read: reading the screen once more", age.Round(time.Millisecond))
				r.rereadUntil(ctx, deadline)
				continue
			}
			if n == nil && away.active() {
				r.tracef("not checked after %s: %s", since(start), away.what)
				return false, cannotCheck(s)
			}
			if blind {
				r.tracef("not checked after %s: no app window could be read", since(start))
				return false, cannotCheck(s)
			}
			r.tracef("still there after %s: %s", since(start), nodeDesc(n))
			return false, nil
		}
		wake := away.wake(deadline)
		if loading != "" {
			wake = minTime(wake, reloadSince.Add(reloadWait))
		}
		silence, err := w.Wait(ctx, s.Seq, wake, false)
		if err != nil {
			return false, err
		}
		if silence > 0 {
			r.tracef("no change reported by the device for %s while waiting for %s to go: reading the screen again", silence, sel.Describe())
			if s2, err := r.rereadUntil(ctx, deadline); err == nil && s2.Signature() != s.Signature() {
				r.silent(silence)
			}
		} else if away.due() {
			r.invalidate()
		}
	}
}

func (r *Runner) evalCondition(ctx context.Context, c *flow.Condition, timeout time.Duration) (bool, error) {
	return r.condition(ctx, c, timeout, false)
}

func (r *Runner) assertCondition(ctx context.Context, c *flow.Condition, timeout time.Duration) (bool, error) {
	return r.condition(ctx, c, timeout, true)
}

func (r *Runner) condition(ctx context.Context, c *flow.Condition, timeout time.Duration, strict bool) (bool, error) {
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
		r.verdictRead()
		_, _, _, err = r.findWithChangeRead(ctx, sel, timeout, true)
		var nf *NotFound
		if errors.As(err, &nf) {
			return false, awayErr(nf, strict)
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
		r.verdictRead()
		ok, err := r.absent(ctx, sel, timeout)
		var nf *NotFound
		if errors.As(err, &nf) {
			return false, awayErr(nf, strict)
		}
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func awayErr(nf *NotFound, strict bool) error {
	if strict && nf.away != "" {
		return nf
	}
	return nil
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

type CSSQuerier interface {
	QueryCSS(ctx context.Context, css string) ([]int, error)
}

func (r *Runner) Locate(ctx context.Context, s *ui.Screen, selector *flow.Selector) (*ui.Node, []*ui.Node, error) {
	sel, err := resolveSelector(selector, func(value string) (string, error) {
		if strings.Contains(value, "${") {
			return "", errors.New("use a resolved selector for inspection")
		}
		return value, nil
	})
	if err != nil || sel == nil {
		return nil, nil, err
	}
	return r.match(ctx, s, sel)
}

func (r *Runner) match(ctx context.Context, s *ui.Screen, sel *match.Selector) (*ui.Node, []*ui.Node, error) {
	q, canCSS := r.Drv.(CSSQuerier)
	var cssErr error
	sel.Walk(func(x *match.Selector) {
		if x.CSS == "" || cssErr != nil {
			return
		}
		x.CSSNodes = nil
		if !canCSS {
			return
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

func timeoutDesc(t time.Duration) string {
	if t <= 0 {
		return "no wait: checked once"
	}
	return fmt.Sprintf("allowed %s", t.Round(time.Millisecond))
}
