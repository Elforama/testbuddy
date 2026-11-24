package engine

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

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

func TestTapTimeoutInvalidValueFails(t *testing.T) {
	f := &fakeDriver{screen: static(btn("Home", "", 100))}
	mustFail(t, runWith(t, f, quick, "- tapOn: {id: feed, optional: true, timeout: \"${'-5'}\"}"), `invalid timeout "-5"`)
}
