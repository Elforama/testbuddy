package engine

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
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
	waitForAnimationOn(context.Background(), f, timeout, clock, false)
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
	waitForAnimationOn(ctx, f, 8*time.Second, clock, false)
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

// A large part that keeps repainting in new colors (like the Flutter regression 07 panel, a
// quarter of the screen fading for 4 s) is not a loop to set aside: the wait runs to its
// timeout. (One that cycles through the same colors is, issue #83: see
// TestWaitForAnimationSetsAsideALargeLoop.)
func TestWaitForAnimationWaitsOnALargeRepaint(t *testing.T) {
	t.Parallel()
	for _, ms := range []int64{20, 27} { // new grays for 5.1 and 6.9 s
		panel := motionPart{rect: image.Rect(0, 600, 1200, 1250), msPerLevel: ms}
		if got := timeAnimationWait(t, 5*time.Second, 0, panel); got != 5*time.Second {
			t.Errorf("returned after %s while a quarter of the screen repainted (a gray level every %d ms), want the 5 s timeout", got, ms)
		}
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
	waitForAnimationOn(context.Background(), f, 10*time.Second, clock, false)
	if got := clock.Now().Sub(start); got < 4500*time.Millisecond || got > 5*time.Second {
		t.Fatalf("waited %s, want the card's stop at 4.5 s (plus up to the 250 ms still screen)", got)
	}
}

// shotWait is a waitForAnimation on an Android fake drawing the parts, with the i-th
// screenshot (from 0) taking shotTime(i).
type shotWait struct {
	timeout  time.Duration
	shotTime func(i int) time.Duration
	// The screen is captured when it is asked for (as Android's agent does: it captures the
	// display, then scales, encodes and sends it), not when the reply comes.
	atAsk bool
	// The app stops presenting frames (a starved emulator) from freezeAt for freezeFor.
	freezeAt, freezeFor time.Duration
	ciMode              bool
}

// run returns how long the wait took on its fake clock.
func (w shotWait) run(parts ...motionPart) time.Duration {
	clock := newFakeClock()
	start := clock.Now()
	screen := motionScreen(clock, parts...)
	var frozen image.Image
	i := 0
	f := &fakeDriver{screen: static(), plat: "android", draw: func() image.Image {
		took := w.shotTime(i)
		i++
		if !w.atAsk {
			clock.Advance(took)
		}
		var img image.Image
		if at := clock.Now().Sub(start); w.freezeFor > 0 && at >= w.freezeAt && at < w.freezeAt+w.freezeFor {
			if frozen == nil {
				frozen = screen()
			}
			img = frozen
		} else {
			img = screen()
		}
		if w.atAsk {
			clock.Advance(took)
		}
		return img
	}}
	waitForAnimationOn(context.Background(), f, w.timeout, clock, w.ciMode)
	return clock.Now().Sub(start)
}

func every(d time.Duration) func(int) time.Duration { return func(int) time.Duration { return d } }

// cycle takes the screenshot times in ms in turn.
func cycle(ms ...int) func(int) time.Duration {
	return func(i int) time.Duration { return time.Duration(ms[i%len(ms)]) * time.Millisecond }
}

// The Flutter regression 07 panel: a quarter of the screen fading slowly (4 s from purple to
// yellow): less change than counts from one 80 ms sample to the next, enough over 250 ms.
var fadePart = motionPart{rect: image.Rect(0, 600, 1200, 1250), msPerLevel: 27}

// issue #136 review: outside CI, a still screen ends as on main whatever the screenshot time:
// at the first sample asked for 250 ms after the first one.
func TestWaitForAnimationStillScreenWithSlowScreenshots(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		shot, want time.Duration
	}{{55 * time.Millisecond, 375 * time.Millisecond}, {200 * time.Millisecond, 600 * time.Millisecond}} {
		if got := (shotWait{timeout: 6 * time.Second, shotTime: every(c.shot)}).run(); got != c.want {
			t.Errorf("waited %s on a still screen with %s screenshots, want %s", got, c.shot, c.want)
		}
	}
}

// issue #136: a screenshot shows the screen when it was asked for, and its reply can come
// late on a slow runner. One reply 300 ms late made two screenshots taken 80 ms apart look
// 250 ms apart (the still span was counted between replies), and the wait ended in the middle
// of the fade. It runs to its timeout.
func TestWaitForAnimationLateScreenshotIsNotStillness(t *testing.T) {
	t.Parallel()
	late := func(i int) time.Duration {
		if i == 14 { // about 1.2 s in
			return 300 * time.Millisecond
		}
		return 20 * time.Millisecond
	}
	if got := (shotWait{timeout: 3 * time.Second, shotTime: late, atAsk: true}).run(fadePart); got < 3*time.Second {
		t.Fatalf("returned after %s while the panel kept changing color, want the 3 s timeout", got)
	}
}

// issue #136: on CI's starved emulator the screenshots of the moving panel came 140-310 ms
// apart, and the app stopped presenting frames for longer than one of those gaps: the panel
// looked still for 250 ms, and the wait ended in the middle of the fade. In CI mode the screen
// must be still for 3 times the longest gap, so a 350 ms freeze, wherever it falls, is not
// stillness. Outside CI it is (the screen did not change for 250 ms).
func TestWaitForAnimationAppFreezeIsNotStillnessInCI(t *testing.T) {
	t.Parallel()
	early := map[bool]int{}
	for _, ciMode := range []bool{false, true} {
		for at := 800 * time.Millisecond; at < 1400*time.Millisecond; at += 10 * time.Millisecond {
			w := shotWait{timeout: 3 * time.Second, shotTime: cycle(230, 200, 310, 140), atAsk: true,
				freezeAt: at, freezeFor: 350 * time.Millisecond, ciMode: ciMode}
			if w.run(fadePart) < 3*time.Second {
				early[ciMode]++
			}
		}
	}
	if early[true] > 0 {
		t.Errorf("in CI mode a 350 ms freeze ended the wait early %d times in 60", early[true])
	}
	if early[false] == 0 {
		t.Errorf("outside CI no 350 ms freeze ended the wait: the test does not reach the case")
	}
}

// issue #136 review: dots that pulse in turn are set aside as loops with slow screenshots too
// (on main they held the wait to its timeout at 150 ms, or 60-300 ms, a screenshot): a rest
// is seen up to a sample late at each end, which the gap that keeps a loop going allows for.
func TestWaitForAnimationSetsAsideLoopsThatRestWithSlowScreenshots(t *testing.T) {
	t.Parallel()
	dot1 := motionPart{rect: image.Rect(60, 300, 300, 450), cycle: 1100 * time.Millisecond, pause: 550 * time.Millisecond}
	dot2 := dot1
	dot2.rect, dot2.from = image.Rect(360, 300, 600, 450), 550*time.Millisecond
	for name, shot := range map[string]func(int) time.Duration{"150 ms": every(150 * time.Millisecond), "60-300 ms": cycle(60, 300, 120, 200, 80, 250, 150, 90, 280, 100)} {
		if got := (shotWait{timeout: 10 * time.Second, shotTime: shot}).run(dot1, dot2); got > 4*time.Second {
			t.Errorf("waited %s on two small loops pulsing in turn with %s screenshots, want about 3.6 s", got, name)
		}
	}
}

// issue #136 review: the trace says how the wait ended, with the screenshots' times and the
// longest gap between two of them (a late reply, or a stalled app).
func TestWaitForAnimationTrace(t *testing.T) {
	t.Parallel()
	panel := motionPart{rect: image.Rect(0, 600, 1200, 1250)}
	wait := func(ctx context.Context, timeout, shot time.Duration, settle bool, parts ...motionPart) string {
		clock := newFakeClock()
		screen := motionScreen(clock, parts...)
		f := &fakeDriver{screen: static(), plat: "android", draw: func() image.Image {
			clock.Advance(shot)
			return screen()
		}}
		if settle { // the event-driven settle takes the whole timeout
			f.onWait = func() { clock.Advance(timeout) }
		}
		return waitForAnimationOn(ctx, f, timeout, clock, false)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	bg := context.Background()
	for _, c := range []struct{ got, want string }{
		{wait(bg, 6*time.Second, 20*time.Millisecond, false), "animation wait: still for 250 ms, after 5 screenshots of 20-20 ms, at most 80 ms apart"},
		{wait(bg, time.Second, 700*time.Millisecond, false, panel), "animation wait: still moving at the timeout: the part at x 0-400, y 185-386 (25 % of the screen) kept changing, after 2 screenshots of 700-700 ms, at most 700 ms apart"},
		{wait(bg, time.Second, 0, true), "animation wait: the timeout passed while waiting for the app to settle, before any screenshot"},
		{wait(cancelled, time.Second, 0, false), "animation wait: stopped, after no screenshot"},
	} {
		if c.got != c.want {
			t.Errorf("trace %q, want %q", c.got, c.want)
		}
	}
}

// staged is a transition in stages, each a part of the screen of its own (a band across
// it, from y in steps of 2600/n), changing for each before resting for gap; it returns
// the parts and when the last one stops.
func staged(n int, each, gap time.Duration) ([]motionPart, time.Duration) {
	var parts []motionPart
	h := 2600 / n
	for i := range n {
		from := time.Duration(i) * (each + gap)
		parts = append(parts, motionPart{rect: image.Rect(0, i*h, 1200, i*h+h-20), from: from, until: from + each})
	}
	return parts, time.Duration(n)*(each+gap) - gap
}

// issue #83 review: transitions whose pauses are shorter than the 250 ms still span are waited
// out to the end of their motion plus the still span, whether each stage changes a new part
// of the screen or the same part again (an early end at such a pause was tried for #83 and
// dropped: these cases ended it early). Pauses of 160-249 ms are the risky band.
func TestWaitForAnimationWaitsOutTransitionsWithShortPauses(t *testing.T) {
	t.Parallel()
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	check := func(t *testing.T, name string, parts []motionPart, last time.Duration) {
		t.Helper()
		if got := timeAnimationWait(t, 8*time.Second, 0, parts...); got < last+250*time.Millisecond || got > last+400*time.Millisecond {
			t.Errorf("%s: waited %s, want the end of its motion (%s) plus the 250 ms still span", name, got, last)
		}
	}
	list, last := staged(6, ms(120), ms(230))
	check(t, "a staggered list (6 items of 120 ms, 230 ms gaps)", list, last)
	stages, last := staged(5, ms(250), ms(230))
	check(t, "5 stages with 230 ms pauses", stages, last)
	for gap := ms(160); gap < ms(250); gap += ms(15) {
		parts, last := staged(6, ms(120), gap)
		check(t, fmt.Sprintf("6 stages with %s gaps", gap), parts, last)
	}
	band := func(y int, from, until time.Duration) motionPart {
		return motionPart{rect: image.Rect(0, y, 1200, y+400), from: from, until: until}
	}
	check(t, "3 stages, the first 1.1 s long", []motionPart{band(300, 0, ms(1100)),
		band(900, ms(1300), ms(1500)), band(1500, ms(1700), ms(2000))}, ms(2000))

	// The same part, again and again: a counter ticking in place (40 ms of change, 200 ms
	// rests; its last tick ends at 1.72 s), and a progress bar filling in 11 steps with its
	// "%" label ticking beside it (until 2.44 s). Under 3 s each: a part that keeps moving
	// longer is a loop, set aside (issue #168).
	counter := motionPart{rect: image.Rect(300, 1000, 900, 1300), cycle: ms(240), pause: ms(200), until: ms(1720)}
	check(t, "a counter ticking in place", []motionPart{counter}, ms(1720))
	var progress []motionPart
	for i := range 11 {
		from := ms(240 * i)
		progress = append(progress, motionPart{rect: image.Rect(i*80, 2300, i*80+80, 2400), from: from, until: from + ms(40)})
	}
	label := motionPart{rect: image.Rect(900, 1200, 1200, 1500), cycle: ms(240), pause: ms(200), until: ms(2440)}
	check(t, "a progress fill with its % label", append(progress, label), ms(2440))

	// A full-screen fade (0-0.3 s), a small title (0.5-0.7 s), a full-screen fade again
	// (0.9-1.2 s), then the content (1.4-1.59 s).
	fade := motionPart{rect: image.Rect(0, 0, 1200, 2600), cycle: ms(900), pause: ms(600), until: ms(1200)}
	title := motionPart{rect: image.Rect(100, 200, 1100, 350), from: ms(500), until: ms(700)}
	content := band(1600, ms(1400), ms(1590))
	check(t, "full fade, title, full fade, content", []motionPart{fade, title, content}, ms(1590))
}

// issue #83: when the wait runs to its timeout, the trace says what kept changing: where on
// the screen (in screen pixels) and how much of it.
func TestWaitForAnimationTraceSaysWhatKeptChanging(t *testing.T) {
	t.Parallel()
	panel := motionPart{rect: image.Rect(0, 600, 1200, 1250)}
	clock := newFakeClock()
	screen := motionScreen(clock, panel)
	f := &fakeDriver{screen: static(), plat: "android", size: [2]int{1200, 2600}, draw: func() image.Image {
		clock.Advance(20 * time.Millisecond)
		return screen()
	}}
	how := waitForAnimationOn(context.Background(), f, time.Second, clock, false)
	want := "animation wait: still moving at the timeout: the part at x 0-1200, y 600-1253 (25 % of the screen) kept changing, after "
	if !strings.HasPrefix(how, want) {
		t.Errorf("trace %q, want it to start %q", how, want)
	}
}

// issue #83: the trace says when a loop was set aside, and where it is, in the screen's
// units: on iOS, points (the spinner covers x 20-100, y 100-150 points; the loop is the grid
// cells it touches, about 18 points each).
func TestWaitForAnimationTraceSaysWhatWasSetAside(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	screen := motionScreen(clock, spinnerPart)
	f := &fakeDriver{screen: static(), plat: "ios", size: [2]int{400, 866}, draw: func() image.Image { return screen() }}
	how := waitForAnimationOn(context.Background(), f, 8*time.Second, clock, false)
	if !strings.HasPrefix(how, "animation wait: still for 250 ms, with a loop set aside at x 18-107, y 89-160, after ") {
		t.Errorf("trace %q, want it to say a loop was set aside, and where, in points", how)
	}
}

// screenFunc is an image whose pixels a function draws: the fakes' screenshots read only the
// pixels they keep, so a screen drawn this way costs little per sample.
type screenFunc struct {
	w, h int
	at   func(x, y int) color.RGBA
}

func (s screenFunc) ColorModel() color.Model { return color.RGBAModel }
func (s screenFunc) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s screenFunc) At(x, y int) color.Color { return s.at(x, y) }

// feedScreen is a phone's home feed (1200x2600 pixels): a static title bar, a static tab bar
// and floating button, and between them the feed, from y 800 to 2400 (61 % of the screen),
// which content draws at each time since the start (ok false: the feed's static look, white
// with gray post lines).
func feedScreen(content func(at time.Duration, x, y int) (c color.RGBA, ok bool)) func(at time.Duration) image.Image {
	return func(at time.Duration) image.Image {
		return screenFunc{1200, 2600, func(x, y int) color.RGBA {
			switch {
			case y < 300: // the title bar
				return color.RGBA{30, 60, 160, 255}
			case y >= 2400: // the tab bar
				return color.RGBA{240, 240, 240, 255}
			case x >= 1000 && x < 1150 && y >= 2200 && y < 2350: // the floating button
				return color.RGBA{200, 30, 30, 255}
			case y >= 800:
				if c, ok := content(at, x, y); ok {
					return c
				}
			}
			if y%200 < 40 && x > 60 && x < 1100 {
				return color.RGBA{120, 120, 120, 255}
			}
			return color.RGBA{255, 255, 255, 255}
		}}
	}
}

func grayRGBA(v int) color.RGBA { return color.RGBA{uint8(v), uint8(v), uint8(v), 255} }

// shimmer is a skeleton placeholder's shimmer: a light band sweeping across the feed every
// 1.3 s, from gray 222 to 248, smoothly (the samples catch it at a new place each sweep).
func shimmer(at time.Duration, x, y int) color.RGBA {
	center := float64(at.Milliseconds()%1300)/1300*1800 - 300
	d := math.Abs(float64(x) - center)
	return grayRGBA(222 + int(26*math.Max(0, 1-d/300)))
}

// gifLoop is a looping animated image across the feed: 7 frames of 140 ms, each a pattern of
// colored stripes of its own.
func gifLoop(at time.Duration, x, y int) color.RGBA {
	f := int(at.Milliseconds()/140) % 7
	return color.RGBA{uint8((x/40 + f*3) % 7 * 36), uint8((y/50 + f) % 5 * 60), uint8(f * 36), 255}
}

// feedWait runs the wait on an Android fake showing screen, each screenshot taking 20 ms,
// and returns how long it took on its fake clock and how it ended.
func feedWait(timeout time.Duration, screen func(at time.Duration) image.Image) (time.Duration, string) {
	clock := newFakeClock()
	start := clock.Now()
	f := &fakeDriver{screen: static(), plat: "android", size: [2]int{1200, 2600}, draw: func() image.Image {
		img := screen(clock.Now().Sub(start))
		clock.Advance(20 * time.Millisecond)
		return img
	}}
	how := waitForAnimationOn(context.Background(), f, timeout, clock, false)
	return clock.Now().Sub(start), how
}

// issue #83: a loop too large for the 10 % cap (a shimmer over a loading feed, a looping
// animated image in a post) held waitForAnimationToEnd to its 15 s timeout, while the rest
// of the screen (title bar, tab bar, floating button) was still. A part that keeps cycling
// through the same pixels is a loop whatever its size: once it has moved for 3 s, it is set
// aside.
func TestWaitForAnimationSetsAsideALargeLoop(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]func(time.Duration, int, int) color.RGBA{"a shimmer": shimmer, "a looping image": gifLoop} {
		got, how := feedWait(15*time.Second, feedScreen(func(at time.Duration, x, y int) (color.RGBA, bool) {
			return content(at, x, y), true
		}))
		if got < 3*time.Second || got > 4500*time.Millisecond {
			t.Errorf("%s over 61 %% of the screen: waited %s (%s), want about 3-4 s", name, got, how)
		}
		if !strings.Contains(how, "with a loop set aside at x ") {
			t.Errorf("%s: trace %q, want it to say where the loop set aside is", name, how)
		}
	}
}

// issue #83: a large loop set aside does not hide what comes in its place. The shimmer gives
// way to the feed's posts, fading in over 600 ms from 2.5 s (before it is a loop) or 3.2 s
// (just after); the wait ends once they are in.
func TestWaitForAnimationLargeLoopDoesNotHideWhatReplacesIt(t *testing.T) {
	t.Parallel()
	for _, arrive := range []time.Duration{2500 * time.Millisecond, 3200 * time.Millisecond} {
		const fade = 600 * time.Millisecond
		got, how := feedWait(15*time.Second, feedScreen(func(at time.Duration, x, y int) (color.RGBA, bool) {
			if at < arrive {
				return shimmer(at, x, y), true
			}
			// A post: a photo above two lines of black text, fading in from white.
			k := math.Min(1, float64(at-arrive)/float64(fade))
			var c color.RGBA
			switch r := y % 400; {
			case r < 250 && x > 60 && x < 1140:
				c = color.RGBA{40, 140, 90, 255}
			case r >= 290 && r < 320 && x > 60 && x < 900, r >= 340 && r < 370 && x > 60 && x < 700:
				c = color.RGBA{0, 0, 0, 255}
			default:
				return grayRGBA(255), true
			}
			mix := func(v uint8) uint8 { return uint8(255 - k*float64(255-int(v))) }
			return color.RGBA{mix(c.R), mix(c.G), mix(c.B), 255}, true
		}))
		if want := arrive + fade + 250*time.Millisecond; got < want || got > want+400*time.Millisecond {
			t.Errorf("posts in at %s: waited %s (%s), want their fade's end plus the still span (%s)", arrive, got, how, want)
		}
	}
}

// issue #83: a screen transition is waited out while a large loop runs: from 2.6 s to 3.8 s
// the next screen (a dark list) slides in from the right over the feed and its shimmer.
func TestWaitForAnimationLargeLoopDoesNotHideATransition(t *testing.T) {
	t.Parallel()
	const from, until = 2600 * time.Millisecond, 3800 * time.Millisecond
	feed := feedScreen(func(at time.Duration, x, y int) (color.RGBA, bool) { return shimmer(at, x, y), true })
	got, how := feedWait(15*time.Second, func(at time.Duration) image.Image {
		img := feed(at)
		if at < from {
			return img
		}
		edge := 1200 - int(1200*math.Min(1, float64(at-from)/float64(until-from)))
		return screenFunc{1200, 2600, func(x, y int) color.RGBA {
			if x < edge {
				return img.At(x, y).(color.RGBA)
			}
			if (y/100)%2 == 0 && (x-edge)%300 < 250 {
				return color.RGBA{20, 20, 40, 255}
			}
			return color.RGBA{60, 60, 90, 255}
		}}
	})
	if want := until + 250*time.Millisecond; got < want || got > want+400*time.Millisecond {
		t.Errorf("waited %s (%s), want the transition's end plus the still span (%s)", got, how, want)
	}
}

// issue #83: while the wait goes on, the trace says what keeps it going: once, 1 s in, which
// part of the screen keeps changing (then the timeout says it again).
func TestWaitForAnimationNotesWhatKeepsItWaiting(t *testing.T) {
	t.Parallel()
	panel := motionPart{rect: image.Rect(0, 600, 1200, 1250), msPerLevel: 20} // new grays for 5 s: no loop
	clock := newFakeClock()
	start := clock.Now()
	screen := motionScreen(clock, panel)
	f := &fakeDriver{screen: static(), plat: "android", size: [2]int{1200, 2600}, draw: func() image.Image {
		clock.Advance(20 * time.Millisecond)
		return screen()
	}}
	var notes []string
	var at []time.Duration
	how := waitForAnimationNoting(context.Background(), f, 3*time.Second, clock, false, func(s string) {
		notes = append(notes, s)
		at = append(at, clock.Now().Sub(start))
	})
	want := "animation wait: still moving after 1 s: the part at x 0-1200, y 600-1253 (25 % of the screen) keeps changing"
	if len(notes) != 1 || notes[0] != want || at[0] < time.Second || at[0] > 1200*time.Millisecond {
		t.Errorf("notes %q at %v, want one, about 1 s in: %q", notes, at, want)
	}
	if !strings.HasPrefix(how, "animation wait: still moving at the timeout: the part at x 0-1200, y 600-1253") {
		t.Errorf("trace %q, want the timeout to say what kept changing", how)
	}
}

// hash32 mixes its arguments into a number that does not repeat with them.
func hash32(v ...int) uint32 {
	h := uint32(2166136261)
	for _, x := range v {
		h ^= uint32(x)
		h *= 16777619
		h ^= h >> 13
		h *= 0x5bd1e995
		h ^= h >> 15
	}
	return h
}

// issue #83 review: what never returns to an earlier frame is no loop, however long it moves
// and whatever colors it uses: each of these is waited out (435b456 took the range of colors
// a part had shown for a loop, and set them aside early).
func TestWaitForAnimationWaitsOutWhatNeverRepeats(t *testing.T) {
	t.Parallel()
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	check := func(name string, timeout, atLeast, atMost time.Duration, content func(at time.Duration, x, y int) color.RGBA) {
		t.Helper()
		got, how := feedWait(timeout, feedScreen(func(at time.Duration, x, y int) (color.RGBA, bool) {
			return content(at, x, y), true
		}))
		if got < atLeast || got > atMost {
			t.Errorf("%s: waited %s (%s), want %s to %s", name, got, how, atLeast, atMost)
		}
	}

	// A grayscale list scrolling (a fling, an auto-scroll) at 400 pixels/s until 6 s: rows
	// 60 pixels high, each its own gray with a bar of its own length.
	check("a list scrolling for 6 s", 15*time.Second, ms(6000), ms(6500), func(at time.Duration, x, y int) color.RGBA {
		off := int(min(at, ms(6000)).Milliseconds() * 400 / 1000)
		row := (y + off) / 60
		if r := (y + off) % 60; r > 15 && r < 45 && x > 60 && x < 100+int(hash32(row)%900) {
			return grayRGBA(int(hash32(row, 1) % 120))
		}
		return grayRGBA(140 + int(hash32(row)%110))
	})

	// A looping image until 3 s, then a picture cross-fading over it until 3.8 s.
	picture := func(x, y int) color.RGBA { return color.RGBA{uint8(x / 5), uint8(y / 11), uint8((x + y) / 15), 255} }
	check("a looping image, then a picture cross-fading over it", 15*time.Second, ms(3800), ms(4300), func(at time.Duration, x, y int) color.RGBA {
		const from, fade = 3000 * time.Millisecond, 800 * time.Millisecond
		if at < from {
			return gifLoop(at, x, y)
		}
		k := math.Min(1, float64(at-from)/float64(fade))
		a, b := gifLoop(from, x, y), picture(x, y)
		mix := func(p, q uint8) uint8 { return uint8(float64(p) + k*(float64(q)-float64(p))) }
		return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), 255}
	})

	// A shimmer, then light gray cards (#ECECEC: within the shimmer's grays) fading in over
	// 600 ms from the shimmer as it was, at 2.5 s (before the shimmer is a loop), 3.2 or
	// 3.6 s (about when it becomes one). The wait may end before they come (on the shimmer
	// set aside), or as they start (their first 150 ms change the screen too little to see),
	// but never while they visibly fade in.
	for _, arrive := range []time.Duration{ms(2500), ms(3200), ms(3600)} {
		const fade = 600 * time.Millisecond
		got, how := feedWait(15*time.Second, feedScreen(func(at time.Duration, x, y int) (color.RGBA, bool) {
			if at < arrive {
				return shimmer(at, x, y), true
			}
			k := math.Min(1, float64(at-arrive)/float64(fade))
			g := int(shimmer(arrive, x, y).R)
			return grayRGBA(g + int(k*float64(236-g))), true
		}))
		// Their first 150 ms change the screen too little to see (under 12 levels).
		early := arrive < ms(3000) && got < arrive+fade
		if early || (got >= arrive+ms(150) && got < arrive+fade) || got > arrive+fade+ms(500) {
			t.Errorf("a shimmer, then light gray cards fading in at %s: waited %s (%s), want it to end before they come (from 3 s on) or at the end of their fade (%s) plus up to 0.5 s",
				arrive, got, how, arrive+fade)
		}
	}

	// A video: 30 frames a second, none like another. It runs to the 8 s timeout, and one last
	// screenshot (20 ms here) at it.
	check("a video", 8*time.Second, ms(8000), ms(8100), func(at time.Duration, x, y int) color.RGBA {
		f := int(at.Milliseconds() / 33)
		v := hash32(x/40, y/40, f)
		return color.RGBA{uint8(v), uint8(v >> 8), uint8(v >> 16), 255}
	})
}

// issue #83 review: a part that moves, rests 0.4-0.6 s and moves again in the same two colors
// (a new pattern each time) is no loop: above a shimmer that is set aside (apart from it: a
// motion next to a loop delays its proof), its move from 2.1 to 3.9 s holds the wait to its
// end, plus the still span.
func TestWaitForAnimationRestingPartIsNoLoop(t *testing.T) {
	t.Parallel()
	moves := [][2]time.Duration{{0, 400 * time.Millisecond}, {800 * time.Millisecond, 1600 * time.Millisecond},
		{2100 * time.Millisecond, 3900 * time.Millisecond}, {4500 * time.Millisecond, 5300 * time.Millisecond}}
	feed := feedScreen(func(at time.Duration, x, y int) (color.RGBA, bool) { return shimmer(at, x, y), true })
	got, how := feedWait(15*time.Second, func(at time.Duration) image.Image {
		img := feed(at)
		var moved time.Duration // how long the part has moved: it moves only during the moves
		for _, m := range moves {
			moved += max(0, min(at, m[1])-m[0])
		}
		off := int(moved.Milliseconds() / 2)
		return screenFunc{1200, 2600, func(x, y int) color.RGBA {
			if y >= 300 && y < 650 {
				if hash32((x+off)/30, y/100)%2 == 0 {
					return color.RGBA{0, 0, 0, 255}
				}
				return color.RGBA{255, 255, 255, 255}
			}
			return img.At(x, y).(color.RGBA)
		}}
	})
	if got < 4150*time.Millisecond || got >= 4500*time.Millisecond {
		t.Errorf("waited %s (%s), want the end of the part's move at 3.9 s plus the still span", got, how)
	}
}

// Maestro's e2e flow waitForAnimationToEnd: an animation that never stops, then
// waitForAnimationToEnd with timeout: 1000, then assertTrue endTime - startTime > 1000. A wait
// that times out ends after a last screenshot taken at its timeout, so more than the timeout
// has passed when it returns (on main it returned at the timeout itself, 1000 ms, and the
// flow's strict check failed).
func TestWaitForAnimationTimeoutEndsAfterALastScreenshot(t *testing.T) {
	t.Parallel()
	panel := motionPart{rect: image.Rect(0, 600, 1200, 1250), msPerLevel: 20}
	if got := timeAnimationWait(t, time.Second, 30*time.Millisecond, panel); got <= time.Second {
		t.Fatalf("returned after %s, want more than the 1 s timeout (a last screenshot taken at it)", got)
	}
}

// shotCut is a fake whose screenshots take shot on the clock and are cut short by the end of
// their context, as a real driver's are.
type shotCut struct {
	*fakeDriver
	clock *fakeClock
	shot  time.Duration
}

func (s shotCut) Screenshot(ctx context.Context, scale float64, f string) ([]byte, error) {
	done := s.clock.Now().Add(s.shot)
	s.clock.advanceTo(done, true)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.fakeDriver.Screenshot(ctx, scale, f)
}

// Review of the fix above: on Android a screenshot takes 50-150 ms, so the timeout often falls
// in one, which the driver then cuts short. The last screenshot still follows, and the wait
// still ends after its timeout.
func TestWaitForAnimationTimeoutInACutScreenshotStillTakesALastOne(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	start := clock.Now()
	screen := motionScreen(clock, motionPart{rect: image.Rect(0, 600, 1200, 1250), msPerLevel: 20})
	f := shotCut{&fakeDriver{screen: static(), plat: "android", draw: func() image.Image { return screen() }}, clock, 70 * time.Millisecond}
	how := waitForAnimationOn(context.Background(), f, time.Second, clock, false)
	if got := clock.Now().Sub(start); got <= time.Second {
		t.Fatalf("returned after %s (%s), want more than the 1 s timeout", got, how)
	}
}
