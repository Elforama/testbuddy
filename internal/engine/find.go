package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
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
		quiet, err := r.waitChange(ctx, events, s.Seq, deadline, silence)
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

// waitChange blocks until the screen may differ from the read at seq: a "settled" event
// newer than seq, or changes that have gone on for restlessAfter without settling, or the
// deadline. It does not touch the device. quiet reports that the silence safety net fired:
// no event at all arrived for `silence`.
func (r *Runner) waitChange(ctx context.Context, events <-chan driver.Event, seq int64, deadline time.Time, silence time.Duration) (quiet bool, err error) {
	dl := time.NewTimer(time.Until(deadline))
	defer dl.Stop()
	var restless <-chan time.Time
	if r.Drv.Seq() > seq {
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
	if settleTimeout <= 0 {
		settleTimeout = 2 * time.Second
	}
	if r.unsettled > 0 {
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
		return nil, s, err
	}
	t := time.Now()
	r.Drv.WaitSettled(ctx, settleTimeout)
	r.traceWait(t, "for a still screen")
	if waited || r.Drv.Seq() != s.Seq {
		n2, s2, _, err := r.find(ctx, sel, 0)
		if err == nil {
			if n2.Bounds != n.Bounds {
				r.tracef("it moved while the screen settled: %v -> %v", n.Bounds, n2.Bounds)
			}
			return n2, s2, nil
		}
		r.tracef("it went while the screen settled (%v): acting on where it was", err)
	}
	return n, s, nil
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
		if quiet, err := r.waitChange(ctx, events, s.Seq, deadline, silentAfter); err != nil {
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
		_, _, _, err = r.find(ctx, sel, timeout)
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
