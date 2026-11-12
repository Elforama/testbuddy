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
	// one-shot animation shorter than this (a fade, a spring, a slow reveal) is always
	// waited out.
	loopAfter = 3 * time.Second
	// A part counts as still moving while it changed within this long (or within two
	// samples, when screenshots are slower): a loop's slow moments, such as the ends of a
	// pulse, do not make it a new motion.
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
	waitForAnimationOn(ctx, d, timeout, realClock{})
}

// waitForAnimationOn is waitForAnimation on a given clock: the tests' fake one lets them
// check the timings exactly, however loaded the machine running them (issue #188).
func waitForAnimationOn(ctx context.Context, d driver.Driver, timeout time.Duration, clock animationClock) {
	ctx, cancel := clock.WithTimeout(ctx, timeout)
	defer cancel()
	_ = d.WaitSettled(ctx, timeout)
	if d.Platform() != "android" && d.Platform() != "ios" {
		return
	}

	// The samples of the last animationStableFor and the one before it: recent[0] is the
	// newest sample at least that old (or the oldest there is).
	var recent []sample
	var loops *loopTracker
	ticker := clock.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		data, err := d.Screenshot(ctx, 0.15, "png")
		if err != nil {
			return
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		now := clock.Now()
		f := pixelsOf(img)
		if len(recent) == 0 || recent[0].frame.w != f.w || recent[0].frame.h != f.h {
			recent, loops = nil, newLoopTracker(f.w, f.h)
		}
		recent = append(recent, sample{now, f})
		for len(recent) > 1 && now.Sub(recent[1].at) >= animationStableFor {
			recent = recent[1:]
		}
		if n := len(recent); n > 1 {
			// Over the same span as the stillness check: a slow loop (a breathing glow)
			// may change too little from one sample to the next to count.
			loops.observe(recent[0].frame, f, now, now.Sub(recent[n-2].at))
		}
		if loops.still(recent) {
			return
		}
		if !ticker.Wait(ctx) {
			return
		}
	}
}

// animationClock is the time waitForAnimationOn runs on.
type animationClock interface {
	Now() time.Time
	WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc)
	NewTicker(d time.Duration) animationTicker
}

// animationTicker ticks every period and, as a time.Ticker does, keeps at most one tick
// not yet waited for.
type animationTicker interface {
	// Wait waits for the next tick. It is false when ctx ends first.
	Wait(ctx context.Context) bool
	Stop()
}

// realClock is the time of day, with the standard library's timeouts and tickers.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func (realClock) NewTicker(d time.Duration) animationTicker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) Wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type sample struct {
	at    time.Time
	frame frame
}

// frame is a screenshot's pixels, 3 bytes (red, green, blue) each, row by row.
type frame struct {
	w, h int
	rgb  []byte
}

// pixelsOf copies an image's pixels into a frame. Screenshots are opaque, so the RGBA and
// NRGBA images PNG decodes to are read directly.
func pixelsOf(img image.Image) frame {
	b := img.Bounds()
	f := frame{w: b.Dx(), h: b.Dy(), rgb: make([]byte, b.Dx()*b.Dy()*3)}
	var pix []byte
	var stride int
	switch m := img.(type) {
	case *image.RGBA:
		pix, stride = m.Pix, m.Stride
	case *image.NRGBA:
		pix, stride = m.Pix, m.Stride
	}
	o := 0
	for y := 0; y < f.h; y++ {
		if pix != nil {
			row := pix[y*stride : y*stride+f.w*4]
			for x := 0; x < len(row); x += 4 {
				f.rgb[o], f.rgb[o+1], f.rgb[o+2] = row[x], row[x+1], row[x+2]
				o += 3
			}
			continue
		}
		for x := 0; x < f.w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			f.rgb[o], f.rgb[o+1], f.rgb[o+2] = byte(r>>8), byte(g>>8), byte(bl>>8)
			o += 3
		}
	}
	return f
}

// loopTracker follows, per cell of a coarse grid, how long that part of the screen has
// kept changing.
type loopTracker struct {
	cellOf  []int32     // per pixel, its cell
	since   []time.Time // when the cell's current run of changes began
	last    []time.Time // when the cell last changed (zero: never)
	changed []bool      // scratch for observe
	mask    []bool      // scratch for still
}

func newLoopTracker(w, h int) *loopTracker {
	size := max(2, (w+loopColumns-1)/loopColumns)
	cols, rows := (w+size-1)/size, (h+size-1)/size
	l := &loopTracker{cellOf: make([]int32, w*h), since: make([]time.Time, cols*rows),
		last: make([]time.Time, cols*rows), changed: make([]bool, cols*rows),
		mask: make([]bool, cols*rows)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			l.cellOf[y*w+x] = int32(y/size*cols + x/size)
		}
	}
	return l
}

// observe records which cells differ between before and after, the sample taken now, the
// last one interval after the sample before it.
func (l *loopTracker) observe(before, after frame, now time.Time, interval time.Duration) {
	clear(l.changed)
	for p, cell := range l.cellOf {
		if !l.changed[cell] && pixelChanged(before.rgb[p*3:], after.rgb[p*3:]) {
			l.changed[cell] = true
		}
	}
	gap := max(loopGap, 2*interval)
	for i, c := range l.changed {
		if !c {
			continue
		}
		if l.last[i].IsZero() || now.Sub(l.last[i]) > gap {
			l.since[i] = now
		}
		l.last[i] = now
	}
}

// still reports whether the screen, loops aside, has not changed for animationStableFor:
// since recent[0], at least that old, every later sample looks like it. Loops found just
// now apply to the samples before too, so a screen whose only motion turns out to be a
// loop is still at once.
func (l *loopTracker) still(recent []sample) bool {
	from := recent[0]
	if recent[len(recent)-1].at.Sub(from.at) < animationStableFor {
		return false
	}
	mask := l.loopMask(from.at)
	for _, s := range recent[1:] {
		if frameChanged(from.frame, s.frame, l.cellOf, mask) {
			return false
		}
	}
	return true
}

// loopMask reports the cells set aside as loops: those that have kept moving for loopAfter
// and moved since from (a part that stopped, or whose loop gave way to content, counts
// again). It is nil when there are none or when together they are too large.
func (l *loopTracker) loopMask(from time.Time) []bool {
	n := 0
	for i := range l.last {
		l.mask[i] = !l.last[i].IsZero() && !l.last[i].Before(from) && l.last[i].Sub(l.since[i]) >= loopAfter
		if l.mask[i] {
			n++
		}
	}
	if n == 0 || float64(n) > maxLoopArea*float64(len(l.mask)) {
		return nil
	}
	return l.mask
}

// Ignore small pixel differences and incidental changes such as the clock or caret, and the
// pixels in loop cells (mask nil: none).
func frameChanged(before, after frame, cellOf []int32, mask []bool) bool {
	if before.w != after.w || before.h != after.h {
		return true
	}
	minimum := max(1, before.w*before.h/100)
	changed := 0
	for p := range before.w * before.h {
		if mask != nil && mask[cellOf[p]] {
			continue
		}
		if pixelChanged(before.rgb[p*3:], after.rgb[p*3:]) {
			changed++
			if changed >= minimum {
				return true
			}
		}
	}
	return false
}

// pixelChanged compares the pixels at the start of two RGB byte runs.
func pixelChanged(a, b []byte) bool {
	return byteDelta(a[0], b[0])+byteDelta(a[1], b[1])+byteDelta(a[2], b[2]) > 12
}

func byteDelta(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
