package engine

import (
	"context"
	"fmt"
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
	var from *ui.Node
	var fromScreen *ui.Screen
	switch {
	case c.From != nil:
		sel, err := r.resolveSelector(c.From)
		if err != nil {
			return err
		}
		n, fs, err := r.findStable(ctx, sel, r.lookupTimeout(c.Optional), 0)
		if err != nil {
			return err
		}
		from, fromScreen = n, fs
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
	var slider *ui.Node
	var before *ui.Screen
	if ci.On() && d.Platform() == "ios" && alongSlider(x1, y1, x2, y2) {
		switch {
		case from != nil:
			if (c.Direction == "LEFT" || c.Direction == "RIGHT") && isSlider(from) && onThumb(from, x1, y1) {
				slider, before = from, fromScreen
			}
		case c.Direction == "":
			if s, err := r.read(ctx); err == nil {
				slider, before = sliderAt(s, x1, y1), s
			}
		}
		if slider != nil {
			opts.Rest = true
		}
	}
	r.invalidate()
	r.mark("swipe", [2]int{x1, y1}, [2]int{x2, y2})
	res, err := d.Swipe(ctx, x1, y1, x2, y2, dur, opts)
	if err != nil {
		r.tracef("swipe (%d, %d) -> (%d, %d) failed: %v", x1, y1, x2, y2, err)
		return err
	}
	r.tracef("swipe (%d, %d) -> (%d, %d) in %d ms: %s", x1, y1, x2, y2, dur.Milliseconds(), actionDesc(res))
	if slider != nil {
		return r.followSlider(ctx, sliderDrag{slider: slider, before: before, x1: x1, y1: y1, x2: x2, y2: y2, dur: dur, opts: opts})
	}
	return nil
}

var dragHoldPlatforms = map[string]bool{"ios": true, "android": true}

func (r *Runner) drag(ctx context.Context, c *flow.Command) error {
	d := r.Drv
	if !dragHoldPlatforms[d.Platform()] {
		return fmt.Errorf("drag is not supported on %s yet: it needs a finger held still before it moves", d.Platform())
	}
	w, h := d.Size()
	screen := ui.Rect{R: w, B: h}
	end := func(sel *flow.Selector, pt string) (int, int, error) {
		if sel == nil {
			p, err := r.eval(pt)
			if err != nil {
				return 0, 0, err
			}
			return r.point(p, screen, false)
		}
		s, err := r.resolveSelector(sel)
		if err != nil {
			return 0, 0, err
		}
		n, _, err := r.findStable(ctx, s, r.lookupTimeout(c.Optional), 0)
		if err != nil {
			return 0, 0, err
		}
		return n.Bounds.CX(), n.Bounds.CY(), nil
	}
	x1, y1, err := end(c.From, c.Start)
	if err != nil {
		return err
	}
	x2, y2, err := end(c.To, c.End)
	if err != nil {
		return err
	}
	clamp := func(v, hi int) int { return max(0, min(hi-1, v)) }
	x1, y1, x2, y2 = clamp(x1, w), clamp(y1, h), clamp(x2, w), clamp(y2, h)
	hold := time.Duration(c.Hold) * time.Millisecond
	dur := time.Duration(c.Duration) * time.Millisecond
	opts := driver.ActionOpts{Hold: max(hold, time.Millisecond)}
	if c.WaitToSettleTimeoutMs > 0 {
		opts.Timeout = time.Duration(c.WaitToSettleTimeoutMs) * time.Millisecond
	}
	r.invalidate()
	r.mark("swipe", [2]int{x1, y1}, [2]int{x2, y2})
	res, err := d.Swipe(ctx, x1, y1, x2, y2, dur, opts)
	if err != nil {
		r.tracef("drag (%d, %d) -> (%d, %d) failed: %v", x1, y1, x2, y2, err)
		return err
	}
	r.tracef("drag (%d, %d) -> (%d, %d): held %d ms, moved in %d ms: %s", x1, y1, x2, y2, hold.Milliseconds(), dur.Milliseconds(), actionDesc(res))
	return nil
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
	default:
		return 0.9, 0.5, 0.1, 0.5
	}
}
