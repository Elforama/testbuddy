package engine

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// A swipe that drags a slider's thumb, in CI mode on iOS (issue #139): the slider is where the
// swipe starts, and the thumb under the finger.
//
// Between two points (item 1): a swipe given as start and end points lifts while moving, so it
// can fling what it moves; it rests and creeps (driver.ActionOpts.Rest, issues #127 and #182)
// only when it starts on a slider's thumb, which it places. Measured on iOS 27 with the app
// stopped 0.9 s of every 1.2 s: 5 of 10 such drags left the slider at 59-66 instead of 98.
//
// After the swipe (item 2): the creep outlasts an app stall of about a second. A longer one
// still gets the moves as one batch with the lift, and the slider stops short of the finger
// (measured: the app stopped 2 s of every 2.3 s). Timing cannot cover every stall, so the
// engine checks: a slider whose thumb stopped short of where the drag ended is dragged again,
// from its thumb to the same end, at most sliderRedrags times, and not again once a drag left
// it where it was.

// sliderRedrags is how many times a slider left short is dragged again.
const sliderRedrags = 2

// sliderShortFrac is how far short of the drag's end (a fraction of the track) a slider may stop
// before it is dragged again: past half a step of a slider with 3 or more steps, well short of
// the stalls' 25-45 %.
const sliderShortFrac = 0.15

// sliderDrag is a swipe on a slider's thumb: the slider as it was before, and the swipe.
type sliderDrag struct {
	slider         *ui.Node
	x1, y1, x2, y2 int
	dur            time.Duration
	opts           driver.ActionOpts
}

// isSlider reports whether n is a horizontal slider whose value reads as a percentage.
func isSlider(n *ui.Node) bool {
	if n == nil || n.Class != "Slider" || n.Bounds.W() <= n.Bounds.H() {
		return false
	}
	_, ok := sliderValue(n)
	return ok
}

// sliderAt is the slider at (x, y) on s whose thumb is there, if any.
func sliderAt(s *ui.Screen, x, y int) *ui.Node {
	if s == nil {
		return nil
	}
	for _, n := range match.Visible(s) {
		if isSlider(n) && n.Bounds.Contains(x, y) && onThumb(n, x, y) {
			return n
		}
	}
	return nil
}

// sliderValue is n's value as a fraction of its range: iOS reports a slider's as "66%".
func sliderValue(n *ui.Node) (float64, bool) {
	v := strings.TrimSpace(n.Value)
	if v == "" {
		v = strings.TrimSpace(n.Text)
	}
	num, ok := strings.CutSuffix(v, "%")
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil || f < 0 || f > 100 {
		return 0, false
	}
	return f / 100, true
}

// track is the span of x the thumb's centre covers: the slider less half a thumb (about as
// wide as the slider is tall) at each end.
func track(n *ui.Node) (lo, hi float64) {
	inset := float64(n.Bounds.H()) / 2
	return float64(n.Bounds.L) + inset, float64(n.Bounds.R) - inset
}

// thumbX is where n's thumb is, from its value.
func thumbX(n *ui.Node) float64 {
	f, _ := sliderValue(n)
	lo, hi := track(n)
	return lo + f*(hi-lo)
}

// onThumb reports whether (x, y) is on n's thumb: a slider takes a drag only there.
func onThumb(n *ui.Node, x, y int) bool {
	d := float64(x) - thumbX(n)
	r := float64(n.Bounds.H())
	return d >= -r && d <= r
}

// followSlider drags a slider left short of where the drag ended again, from its thumb (see
// above).
func (r *Runner) followSlider(ctx context.Context, sd sliderDrag) error {
	lo, hi := track(sd.slider)
	end := min(max(float64(sd.x2), lo), hi)
	dir := 1.0
	if sd.x2 < sd.x1 {
		dir = -1
	}
	prev := -1.0
	for i := 0; i <= sliderRedrags; i++ {
		s, err := r.read(ctx)
		if err != nil {
			return nil // the next step reads again and says what is wrong
		}
		var n *ui.Node
		for _, c := range s.Nodes {
			if isSlider(c) && c.Bounds == sd.slider.Bounds && c.ID == sd.slider.ID {
				n = c
				break
			}
		}
		if n == nil {
			return nil
		}
		f, _ := sliderValue(n)
		at := thumbX(n)
		short := dir * (end - at) / (hi - lo)
		if short <= sliderShortFrac || f == prev {
			return nil
		}
		if i == sliderRedrags {
			r.tracef("slider %s at %.0f%%, still short of the drag's end after %d more drags", n.ID, f*100, sliderRedrags)
			return nil
		}
		prev = f
		tx, ty := int(at+0.5), n.Bounds.CY()
		r.tracef("slider %s at %.0f%% stopped short of the drag's end (x %d): dragging again from its thumb (%d, %d) (issue #139)", n.ID, f*100, sd.x2, tx, ty)
		r.invalidate()
		r.mark("swipe", [2]int{tx, ty}, [2]int{sd.x2, sd.y2})
		res, err := r.Drv.Swipe(ctx, tx, ty, sd.x2, sd.y2, sd.dur, sd.opts)
		if err != nil {
			r.tracef("swipe (%d, %d) -> (%d, %d) failed: %v", tx, ty, sd.x2, sd.y2, err)
			return err
		}
		r.tracef("swipe (%d, %d) -> (%d, %d) in %d ms: %s", tx, ty, sd.x2, sd.y2, sd.dur.Milliseconds(), actionDesc(res))
	}
	return nil
}
