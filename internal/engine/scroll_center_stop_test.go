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

func bouncingEnd(f *fakeDriver, sprung, settle time.Duration) {
	var pull, last atomic.Int64
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
		move(x1, y1, x2, y2)
		drags++
		final := int64(drags % 2)
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

func TestScrollCenterStopEndsScroll(t *testing.T) {
	t.Setenv("TB_CI", "0")
	for _, tc := range []struct {
		name           string
		sprung, settle time.Duration
	}{
		{"point off once settled", 150 * time.Millisecond, 0},
		{"point off before the look", 80 * time.Millisecond, 150 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			f.scroller(30, false, 800)
			bouncingEnd(f, tc.sprung, tc.settle)
			l := &swipeLog{
				fakeDriver:  f,
				slow:        func(int) time.Duration { return 500 * time.Millisecond },
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
		off, _ := f.scroller(60, false, 0)
		move := f.onSwipe
		f.onSwipe = func(x1, y1, x2, y2 int) { move(x1, y1, x2, y1+(y2-y1)/2) }
		odd := func(n int) bool { return n%2 == 1 }
		l := &swipeLog{
			fakeDriver:  f,
			dropped:     odd,
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

func TestScrollCenterStopUndoneByLateMove(t *testing.T) {
	t.Setenv("TB_CI", "0")
	f := &fakeDriver{plat: "ios"}
	off, _ := f.scroller(60, false, 300)
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
		move(x1, y1, x1, y1+1)
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
