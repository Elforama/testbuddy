package engine

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// A button springing in overshoots past a clipping edge: for a few reads it is not on screen at
// all, then comes back to rest. The tap waits for it to rest instead of taking the miss as
// "gone" and tapping the sliver it last saw (the bench's Android "Sprung" button).
func TestTapWaitsForSpringBackIntoSight(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		at := func(l, r int) []*ui.Node {
			return []*ui.Node{{Text: "Sprung", Bounds: ui.Rect{L: l, T: 100, R: r, B: 150}, Enabled: true, Clickable: true}}
		}
		switch {
		case n == 1:
			return at(319, 566)
		case n == 2:
			return at(87, 334)
		case n == 3:
			return at(42, 90) // clipped by the lane's left edge
		case n <= 7:
			return nil // past the edge: out of sight
		default:
			return at(200, 447) // at rest
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Sprung"))
	if len(f.taps) != 1 || f.taps[0][0] < 200 || f.taps[0][0] > 447 {
		t.Errorf("taps %v, want one on the button at rest (x 200-447)", f.taps)
	}
}

// The same spring, but held at the clipping edge for a few reads: two reads agree on the
// clipped sliver, which is not the button at rest (it was full width a moment ago).
func TestTapWaitsForSpringHeldAtEdge(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		at := func(l, r int) []*ui.Node {
			return []*ui.Node{{Text: "Sprung", Bounds: ui.Rect{L: l, T: 100, R: r, B: 150}, Enabled: true, Clickable: true}}
		}
		switch {
		case n == 1:
			return at(447, 694)
		case n == 2:
			return at(93, 340)
		case n <= 6:
			return at(42, 94) // cut off by the lane's left edge, the same on each read
		default:
			return at(200, 447)
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Sprung"))
	if len(f.taps) != 1 || f.taps[0][0] < 200 || f.taps[0][0] > 447 {
		t.Errorf("taps %v, want one on the button at rest (x 200-447)", f.taps)
	}
}

// Right after an action, a spring pausing at a turn (two quick reads agree) is not taken for
// at rest: the reads must agree for restSpanAfterAction.
func TestTapAfterActionWaitsOutASpringsTurn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var opened atomic.Bool
	var since atomic.Int64
	f.screen = func() []*ui.Node {
		open := &ui.Node{Text: "Spring in", Bounds: ui.Rect{L: 0, T: 0, R: 200, B: 50}, Enabled: true, Clickable: true}
		if !opened.Load() {
			return []*ui.Node{open}
		}
		ms := time.Since(time.Unix(0, since.Load())).Milliseconds()
		l := 200 // at rest from 400 ms
		switch {
		case ms < 60:
			l = 280 // a turn: still for 60 ms
		case ms < 400:
			l = 50 + int(ms-60)/30*10 // moving: a new place every 30 ms
		}
		return []*ui.Node{open, {Text: "Sprung", Bounds: ui.Rect{L: l, T: 100, R: l + 100, B: 150}, Enabled: true, Clickable: true}}
	}
	f.onTap = func(x, y int) {
		if !opened.Load() {
			since.Store(time.Now().UnixNano())
			opened.Store(true)
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Spring in\n- tapOn: Sprung"))
	if len(f.taps) != 2 || f.taps[1][0] != 250 {
		t.Errorf("taps %v, want the second on the button at rest (x 250)", f.taps)
	}
}

// A pulsing button (it grows and shrinks around its middle, forever) is not "cut off at an
// edge": it is tapped without waiting out restWait.
func TestTapOnPulseIsNotHeldAsCutOff(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		d := int(reads.Add(1) % 2) // 0 or 1: grown by 1 px each side, or not
		if reads.Load() > 3 {
			d = 0
		}
		return []*ui.Node{{Text: "Pulse", Bounds: ui.Rect{L: 42 - d, T: 100 - d, R: 273 + d, B: 226 + d}, Enabled: true, Clickable: true}}
	}
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- tapOn: Pulse"))
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s: held as cut off", d)
	}
}

// A tap target under the keyboard, which Android leaves out of the tree, is found once the
// keyboard is hidden: after keyboardGrace, the tap hides it and looks again.
func TestTapHidesKeyboardOverTarget(t *testing.T) {
	t.Parallel()
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
	mustPass(t, runWith(t, f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: time.Second}, "- tapOn: {id: kb_submit}"))
	if len(f.called("HideKeyboard")) != 1 || len(f.taps) != 1 || f.taps[0][1] != 625 {
		t.Errorf("hides %v, taps %v", f.called("HideKeyboard"), f.taps)
	}
	if d := time.Since(start); d < keyboardGrace || d > keyboardGrace+2*time.Second {
		t.Errorf("took %s", d)
	}
}

// iOS (CI's iPhone 17 Pro simulator, on-screen keyboard): the keyboard's views are listed in
// the app's own window, so a Submit button under it is found but covered, and no drag brings
// it clear (bench/flows/scenarios/keyboard.yaml). Tapping its middle would hit the keyboard:
// the tap hides the keyboard and finds the button again instead.
func TestTapHidesKeyboardCoveringTarget(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios"}
	f.kbShown = true
	f.screen = func() []*ui.Node {
		field := btn("Bottom field", "kb_bottom", 400)
		submit := btn("Submit", "kb_submit", 600)
		submit.Class = "Button"
		for _, c := range f.calls { // f.mu is held here
			if c == "HideKeyboard" {
				return []*ui.Node{field, submit}
			}
		}
		host := &ui.Node{Class: "Other", Bounds: ui.Rect{T: 540, R: 400, B: 800}}
		key := &ui.Node{Class: "Button", Text: "q", Depth: 1, Enabled: true, Bounds: ui.Rect{T: 540, R: 400, B: 800}}
		return []*ui.Node{field, submit, host, key}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: kb_submit}"))
	if len(f.called("HideKeyboard")) != 1 || len(f.taps) != 1 || f.taps[0][1] != 625 {
		t.Errorf("hides %v, taps %v: want the keyboard hidden, then one tap on Submit", f.called("HideKeyboard"), f.taps)
	}
}
