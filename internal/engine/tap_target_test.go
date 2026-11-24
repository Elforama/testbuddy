package engine

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

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
			return at(42, 90)
		case n <= 7:
			return nil
		default:
			return at(200, 447)
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Sprung"))
	if len(f.taps) != 1 || f.taps[0][0] < 200 || f.taps[0][0] > 447 {
		t.Errorf("taps %v, want one on the button at rest (x 200-447)", f.taps)
	}
}

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
			return at(42, 94)
		default:
			return at(200, 447)
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Sprung"))
	if len(f.taps) != 1 || f.taps[0][0] < 200 || f.taps[0][0] > 447 {
		t.Errorf("taps %v, want one on the button at rest (x 200-447)", f.taps)
	}
}

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
		l := 200
		switch {
		case ms < 60:
			l = 280
		case ms < 400:
			l = 50 + int(ms-60)/30*10
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

func TestTapOnPulseIsNotHeldAsCutOff(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		d := int(reads.Add(1) % 2)
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

func TestTapHidesKeyboardOverTarget(t *testing.T) {
	t.Parallel()
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
	mustPass(t, runWith(t, f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: time.Second}, "- tapOn: {id: kb_submit}"))
	if len(f.called("HideKeyboard")) != 1 || len(f.taps) != 1 || f.taps[0][1] != 625 {
		t.Errorf("hides %v, taps %v", f.called("HideKeyboard"), f.taps)
	}
	if d := time.Since(start); d < keyboardGrace || d > keyboardGrace+2*time.Second {
		t.Errorf("took %s", d)
	}
}

func TestTapHidesKeyboardCoveringTarget(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios"}
	f.kbShown = true
	f.screen = func() []*ui.Node {
		field := btn("Bottom field", "kb_bottom", 400)
		submit := btn("Submit", "kb_submit", 600)
		submit.Class = "Button"
		for _, c := range f.calls {
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
