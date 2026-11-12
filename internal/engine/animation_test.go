package engine

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"sync"
	"testing"
	"time"
)

// fakeClock is a clock that moves only when the wait sleeps (to its next tick) or a
// screenshot takes time. The timings the tests check are then exact, however slow or loaded
// the machine running them: on a real clock a CI runner's late ticks stretched them (issue
// #188).
type fakeClock struct {
	mu        sync.Mutex
	now       time.Time
	deadlines []fakeDeadline
}

type fakeDeadline struct {
	at     time.Time
	cancel context.CancelFunc
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock on by d, ending the contexts whose timeouts it passes.
func (c *fakeClock) Advance(d time.Duration) { c.advanceTo(c.Now().Add(d), false) }

// advanceTo moves the clock on to t, or (stopAtDeadline) to the first timeout before it.
func (c *fakeClock) advanceTo(t time.Time, stopAtDeadline bool) {
	c.mu.Lock()
	if stopAtDeadline {
		for _, dl := range c.deadlines {
			if dl.at.Before(t) {
				t = dl.at
			}
		}
	}
	if t.After(c.now) {
		c.now = t
	}
	var due []context.CancelFunc
	kept := c.deadlines[:0]
	for _, dl := range c.deadlines {
		if dl.at.After(c.now) {
			kept = append(kept, dl)
		} else {
			due = append(due, dl.cancel)
		}
	}
	c.deadlines = kept
	c.mu.Unlock()
	for _, cancel := range due {
		cancel()
	}
}

func (c *fakeClock) WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.deadlines = append(c.deadlines, fakeDeadline{c.now.Add(d), cancel})
	c.mu.Unlock()
	c.advanceTo(c.Now(), false) // a timeout of 0 or less has ended already
	return ctx, cancel
}

func (c *fakeClock) NewTicker(d time.Duration) animationTicker {
	return &fakeTicker{clock: c, period: d, next: c.Now().Add(d)}
}

// fakeTicker ticks at its start plus each period. As with a time.Ticker, a tick that came
// while the wait was busy is there at once, and the ones after it are dropped.
type fakeTicker struct {
	clock  *fakeClock
	period time.Duration
	next   time.Time
}

func (t *fakeTicker) Wait(ctx context.Context) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		if now := t.clock.Now(); !t.next.After(now) {
			for !t.next.After(now) {
				t.next = t.next.Add(t.period)
			}
			return true
		}
		t.clock.advanceTo(t.next, true)
	}
}

func (t *fakeTicker) Stop() {}

// A part of a motionScreen that repaints while it moves: from..until after the start
// (until 0: forever), one gray level every msPerLevel (0: 4 ms, 20 levels a sample). With a
// cycle, it rests for the last pause of each one (the slow moments of a pulse).
type motionPart struct {
	rect         image.Rectangle
	from, until  time.Duration
	msPerLevel   int64
	cycle, pause time.Duration
}

// motionScreen draws a phone's screen (1200x2600 pixels, 400x866 points at 3x) in white,
// with each part filled in a gray that changes with the clock's time while the part moves.
func motionScreen(clock *fakeClock, parts ...motionPart) func() *image.RGBA {
	start := clock.Now()
	return func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 1200, 2600))
		draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
		elapsed := clock.Now().Sub(start)
		for _, p := range parts {
			at := max(elapsed, p.from)
			if p.until > 0 {
				at = min(at, p.until)
			}
			if p.cycle > 0 { // cycles from p.from
				rel := at - p.from
				at = p.from + rel/p.cycle*p.cycle + min(rel%p.cycle, p.cycle-p.pause)
			}
			per := p.msPerLevel
			if per == 0 {
				per = 4
			}
			gray := uint8(at.Milliseconds() / per % 256)
			draw.Draw(img, p.rect, image.NewUniform(color.Gray{gray}), image.Point{}, draw.Src)
		}
		return img
	}
}

// timeAnimationWait runs waitForAnimation on an iOS fake drawing the parts, each screenshot
// taking shotTime, and returns how long it took on its fake clock.
func timeAnimationWait(t *testing.T, timeout, shotTime time.Duration, parts ...motionPart) time.Duration {
	t.Helper()
	clock := newFakeClock()
	start := clock.Now()
	screen := motionScreen(clock, parts...)
	f := &fakeDriver{screen: static(), plat: "ios", draw: func() image.Image {
		clock.Advance(shotTime)
		return screen()
	}}
	waitForAnimationOn(context.Background(), f, timeout, clock)
	return clock.Now().Sub(start)
}

// A spinner-sized part (80x50 points, 1.2 % of the screen: more than the 1 % that counts as
// a change) and a progress bar across the bottom (8 points high).
var (
	spinnerPart = motionPart{rect: image.Rect(60, 300, 300, 450)}
	barPart     = motionPart{rect: image.Rect(0, 2400, 1200, 2424)}
)

// A still screen is still after the first 250 ms (at the 80 ms tick after it: 320 ms).
func TestWaitForAnimationStillScreen(t *testing.T) {
	t.Parallel()
	if got := timeAnimationWait(t, 6*time.Second, 0); got < 250*time.Millisecond || got > 350*time.Millisecond {
		t.Fatalf("waited %s on a still screen, want about 0.3 s", got)
	}
}

// issue #168: a small part that never stops moving (a spinner, a marquee, a pulsing button)
// does not hold waitForAnimationToEnd to its timeout once it has moved for 3 s.
func TestWaitForAnimationSetsAsideALoop(t *testing.T) {
	t.Parallel()
	if got := timeAnimationWait(t, 8*time.Second, 0, spinnerPart); got < 3*time.Second || got > 3500*time.Millisecond {
		t.Fatalf("waited %s on a screen with a small endless loop, want about 3.1 s", got)
	}
}

// issue #168: two loops apart (a spinner at the top, a progress bar at the bottom) are set
// aside too.
func TestWaitForAnimationSetsAsideTwoLoops(t *testing.T) {
	t.Parallel()
	if got := timeAnimationWait(t, 8*time.Second, 0, spinnerPart, barPart); got < 3*time.Second || got > 3500*time.Millisecond {
		t.Fatalf("waited %s on a screen with two small endless loops, want about 3.1 s", got)
	}
}

// issue #168 review: a slow loop (a breathing glow: 3 gray levels a sample, too little to
// count from one sample to the next, enough over 250 ms) is set aside too.
func TestWaitForAnimationSetsAsideASlowLoop(t *testing.T) {
	t.Parallel()
	glow := spinnerPart
	glow.msPerLevel = 27
	if got := timeAnimationWait(t, 8*time.Second, 0, glow); got < 3*time.Second || got > 3500*time.Millisecond {
		t.Fatalf("waited %s on a screen with a slow endless loop, want about 3.1 s", got)
	}
}

// issue #168: dots that pulse in turn (a typing indicator) are loops that rest while the
// other moves. Each dot's rests (no change seen for about 0.2-0.4 s, while the screen is not
// still) do not make it a new motion, so both are set aside (issue #188 review: this pins
// loopGap; a lone loop's rests short enough not to look still stay within two samples).
func TestWaitForAnimationSetsAsideLoopsThatRest(t *testing.T) {
	t.Parallel()
	dot1 := motionPart{rect: image.Rect(60, 300, 300, 450), cycle: 1100 * time.Millisecond, pause: 550 * time.Millisecond}
	dot2 := dot1
	dot2.rect, dot2.from = image.Rect(360, 300, 600, 450), 550*time.Millisecond
	if got := timeAnimationWait(t, 8*time.Second, 0, dot1, dot2); got < 3*time.Second || got > 4*time.Second {
		t.Fatalf("waited %s on a screen with two small loops pulsing in turn, want about 3.5 s", got)
	}
}

// issue #188 review: a timeout that passes during a slow screenshot ends the wait after that
// screenshot, not a tick later.
func TestWaitForAnimationTimesOutDuringASlowScreenshot(t *testing.T) {
	t.Parallel()
	panel := motionPart{rect: image.Rect(0, 600, 1200, 1250)}
	if got := timeAnimationWait(t, time.Second, 700*time.Millisecond, panel); got != 1400*time.Millisecond {
		t.Fatalf("returned after %s, want the end of the screenshot the 1 s timeout fell in (1.4 s)", got)
	}
}

// issue #188 review: a context ended from outside (the flow stopped) ends the wait at once.
func TestWaitForAnimationStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	start := clock.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	screen := motionScreen(clock, motionPart{rect: image.Rect(0, 600, 1200, 1250)})
	f := &fakeDriver{screen: static(), plat: "ios", draw: func() image.Image {
		if clock.Now().Sub(start) >= time.Second {
			cancel()
		}
		return screen()
	}}
	waitForAnimationOn(ctx, f, 8*time.Second, clock)
	// The first screenshot from 1 s on, at the tick at 1.04 s, ends the flow.
	if got := clock.Now().Sub(start); got != 1040*time.Millisecond {
		t.Fatalf("returned after %s, want 1.04 s, when the flow stopped", got)
	}
}

// issue #168 review: with slow screenshots (450 ms each: a slow runner, a large screen) a
// loop is still found.
func TestWaitForAnimationSetsAsideALoopWithSlowScreenshots(t *testing.T) {
	t.Parallel()
	if got := timeAnimationWait(t, 10*time.Second, 450*time.Millisecond, spinnerPart); got < 3*time.Second || got > 5*time.Second {
		t.Fatalf("waited %s on a screen with a small endless loop and slow screenshots, want about 4 s", got)
	}
}

// A large part that keeps repainting (like the Flutter regression 07 panel, a quarter of the
// screen) is not a loop to set aside: the wait runs to its timeout.
func TestWaitForAnimationWaitsOnALargeRepaint(t *testing.T) {
	t.Parallel()
	panel := motionPart{rect: image.Rect(0, 600, 1200, 1250)}
	if got := timeAnimationWait(t, 4*time.Second, 0, panel); got != 4*time.Second {
		t.Fatalf("returned after %s while a quarter of the screen repainted, want the 4 s timeout", got)
	}
}

// One-shot animations, even small ones, are waited out: a 0.8 s badge, a 1.5 s fade-in (the
// bench's anim_faded) and a 2.5 s reveal (issue #168 review).
func TestWaitForAnimationWaitsOutOneShotAnimations(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{800 * time.Millisecond, 1500 * time.Millisecond, 2500 * time.Millisecond} {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			badge := motionPart{rect: image.Rect(60, 900, 300, 1050), until: d}
			if got := timeAnimationWait(t, 8*time.Second, 0, badge); got < d+250*time.Millisecond || got > d+500*time.Millisecond {
				t.Fatalf("waited %s on a %s animation, want its end plus the 250 ms still screen", got, d)
			}
		})
	}
}

// issue #168: a loop set aside does not hide other motion. A card sliding down the screen
// for 4.5 s (each part of the screen changes only while the card passes) is waited out.
func TestWaitForAnimationLoopDoesNotHideOtherMotion(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	start := clock.Now()
	loop := motionScreen(clock, spinnerPart)
	f := &fakeDriver{screen: static(), plat: "ios", draw: func() image.Image {
		img := loop()
		y := 600 + int(min(clock.Now().Sub(start), 4500*time.Millisecond).Milliseconds()*3/10) // 100 points/s
		draw.Draw(img, image.Rect(450, y, 750, y+300), image.Black, image.Point{}, draw.Src)
		return img
	}}
	waitForAnimationOn(context.Background(), f, 10*time.Second, clock)
	if got := clock.Now().Sub(start); got < 4500*time.Millisecond || got > 5*time.Second {
		t.Fatalf("waited %s, want the card's stop at 4.5 s (plus up to the 250 ms still screen)", got)
	}
}
