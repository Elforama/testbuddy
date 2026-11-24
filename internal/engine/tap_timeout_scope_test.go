package engine

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func TestTapTimeoutKeepsSwallowedTapReplay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		under := btn("Under", "under", 300)
		under.Class = "Button"
		f := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
		f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
			if n == 1 {
				time.Sleep(time.Second)
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
			f.change(true)
		}()
		runWith(t, f, Options{}, "- tapOn: {text: Under, timeout: 1000}")
		<-done
		if n := tapCount(f); n != 2 {
			t.Errorf("%d taps, want 2: the swallowed tap is sent again once the veil goes", n)
		}
	})
}

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

func TestTapTimeoutStillHidesKeyboard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeDriver{}
		kb := ui.Rect{T: 500, R: 400, B: 800}
		f.keyboard = &kb
		f.kbShown = true
		f.screen = func() []*ui.Node {
			field := btn("Bottom field", "kb_bottom", 400)
			if len(f.calls) > 0 && f.calls[len(f.calls)-1] == "HideKeyboard" {
				f.keyboard, f.kbShown = nil, false
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

func TestTapTimeoutRejectsNaNAndInf(t *testing.T) {
	for _, v := range []string{"NaN", "Inf", "-Inf"} {
		f := &fakeDriver{screen: static(btn("Home", "", 100))}
		mustFail(t, runWith(t, f, quick, "- tapOn: {text: Home, optional: true, timeout: \"${'"+v+"'}\"}"), `invalid timeout "`+v+`"`)
	}
}
