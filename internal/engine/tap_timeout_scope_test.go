package engine

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #278 (review): a tap's timeout bounds only the lookup of its target. What the tap does
// after it found the target (replay a swallowed tap, wait out a cover, hide the keyboard) keeps
// the budgets it has without one. Fake clock (synctest), default budgets.

// iOS: a hidden veil swallows the first tap, which takes its whole 1 s no-change window to
// say so; the veil goes 1.5 s in. With timeout: 1000 the tap is still sent again.
func TestTapTimeoutKeepsSwallowedTapReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		under := btn("Under", "under", 300)
		under.Class = "Button"
		f := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
		f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
			if n == 1 {
				time.Sleep(time.Second) // the agent's no-change window
			}
			return res
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(1500 * time.Millisecond)
			f.mu.Lock()
			f.tapSettle = driver.Changed
			f.mu.Unlock()
			f.change(true) // the hidden veil went
		}()
		runWith(t, f, Options{}, "- tapOn: {text: Under, timeout: 1000}")
		<-done
		if n := tapCount(f); n != 2 {
			t.Errorf("%d taps, want 2: the swallowed tap is sent again once the veil goes", n)
		}
	})
}

// A loading overlay over the target goes 1.5 s in: with timeout: 500 the tap still waits it
// out and taps the element, not the overlay.
func TestTapTimeoutKeepsLoadingOverlayWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{}
		var veil atomic.Bool
		veil.Store(true)
		f.screen = func() []*ui.Node {
			root := at("Frame", 0, 0, 0, 400, 800)
			key := clickable(1, 50, 300, 150, 400)
			key.Text = "1"
			nodes := []*ui.Node{root, key}
			if veil.Load() {
				nodes = append(nodes, at("ViewGroup", 1, 0, 0, 400, 800), at("android.widget.ProgressBar", 2, 0, 0, 400, 800))
			}
			return nodes
		}
		var tappedUnder atomic.Bool
		f.onTap = func(x, y int) { tappedUnder.Store(veil.Load()) }
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(1500 * time.Millisecond)
			veil.Store(false)
			f.change(true)
		}()
		res := runWith(t, f, Options{}, "- tapOn: {text: \"1\", optional: true, timeout: 500}")
		<-done
		mustPass(t, res)
		if tappedUnder.Load() || len(f.taps) != 1 {
			t.Errorf("taps %v, under the overlay: %v", f.taps, tappedUnder.Load())
		}
	})
}

// A target under the keyboard (Android leaves it out of the tree): with timeout: 500, shorter
// than keyboardGrace, the tap still hides the keyboard and finds it.
func TestTapTimeoutStillHidesKeyboard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{}
		kb := ui.Rect{T: 500, R: 400, B: 800}
		f.keyboard = &kb
		f.kbShown = true
		f.screen = func() []*ui.Node {
			field := btn("Bottom field", "kb_bottom", 400)
			if len(f.calls) > 0 && f.calls[len(f.calls)-1] == "HideKeyboard" {
				f.keyboard, f.kbShown = nil, false // hidden (f.mu is held here)
				return []*ui.Node{field, btn("Submit", "kb_submit", 600)}
			}
			return []*ui.Node{field}
		}
		start := time.Now()
		mustPass(t, runWith(t, f, Options{}, "- tapOn: {id: kb_submit, optional: true, timeout: 500}"))
		if len(f.called("HideKeyboard")) != 1 || len(f.taps) != 1 || f.taps[0][1] != 625 {
			t.Errorf("hides %v, taps %v: want the keyboard hidden, then one tap on Submit", f.called("HideKeyboard"), f.taps)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("took %s, want about the 500 ms timeout before hiding the keyboard", d)
		}
	})
}

// NaN and infinite timeouts are not numbers of milliseconds: the step fails clearly.
func TestTapTimeoutRejectsNaNAndInf(t *testing.T) {
	for _, v := range []string{"NaN", "Inf", "-Inf"} {
		f := &fakeDriver{screen: static(btn("Home", "", 100))}
		mustFail(t, runWith(t, f, quick, "- tapOn: {text: Home, optional: true, timeout: \"${'"+v+"'}\"}"), `invalid timeout "`+v+`"`)
	}
}
