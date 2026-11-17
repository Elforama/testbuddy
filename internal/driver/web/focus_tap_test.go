package web

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Issue #311: a tap whose only effect is focus, on a field (or other element) that already had
// it, changes nothing page.js reports: no mutation, no focusin. It waited out the whole
// no-change window (500 ms) every time. The tap's target having the focus is its reaction.

// focusPage is a fake page whose focus check answers kept for the tap's point (30, 40), and
// that reports no change of its own; checks counts the focus checks.
func focusPage(t *testing.T, kept bool) (*fakeCDP, *Driver, *atomic.Int32) {
	page, d := inputPage(t)
	var checks atomic.Int32
	page.evaluator(func(expr, _ string) any {
		if expr == fmt.Sprintf(focusKeptJS, 30, 40) {
			checks.Add(1)
			return value(kept)
		}
		return nil
	})
	return page, d, &checks
}

// Issue #311: a tap on the element that has the focus ends at once, as a change, both when it
// waits only for the page to react and when it waits for the page to settle.
func TestTapOnFocusedTargetIsAReaction(t *testing.T) {
	_, d, checks := focusPage(t, true)
	t0 := time.Now()
	r, err := d.Tap(ctxT(t), 30, 40, driver.ActionOpts{React: true, NoChangeMs: 500})
	if err != nil || r.Settle != driver.Changed || time.Since(t0) > 200*time.Millisecond {
		t.Errorf("tap on the focused field: %+v, %v after %s; want changed at once", r, err, time.Since(t0))
	}
	t0 = time.Now()
	r, err = d.Tap(ctxT(t), 30, 40, driver.ActionOpts{NoChangeMs: 500})
	if err != nil || r.Settle != driver.Changed || time.Since(t0) > 200*time.Millisecond {
		t.Errorf("settling tap on the focused field: %+v, %v after %s; want changed after the quiet window", r, err, time.Since(t0))
	}
	if n := checks.Load(); n != 2 {
		t.Errorf("%d focus checks, want 2", n)
	}
}

// Issue #311: a tap whose target does not have the focus, and that changed nothing, still
// waits out the no-change window: a silent tap.
func TestSilentTapStillWaits(t *testing.T) {
	_, d, checks := focusPage(t, false)
	t0 := time.Now()
	r, err := d.Tap(ctxT(t), 30, 40, driver.ActionOpts{React: true, NoChangeMs: 100})
	if err != nil || r.Settle != driver.NoChange || time.Since(t0) < 95*time.Millisecond {
		t.Errorf("silent tap: %+v, %v after %s; want no change after the window", r, err, time.Since(t0))
	}
	if n := checks.Load(); n != 1 {
		t.Errorf("%d focus checks, want 1", n)
	}
}

// Issue #311: a tap the page reacted to at once is not asked about focus: no extra round trip.
func TestReactingTapSkipsFocusCheck(t *testing.T) {
	page, d, checks := focusPage(t, true)
	page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
		if m.params()["type"] == "mouseReleased" {
			page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "payload": `{"a":0,"s":1}`}, "")
		}
		return map[string]any{}
	})
	if r, err := d.Tap(ctxT(t), 30, 40, driver.ActionOpts{React: true}); err != nil || r.Settle != driver.Changed {
		t.Errorf("tap: %+v, %v", r, err)
	}
	if n := checks.Load(); n != 0 {
		t.Errorf("%d focus checks after a tap that changed the page, want 0", n)
	}
}

// Issue #311, reliability: what the focused field's tap started late (a short timer: a list
// that opens 200 ms later) still holds the page unsettled, so the next step sees it. The tap's
// focus counts as a change of the page, which the timer then holds.
func TestFocusTapLateReactionHolds(t *testing.T) {
	page, d, _ := focusPage(t, true)
	page.on("Input.dispatchMouseEvent", func(m cdpMsg) any {
		if m.params()["type"] == "mouseReleased" {
			page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "executionContextId": 1,
				"payload": `{"k":"t","t":1}`}, "")
		}
		return map[string]any{}
	})
	r, err := d.Tap(ctxT(t), 30, 40, driver.ActionOpts{React: true, NoChangeMs: 500})
	if err != nil || r.Settle != driver.Changed {
		t.Fatalf("tap: %+v, %v", r, err)
	}
	time.Sleep(quietWindow + 100*time.Millisecond)
	d.mu.Lock()
	settled := d.isSettled()
	d.mu.Unlock()
	if settled {
		t.Fatal("settled while the tap's short timer was pending")
	}
	page.event("Runtime.bindingCalled", map[string]any{"name": "__tbNotify", "executionContextId": 1,
		"payload": `{"k":"t","t":0}`}, "")
	eventually(t, "the page to settle once the timer ran", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.isSettled()
	})
}
