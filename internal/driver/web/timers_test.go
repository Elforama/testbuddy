package web

// Short page timers hold the page unsettled (issue #312): page.js reports how many
// setTimeout callbacks due soon are pending ({"k":"t","t":n}); the page does not settle while
// one is, nor for a quiet window after the last one ran, so the quiet window itself can be
// short. A timer that keeps the page busy long after its last change (a poller) stops counting.

import (
	"testing"
	"time"
)

func timersNote(n int) []byte {
	return raw(map[string]any{"name": "__tbNotify", "payload": jsonArg(map[string]any{"k": "t", "t": n})})
}

func pendingTimers(d *Driver) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.timerCount()
}

// Issue #312: a timer's bookkeeping is not a change: it moves neither seq nor the solid count.
func TestTimerNoteIsNotAChange(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.onEvent("Runtime.bindingCalled", timersNote(2), "")
	if seq, solid, _ := state(d); seq != 0 || solid != 0 {
		t.Errorf("a timer note counted as a change: seq %d solid %d", seq, solid)
	}
	if n := pendingTimers(d); n != 2 {
		t.Errorf("pending timers %d, want 2", n)
	}
	d.onEvent("Runtime.bindingCalled", timersNote(1), "frame-session") // a frame's own count adds
	if n := pendingTimers(d); n != 3 {
		t.Errorf("pending timers with a frame's %d, want 3", n)
	}
}

// Issue #312: a two-phase update (a change, then a 200 ms timer that renders the rest) settles
// only after phase two: the pending timer holds the page past the quiet window.
func TestShortTimerHoldsSettle(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	events, cancel := subscribe(d)
	defer cancel()
	d.onEvent("Runtime.bindingCalled", timersNote(1), "") // armed by the click
	d.onEvent("Runtime.bindingCalled", notify(0, 1), "")  // phase one renders
	for deadline := time.After(quietWindow + 150*time.Millisecond); ; {
		select {
		case e := <-events:
			if e.Kind == "settled" {
				t.Fatal("settled while a short timer was pending")
			}
			continue
		case <-deadline:
		}
		break
	}
	t0 := time.Now()
	d.onEvent("Runtime.bindingCalled", timersNote(0), "") // the timer ran, changing nothing
	waitEvent(t, events, "settled")
	if el := time.Since(t0); el < quietWindow-5*time.Millisecond {
		t.Errorf("settled %s after the timer ran, before a %s quiet window", el, quietWindow)
	}
}

// Issue #312: onQuiet with timers: pending ones hold the page, unless the page has not changed
// for timerGrace (a poller re-arming forever); one that just ran holds it for a quiet window.
func TestOnQuietTimers(t *testing.T) {
	old := time.Now().Add(-timerGrace - time.Second)
	cases := []struct {
		name    string
		set     func(d *Driver)
		settles bool
	}{
		{"timer pending", func(d *Driver) { d.timers[""] = 1; d.lastEvent = time.Now().Add(-quietWindow - 50*time.Millisecond) }, false},
		{"frame's timer pending", func(d *Driver) { d.timers["f"] = 1; d.lastEvent = time.Now().Add(-quietWindow - 50*time.Millisecond) }, false},
		{"timer past its grace", func(d *Driver) { d.timers[""] = 1 }, true},
		{"timer just ran", func(d *Driver) {
			d.lastEvent = time.Now().Add(-quietWindow - 50*time.Millisecond)
			d.timerAt = time.Now()
		}, false},
		{"timer ran a while ago", func(d *Driver) { d.timerAt = time.Now().Add(-quietWindow - 50*time.Millisecond) }, true},
		{"timer ran just now, page still past grace", func(d *Driver) { d.timerAt = time.Now() }, true},
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
