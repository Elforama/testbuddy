package ios

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Issue #212: two tapOn steps on the same spot, one right after the other, are two taps, not a
// double tap. Sent within UIKit's double-tap interval (350 ms), a WKWebView took them as its
// double-tap zoom: the page zoomed (or zoomed back out) and one or both clicks were lost
// ("Taps: 2" read "Taps: 1"). A tap near the last one waits until that interval has passed
// since it; a tap elsewhere does not.
func TestTapsOnOneSpotAreNotADoubleTap(t *testing.T) {
	const uikitDoubleTap = 350 * time.Millisecond
	r := newFakeRunner(t)
	var mu sync.Mutex
	var arrived []time.Time
	stamp := func(map[string]any) map[string]any {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		return map[string]any{"result": "changed", "seq": 1}
	}
	r.on("tap", stamp)
	r.on("doubleTap", stamp)
	d := newIOSDriver(t, r)
	gap := func(i int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return arrived[i].Sub(arrived[i-1])
	}
	tap := func(x, y int) {
		t.Helper()
		if _, err := d.Tap(ctxT(t), x, y, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
	}

	tap(49, 158)
	tap(49, 158) // the same spot
	if g := gap(1); g < uikitDoubleTap {
		t.Errorf("a second tap on the same spot went %v after the first: within the double-tap interval (%v), a web view takes the pair as a double tap", g, uikitDoubleTap)
	}
	tap(52, 161) // a few points off: still the same spot to a double-tap recognizer
	if g := gap(2); g < uikitDoubleTap {
		t.Errorf("a tap 3 pt from the last went %v after it; want at least %v", g, uikitDoubleTap)
	}
	if _, err := d.DoubleTap(ctxT(t), 52, 161, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if g := gap(3); g < uikitDoubleTap {
		t.Errorf("a double tap on the spot of the last tap went %v after it (a triple tap); want at least %v", g, uikitDoubleTap)
	}
	tap(52, 161)
	if g := gap(4); g < uikitDoubleTap {
		t.Errorf("a tap on the spot of the last double tap went %v after it; want at least %v", g, uikitDoubleTap)
	}
	tap(300, 700) // elsewhere: no double tap to avoid
	if g := gap(5); g >= uikitDoubleTap {
		t.Errorf("a tap far from the last waited %v; only taps near the last one wait", g)
	}
}

// Issue #212: a tap of a tapOn's repeat after its first goes when the flow's delay says (a
// repeat may be meant as a double tap), and still counts as the last tap for the next one. A
// long press or swipe ends the tap sequence: a tap after one does not wait.
func TestTapSpacingLeavesRepeatsAndOtherGesturesAlone(t *testing.T) {
	const uikitDoubleTap = 350 * time.Millisecond
	r := newFakeRunner(t)
	var mu sync.Mutex
	var arrived []time.Time
	stamp := func(map[string]any) map[string]any {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		return map[string]any{"result": "changed", "seq": 1}
	}
	for _, op := range []string{"tap", "press", "swipe"} {
		r.on(op, stamp)
	}
	d := newIOSDriver(t, r)
	gap := func(i int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return arrived[i].Sub(arrived[i-1])
	}
	tap := func(o driver.ActionOpts) {
		t.Helper()
		if _, err := d.Tap(ctxT(t), 49, 158, o); err != nil {
			t.Fatal(err)
		}
	}

	tap(driver.ActionOpts{})
	tap(driver.ActionOpts{InRepeat: true})
	if g := gap(1); g >= uikitDoubleTap {
		t.Errorf("a repeat's second tap waited %v; the flow's delay times it", g)
	}
	tap(driver.ActionOpts{}) // the next step: kept apart from the repeat's last tap
	if g := gap(2); g < uikitDoubleTap {
		t.Errorf("a tap after a repeat went %v after its last tap; want at least %v", g, uikitDoubleTap)
	}
	if _, err := d.LongPress(ctxT(t), 49, 158, 10*time.Millisecond, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	tap(driver.ActionOpts{})
	if g := gap(4); g >= uikitDoubleTap {
		t.Errorf("a tap after a long press waited %v", g)
	}
	if _, err := d.Swipe(ctxT(t), 49, 158, 49, 300, 10*time.Millisecond, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	tap(driver.ActionOpts{})
	if g := gap(6); g >= uikitDoubleTap {
		t.Errorf("a tap after a swipe waited %v", g)
	}
}

// Issue #212: a tap cancelled while it waits out the last tap's double-tap window returns the
// context's error and sends nothing.
func TestTapCancelledWhileSpacedSendsNothing(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 1} })
	d := newIOSDriver(t, r)
	if _, err := d.Tap(ctxT(t), 49, 158, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.Tap(ctx, 49, 158, driver.ActionOpts{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err %v, want the context's", err)
	}
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Errorf("the cancelled tap returned after %v", took)
	}
	if n := len(r.requests("tap")); n != 1 {
		t.Errorf("%d taps sent, want 1 (the cancelled one sends nothing)", n)
	}
}

// Issue #212: the window counts from the touch's confirmation, so the agent's settle wait after
// it (settleMs) already counts toward it.
func TestTapSpacingCountsTheSettleWait(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tap", func(map[string]any) map[string]any {
		return map[string]any{"result": "changed", "seq": 1, "settleMs": int(doubleTapWindow.Milliseconds())}
	})
	d := newIOSDriver(t, r)
	if _, err := d.Tap(ctxT(t), 49, 158, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := d.Tap(ctxT(t), 49, 158, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("a tap after one that settled for the whole window waited %v", took)
	}
}
