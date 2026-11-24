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
	animationStableFor = 250 * time.Millisecond
	loopAfter          = 3 * time.Second
	loopGap            = 400 * time.Millisecond
	maxLoopArea        = 0.10
	loopReturns        = 2
	loopPeriodMin      = 300 * time.Millisecond
	loopHistory        = 3500 * time.Millisecond
	loopMismatch       = 0.05
	loopRest           = 2 * time.Second
	noteAfter          = time.Second
	loopColumns        = 24
	ciStillGaps        = 3
	animationScale     = 0.15
)

func waitForAnimation(ctx context.Context, d driver.Driver, timeout time.Duration, note func(string)) string {
	return waitForAnimationNoting(ctx, d, timeout, realClock{}, ci.On(), note)
}

func waitForAnimationOn(ctx context.Context, d driver.Driver, timeout time.Duration, clock animationClock, ciMode bool) string {
	return waitForAnimationNoting(ctx, d, timeout, clock, ciMode, nil)
}

func waitForAnimationNoting(ctx context.Context, d driver.Driver, timeout time.Duration, clock animationClock, ciMode bool, note func(string)) string {
	outer := ctx
	ctx, cancel := clock.WithTimeout(ctx, timeout)
	defer cancel()
	settleFrom := clock.Now()
	deadline := settleFrom.Add(timeout)
	_ = d.WaitSettled(ctx, timeout)
	if d.Platform() != "android" && d.Platform() != "ios" {
		return ""
	}
	noted := note == nil
	if took := clock.Now().Sub(settleFrom); !noted && took >= noteAfter && ctx.Err() == nil {
		note(fmt.Sprintf("animation wait: the app's own changes took %d ms to settle, before the first screenshot", took.Milliseconds()))
		noted = true
	}

	var recent []sample
	var loops *loopTracker
	var shots shotTimes
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
	var first time.Time
	last := false
	var judgedAt time.Time
	sample := func(ctx context.Context) (how string, still, done bool) {
		asked := clock.Now()
		data, err := d.Screenshot(ctx, animationScale, "png")
		if err != nil && (ctx.Err() != nil || last || clock.Now().Add(250*time.Millisecond).After(deadline)) {
			return "", false, false
		}
		if err != nil {
			return fmt.Sprintf("animation wait: a screenshot failed, after %s: %v", shots, err), false, true
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			if last {
				return "", false, false
			}
			return fmt.Sprintf("animation wait: a screenshot could not be read, after %s: %v", shots, err), false, true
		}
		now := clock.Now()
		judgedAt = now
		if shots.n == 0 {
			first = asked
		}
		shots.add(asked, now)
		f := pixelsOf(img)
		if len(recent) == 0 || recent[0].frame.w != f.w || recent[0].frame.h != f.h {
			recent, loops = nil, newLoopTracker(f.w, f.h)
			sw, sh = d.Size()
		}
		recent = append(recent, sample{asked, now, f})
		for len(recent) > 1 && asked.Sub(recent[1].asked) >= stillSpan(recent[1:], ciMode) {
			recent = recent[1:]
		}
		if n := len(recent); n > 1 {
			loops.observe(recent[0], f, now, now.Sub(recent[n-2].at))
		}
		loops.remember(recent[len(recent)-1])
		if span := stillSpan(recent, ciMode); loops.still(recent, span) {
			if aside, ok := loops.maskBox(recent[0].at); ok {
				return fmt.Sprintf("animation wait: still for %d ms, with a loop set aside at %s, after %s",
					span.Milliseconds(), where(aside, f), shots), true, true
			}
			return fmt.Sprintf("animation wait: still for %d ms, after %s", span.Milliseconds(), shots), true, true
		}
		if n := len(recent); !noted && !last && n > 1 && asked.Sub(first) >= noteAfter {
			noted = true
			if b := changeBox(recent[0].frame, f, recent[n-2].frame); b.n > 0 {
				note(fmt.Sprintf("animation wait: still moving after %d s: the part at %s (%s of the screen) keeps changing",
					int(noteAfter.Seconds()), where(b, f), b.share(f)))
			} else {
				note(fmt.Sprintf("animation wait: still moving after %d s", int(noteAfter.Seconds())))
			}
		}
		return "", false, false
	}
	ticker := clock.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if how, _, done := sample(ctx); done {
			return how
		}
		if !ticker.Wait(ctx) {
			break
		}
	}
	if outer.Err() == nil && shots.n > 0 && !judgedAt.After(deadline) {
		last = true
		lastCtx, cancelLast := clock.WithTimeout(outer, 500*time.Millisecond)
		defer cancelLast()
		if how, still, _ := sample(lastCtx); still {
			return how
		}
	}
	return ended()
}

func stillSpan(samples []sample, ciMode bool) time.Duration {
	span := animationStableFor
	if !ciMode {
		return span
	}
	for i := 1; i < len(samples); i++ {
		span = max(span, ciStillGaps*samples[i].asked.Sub(samples[i-1].asked))
	}
	return span
}

type shotTimes struct {
	n               int
	min, max, apart time.Duration
	lastAsked       time.Time
}

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

type animationClock interface {
	Now() time.Time
	WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc)
	NewTicker(d time.Duration) animationTicker
}

type animationTicker interface {
	Wait(ctx context.Context) bool
	Stop()
}

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
	asked, at time.Time
	frame     frame
}

type frame struct {
	w, h int
	rgb  []byte
}

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

type loopTracker struct {
	w, cols  int
	size     int
	cellOf   []int32
	pixOf    [][]int32
	since    []time.Time
	last     []time.Time
	changed  []bool
	mask     []bool
	seen     []bool
	returns  []int
	returned []time.Time
	loopFrom []time.Time
	history  []sample
}

func newLoopTracker(w, h int) *loopTracker {
	size := max(2, (w+loopColumns-1)/loopColumns)
	cols, rows := (w+size-1)/size, (h+size-1)/size
	n := cols * rows
	l := &loopTracker{w: w, cols: cols, size: size, cellOf: make([]int32, w*h), pixOf: make([][]int32, n),
		since: make([]time.Time, n), last: make([]time.Time, n), changed: make([]bool, n),
		mask: make([]bool, n), seen: make([]bool, n), returns: make([]int, n),
		returned: make([]time.Time, n), loopFrom: make([]time.Time, n)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := int32(y/size*cols + x/size)
			l.cellOf[y*w+x] = c
			l.pixOf[c] = append(l.pixOf[c], int32(y*w+x))
		}
	}
	return l
}

func (l *loopTracker) remember(s sample) {
	l.history = append(l.history, s)
	for len(l.history) > 1 && s.asked.Sub(l.history[0].asked) > loopHistory {
		l.history[0] = sample{}
		l.history = l.history[1:]
	}
}

func (l *loopTracker) proven(i int) bool { return l.returns[i] >= loopReturns }

func (l *loopTracker) observe(from sample, after frame, now time.Time, interval time.Duration) {
	before := from.frame
	clear(l.changed)
	for p, cell := range l.cellOf {
		if !l.changed[cell] && pixelChanged(before.rgb[p*3:], after.rgb[p*3:]) {
			l.changed[cell] = true
		}
	}
	clear(l.seen)
	var part []int32
	for i, c := range l.changed {
		if !c || l.seen[i] {
			continue
		}
		part = l.partFrom(i, part[:0])
		back, ok := l.cameBack(part, after, from.asked)
		for _, c := range part {
			switch {
			case !ok:
				if l.proven(int(c)) {
					l.since[c] = now
				}
				l.returns[c], l.loopFrom[c] = 0, time.Time{}
			case now.Sub(l.returned[c]) >= loopPeriodMin || l.returns[c] == 0:
				if l.returns[c] == 0 || back.Before(l.loopFrom[c]) {
					l.loopFrom[c] = back
				}
				l.returns[c]++
				l.returned[c] = now
			}
		}
	}

	gap := max(loopGap, 4*interval)
	for i, c := range l.changed {
		if !c {
			continue
		}
		rest := gap
		if l.proven(i) {
			rest = max(rest, loopRest)
		}
		if l.last[i].IsZero() || now.Sub(l.last[i]) > rest {
			l.since[i] = now
		}
		if l.proven(i) && l.loopFrom[i].Before(l.since[i]) {
			l.since[i] = l.loopFrom[i]
		}
		l.last[i] = now
	}
}

func (l *loopTracker) partFrom(i int, part []int32) []int32 {
	l.seen[i] = true
	part = append(part, int32(i))
	for k := len(part) - 1; k < len(part); k++ {
		c := int(part[k])
		x, y := c%l.cols, c/l.cols
		for _, d := range [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
			nx, ny := x+d[0], y+d[1]
			n := ny*l.cols + nx
			if nx < 0 || nx >= l.cols || ny < 0 || n >= len(l.changed) || l.seen[n] || !l.changed[n] || l.proven(n) != l.proven(i) {
				continue
			}
			l.seen[n] = true
			part = append(part, int32(n))
		}
	}
	return part
}

func (l *loopTracker) cameBack(part []int32, f frame, limit time.Time) (time.Time, bool) {
	n := 0
	for _, c := range part {
		n += len(l.pixOf[c])
	}
	allowed := int(loopMismatch * float64(n))
	for k := len(l.history) - 1; k >= 0; k-- {
		h := l.history[k]
		if !h.asked.Before(limit) || h.frame.w != f.w || h.frame.h != f.h {
			continue
		}
		off := 0
	cells:
		for _, c := range part {
			for _, p := range l.pixOf[c] {
				if pixelChanged(h.frame.rgb[p*3:], f.rgb[p*3:]) {
					if off++; off > allowed {
						break cells
					}
				}
			}
		}
		if off <= allowed {
			return h.asked, true
		}
	}
	return time.Time{}, false
}

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

func (l *loopTracker) loopMask(from time.Time) []bool {
	proven, other := 0, 0
	for i := range l.last {
		l.mask[i] = !l.last[i].IsZero() && !l.last[i].Before(from) && l.last[i].Sub(l.since[i]) >= loopAfter
		switch {
		case !l.mask[i]:
		case l.proven(i):
			proven++
		default:
			other++
		}
	}
	if float64(other) > maxLoopArea*float64(len(l.mask)) {
		for i := range l.mask {
			l.mask[i] = l.mask[i] && l.proven(i)
		}
		other = 0
	}
	if proven+other == 0 {
		return nil
	}
	return l.mask
}

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

func pixelChanged(a, b []byte) bool {
	return byteDelta(a[0], b[0])+byteDelta(a[1], b[1])+byteDelta(a[2], b[2]) > 12
}

func byteDelta(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

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

func (b box) on(sw, sh int, f frame) string {
	sx, sy := 1/animationScale, 1/animationScale
	if sw > 0 && sh > 0 && f.w > 0 && f.h > 0 {
		sx, sy = float64(sw)/float64(f.w), float64(sh)/float64(f.h)
	}
	px := func(v int, s float64) int { return int(math.Round(float64(v) * s)) }
	return fmt.Sprintf("x %d-%d, y %d-%d", px(b.x0, sx), px(b.x1, sx), px(b.y0, sy), px(b.y1, sy))
}

func (b box) share(f frame) string {
	return fmt.Sprintf("%d %%", int(math.Round(100*float64(b.n)/float64(max(1, f.w*f.h)))))
}

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
