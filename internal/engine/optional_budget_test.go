package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #278: an optional tap on an element that is not on a still screen gives up after
// optionalQuietBudget, not after the whole optional budget (7 s). Timed on synctest's fake
// clock, with the default budgets.
func TestOptionalTapAbsentOnStillScreenGivesUpEarly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{screen: static(btn("Home", "", 100))}
		start := time.Now()
		res := runWith(t, f, Options{}, "- tapOn: {id: feed, optional: true}\n- tapOn: Home")
		took := time.Since(start)
		mustPass(t, res)
		if len(f.taps) != 1 {
			t.Fatalf("taps %v, want only Home's", f.taps)
		}
		t.Logf("optional tapOn on an absent element, still screen: %s", took)
		if took > optionalQuietBudget+time.Second {
			t.Errorf("the optional tap gave up after %s, want within %s of a still screen", took, optionalQuietBudget)
		}
	})
}

// Issue #278: the screen keeps changing (a loading spinner) and the optional element (a
// dialog) appears 4 s in, well past optionalQuietBudget: it is still tapped, as Maestro's 7 s
// optional lookup would.
func TestOptionalTapLateWhileScreenChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{}
		var frame atomic.Int64
		var shown atomic.Bool
		f.screen = func() []*ui.Node {
			nodes := []*ui.Node{btn(fmt.Sprintf("Loading %d", frame.Load()), "spinner", 100)}
			if shown.Load() {
				nodes = append(nodes, btn("Allow", "allow", 400))
			}
			return nodes
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 1; i <= 13; i++ { // a frame every 300 ms, up to 3.9 s
				time.Sleep(300 * time.Millisecond)
				frame.Add(1)
				f.change(true)
			}
			time.Sleep(100 * time.Millisecond)
			shown.Store(true)
			f.change(true)
		}()
		res := runWith(t, f, Options{}, "- tapOn: {id: allow, optional: true}")
		<-done
		mustPass(t, res)
		if len(f.taps) != 1 || f.taps[0][1] != 425 {
			t.Fatalf("taps %v, want one on Allow (y 425)", f.taps)
		}
	})
}

// Issue #278: on a still screen, an optional dialog that comes a little late (1 s, inside
// optionalQuietBudget) is still tapped.
func TestOptionalTapLateOnStillScreen(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{}
		var shown atomic.Bool
		f.screen = func() []*ui.Node {
			if shown.Load() {
				return []*ui.Node{btn("Home", "", 100), btn("Allow", "allow", 400)}
			}
			return []*ui.Node{btn("Home", "", 100)}
		}
		go func() {
			time.Sleep(optionalQuietBudget - 500*time.Millisecond)
			shown.Store(true)
			f.change(true)
		}()
		mustPass(t, runWith(t, f, Options{}, "- tapOn: {id: allow, optional: true}"))
		if len(f.taps) != 1 || f.taps[0][1] != 425 {
			t.Fatalf("taps %v, want one on Allow (y 425)", f.taps)
		}
	})
}
