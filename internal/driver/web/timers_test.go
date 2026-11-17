package web

// Short page timers hold the page unsettled (issue #312): page.js reports how many short
// callbacks are pending ({"k":"t","t":n}) in each JavaScript context; the page does not
// settle while one is, nor for a quiet window after the last one ran, so the quiet window
// itself can be short. Timers, and the requests an action starts, hold a settle for at most
// softCap after the action, however often the page changes.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// timersNote is page.js's pending-timer count from the JavaScript context ctxID.
func timersNote(n int, ctxID int) []byte {
	return raw(map[string]any{"name": "__tbNotify", "executionContextId": ctxID,
		"payload": jsonArg(map[string]any{"k": "t", "t": n})})
}

func pendingTimers(d *Driver) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.timerCount()
}

// actAsync runs an action that does do, and says when it has settled and how.
func actAsync(d *Driver, do func()) <-chan driver.ActionResult {
	ch := make(chan driver.ActionResult, 1)
	go func() {
		res, _ := d.act(context.Background(), driver.ActionOpts{}, func() error { do(); return nil })
		ch <- res
	}()
	return ch
}

// Issue #312: a timer's bookkeeping is not a change: it moves neither seq nor the solid count.
func TestTimerNoteIsNotAChange(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.onEvent("Runtime.bindingCalled", timersNote(2, 1), "")
	if seq, solid, _ := state(d); seq != 0 || solid != 0 {
		t.Errorf("a timer note counted as a change: seq %d solid %d", seq, solid)
	}
	if n := pendingTimers(d); n != 2 {
		t.Errorf("pending timers %d, want 2", n)
	}
	d.onEvent("Runtime.bindingCalled", timersNote(1, 1), "frame-session") // a frame's own count adds
	if n := pendingTimers(d); n != 3 {
		t.Errorf("pending timers with a frame's %d, want 3", n)
	}
}

// Issue #312: the page and a same-process iframe share a session but not a JavaScript context:
// the iframe's count neither erases the page's pending timer nor stands in for it.
func TestTimersPerContext(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.onEvent("Runtime.bindingCalled", timersNote(1, 1), "") // the page: one pending
	d.onEvent("Runtime.bindingCalled", timersNote(0, 2), "") // its iframe: none
	if n := pendingTimers(d); n != 1 {
		t.Errorf("an iframe with no timers erased the page's pending one: %d pending, want 1", n)
	}
	d.onEvent("Runtime.bindingCalled", timersNote(2, 2), "") // the iframe: two
	d.onEvent("Runtime.bindingCalled", timersNote(0, 1), "") // the page's ran
	if n := pendingTimers(d); n != 2 {
		t.Errorf("the page's timer ending erased its iframe's: %d pending, want 2", n)
	}
	// A context destroyed (its document gone) takes its count with it.
	d.onEvent("Runtime.executionContextDestroyed", raw(map[string]any{"executionContextId": 2}), "")
	if n := pendingTimers(d); n != 0 {
		t.Errorf("a destroyed context's timers still count: %d pending", n)
	}
	d.onEvent("Runtime.bindingCalled", timersNote(1, 7), "frame-session")
	d.onEvent("Runtime.executionContextsCleared", raw(map[string]any{}), "frame-session")
	if n := pendingTimers(d); n != 0 {
		t.Errorf("a frame session's cleared contexts' timers still count: %d pending", n)
	}
	// An out-of-process iframe detached: its session's timers go.
	d.onEvent("Runtime.bindingCalled", timersNote(1, 3), "oopif-session")
	d.onEvent("Target.detachedFromTarget", raw(map[string]any{"sessionId": "oopif-session"}), "")
	if n := pendingTimers(d); n != 0 {
		t.Errorf("a detached frame's timers still count: %d pending", n)
	}
}

// Issue #312: a two-phase update (a change, then a 200 ms timer that renders the rest) settles
// only after phase two: the pending timer holds the page past the quiet window.
func TestShortTimerHoldsSettle(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	t0 := time.Now()
	done := actAsync(d, func() {
		d.onEvent("Runtime.bindingCalled", timersNote(1, 1), "") // armed by the click
		d.onEvent("Runtime.bindingCalled", notify(0, 1), "")     // phase one renders
	})
	select {
	case <-done:
		t.Fatalf("settled after %s while a short timer was pending", time.Since(t0))
	case <-time.After(quietWindow + 150*time.Millisecond):
	}
	t1 := time.Now()
	d.onEvent("Runtime.bindingCalled", timersNote(0, 1), "") // the timer ran, changing nothing
	res := <-done
	if res.Settle != driver.Changed {
		t.Errorf("settle %v, want changed", res.Settle)
	}
	if el := time.Since(t1); el < quietWindow-5*time.Millisecond {
		t.Errorf("settled %s after the timer ran, before a %s quiet window", el, quietWindow)
	}
}

// Issue #312: a countdown that re-arms its timer each tick and changes the page each time
// (re-armed from a MessageChannel or an effect, so page.js cannot tell it from a new timer)
// holds a settle for at most softCap after the action, not for good.
func TestTimersHoldAtMostSoftCap(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // the countdown: a tick every 200 ms, its next timer always pending
		defer wg.Done()
		for {
			d.onEvent("Runtime.bindingCalled", timersNote(1, 1), "")
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
			}
			d.onEvent("Runtime.bindingCalled", notify(0, 1), "")
		}
	}()
	defer func() { close(stop); wg.Wait() }()
	time.Sleep(500 * time.Millisecond)
	t0 := time.Now()
	res := <-actAsync(d, func() { d.onEvent("Runtime.bindingCalled", notify(0, 1), "") })
	if el := time.Since(t0); res.Settle != driver.Changed || el > softCap+400*time.Millisecond {
		t.Errorf("a tap on a page with a countdown: settle %v after %s, want changed within about %s", res.Settle, el, softCap)
	}
}

// Issue #312: an XHR/fetch to another site that the action started (an API on its own domain)
// holds the settle until it is answered.
func TestThirdPartyRequestAfterActionHolds(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.pageURL = "http://app.test/"
	t0 := time.Now()
	done := actAsync(d, func() {
		d.onEvent("Runtime.bindingCalled", notify(0, 1), "") // "Loading…"
		d.onEvent("Network.requestWillBeSent", raw(map[string]any{"requestId": "api", "type": "Fetch",
			"documentURL": "http://app.test/", "request": map[string]any{"url": "https://api.example.com/items"}}), "")
	})
	select {
	case <-done:
		t.Fatalf("settled after %s with the action's cross-site request unanswered", time.Since(t0))
	case <-time.After(quietWindow + 150*time.Millisecond):
	}
	d.onEvent("Network.loadingFinished", raw(map[string]any{"requestId": "api"}), "")
	if res := <-done; res.Settle != driver.Changed {
		t.Errorf("settle %v, want changed", res.Settle)
	}
}

// Issue #312: onQuiet with timers: pending ones hold the page within softCap of the action,
// whatever changed since; one that just ran holds it for a quiet window.
func TestOnQuietTimers(t *testing.T) {
	old := time.Now().Add(-softCap - time.Second)
	recent := time.Now().Add(-quietWindow - 50*time.Millisecond)
	cases := []struct {
		name    string
		set     func(d *Driver)
		settles bool
	}{
		{"timer pending after the action", func(d *Driver) { d.timers["/1"] = 1; d.softFrom = recent }, false},
		{"frame's timer pending", func(d *Driver) { d.timers["f/1"] = 1; d.softFrom = recent }, false},
		{"request pending after the action", func(d *Driver) { d.softReqs = map[string]bool{"r": true}; d.softFrom = recent }, false},
		{"timer past softCap", func(d *Driver) { d.timers["/1"] = 1; d.softFrom = old }, true},
		{"timer past softCap, page just changed", func(d *Driver) {
			d.timers["/1"] = 1
			d.softFrom = old
			d.lastEvent = recent
		}, true},
		{"timer just ran", func(d *Driver) { d.softFrom = recent; d.timerAt = time.Now() }, false},
		{"timer ran a while ago", func(d *Driver) { d.softFrom = recent; d.timerAt = recent }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newBareDriver()
			events, cancel := subscribe(d)
			defer cancel()
			d.seq, d.solid, d.lastEvent = 5, 3, old
			d.timers = map[string]int{}
			d.quiet = time.AfterFunc(time.Hour, func() {})
			defer d.quiet.Stop()
			c.set(d)
			d.onQuiet()
			if got := len(events) > 0; got != c.settles {
				t.Errorf("settled = %v, want %v", got, c.settles)
			}
		})
	}
}
