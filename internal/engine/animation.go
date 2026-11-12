package engine

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const (
	// The screen counts as still once it has not changed for this long.
	animationStableFor = 250 * time.Millisecond
	// A part of the screen that has kept moving for this long is a loop (a spinner, a
	// marquee, a pulsing button): it is not going to stop, so it is not waited for. A
	// one-shot animation shorter than this is always waited out.
	loopAfter = time.Second
	// A part counts as still moving while it changed within this long: a loop's slow
	// moments (the ends of a pulse) do not make it a new motion.
	loopGap = 400 * time.Millisecond
	// Loops are set aside only while all of them together cover at most this share of the
	// screen. Past it the screen is really still moving (a large panel repainting), and
	// the wait runs to its timeout.
	maxLoopArea = 0.10
	// The loop grid has this many cells across (a cell is about 17 points on a phone).
	loopColumns = 24
)

// Accessibility events cannot see animations that only repaint pixels. Sample small
// screenshots for this explicit mobile command after the usual event-driven settle.
//
// Parts that never stop moving (issue #168: a spinner, a marquee, a pulsing button) are set
// aside once they have moved for loopAfter and while they stay small; the rest of the
// screen must then be still for animationStableFor.
func waitForAnimation(ctx context.Context, d driver.Driver, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_ = d.WaitSettled(ctx, timeout)
	if d.Platform() != "android" && d.Platform() != "ios" {
		return
	}

	var recent []sample // the samples of the last animationStableFor and the one before
	var loops *loopTracker
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		data, err := d.Screenshot(ctx, 0.15, "png")
		if err != nil {
			return
		}
		frame, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		now := time.Now()
		if len(recent) == 0 || recent[len(recent)-1].frame.Bounds() != frame.Bounds() {
			recent, loops = nil, newLoopTracker(frame.Bounds())
		} else {
			loops.observe(recent[len(recent)-1].frame, frame, now)
		}
		recent = append(recent, sample{now, frame})
		for len(recent) > 1 && now.Sub(recent[1].at) >= animationStableFor {
			recent = recent[1:]
		}
		if stillSince(recent, loops.mask(now)) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type sample struct {
	at    time.Time
	frame image.Image
}

// stillSince reports whether the screen, loops aside, has not changed for
// animationStableFor: since recent[0], at least that old, every later sample looks like it.
// Loops found just now apply to the samples before too, so a screen whose only motion turns
// out to be a loop is still at once.
func stillSince(recent []sample, inLoop func(x, y int) bool) bool {
	last := recent[len(recent)-1]
	if last.at.Sub(recent[0].at) < animationStableFor {
		return false
	}
	for _, s := range recent[1:] {
		if frameChanged(recent[0].frame, s.frame, inLoop) {
			return false
		}
	}
	return true
}

// loopTracker follows, per cell of a coarse grid, how long that part of the screen has
// kept changing from one sample to the next.
type loopTracker struct {
	bounds image.Rectangle
	size   int // a cell's side, in pixels
	cols   int
	since  []time.Time // when the cell's current run of changes began
	last   []time.Time // when the cell last changed (zero: never)
}

func newLoopTracker(bounds image.Rectangle) *loopTracker {
	size := max(2, (bounds.Dx()+loopColumns-1)/loopColumns)
	cols := (bounds.Dx() + size - 1) / size
	rows := (bounds.Dy() + size - 1) / size
	return &loopTracker{bounds: bounds, size: size, cols: cols,
		since: make([]time.Time, cols*rows), last: make([]time.Time, cols*rows)}
}

// observe records which cells changed between two consecutive samples.
func (l *loopTracker) observe(before, after image.Image, now time.Time) {
	changed := make([]bool, len(l.last))
	b := l.bounds
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := l.cell(x, y)
			if !changed[i] && pixelChanged(before.At(x, y), after.At(x, y)) {
				changed[i] = true
			}
		}
	}
	for i, c := range changed {
		if !c {
			continue
		}
		if l.last[i].IsZero() || now.Sub(l.last[i]) > loopGap {
			l.since[i] = now
		}
		l.last[i] = now
	}
}

// mask reports whether a pixel is in a cell that is looping now; nil when no cell is, or
// when together they are too large to be set aside.
func (l *loopTracker) mask(now time.Time) func(x, y int) bool {
	var mask []bool
	n := 0
	for i := range l.last {
		if l.last[i].IsZero() || now.Sub(l.last[i]) > loopGap || l.last[i].Sub(l.since[i]) < loopAfter {
			continue
		}
		if mask == nil {
			mask = make([]bool, len(l.last))
		}
		mask[i] = true
		n++
	}
	if mask == nil || float64(n) > maxLoopArea*float64(len(l.last)) {
		return nil
	}
	return func(x, y int) bool { return mask[l.cell(x, y)] }
}

func (l *loopTracker) cell(x, y int) int {
	return (y-l.bounds.Min.Y)/l.size*l.cols + (x-l.bounds.Min.X)/l.size
}

// Ignore small pixel differences and incidental changes such as the clock or caret, and the
// pixels in loops (inLoop nil: none).
func frameChanged(before, after image.Image, inLoop func(x, y int) bool) bool {
	bounds := before.Bounds()
	if bounds != after.Bounds() {
		return true
	}
	minimum := max(1, bounds.Dx()*bounds.Dy()/100)
	changed := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if inLoop != nil && inLoop(x, y) {
				continue
			}
			if pixelChanged(before.At(x, y), after.At(x, y)) {
				changed++
				if changed >= minimum {
					return true
				}
			}
		}
	}
	return false
}

func pixelChanged(before, after interface{ RGBA() (r, g, b, a uint32) }) bool {
	r1, g1, b1, _ := before.RGBA()
	r2, g2, b2, _ := after.RGBA()
	return channelDelta(r1, r2)+channelDelta(g1, g2)+channelDelta(b1, b2) > 12*257
}

func channelDelta(a, b uint32) uint32 {
	return max(a, b) - min(a, b)
}
