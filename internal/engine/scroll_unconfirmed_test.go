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
	// verdict: the agent's delivery verdict on the n-th swipe (#334's rules), nil for an agent
	// that gives no token (outside CI mode). The token is n.
	verdict func(n int) string
	// implicit: the verdict on the n-th swipe rests on testmanagerd's implicit confirmation (no
	// process reported its touches).
	implicit func(n int) bool
	// landOnAsk: the n-th swipe's touches move the list while the host asks about them.
	landOnAsk func(n int) bool

	mu       sync.Mutex
	sent     []time.Time
	returned []time.Time
	asked    []int64 // the tokens asked about
	pending  map[int]func()
}

func (l *swipeLog) Delivery(ctx context.Context, token int64, wait time.Duration) (driver.DeliveryVerdict, error) {
	l.mu.Lock()
	l.asked = append(l.asked, token)
	land := l.pending[int(token)]
	delete(l.pending, int(token))
	l.mu.Unlock()
	if land != nil {
		land()
	}
	ms := 23
	v := driver.DeliveryVerdict{Verdict: l.verdict(int(token)), ReportMs: &ms, AnswerMs: []int{4, 3}}
	v.Implicit = l.implicit != nil && l.implicit(int(token))
	return v, nil
}

func (l *swipeLog) askedAbout() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int64(nil), l.asked...)
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
	onAsk := l.landOnAsk != nil && l.landOnAsk(n)
	if (l.dropped != nil && l.dropped(n)) || delay > 0 || onAsk {
		// Recorded as sent, but the screen does not move (or not yet).
		l.fakeDriver.mu.Lock()
		l.fakeDriver.swipes = append(l.fakeDriver.swipes, o)
		l.fakeDriver.swipeAt = append(l.fakeDriver.swipeAt, [4]int{x1, y1, x2, y2})
		move := l.fakeDriver.onSwipe
		l.fakeDriver.mu.Unlock()
		if delay > 0 && move != nil {
			time.AfterFunc(delay, func() { move(x1, y1, x2, y2) })
		}
		if onAsk && move != nil {
			l.mu.Lock()
			if l.pending == nil {
				l.pending = map[int]func(){}
			}
			l.pending[n] = func() { move(x1, y1, x2, y2) }
			l.mu.Unlock()
		}
		res = driver.ActionResult{Settle: driver.NoChange}
	} else {
		res, err = l.fakeDriver.Swipe(ctx, x1, y1, x2, y2, d, o)
	}
	res.Unconfirmed = l.unconfirmed != nil && l.unconfirmed(n)
	if l.verdict != nil {
		res.DeliveryToken = int64(n)
	}
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
		if line := flingLine(t, trace); !strings.Contains(line, "reached the app is unknown") {
			t.Errorf("issue #111: %q does not say whether the touches reached the app is unknown", line)
		}
		mustFail(t, res, "reached the app is unknown")
	})
}

// Issue #111, item 3: the iOS agent reports a swipe whose touches testmanagerd only confirmed
// implicitly ("Timed out after waiting 5.0s for TouchEventsCompleted"). The trace says so on
// every run. testmanagerd's late confirmation does not show the system routed the touches (it
// may hold them and deliver them later), so whether they reached the app is unknown: never a
// second drag (review of c5e98bf).
func TestScrollUnconfirmedSwipeSentAgain(t *testing.T) {
	setup := func() *swipeLog {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		return &swipeLog{
			fakeDriver:  f,
			dropped:     func(n int) bool { return n == 1 },
			unconfirmed: func(n int) bool { return n == 1 },
			verdict: func(n int) string {
				if n == 1 {
					return driver.NotDelivered
				}
				return driver.Delivered
			},
			implicit: func(n int) bool { return n == 1 },
		}
	}
	// The review's timeline: the system holds the touches past testmanagerd's implicit
	// confirmation, the list reads unmoved, the app answers its idle asks promptly, and the
	// held touches land later. A drag sent again then would land too: a double scroll. The
	// verdict rests on no process's report (implicit), so it is not trusted as "not delivered".
	t.Run("CI mode, held touches land late", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		l := setup()
		l.dropped = nil
		l.late = func(n int) time.Duration {
			if n == 1 {
				return 1500 * time.Millisecond
			}
			return 0
		}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if strings.Contains(trace, "sending it again") {
			t.Errorf("review of c5e98bf: a swipe no process reported was sent again (a double scroll once the held touches land); trace:\n%s", trace)
		}
		if !strings.Contains(trace, "whether it reached the app is unknown: neither the app nor the system reported its touches") {
			t.Errorf("review of c5e98bf: the trace does not say why it is unknown; trace:\n%s", trace)
		}
	})
	t.Run("outside CI", func(t *testing.T) {
		t.Setenv("TB_CI", "0")
		l := setup()
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if !strings.Contains(trace, "swipe 1: neither the app nor the system reported its touches") {
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
			// The agent's idle answers come late from a blocked app: no proof.
			verdict: func(int) string { return driver.Unknown },
		}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if strings.Contains(trace, "sending it again at once") {
			t.Errorf("issue #111: a drag whose touches landed late was sent again; trace:\n%s", trace)
		}
		if !strings.Contains(trace, "swipe 1: neither the app nor the system reported its touches") {
			t.Errorf("the trace does not report the unconfirmed swipe; trace:\n%s", trace)
		}
	})
}

// Issue #111, with #334's evidence: in CI mode the agent judges whether the app got a swipe's
// touches (SpringBoard's report, or testmanagerd's implicit confirmation, then two prompt idle
// answers with no report from the app). A drag that left the list unmoved and that the app
// provably did not get is sent again at once: it is not a drag that found nothing, so neither
// the 2 s wait for a change nor the switch to flinging comes of it. On doubt ("unknown") it
// counts as delivered: never a second drag that could scroll the list twice as far.
func TestScrollUndeliveredSwipeSentAgain(t *testing.T) {
	setup := func(verdict string, unconfirmed bool) *swipeLog {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		return &swipeLog{
			fakeDriver:  f,
			dropped:     func(n int) bool { return n == 1 },
			unconfirmed: func(n int) bool { return unconfirmed && n == 1 },
			verdict: func(n int) string {
				if n == 1 {
					return verdict
				}
				return driver.Delivered
			},
		}
	}
	t.Run("CI mode, not delivered", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		l := setup(driver.NotDelivered, false)
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if !strings.Contains(trace, "swipe 1 did not reach the app") {
			t.Errorf("issue #111: the trace does not say the swipe did not reach the app; trace:\n%s", trace)
		}
		if strings.Contains(trace, "waiting up to 2 s for a change") || strings.Contains(trace, "2 drags in a row") {
			t.Errorf("issue #111: the undelivered swipe counted as one that found nothing; trace:\n%s", trace)
		}
		if g := l.gap(t, 1); g < 450*time.Millisecond || g > 1200*time.Millisecond {
			t.Errorf("issue #111: the drag after an undelivered one went %v later, want at once after the 500 ms look", g)
		}
		l.fakeDriver.mu.Lock()
		at := l.fakeDriver.swipeAt
		l.fakeDriver.mu.Unlock()
		if at[0] != at[1] {
			t.Errorf("the drag sent again is %v, want the same as the first, %v", at[1], at[0])
		}
	})
	t.Run("CI mode, unknown counts as delivered", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		// testmanagerd confirmed it only implicitly, but the app's idle answers came late (its
		// main thread blocked, the touches maybe queued): no proof.
		l := setup(driver.Unknown, true)
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if strings.Contains(trace, "sending it again") {
			t.Errorf("issue #111: a swipe whose delivery is unknown was sent again; trace:\n%s", trace)
		}
		if !strings.Contains(trace, "whether it reached the app is unknown") {
			t.Errorf("issue #111: the trace does not say whether the swipe reached the app is unknown; trace:\n%s", trace)
		}
		if g := l.gap(t, 1); g < 1500*time.Millisecond {
			t.Errorf("issue #111: on unknown the 2 s wait for a change stays: the next drag went %v later", g)
		}
	})
	t.Run("CI mode, a drag that moved the list is not asked about", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		l := setup(driver.Delivered, false)
		l.dropped = func(int) bool { return false }
		res, _ := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if a := l.askedAbout(); len(a) != 0 {
			t.Errorf("asked about swipes %v, want none: every drag moved the list", a)
		}
	})
	t.Run("outside CI", func(t *testing.T) {
		t.Setenv("TB_CI", "0")
		l := setup(driver.NotDelivered, true)
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if a := l.askedAbout(); len(a) != 0 {
			t.Errorf("outside CI mode asked about swipes %v, want none", a)
		}
		if strings.Contains(trace, "sending it again") {
			t.Errorf("outside CI mode a swipe was sent again; trace:\n%s", trace)
		}
	})
	// The trace names what is known of the touches, not testmanagerd's timeout.
	t.Run("trace wording", func(t *testing.T) {
		t.Setenv("TB_CI", "0")
		l := setup(driver.Unknown, true)
		_, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		if strings.Contains(trace, "testmanagerd timed out") {
			t.Errorf("issue #111: the trace blames testmanagerd's timeout; trace:\n%s", trace)
		}
		if !strings.Contains(trace, "whether it reached the app is unknown") {
			t.Errorf("issue #111: the trace does not say whether swipe 1 reached the app is unknown; trace:\n%s", trace)
		}
	})
}

// Review of c5e98bf: the guards around sending a swipe again (issue #111), in CI mode.
func TestScrollUndeliveredSwipeGuards(t *testing.T) {
	t.Setenv("TB_CI", "1")
	notDelivered := func(int) string { return driver.NotDelivered }
	countAgain := func(trace string) int { return strings.Count(trace, "sending it again at once") }
	// The screen is read again right before the re-send: a list that moved while the app was
	// asked (its touches landed meanwhile) is not dragged again.
	t.Run("the list moved during the ask", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		l := &swipeLog{fakeDriver: f, verdict: notDelivered, landOnAsk: func(n int) bool { return n == 1 }}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 8000}")
		mustPass(t, res)
		if n := countAgain(trace); n != 0 {
			t.Errorf("review of c5e98bf: sent again %d times though the list moved during the ask; trace:\n%s", n, trace)
		}
	})
	// At most 2 re-sends in a row, as a tap's (tap_undelivered): then the drag counts as one
	// that moved nothing.
	t.Run("at most 2 in a row", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		l := &swipeLog{fakeDriver: f, verdict: notDelivered, dropped: func(n int) bool { return n <= 4 }}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 10000}")
		mustPass(t, res)
		if n := countAgain(trace); n != 2 {
			t.Errorf("review of c5e98bf: sent again %d times in a row, want 2; trace:\n%s", n, trace)
		}
	})
	// A fling carries a token too: one the app provably did not get, that left the list
	// unmoved, is sent again, the same fling.
	t.Run("a fling", func(t *testing.T) {
		f := &fakeDriver{plat: "ios"}
		f.scroller(200, false, 0)
		l := &swipeLog{
			fakeDriver: f,
			dropped:    func(n int) bool { return n <= 3 },
			verdict: func(n int) string {
				if n == 3 {
					return driver.NotDelivered
				}
				return driver.Delivered
			},
		}
		res, trace := runTraced(t, l, "- scrollUntilVisible: {element: Item 60, timeout: 12000}")
		mustPass(t, res)
		if !strings.Contains(trace, "fling 3:") {
			t.Fatalf("swipe 3 is not a fling; trace:\n%s", trace)
		}
		if !strings.Contains(trace, "swipe 3 did not reach the app") || countAgain(trace) != 1 {
			t.Errorf("review of c5e98bf: the undelivered fling was not sent again once; trace:\n%s", trace)
		}
		l.fakeDriver.mu.Lock()
		at, opts := l.fakeDriver.swipeAt, l.fakeDriver.swipes
		l.fakeDriver.mu.Unlock()
		if len(at) < 4 || at[3] != at[2] || opts[3].Drag {
			t.Errorf("the swipe after the undelivered fling is %v (drag %v), want the same fling, %v", at[3], opts[3].Drag, at[2])
		}
	})
}
