package engine

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
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
		{wait(bg, time.Second, 700*time.Millisecond, false, panel), "animation wait: still moving at the timeout, after 1 screenshot of 700 ms"},
		{wait(bg, time.Second, 0, true), "animation wait: the timeout passed while waiting for the app to settle, before any screenshot"},
		{wait(cancelled, time.Second, 0, false), "animation wait: stopped, after no screenshot"},
	} {
		if c.got != c.want {
			t.Errorf("trace %q, want %q", c.got, c.want)
		}
	}
}

// issue #83: a large part that keeps changing, with short rests (a feed post that
// repaints: 200 ms of change, then 240 ms of rest, over and over), held
// waitForAnimationToEnd to its 15 s timeout, where Maestro's ends at its first pair of
// matching screenshots. The rests are shorter than the 250 ms still span, and the part is
// too large to set aside as a loop. Once the same part has changed in 3 bursts in a row, and
// the screen has been watched for 1 s, a rest in which it does not change at all (by
// Maestro's formula and 0.005 % threshold) ends the wait.
func TestWaitForAnimationEndsOnARestlessScreen(t *testing.T) {
	t.Parallel()
	post := motionPart{rect: image.Rect(0, 600, 1200, 1300), cycle: 440 * time.Millisecond, pause: 240 * time.Millisecond}
	clock := newFakeClock()
	start := clock.Now()
	screen := motionScreen(clock, post)
	f := &fakeDriver{screen: static(), plat: "android", size: [2]int{1200, 2600}, draw: func() image.Image { return screen() }}
	how := waitForAnimationOn(context.Background(), f, 15*time.Second, clock, false)
	if got := clock.Now().Sub(start); got < time.Second || got > 2*time.Second {
		t.Fatalf("waited %s on a screen that rests 240 ms between changes, want 1-2 s (trace: %s)", got, how)
	}
	if !strings.Contains(how, "still enough") || !strings.Contains(how, "x 0-1200, y 600-1300") {
		t.Errorf("trace %q, want it to say the wait ended early, still enough, and what kept changing", how)
	} else {
		t.Log(how)
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

// issue #83 review: the early end at a rest does not cut a real transition short. A
// transition whose stages change different parts of the screen, with pauses shorter than the
// 250 ms still span between them, is waited out to the end of its last stage plus the still
// span, as before the restless rule: its pauses are not rests of a motion that repeats in the
// same place. Pauses of 160-249 ms (long enough to look quiet for 150 ms) are the risky band.
func TestWaitForAnimationRestlessDoesNotCutATransitionShort(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, name string, parts []motionPart, last time.Duration) {
		t.Helper()
		if got := timeAnimationWait(t, 8*time.Second, 0, parts...); got < last+250*time.Millisecond || got > last+400*time.Millisecond {
			t.Errorf("%s: waited %s, want the end of its last stage (%s) plus the 250 ms still span", name, got, last)
		}
	}
	list, last := staged(6, 120*time.Millisecond, 230*time.Millisecond)
	check(t, "a staggered list (6 items of 120 ms, 230 ms gaps)", list, last)
	stages, last := staged(5, 250*time.Millisecond, 230*time.Millisecond)
	check(t, "5 stages with 230 ms pauses", stages, last)
	for gap := 160 * time.Millisecond; gap < 250*time.Millisecond; gap += 15 * time.Millisecond {
		parts, last := staged(6, 120*time.Millisecond, gap)
		check(t, fmt.Sprintf("6 stages with %s gaps", gap), parts, last)
	}
	// A long first stage, then two short ones: a pause after 1 s of change does not end it.
	stage := func(y int, from, until time.Duration) motionPart {
		return motionPart{rect: image.Rect(0, y, 1200, y+400), from: from, until: until}
	}
	check(t, "3 stages, the first 1.1 s long", []motionPart{stage(300, 0, 1100*time.Millisecond),
		stage(900, 1300*time.Millisecond, 1500*time.Millisecond), stage(1500, 1700*time.Millisecond, 2*time.Second)}, 2*time.Second)
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
