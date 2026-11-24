package web

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func timersNote(n int, ctxID int) []byte {
	return raw(map[string]any{"name": "__tbNotify", "executionContextId": ctxID,
		"payload": jsonArg(map[string]any{"k": "t", "t": n})})
}

func pendingTimers(d *Driver) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.timerCount()
}

func actAsync(d *Driver, do func()) <-chan driver.ActionResult {
	ch := make(chan driver.ActionResult, 1)
	go func() {
		res, _ := d.act(context.Background(), driver.ActionOpts{}, func() error { do(); return nil })
		ch <- res
	}()
	return ch
}

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
	d.onEvent("Runtime.bindingCalled", timersNote(1, 1), "frame-session")
	if n := pendingTimers(d); n != 3 {
		t.Errorf("pending timers with a frame's %d, want 3", n)
	}
}

func TestTimersPerContext(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.onEvent("Runtime.bindingCalled", timersNote(1, 1), "")
	d.onEvent("Runtime.bindingCalled", timersNote(0, 2), "")
	if n := pendingTimers(d); n != 1 {
		t.Errorf("an iframe with no timers erased the page's pending one: %d pending, want 1", n)
	}
	d.onEvent("Runtime.bindingCalled", timersNote(2, 2), "")
	d.onEvent("Runtime.bindingCalled", timersNote(0, 1), "")
	if n := pendingTimers(d); n != 2 {
		t.Errorf("the page's timer ending erased its iframe's: %d pending, want 2", n)
	}
	d.onEvent("Runtime.executionContextDestroyed", raw(map[string]any{"executionContextId": 2}), "")
	if n := pendingTimers(d); n != 0 {
		t.Errorf("a destroyed context's timers still count: %d pending", n)
	}
	d.onEvent("Runtime.bindingCalled", timersNote(1, 7), "frame-session")
	d.onEvent("Runtime.executionContextsCleared", raw(map[string]any{}), "frame-session")
	if n := pendingTimers(d); n != 0 {
		t.Errorf("a frame session's cleared contexts' timers still count: %d pending", n)
	}
	d.onEvent("Runtime.bindingCalled", timersNote(1, 3), "oopif-session")
	d.onEvent("Target.detachedFromTarget", raw(map[string]any{"sessionId": "oopif-session"}), "")
	if n := pendingTimers(d); n != 0 {
		t.Errorf("a detached frame's timers still count: %d pending", n)
	}
}

func TestShortTimerHoldsSettle(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	t0 := time.Now()
	done := actAsync(d, func() {
		d.onEvent("Runtime.bindingCalled", timersNote(1, 1), "")
		d.onEvent("Runtime.bindingCalled", notify(0, 1), "")
	})
	select {
	case <-done:
		t.Fatalf("settled after %s while a short timer was pending", time.Since(t0))
	case <-time.After(quietWindow + 150*time.Millisecond):
	}
	t1 := time.Now()
	d.onEvent("Runtime.bindingCalled", timersNote(0, 1), "")
	res := <-done
	if res.Settle != driver.Changed {
		t.Errorf("settle %v, want changed", res.Settle)
	}
	if el := time.Since(t1); el < quietWindow-5*time.Millisecond {
		t.Errorf("settled %s after the timer ran, before a %s quiet window", el, quietWindow)
	}
}

func TestTimersHoldAtMostSoftCap(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
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

func TestThirdPartyRequestAfterActionHolds(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.pageURL = "http://app.test/"
	t0 := time.Now()
	done := actAsync(d, func() {
		d.onEvent("Runtime.bindingCalled", notify(0, 1), "")
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
