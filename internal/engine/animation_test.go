package engine

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"
)

// A part of a motionScreen that repaints while it moves: from..until after the start
// (until 0: forever).
type motionPart struct {
	rect        image.Rectangle
	from, until time.Duration
}

// motionScreen draws a phone's screen (1200x2600 pixels, 400x866 points at 3x) in white,
// with each part filled in a gray that changes with time while the part moves.
func motionScreen(start time.Time, parts ...motionPart) func() *image.RGBA {
	return func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 1200, 2600))
		draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
		elapsed := time.Since(start)
		for _, p := range parts {
			at := max(elapsed, p.from)
			if p.until > 0 {
				at = min(at, p.until)
			}
			// 20 levels apart from one 80 ms sample to the next, repeating every ~1 s.
			gray := uint8(at.Milliseconds() / 4 % 256)
			draw.Draw(img, p.rect, image.NewUniform(color.Gray{gray}), image.Point{}, draw.Src)
		}
		return img
	}
}

func timeAnimationWait(t *testing.T, timeout time.Duration, parts ...motionPart) time.Duration {
	t.Helper()
	start := time.Now()
	screen := motionScreen(start, parts...)
	f := &fakeDriver{screen: static(), plat: "ios", draw: func() image.Image { return screen() }}
	waitForAnimation(context.Background(), f, timeout)
	return time.Since(start)
}

// A spinner-sized part (80x50 points, 1.2 % of the screen: more than the 1 % that counts as
// a change) and a progress bar across the bottom (8 points high).
var (
	spinnerPart = motionPart{rect: image.Rect(60, 300, 300, 450)}
	barPart     = motionPart{rect: image.Rect(0, 2400, 1200, 2424)}
)

// A still screen is still after the first 250 ms.
func TestWaitForAnimationStillScreen(t *testing.T) {
	t.Parallel()
	if got := timeAnimationWait(t, 6*time.Second); got > 600*time.Millisecond {
		t.Fatalf("waited %s on a still screen, want about 0.3 s", got)
	}
}

// issue #168: a small part that never stops moving (a spinner, a marquee, a pulsing button)
// does not hold waitForAnimationToEnd to its timeout once it has moved for a second.
func TestWaitForAnimationSetsAsideALoop(t *testing.T) {
	t.Parallel()
	if got := timeAnimationWait(t, 6*time.Second, spinnerPart); got > 2500*time.Millisecond {
		t.Fatalf("waited %s on a screen with a small endless loop, want about 1.1 s", got)
	}
}

// issue #168: two loops apart (a spinner at the top, a progress bar at the bottom) are set
// aside too.
func TestWaitForAnimationSetsAsideTwoLoops(t *testing.T) {
	t.Parallel()
	if got := timeAnimationWait(t, 6*time.Second, spinnerPart, barPart); got > 2500*time.Millisecond {
		t.Fatalf("waited %s on a screen with two small endless loops, want about 1.1 s", got)
	}
}

// A large part that keeps repainting (like the Flutter regression 07 panel, a quarter of the
// screen) is not a loop to set aside: the wait runs to its timeout.
func TestWaitForAnimationWaitsOnALargeRepaint(t *testing.T) {
	t.Parallel()
	panel := motionPart{rect: image.Rect(0, 600, 1200, 1250)}
	if got := timeAnimationWait(t, 2*time.Second, panel); got < 1900*time.Millisecond {
		t.Fatalf("returned after %s while a quarter of the screen repainted, want the 2 s timeout", got)
	}
}

// A short one-shot animation, even a small one, is waited out.
func TestWaitForAnimationWaitsOutAShortAnimation(t *testing.T) {
	t.Parallel()
	badge := motionPart{rect: image.Rect(60, 900, 300, 1050), until: 800 * time.Millisecond}
	if got := timeAnimationWait(t, 6*time.Second, badge); got < 1050*time.Millisecond || got > 2500*time.Millisecond {
		t.Fatalf("waited %s on a 0.8 s animation, want its end plus the 250 ms still screen", got)
	}
}

// issue #168: a loop set aside does not hide other motion. A card sliding down the screen
// for 2.5 s (each part of the screen changes only while the card passes) is waited out.
func TestWaitForAnimationLoopDoesNotHideOtherMotion(t *testing.T) {
	t.Parallel()
	start := time.Now()
	loop := motionScreen(start, spinnerPart)
	f := &fakeDriver{screen: static(), plat: "ios", draw: func() image.Image {
		img := loop()
		y := 600 + int(min(time.Since(start), 2500*time.Millisecond).Milliseconds()*3/5) // 200 points/s
		draw.Draw(img, image.Rect(450, y, 750, y+300), image.Black, image.Point{}, draw.Src)
		return img
	}}
	waitForAnimation(context.Background(), f, 6*time.Second)
	if got := time.Since(start); got < 2700*time.Millisecond || got > 4*time.Second {
		t.Fatalf("waited %s, want the card's stop at 2.5 s plus the 250 ms still screen", got)
	}
}
