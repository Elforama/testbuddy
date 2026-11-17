package engine

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #278: with no timeout, an optional tap on an absent element still looks for the whole
// optional budget (7 s), as Maestro's optional lookup does. Fake clock (synctest), default
// budgets.
func TestOptionalTapDefaultBudgetUnchanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{screen: static(btn("Home", "", 100))}
		start := time.Now()
		res := runWith(t, f, Options{}, "- tapOn: {id: feed, optional: true}\n- tapOn: Home")
		took := time.Since(start)
		mustPass(t, res)
		if res.Steps[0].Status != Warned || len(f.taps) != 1 {
			t.Fatalf("steps %v, taps %v: want the optional tap skipped and Home tapped", stepStatuses(res), f.taps)
		}
		if took < 7*time.Second || took > 8*time.Second {
			t.Errorf("the optional tap gave up after %s, want the default 7 s", took)
		}
	})
}

// Issue #278: an optional tap with its own timeout gives up on an absent element once that
// timeout is out, not after 7 s; timeout 0 looks once.
func TestOptionalTapTimeoutGivesUpOnTime(t *testing.T) {
	for _, tc := range []struct {
		timeout  string
		max      time.Duration
		maxReads int64
	}{
		{"500", 600 * time.Millisecond, 0},
		{"0", 50 * time.Millisecond, 3},
	} {
		t.Run(tc.timeout, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &fakeDriver{screen: static(btn("Home", "", 100))}
				start := time.Now()
				res := runWith(t, f, Options{}, "- tapOn: {id: feed, optional: true, timeout: "+tc.timeout+"}\n- tapOn: Home")
				took := time.Since(start)
				mustPass(t, res)
				if res.Steps[0].Status != Warned || len(f.taps) != 1 {
					t.Fatalf("steps %v, taps %v: want the optional tap skipped and Home tapped", stepStatuses(res), f.taps)
				}
				if took > tc.max {
					t.Errorf("timeout %s: the optional tap gave up after %s, want within %s", tc.timeout, took, tc.max)
				}
				if tc.maxReads > 0 && f.reads.Load() > tc.maxReads {
					t.Errorf("timeout 0: %d screen reads, want at most %d", f.reads.Load(), tc.maxReads)
				}
			})
		})
	}
}

// Issue #278: an element that appears within the step's timeout (a dialog 300 ms in, timeout
// 1000) is tapped.
func TestOptionalTapTimeoutTapsLateElement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{}
		var shown atomic.Bool
		f.screen = func() []*ui.Node {
			if shown.Load() {
				return []*ui.Node{btn("Home", "", 100), btn("Not now", "notnow", 400)}
			}
			return []*ui.Node{btn("Home", "", 100)}
		}
		go func() {
			time.Sleep(300 * time.Millisecond)
			shown.Store(true)
			f.change(true)
		}()
		res := runWith(t, f, Options{}, "- tapOn: {id: notnow, optional: true, timeout: 1000}")
		mustPass(t, res)
		if res.Steps[0].Status != Passed || len(f.taps) != 1 || f.taps[0][1] != 425 {
			t.Fatalf("steps %v, taps %v: want one tap on Not now (y 425)", stepStatuses(res), f.taps)
		}
	})
}

// Issue #278: a ${...} timeout that is not a number of milliseconds, 0 or more, fails the
// step clearly, optional or not: it is not skipped as an element not found.
func TestTapTimeoutInvalidValueFails(t *testing.T) {
	f := &fakeDriver{screen: static(btn("Home", "", 100))}
	mustFail(t, runWith(t, f, quick, "- tapOn: {id: feed, optional: true, timeout: \"${'-5'}\"}"), `invalid timeout "-5"`)
}
