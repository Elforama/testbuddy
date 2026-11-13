package engine

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// settledFake is a fake whose driver can say it is settled right now (driver.SettledChecker),
// as the web driver can.
type settledFake struct {
	*fakeDriver
	unsettled atomic.Bool // SettledNow answers false
}

func (f *settledFake) SettledNow() bool { return !f.unsettled.Load() }

// twoTaps runs "tap A, tap B" on d (f being its fake) and returns the screen reads the second
// tap made and how long it took, from the first tap. onSecond runs inside every WaitSettled
// that follows the second tap's first read of the screen.
func twoTaps(t *testing.T, d driver.Driver, f *fakeDriver, onSecond func()) (reads int64, took time.Duration) {
	t.Helper()
	var mu sync.Mutex
	var after int64
	var at time.Time
	f.onTap = func(x, y int) {
		mu.Lock()
		defer mu.Unlock()
		if at.IsZero() {
			after, at = f.reads.Load(), time.Now()
		}
	}
	if onSecond != nil {
		f.onWait = func() {
			mu.Lock()
			read := !at.IsZero() && f.reads.Load() > after
			mu.Unlock()
			if read {
				onSecond()
			}
		}
	}
	mustPass(t, runWith(t, d, quick, "- tapOn: A\n- tapOn: B"))
	mu.Lock()
	defer mu.Unlock()
	return f.reads.Load() - after, time.Since(at)
}

// Issue #80: on a still screen whose driver reports every change (the web), a tap right after
// another tap read its target three times, 40 ms apart, although no change event arrived since
// the first read: every change bumps the counter, so those reads could not differ. It reads it
// once and taps.
func TestTapOnStillTrackedScreenReadsOnce(t *testing.T) {
	f := &fakeDriver{plat: "web", tracked: true, screen: static(btn("A", "", 100), btn("B", "", 300))}
	reads, took := twoTaps(t, &settledFake{fakeDriver: f}, f, nil)
	if reads != 1 {
		t.Errorf("the second tap read the screen %d times, want 1: nothing changed since its first read", reads)
	}
	if took >= restGap {
		t.Errorf("the second tap took %s, want no stability re-reads (they are %s apart)", took, restGap)
	}
	if len(f.taps) != 2 || f.taps[1] != [2]int{200, 325} {
		t.Errorf("taps %v", f.taps)
	}
}

// Issue #80 guard: a change event between the first read and the tap still has the target read
// again until it rests, where it moved to.
func TestTapOnTrackedScreenRereadsAfterChange(t *testing.T) {
	var top atomic.Int64
	top.Store(300)
	f := &fakeDriver{plat: "web", tracked: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("A", "", 100), btn("B", "", int(top.Load()))} }
	var once sync.Once
	reads, _ := twoTaps(t, &settledFake{fakeDriver: f}, f, func() {
		once.Do(func() { // it moves while the screen settles, after the second tap's first read
			top.Store(340)
			f.change(true)
		})
	})
	if reads < 2 {
		t.Errorf("the second tap read the screen %d times: a change arrived since its first read", reads)
	}
	if len(f.taps) != 2 || f.taps[1] != [2]int{200, 365} {
		t.Errorf("taps %v, want the second where B moved to, (200, 365)", f.taps)
	}
}

// Issue #80 guard: where the driver cannot promise an event for every change, the reads stay.
// The target slides on without an event (Android and iOS motion; on the web, an animation that
// was running when it was read, which the driver reports by leaving the screen untracked), or
// the driver is not settled right now, or cannot say: the tap waits for the target to rest and
// lands there.
func TestTapOnUntrackedScreenStillRereads(t *testing.T) {
	cases := []struct {
		name      string
		plat      string
		tracked   bool
		checker   bool // the driver is a SettledChecker
		unsettled bool // and says it is not settled
	}{
		{"android", "android", false, false, false},
		{"ios", "ios", false, false, false},
		{"web, animation running", "web", false, true, false},
		{"web, not settled", "web", true, true, true},
		{"web, no settled check", "web", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var slid atomic.Bool
			f := &fakeDriver{plat: c.plat, tracked: c.tracked}
			f.screen = func() []*ui.Node {
				top := 300
				if slid.Load() {
					top = 340
				}
				return []*ui.Node{btn("A", "", 100), btn("B", "", top)}
			}
			var d driver.Driver = f
			if c.checker {
				sf := &settledFake{fakeDriver: f}
				sf.unsettled.Store(c.unsettled)
				d = sf
			}
			// It slides on silently (no change event) once the second tap's first read is made.
			reads, _ := twoTaps(t, d, f, func() { slid.Store(true) })
			if reads < 3 {
				t.Errorf("the second tap read the screen %d times: it must check that the target rests", reads)
			}
			if len(f.taps) != 2 || f.taps[1] != [2]int{200, 365} {
				t.Errorf("taps %v, want the second where B came to rest, (200, 365)", f.taps)
			}
		})
	}
}
