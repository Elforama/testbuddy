package engine

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// bouncingEnd makes f's list (scroller, at its end) behave as an iOS list does when dragged
// past its end: the drag pulls the content along (rubber band), it springs back to where it
// was, and settles there or a point off it (rounding of the spring's last frame), alternately.
// The list's offset never changes. sprung is how long after the drag the spring back ends; settle, when nonzero, how long after the drag the final point shows (else it
// shows on the next WaitSettled, as the settled screen).
func bouncingEnd(f *fakeDriver, sprung, settle time.Duration) {
	var pull, last atomic.Int64 // the rubber band's pull, and the settled point's offset
	var mu sync.Mutex
	var pending func()
	rows := f.screen
	f.screen = func() []*ui.Node {
		nodes := rows()
		d := int(pull.Load() + last.Load())
		for _, n := range nodes[1:] {
			n.Bounds.T -= d
			n.Bounds.B -= d
		}
		return nodes
	}
	move := f.onSwipe
	drags := 0
	f.onSwipe = func(x1, y1, x2, y2 int) {
		move(x1, y1, x2, y2) // at its end: the offset stays
		drags++
		final := int64(drags % 2)
		// Springs back frame by frame (never still until it is back), over sprung.
		pull.Store(40)
		f.change(true)
		for i := 1; i <= 8; i++ {
			left := int64(40 - 5*i)
			time.AfterFunc(sprung*time.Duration(i)/8, func() { pull.Store(left); f.change(true) })
		}
		land := func() {
			if last.Swap(final) != final {
				f.change(true)
			}
		}
		if settle > 0 {
			time.AfterFunc(settle, land)
			return
		}
		mu.Lock()
		pending = land
		mu.Unlock()
	}
	f.onWait = func() {
		mu.Lock()
		p := pending
		pending = nil
		mu.Unlock()
		if p != nil {
			p()
		}
	}
}

// Issue #377: on iOS a centering drag on a list at its end springs back to where it was, or a
// point off it, and its touches are often reported unconfirmed. Once the scroll has decided
// that the element is fully visible and its list cannot move farther, it ends: no more drags
// (before, the settled screen a point off undid the stop, and up to 4 more drags went, 2-4 s).
// A point off is not a move either: the stop comes after the first centering drag.
func TestScrollCenterStopEndsScroll(t *testing.T) {
	t.Setenv("TB_CI", "0")
	for _, tc := range []struct {
		name           string
		sprung, settle time.Duration
	}{
		// The spring back shows before the 500 ms look; the last point once the screen settles.
		// Both well before it (review of #377: timers racing the look flake under load).
		{"point off once settled", 150 * time.Millisecond, 0},
		// The last point shows before the 500 ms look.
		{"point off before the look", 80 * time.Millisecond, 150 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			f.scroller(30, false, 800) // at its end: Item 27 at 650-700, whole, off-center
			bouncingEnd(f, tc.sprung, tc.settle)
			l := &swipeLog{
				fakeDriver:  f,
				slow:        func(int) time.Duration { return 500 * time.Millisecond }, // an iOS drag's send
				unconfirmed: func(int) bool { return true },
			}
			res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 27, centerElement: true, timeout: 15000}")
			mustPass(t, res)
			f.mu.Lock()
			drags := len(f.swipeAt)
			f.mu.Unlock()
			if drags != 1 {
				t.Errorf("issue #377: %d drags, want 1: the list is at its end; trace:\n%s", drags, trace)
			}
			if c := strings.Count(trace, "its list cannot move farther toward the center"); c != 1 {
				t.Errorf("issue #377: the stop traced %d times, want once; trace:\n%s", c, trace)
			}
		})
	}
}

// Issue #377, CI mode: a centering drag the app provably did not get (#111) is sent again at
// the stop, but at most once per stop: the drag sent again that moves nothing either ends the
// scroll. A centering drag that moves the element starts a new stop, with its own resend: a
// centering of several drags, two of them undelivered, is not stopped short (review of #377).
func TestScrollCenterStopResendsOnce(t *testing.T) {
	t.Setenv("TB_CI", "1")
	t.Run("at its end", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		f.scroller(30, false, 800)
		l := &swipeLog{
			fakeDriver:  f,
			dropped:     func(int) bool { return true },
			unconfirmed: func(int) bool { return true },
			verdict:     func(int) string { return driver.NotDelivered },
		}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 27, centerElement: true, timeout: 15000}")
		mustPass(t, res)
		f.mu.Lock()
		drags := len(f.swipeAt)
		f.mu.Unlock()
		if drags != 2 {
			t.Errorf("issue #377: %d drags, want 2 (one sent again); trace:\n%s", drags, trace)
		}
		if c := strings.Count(trace, "sending it again"); c != 1 {
			t.Errorf("issue #377: %s sent again, want once; trace:\n%s", fmt.Sprint(c), trace)
		}
	})
	t.Run("two stops", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		off, _ := f.scroller(60, false, 0) // Item 13 at 750-800: whole, far below the middle
		move := f.onSwipe
		f.onSwipe = func(x1, y1, x2, y2 int) { move(x1, y1, x2, y1+(y2-y1)/2) } // the list follows half the drag
		odd := func(n int) bool { return n%2 == 1 }
		l := &swipeLog{
			fakeDriver:  f,
			dropped:     odd, // drags 1 and 3 never reach the app; their resends do
			unconfirmed: odd,
			verdict: func(n int) string {
				if odd(n) {
					return driver.NotDelivered
				}
				return driver.Delivered
			},
		}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 13, centerElement: true, timeout: 15000}")
		mustPass(t, res)
		if c := strings.Count(trace, "sending it again"); c != 2 {
			t.Errorf("review of #377: %d sent again, want 2 (one per stop); trace:\n%s", c, trace)
		}
		if cy := 100 + 13*50 - int(off.Load()) + 25; cy < 400-160 || cy > 400+160 {
			t.Errorf("review of #377: Item 13's center at %d, want near 400; trace:\n%s", cy, trace)
		}
	})
}

// Review of #377: a stop judged on a look the app had not caught up with (its touches queued,
// slow to show the drag) does not hold once the element moves: centering goes on and ends with
// the element centered. Here the drag shows only once the screen settles, and moves the list a
// fifth of the way.
func TestScrollCenterStopUndoneByLateMove(t *testing.T) {
	t.Setenv("TB_CI", "0")
	f := &fakeDriver{plat: "ios"}
	off, _ := f.scroller(60, false, 300) // Item 16 at 600-650: whole, off-center
	move := f.onSwipe
	var mu sync.Mutex
	var pending func()
	drags := 0
	f.onSwipe = func(x1, y1, x2, y2 int) {
		drags++
		if drags > 1 {
			move(x1, y1, x2, y2)
			return
		}
		move(x1, y1, x1, y1+1) // the touch-down nudges the list a point: within the slack
		mu.Lock()
		pending = func() { move(x1, y1, x2, y1+(y2-y1)/5) }
		mu.Unlock()
	}
	f.onWait = func() {
		mu.Lock()
		p := pending
		pending = nil
		mu.Unlock()
		if p != nil {
			p()
		}
	}
	l := &swipeLog{fakeDriver: f, slow: func(int) time.Duration { return 700 * time.Millisecond }}
	res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 16, centerElement: true, timeout: 15000}")
	mustPass(t, res)
	if cy := 100 + 16*50 - int(off.Load()) + 25; cy < 400-160 || cy > 400+160 {
		t.Errorf("review of #377: Item 16's center at %d, want near 400; trace:\n%s", cy, trace)
	}
}
