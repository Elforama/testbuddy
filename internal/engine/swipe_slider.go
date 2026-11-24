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

const sliderRedrags = 2

const sliderShortFrac = 0.18

const sliderEdgePt = 20

type sliderDrag struct {
	slider         *ui.Node
	before         *ui.Screen
	x1, y1, x2, y2 int
	dur            time.Duration
	opts           driver.ActionOpts
}

func alongSlider(x1, y1, x2, y2 int) bool {
	dx, dy := x2-x1, y2-y1
	return dx != 0 && abs(dx) > abs(dy)
}

func isSlider(n *ui.Node) bool {
	if n == nil || n.Class != "Slider" || !n.Enabled || n.Bounds.W() <= n.Bounds.H() {
		return false
	}
	_, ok := sliderValue(n)
	return ok
}

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

func track(n *ui.Node) (lo, hi float64) {
	inset := float64(n.Bounds.H()) / 2
	return float64(n.Bounds.L) + inset, float64(n.Bounds.R) - inset
}

func thumbX(n *ui.Node) float64 {
	f, _ := sliderValue(n)
	lo, hi := track(n)
	return lo + f*(hi-lo)
}

func onThumb(n *ui.Node, x, y int) bool {
	d := float64(x) - thumbX(n)
	r := float64(n.Bounds.H())
	return d >= -r && d <= r
}

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
			return nil
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

func sameSlider(s *ui.Screen, was *ui.Node) *ui.Node {
	for _, c := range match.Visible(s) {
		if isSlider(c) && c.Bounds == was.Bounds && c.ID == was.ID {
			return c
		}
	}
	return nil
}

func sameTop(before, s *ui.Screen) bool {
	if before == nil || len(before.Windows) == 0 || len(s.Windows) == 0 {
		return true
	}
	a, b := before.Windows[0], s.Windows[0]
	return a.Type == b.Type && a.Package == b.Package && len(before.Windows) == len(s.Windows)
}
