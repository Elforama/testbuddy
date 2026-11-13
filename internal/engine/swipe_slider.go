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
// from its thumb to the same end, at most sliderRedrags times. A long stall loses the whole
// drag as often as part of it (measured with the app stopped 2.5 s from 0.15 s into the swipe:
// 10 of 10 swipes left the bench slider at 50, where it started), so a first drag that left the
// slider where it was is dragged again too, once.
//
// Not dragged again (review of #139): a slider the drag moved the wrong way (right to left
// layout: the thumb is not where the value puts it), a disabled one, one a drag again left
// where it was (an app that clamps the value, a slider with few steps), one under another
// window or a cover that came up since, and a thumb near the screen's edge (a touch there can
// be the system's back swipe).

// sliderRedrags is how many times a slider left short is dragged again.
const sliderRedrags = 2

// sliderShortFrac is how far short of the drag's end (a fraction of the track) a slider may stop
// before it is dragged again: past half a step of a slider with 3 or more steps (1/6), short of
// the measured stalls' 21-50 %.
const sliderShortFrac = 0.18

// sliderEdgePt: a drag again never starts this close to the screen's left or right edge.
const sliderEdgePt = 20

// sliderDrag is a swipe on a slider's thumb: the slider and the screen as they were before, and
// the swipe.
type sliderDrag struct {
	slider         *ui.Node
	before         *ui.Screen
	x1, y1, x2, y2 int
	dur            time.Duration
	opts           driver.ActionOpts
}

// alongSlider reports whether a swipe goes mostly sideways, along a (horizontal) slider.
func alongSlider(x1, y1, x2, y2 int) bool {
	dx, dy := x2-x1, y2-y1
	return dx != 0 && abs(dx) > abs(dy)
}

// isSlider reports whether n is an enabled horizontal slider whose value reads as a percentage.
func isSlider(n *ui.Node) bool {
	if n == nil || n.Class != "Slider" || !n.Enabled || n.Bounds.W() <= n.Bounds.H() {
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
	prev, _ := sliderValue(sd.slider)
	for i := 0; ; i++ {
		s, err := r.read(ctx)
		if err != nil {
			return nil // the next step reads again and says what is wrong
		}
		n := sameSlider(s, sd.slider)
		if n == nil || !sameTop(sd.before, s) {
			return nil
		}
		f, _ := sliderValue(n)
		at := thumbX(n)
		short := dir * (end - at) / (hi - lo)
		moved := dir * (f - prev)
		tx, ty := int(at+0.5), n.Bounds.CY()
		switch {
		case short <= sliderShortFrac:
			return nil
		case moved < 0, moved == 0 && i > 0:
			// The wrong way (the thumb is not where the value says), or a drag again changed
			// nothing: the app holds it there.
			return nil
		case i == sliderRedrags:
			r.tracef("slider %s at %.0f%%, still short of the drag's end after %d more drags", n.ID, f*100, sliderRedrags)
			return nil
		case tx < sliderEdgePt || tx >= s.Width-sliderEdgePt, coverAt(s, n, tx, ty) != nil:
			return nil
		}
		prev = f
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
}

// sameSlider is the slider on s that was swiped (same id and place, still on screen), or nil.
func sameSlider(s *ui.Screen, was *ui.Node) *ui.Node {
	for _, c := range match.Visible(s) {
		if isSlider(c) && c.Bounds == was.Bounds && c.ID == was.ID {
			return c
		}
	}
	return nil
}

// sameTop reports whether s's top window is the one before the swipe (no sheet, alert or other
// window came up over the slider). With no screen from before, it is taken as the same.
func sameTop(before, s *ui.Screen) bool {
	if before == nil || len(before.Windows) == 0 || len(s.Windows) == 0 {
		return true
	}
	a, b := before.Windows[0], s.Windows[0]
	return a.Type == b.Type && a.Package == b.Package && len(before.Windows) == len(s.Windows)
}
