package engine

import (
	"context"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

func (r *Runner) swipe(ctx context.Context, c *flow.Command) error {
	d := r.Drv
	w, h := d.Size()
	screen := ui.Rect{R: w, B: h}
	dur := time.Duration(c.Duration) * time.Millisecond
	opts := driver.ActionOpts{}
	if c.WaitToSettleTimeoutMs > 0 {
		opts.Timeout = time.Duration(c.WaitToSettleTimeoutMs) * time.Millisecond
	}
	var x1, y1, x2, y2 int
	switch {
	case c.From != nil:
		sel, err := r.resolveSelector(c.From)
		if err != nil {
			return err
		}
		n, _, err := r.findStable(ctx, sel, r.lookupTimeout(c.Optional), 0)
		if err != nil {
			return err
		}
		x1, y1 = n.Bounds.CX(), n.Bounds.CY()
		if c.Point != "" {
			p, err := r.eval(c.Point)
			if err != nil {
				return err
			}
			if x1, y1, err = r.point(p, n.Bounds, true); err != nil {
				return err
			}
		}
		// To near the screen's edge, and at least 70% of the element's size: a pager turns
		// a page only for a swipe past its middle (or a fast one), and one from its centre to
		// 10% of the screen falls short when the pager has margins.
		x2, y2 = x1, y1
		bw, bh := n.Bounds.W()*7/10, n.Bounds.H()*7/10
		switch c.Direction {
		case "UP":
			y2 = max(0, min(h/10, y1-bh))
		case "DOWN":
			y2 = min(h-1, max(h*9/10, y1+bh))
		case "LEFT":
			x2 = max(0, min(w/10, x1-bw))
		case "RIGHT":
			x2 = min(w-1, max(w*9/10, x1+bw))
		}
		// A CI allowance (package ci): it places what it starts on (a slider's thumb, a page, a
		// row), and on a slow CI runner an app that is behind can get the last moves in one
		// batch with the lift and settle short (issue #127: a slider at 71 instead of 80+). In
		// CI the finger rests before it moves and before it lifts (+400 ms on iOS); elsewhere
		// the swipe is as before.
		opts.Rest = ci.On()
	case c.Direction != "":
		fx1, fy1, fx2, fy2 := directionSwipe(c.Direction, d.Platform())
		x1, y1, x2, y2 = int(fx1*float64(w)), int(fy1*float64(h)), int(fx2*float64(w)), int(fy2*float64(h))
	default:
		start, err := r.eval(c.Start)
		if err != nil {
			return err
		}
		end, err := r.eval(c.End)
		if err != nil {
			return err
		}
		if x1, y1, err = r.point(start, screen, false); err != nil {
			return err
		}
		if x2, y2, err = r.point(end, screen, false); err != nil {
			return err
		}
	}
	if d.Platform() == "ios" {
		clamp := func(v, hi int) int { return max(0, min(hi-1, v)) }
		x1, y1, x2, y2 = clamp(x1, w), clamp(y1, h), clamp(x2, w), clamp(y2, h)
	}
	r.invalidate()
	r.mark("swipe", [2]int{x1, y1}, [2]int{x2, y2})
	res, err := d.Swipe(ctx, x1, y1, x2, y2, dur, opts)
	if err != nil {
		r.tracef("swipe (%d, %d) -> (%d, %d) failed: %v", x1, y1, x2, y2, err)
	} else {
		r.tracef("swipe (%d, %d) -> (%d, %d) in %d ms: %s", x1, y1, x2, y2, dur.Milliseconds(), actionDesc(res))
	}
	return err
}

func directionSwipe(dir, platform string) (x1, y1, x2, y2 float64) {
	switch dir {
	case "UP":
		if platform == "ios" {
			return 0.5, 0.9, 0.5, 0.1
		}
		return 0.5, 0.5, 0.5, 0.1
	case "DOWN":
		return 0.5, 0.2, 0.5, 0.9
	case "RIGHT":
		return 0.1, 0.5, 0.9, 0.5
	default: // LEFT
		return 0.9, 0.5, 0.1, 0.5
	}
}
