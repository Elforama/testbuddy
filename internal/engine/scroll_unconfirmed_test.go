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

type swipeLog struct {
	*fakeDriver
	slow        func(n int) time.Duration
	dropped     func(n int) bool
	unconfirmed func(n int) bool
	late        func(n int) time.Duration
	verdict     func(n int) string
	implicit    func(n int) bool
	landOnAsk   func(n int) bool

	mu       sync.Mutex
	sent     []time.Time
	returned []time.Time
	asked    []int64
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

func TestScrollUndeliveredSwipeGuards(t *testing.T) {
	t.Setenv("TB_CI", "1")
	notDelivered := func(int) string { return driver.NotDelivered }
	countAgain := func(trace string) int { return strings.Count(trace, "sending it again at once") }
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
