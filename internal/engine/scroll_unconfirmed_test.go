package engine

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// swipeLog wraps the fake driver for issue #111: each swipe can take a while to send (a
// simulator still starting up), be dropped (its touches never reached the app, so the screen
// does not move), and say so, as the iOS agent does when testmanagerd only confirmed it
// implicitly. It records when each swipe was sent and when it returned.
type swipeLog struct {
	*fakeDriver
	slow        func(n int) time.Duration // how long the n-th swipe (from 1) takes to send
	dropped     func(n int) bool          // whether the n-th swipe's touches never reached the app
	unconfirmed func(n int) bool          // whether the agent reports the n-th swipe as unconfirmed
	// late: how long after the n-th swipe returns its touches move the list (an app whose
	// main thread was blocked: the queued touches land once it runs); 0 for at once.
	late func(n int) time.Duration

	mu       sync.Mutex
	sent     []time.Time
	returned []time.Time
}

func (l *swipeLog) Swipe(ctx context.Context, x1, y1, x2, y2 int, d time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	l.mu.Lock()
	l.sent = append(l.sent, time.Now())
	n := len(l.sent)
	l.mu.Unlock()
	if l.slow != nil {
		time.Sleep(l.slow(n))
	}
	var res driver.ActionResult
	var err error
	delay := time.Duration(0)
	if l.late != nil {
		delay = l.late(n)
	}
	if (l.dropped != nil && l.dropped(n)) || delay > 0 {
		// Recorded as sent, but the screen does not move (or not yet).
		l.fakeDriver.mu.Lock()
		l.fakeDriver.swipes = append(l.fakeDriver.swipes, o)
		l.fakeDriver.swipeAt = append(l.fakeDriver.swipeAt, [4]int{x1, y1, x2, y2})
		move := l.fakeDriver.onSwipe
		l.fakeDriver.mu.Unlock()
		if delay > 0 && move != nil {
			time.AfterFunc(delay, func() { move(x1, y1, x2, y2) })
		}
		res = driver.ActionResult{Settle: driver.NoChange}
	} else {
		res, err = l.fakeDriver.Swipe(ctx, x1, y1, x2, y2, d, o)
	}
	res.Unconfirmed = l.unconfirmed != nil && l.unconfirmed(n)
	l.mu.Lock()
	l.returned = append(l.returned, time.Now())
	l.mu.Unlock()
	return res, err
}

// gap is the time between the n-th swipe's return and the next swipe's send (n from 1).
func (l *swipeLog) gap(t *testing.T, n int) time.Duration {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.sent) <= n {
		t.Fatalf("%d swipes sent, want more than %d", len(l.sent), n)
	}
	return l.sent[n].Sub(l.returned[n-1])
}

func runTraced(t *testing.T, d driver.Driver, yaml string) (FlowResult, string) {
	t.Helper()
	var buf bytes.Buffer
	o := quick
	o.Trace = NewTrace(&buf)
	res := runWith(t, d, o, yaml)
	return res, buf.String()
}

// Issue #111, item 1: after a drag that moved nothing, tb waits up to 2 s for a change. That
// wait was timed from when the drag began, so a drag that itself took longer than 2 s to send
// (a simulator still starting up) got no wait at all, and the trace said nothing of it. In CI
// mode (where the slow simulators are) it is timed from when the drag returned. Outside CI the
// timing stays as it was, and the trace says the drag used up the wait.
func TestScrollWaitTimedFromDragReturn(t *testing.T) {
	setup := func() *swipeLog {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		return &swipeLog{
			fakeDriver: f,
			slow: func(n int) time.Duration {
				if n == 1 {
					return 2300 * time.Millisecond
				}
				return 0
			},
			dropped: func(n int) bool { return n == 1 },
		}
	}
	t.Run("CI mode", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		l := setup()
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if !strings.Contains(trace, "waiting up to 2 s for a change") {
			t.Errorf("issue #111: no 2 s wait after a slow drag that moved nothing; trace:\n%s", trace)
		}
		if g := l.gap(t, 1); g < 1500*time.Millisecond {
			t.Errorf("issue #111: the next drag went %v after the slow one returned, want the 2 s wait for a change", g)
		}
	})
	t.Run("outside CI", func(t *testing.T) {
		t.Setenv("TB_CI", "0")
		l := setup()
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if !strings.Contains(trace, "which used up the 2 s wait for a change") {
			t.Errorf("issue #111: the trace does not say the slow drag used up the wait; trace:\n%s", trace)
		}
		if g := l.gap(t, 1); g > 1500*time.Millisecond {
			t.Errorf("outside CI the wait is timed from the drag's start, as before: the next drag went %v after", g)
		}
	})
}

// Issue #111, item 2: drags whose touches never reached the app were reported as "2 drags in a
// row brought nothing new into view (a paging list springs back)", which names the wrong cause.
// The line names every cause it may be, and says when the agent reported the touches as never
// confirmed.
func TestScrollFruitlessDragsWording(t *testing.T) {
	t.Setenv("TB_CI", "0")
	flingLine := func(t *testing.T, trace string) string {
		t.Helper()
		for _, line := range strings.Split(trace, "\n") {
			if strings.Contains(line, "flinging from now on") {
				return line
			}
		}
		t.Fatalf("no switch to flinging; trace:\n%s", trace)
		return ""
	}
	t.Run("touches dropped", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		l := &swipeLog{fakeDriver: f, dropped: func(n int) bool { return true }}
		_, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 6000}")
		if line := flingLine(t, trace); !strings.Contains(line, "touches that did not reach the app") {
			t.Errorf("issue #111: %q names only a paging list", line)
		}
	})
	t.Run("touches never confirmed", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		l := &swipeLog{fakeDriver: f, dropped: func(n int) bool { return true }, unconfirmed: func(n int) bool { return true }}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 6000}")
		if line := flingLine(t, trace); !strings.Contains(line, "the app never confirmed the touches of") {
			t.Errorf("issue #111: %q does not say the app never confirmed the touches", line)
		}
		mustFail(t, res, "the app never confirmed the touches of")
	})
}

// Issue #111, item 3: the iOS agent reports a swipe whose touches testmanagerd only confirmed
// implicitly ("Timed out after waiting 5.0s for TouchEventsCompleted"). The trace says so on
// every run. In CI mode, when the screen did not move, the drag is sent again at once: no wait
// for a change that will not come, and it does not count as a drag that found nothing new.
func TestScrollUnconfirmedSwipeSentAgain(t *testing.T) {
	setup := func() *swipeLog {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		return &swipeLog{
			fakeDriver:  f,
			dropped:     func(n int) bool { return n == 1 },
			unconfirmed: func(n int) bool { return n == 1 },
		}
	}
	t.Run("CI mode", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		l := setup()
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if !strings.Contains(trace, "the app never confirmed swipe 1's touches") {
			t.Errorf("issue #111: the trace does not report the unconfirmed swipe; trace:\n%s", trace)
		}
		if !strings.Contains(trace, "sending it again at once (CI mode)") {
			t.Errorf("issue #111: the unconfirmed swipe was not sent again at once; trace:\n%s", trace)
		}
		// Sent again once the list has not moved 500 ms after the drag returned: not after
		// the 2 s wait for a change.
		if g := l.gap(t, 1); g < 450*time.Millisecond || g > time.Second {
			t.Errorf("issue #111: the drag after an unconfirmed one that moved nothing went %v later, want after 500 ms", g)
		}
		l.fakeDriver.mu.Lock()
		at := l.fakeDriver.swipeAt
		l.fakeDriver.mu.Unlock()
		if at[0] != at[1] {
			t.Errorf("the drag sent again is %v, want the same as the first, %v", at[1], at[0])
		}
	})
	t.Run("outside CI", func(t *testing.T) {
		t.Setenv("TB_CI", "0")
		l := setup()
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if !strings.Contains(trace, "the app never confirmed swipe 1's touches") {
			t.Errorf("issue #111: the trace does not report the unconfirmed swipe; trace:\n%s", trace)
		}
		if strings.Contains(trace, "sending it again at once") {
			t.Errorf("outside CI the timing stays as before; trace:\n%s", trace)
		}
		if g := l.gap(t, 1); g < 1500*time.Millisecond {
			t.Errorf("outside CI the 2 s wait for a change stays: the next drag went %v later", g)
		}
	})
	// The signal also comes for an app whose main thread was blocked past testmanagerd's 5 s:
	// its queued touches still land, a moment after the swipe returns. Sending the drag again
	// would scroll the list twice as far, past the element.
	t.Run("CI mode, the touches land late", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		l := &swipeLog{
			fakeDriver:  f,
			unconfirmed: func(n int) bool { return n == 1 },
			late: func(n int) time.Duration {
				if n == 1 {
					return 200 * time.Millisecond
				}
				return 0
			},
		}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if strings.Contains(trace, "sending it again at once") {
			t.Errorf("issue #111: a drag whose touches landed late was sent again; trace:\n%s", trace)
		}
		if !strings.Contains(trace, "the app never confirmed swipe 1's touches") {
			t.Errorf("the trace does not report the unconfirmed swipe; trace:\n%s", trace)
		}
	})
}
