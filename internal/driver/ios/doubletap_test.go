package ios

import (
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
