package engine

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
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
	// A part counts as still moving while it changed within this long (or within four
	// samples, when screenshots are slower): a loop's slow moments, such as the ends of a
	// pulse, do not make it a new motion. A rest is seen up to a sample late at each end;
	// with screenshots 150 ms apart, the 550 ms rests of two dots pulsing in turn otherwise
	// looked like new motions, and the wait ran to its timeout (issue #136 review).
	loopGap = 400 * time.Millisecond
	// Loops are set aside only while all of them together cover at most this share of the
	// screen. Past it the screen is really still moving (a large panel repainting), and
	// the wait runs to its timeout.
	maxLoopArea = 0.10
	// The loop grid has this many cells across (a cell is about 17 points on a phone).
	loopColumns = 24
	// In CI mode the screen must be still for this many times the longest gap between two
	// screenshots in the still span, when that is longer than animationStableFor (issue
	// #136). A starved emulator that is slow to take screenshots is slow to draw too: on CI
	// the moving Flutter regression 07 panel was captured 140-310 ms apart, and its app
	// stopped presenting frames for longer than such a gap, so its slow fade looked still for
	// 250 ms. A freeze shorter than three gaps does not count as stillness (with two, a
	// 350 ms freeze between screenshots 150 ms apart still did). With screenshots every
	// 80 ms (a still screen on CI: about 55 ms each) the span stays 250 ms.
	ciStillGaps = 3
	// Screenshots are taken at this scale.
	animationScale = 0.15
	// A screen is restless (issue #83: a feed post that keeps repainting, with rests shorter
	// than the still span) once the same part of it has changed in restlessRepeats+1 bursts
	// in a row, a rest (no change at all for quietFor) after each, and it has been watched
	// for restlessScreenAfter. A restless screen is still enough at its next rest.
	//
	// The bursts must repeat in the same place (issue #83 review): a staggered list or a
	// transition in stages changes a different part of the screen at each stage, with pauses
	// as short as a loop's rests, and is waited out to its end. Two bursts are in the same
	// place when at least half the cells of the smaller one changed in the other too.
	restlessScreenAfter = time.Second
	restlessRepeats     = 2
	// How long a restless screen must not change at all (see quietDiff) to be still enough:
	// two gaps between screenshots 80 ms apart. Maestro ends its wait at the first pair of
	// screenshots that match, taken one full screenshot (a few hundred ms) apart.
	quietFor = 150 * time.Millisecond
	// The most two screenshots may differ, in percent of the largest difference there could
	// be (every color channel of every pixel from 0 to 255), for a screen to count as not
	// changing: Maestro's formula and threshold (ScreenshotUtils.waitUntilScreenIsStatic
	// calls romankh3 image-comparison's getDifferencePercent, in percent, with 0.005), on
	// 0.15-scale screenshots: an estimate of Maestro's measure, which compares full-size
	// ones, so small changes may be missed or amplified. It is far stricter than
	// frameChanged's: a slow fade that frameChanged misses from one sample to the next, or a
	// few hundred pixels of a small moving part, are over it.
	quietDiff = 0.005
)

// Accessibility events cannot see animations that only repaint pixels. Sample small
// screenshots for this explicit mobile command after the usual event-driven settle.
//
// Parts that never stop moving (issue #168: a spinner, a marquee, a pulsing button) are set
// aside once they have moved for loopAfter and while they stay small; the rest of the
// screen must then be still for animationStableFor.
//
// A restless screen (issue #83: the same part of it keeps changing, with rests shorter than
// animationStableFor, and is too large to be a loop) is still enough at a rest: once it does
// not change at all, by quietDiff, for quietFor. It runs on Android and iOS alike: the bursts
// must repeat in the same place, which a sequenced animation (iOS runs them; tb turns
// Android's off) does not do.
//
// It returns how the wait ended, for the trace ("" off Android and iOS, where it takes no
// screenshots): still, still enough, or what kept changing at the timeout, in the units of
// d.Size() (Android: pixels; iOS: points).
func waitForAnimation(ctx context.Context, d driver.Driver, timeout time.Duration) string {
	return waitForAnimationOn(ctx, d, timeout, realClock{}, ci.On())
}

// waitForAnimationOn is waitForAnimation on a given clock: the tests' fake one lets them
// check the timings exactly, however loaded the machine running them (issue #188). ciMode
// is ci.On().
func waitForAnimationOn(ctx context.Context, d driver.Driver, timeout time.Duration, clock animationClock, ciMode bool) string {
	outer := ctx
	ctx, cancel := clock.WithTimeout(ctx, timeout)
	defer cancel()
	_ = d.WaitSettled(ctx, timeout)
	if d.Platform() != "android" && d.Platform() != "ios" {
		return ""
	}

	// The samples of the still span and the one before it: recent[0] is the newest sample
	// asked for at least that long before the newest one (or the oldest there is).
	var recent []sample
	var loops *loopTracker
	var shots shotTimes
	var first time.Time // when the first screenshot was asked for (since the last rotation)
	var bursts burstTracker
	sw, sh := d.Size()
	where := func(b box, f frame) string { return b.on(sw, sh, f) }
	ended := func() string {
		if outer.Err() != nil {
			return fmt.Sprintf("animation wait: stopped, after %s", shots)
		}
		if shots.n == 0 {
			return "animation wait: the timeout passed while waiting for the app to settle, before any screenshot"
		}
		if n := len(recent); n > 1 {
			if b := changeBox(recent[0].frame, recent[n-1].frame, recent[n-2].frame); b.n > 0 {
				return fmt.Sprintf("animation wait: still moving at the timeout: the part at %s (%s of the screen) kept changing, after %s",
					where(b, recent[n-1].frame), b.share(recent[n-1].frame), shots)
			}
		}
		return fmt.Sprintf("animation wait: still moving at the timeout, after %s", shots)
	}
	ticker := clock.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		asked := clock.Now()
		data, err := d.Screenshot(ctx, animationScale, "png")
		if ctx.Err() != nil {
			return ended()
		}
		if err != nil {
			return fmt.Sprintf("animation wait: a screenshot failed, after %s: %v", shots, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return fmt.Sprintf("animation wait: a screenshot could not be read, after %s: %v", shots, err)
		}
		now := clock.Now()
		shots.add(asked, now)
		f := pixelsOf(img)
		if len(recent) == 0 || recent[0].frame.w != f.w || recent[0].frame.h != f.h {
			recent, loops = nil, newLoopTracker(f.w, f.h)
			bursts = newBurstTracker(len(loops.since))
			first = asked
		}
		recent = append(recent, sample{asked, now, f})
		recent = trimTo(recent, animationStableFor, ciMode)
		if n := len(recent); n > 1 {
			// Over the same span as the stillness check: a slow loop (a breathing glow)
			// may change too little from one sample to the next to count.
			loops.observe(recent[0].frame, f, now, now.Sub(recent[n-2].at))
			// The cells of the current burst: what changed since the sample before.
			loops.mark(recent[n-2].frame, f, bursts.cur)
		}
		if span := stillSpan(recent, ciMode); loops.still(recent, span) {
			if aside, ok := loops.maskBox(recent[0].at); ok {
				return fmt.Sprintf("animation wait: still for %d ms, with a loop set aside at %s, after %s",
					span.Milliseconds(), where(aside, f), shots)
			}
			return fmt.Sprintf("animation wait: still for %d ms, after %s", span.Milliseconds(), shots)
		}
		// A restless screen (issue #83): still enough at a rest, once it has not changed at
		// all for quietFor, when the bursts before have repeated in the same place.
		quiet := trimTo(recent, quietFor, ciMode)
		span := spanOf(quietFor, quiet, ciMode)
		bursts.rest(loops.quiet(quiet, span))
		if bursts.resting && bursts.repeats >= restlessRepeats && asked.Sub(first) >= restlessScreenAfter {
			what := ""
			if n := len(recent) - len(quiet); n > 0 { // the last change, before the quiet samples
				if b := changeBox(recent[0].frame, quiet[0].frame, recent[n-1].frame); b.n > 0 {
					what = fmt.Sprintf(" (the part at %s, %s of the screen)", where(b, f), b.share(f))
				}
			}
			return fmt.Sprintf("animation wait: still enough: the same part kept changing%s for %d ms, in %d bursts with short rests, then did not change for %d ms (under a %.3f %% difference, Maestro's threshold), after %s",
				what, quiet[0].asked.Sub(first).Milliseconds(), bursts.repeats+1, span.Milliseconds(), quietDiff, shots)
		}
		if !ticker.Wait(ctx) {
			break
		}
	}
	return ended()
}

// stillSpan is how long the screen must look the same, over samples, to be still:
// animationStableFor, and in CI mode at least ciStillGaps times the longest gap between two
// of them.
func stillSpan(samples []sample, ciMode bool) time.Duration {
	return spanOf(animationStableFor, samples, ciMode)
}

// trimTo drops the samples before the newest one asked for at least spanOf(base) before the
// newest sample: the samples of a span of base and the one before it.
func trimTo(samples []sample, base time.Duration, ciMode bool) []sample {
	newest := samples[len(samples)-1].asked
	for len(samples) > 1 && newest.Sub(samples[1].asked) >= spanOf(base, samples[1:], ciMode) {
		samples = samples[1:]
	}
	return samples
}

// spanOf is base, and in CI mode at least ciStillGaps times the longest gap between two of
// the samples.
func spanOf(base time.Duration, samples []sample, ciMode bool) time.Duration {
	span := base
	if !ciMode {
		return span
	}
	for i := 1; i < len(samples); i++ {
		span = max(span, ciStillGaps*samples[i].asked.Sub(samples[i-1].asked))
	}
	return span
}

// shotTimes counts the screenshots of a wait, how long they took and the longest gap
// between two of them, for the trace: a late reply (a long screenshot) or a stalled app (a
// long gap, the screen still across it) shows there (issue #136).
type shotTimes struct {
	n               int
	min, max, apart time.Duration
	lastAsked       time.Time
}

// add counts a screenshot asked for at asked that came back at.
func (s *shotTimes) add(asked, at time.Time) {
	d := at.Sub(asked)
	if s.n == 0 || d < s.min {
		s.min = d
	}
	if s.n > 0 {
		s.apart = max(s.apart, asked.Sub(s.lastAsked))
	}
	s.max = max(s.max, d)
	s.lastAsked = asked
	s.n++
}

func (s shotTimes) String() string {
	switch s.n {
	case 0:
		return "no screenshot"
	case 1:
		return fmt.Sprintf("1 screenshot of %d ms", s.max.Milliseconds())
	}
	return fmt.Sprintf("%d screenshots of %d-%d ms, at most %d ms apart", s.n,
		s.min.Milliseconds(), s.max.Milliseconds(), s.apart.Milliseconds())
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

// A screenshot shows the screen when it was asked for (Android's agent captures the display
// first, then scales, encodes and sends it), and comes back at: its reply can come late on a
// slow runner. The time between two samples is counted between their requests, so a late
// reply does not stretch it (issue #136).
type sample struct {
	asked, at time.Time
	frame     frame
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
	w       int         // the frames' width
	cellOf  []int32     // per pixel, its cell
	since   []time.Time // when the cell's current run of changes began
	last    []time.Time // when the cell last changed (zero: never)
	changed []bool      // scratch for observe
	mask    []bool      // scratch for still
}

func newLoopTracker(w, h int) *loopTracker {
	size := max(2, (w+loopColumns-1)/loopColumns)
	cols, rows := (w+size-1)/size, (h+size-1)/size
	l := &loopTracker{w: w, cellOf: make([]int32, w*h), since: make([]time.Time, cols*rows),
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
	gap := max(loopGap, 4*interval)
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

// still reports whether the screen, loops aside, has not changed for span: since recent[0],
// asked for at least span before the newest sample, every later sample looks like it. Loops
// found just now apply to the samples before too, so a screen whose only motion turns out to
// be a loop is still at once.
func (l *loopTracker) still(recent []sample, span time.Duration) bool {
	from := recent[0]
	if recent[len(recent)-1].asked.Sub(from.asked) < span {
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

// quiet reports whether the screen, loops aside, has not changed at all for span (by
// Maestro's threshold, quietDiff): since recent[0], asked for at least span before the
// newest sample, every later sample looks the same.
func (l *loopTracker) quiet(recent []sample, span time.Duration) bool {
	from := recent[0]
	if recent[len(recent)-1].asked.Sub(from.asked) < span {
		return false
	}
	mask := l.loopMask(from.at)
	for _, s := range recent[1:] {
		if diffPercent(from.frame, s.frame, l.cellOf, mask) > quietDiff {
			return false
		}
	}
	return true
}

// maskBox is where the loops set aside since from are, for the trace; ok is false when none
// is.
func (l *loopTracker) maskBox(from time.Time) (b box, ok bool) {
	mask := l.loopMask(from)
	if mask == nil {
		return box{}, false
	}
	for p, cell := range l.cellOf {
		if mask[cell] {
			b.add(p%l.w, p/l.w)
		}
	}
	return b, b.n > 0
}

// diffPercent is how much two frames differ, pixels in loop cells aside (mask nil: none), as
// romankh3 image-comparison's getDifferencePercent (which Maestro uses) counts it: the sum of
// the differences of every color channel, in percent of the largest sum there could be.
func diffPercent(a, b frame, cellOf []int32, mask []bool) float64 {
	if a.w != b.w || a.h != b.h {
		return 100
	}
	var sum, n int64
	for p := range a.w * a.h {
		if mask != nil && mask[cellOf[p]] {
			continue
		}
		o := p * 3
		sum += int64(byteDelta(a.rgb[o], b.rgb[o]) + byteDelta(a.rgb[o+1], b.rgb[o+1]) + byteDelta(a.rgb[o+2], b.rgb[o+2]))
		n++
	}
	if n == 0 {
		return 0
	}
	return 100 * float64(sum) / float64(765*n)
}

// box is the bounds of a set of pixels of a frame (x1 and y1 past the last), and how many
// there are.
type box struct {
	x0, y0, x1, y1, n int
}

func (b *box) add(x, y int) {
	if b.n == 0 {
		b.x0, b.y0, b.x1, b.y1 = x, y, x+1, y+1
	} else {
		b.x0, b.y0, b.x1, b.y1 = min(b.x0, x), min(b.y0, y), max(b.x1, x+1), max(b.y1, y+1)
	}
	b.n++
}

// on gives the box, a set of f's pixels, in the units of a screen sw x sh (d.Size():
// Android pixels, iOS points). A screenshot's size is about animationScale of the screen's,
// rounded differently per platform, so the frame's own size gives the scale; without a
// screen size, 1/animationScale does.
func (b box) on(sw, sh int, f frame) string {
	sx, sy := 1/animationScale, 1/animationScale
	if sw > 0 && sh > 0 && f.w > 0 && f.h > 0 {
		sx, sy = float64(sw)/float64(f.w), float64(sh)/float64(f.h)
	}
	px := func(v int, s float64) int { return int(math.Round(float64(v) * s)) }
	return fmt.Sprintf("x %d-%d, y %d-%d", px(b.x0, sx), px(b.x1, sx), px(b.y0, sy), px(b.y1, sy))
}

// burstTracker follows the bursts of change of a screen and the rests between them: how many
// bursts in a row have changed the same part of the screen as the one before (issue #83
// review: a staggered list or a transition in stages changes a new part at each stage).
type burstTracker struct {
	cur, prev []bool // per cell, whether it changed in the current burst, and in the last one
	repeats   int    // the bursts in a row that were in the same place as the one before
	resting   bool   // whether the screen rests now
}

func newBurstTracker(cells int) burstTracker {
	return burstTracker{cur: make([]bool, cells)}
}

// rest records whether the screen rests now. When a rest begins, the burst before it ends.
func (t *burstTracker) rest(resting bool) {
	began := resting && !t.resting
	t.resting = resting
	if !began {
		return
	}
	if t.prev != nil && samePlace(t.prev, t.cur) {
		t.repeats++
	} else {
		t.repeats = 0
	}
	if t.prev == nil {
		t.prev = make([]bool, len(t.cur))
	}
	t.prev, t.cur = t.cur, t.prev
	clear(t.cur)
}

// samePlace reports whether two bursts changed the same part of the screen: at least half
// the cells of the smaller one changed in the other too (an empty burst is in no place).
func samePlace(a, b []bool) bool {
	na, nb, both := 0, 0, 0
	for i := range a {
		if a[i] {
			na++
		}
		if b[i] {
			nb++
		}
		if a[i] && b[i] {
			both++
		}
	}
	return na > 0 && nb > 0 && 2*both >= min(na, nb)
}

// mark sets in cells the cells that differ between before and after.
func (l *loopTracker) mark(before, after frame, cells []bool) {
	for p, cell := range l.cellOf {
		if !cells[cell] && pixelChanged(before.rgb[p*3:], after.rgb[p*3:]) {
			cells[cell] = true
		}
	}
}

// share is how many of f's pixels are in the set, in percent of the screen.
func (b box) share(f frame) string {
	return fmt.Sprintf("%d %%", int(math.Round(100*float64(b.n)/float64(max(1, f.w*f.h)))))
}

// changeBox is where newest differs from either earlier frame (from: the first of the still
// span, so a slow change shows; prev: the one just before newest), for the trace.
func changeBox(from, newest, prev frame) box {
	var b box
	if from.w != newest.w || from.h != newest.h || prev.w != newest.w || prev.h != newest.h {
		return b
	}
	for p := range newest.w * newest.h {
		o := p * 3
		if pixelChanged(from.rgb[o:], newest.rgb[o:]) || pixelChanged(prev.rgb[o:], newest.rgb[o:]) {
			b.add(p%newest.w, p/newest.w)
		}
	}
	return b
}
