package engine

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #149 review: buttons whose own tap moves or removes them, in CI mode. None may be tapped
// twice, scrolled after, or fail the step. On a still screen the agent sees no change between
// the host's read and the touch, so there is no look at all. On a changing screen (bench
// Animations never stops), add item's new row where the button was shows the tap took, even
// a plain text one, and one reading like the rows before it: the tap stands (issue #149 review:
// the bench's "Add row" was tapped twice, in 8 runs of 10, when this was not counted). A toggle
// that now reads "Show less", a row's delete button and a card's dismiss button that removed
// themselves stand even when the screen was changing.
func TestIOSShiftCheckLeavesATapThatTookAlone(t *testing.T) {
	t.Setenv("TB_CI", "1")
	for _, tc := range []struct {
		name     string
		probe    func() shiftProbe
		changed  bool // the agent saw a change between the host's read and the touch
		wantTaps int  // one per step
	}{
		{"add item, a still screen", addItemProbe, false, 2},
		{"add item, a changing screen", addItemProbe, true, 2},
		{"add item, rows alike, a changing screen", addSameItemProbe, true, 2},
		{"expand toggle, a still screen", expandProbe, false, 1},
		{"expand toggle, a changing screen", expandProbe, true, 1},
		{"delete a row in a list, a still screen", deleteRowProbe, false, 2},
		{"delete a row in a list, a changing screen", deleteRowProbe, true, 2},
		{"dismiss a card in a scroll view, a still screen", dismissCardProbe, false, 1},
		{"dismiss a card in a scroll view, a changing screen", dismissCardProbe, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.probe()
			f := &fakeDriver{plat: "ios"}
			d := &swiftUIDriver{fakeDriver: f, changed: tc.changed}
			f.screen = p.screen
			f.onTap = func(x, y int) { p.onTap(f, x, y) }
			var buf syncBuf
			o := quick
			o.Trace = NewTrace(&buf)
			res := runWith(t, d, o, p.flow)
			if res.Err != nil || res.Status != Passed {
				t.Fatalf("flow %s: %v; trace:\n%s", res.Status, res.Err, buf.String())
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.taps) != tc.wantTaps || len(f.swipeAt) != 0 {
				t.Errorf("taps %v, swipes %v: want %d taps and no swipe; trace:\n%s", f.taps, f.swipeAt, tc.wantTaps, buf.String())
			}
			if p.check != nil {
				p.check(t)
			}
		})
	}
}

// shiftProbe: a screen whose button's own tap moves or removes it.
type shiftProbe struct {
	screen func() []*ui.Node
	onTap  func(f *fakeDriver, x, y int)
	flow   string
	check  func(t *testing.T)
}

// An "Add item" button below its rows: its tap adds a row above it, and it moves down.
func addItemProbe() shiftProbe {
	return addRowsProbe(func(i int) string { return fmt.Sprintf("Item %d", i+1) })
}

// The same, every row reading "Item": the new one is told by the count.
func addSameItemProbe() shiftProbe { return addRowsProbe(func(int) string { return "Item" }) }

func addRowsProbe(label func(i int) string) shiftProbe {
	var rows atomic.Int32
	rows.Store(1)
	return shiftProbe{
		screen: func() []*ui.Node {
			nodes := []*ui.Node{{Class: "View", Bounds: ui.Rect{R: 400, B: 800}}}
			k := int(rows.Load())
			for i := 0; i < k; i++ {
				nodes = append(nodes, &ui.Node{Depth: 1, Class: "StaticText", Text: label(i), Bounds: ui.Rect{T: 100 + 50*i, R: 400, B: 150 + 50*i}})
			}
			add := btn("Add item", "add", 100+50*k)
			add.Depth, add.Class = 1, "Button"
			done := btn("Done", "done", 700)
			done.Depth, done.Class = 1, "Button"
			return append(nodes, add, done)
		},
		onTap: func(f *fakeDriver, x, y int) {
			if k := int(rows.Load()); y >= 100+50*k && y < 150+50*k {
				rows.Add(1)
			}
			f.change(true)
		},
		flow: "- tapOn: {id: add}\n- tapOn: {id: done}",
		check: func(t *testing.T) {
			if rows.Load() != 2 {
				t.Errorf("rows %d, want 2 (one add)", rows.Load())
			}
		},
	}
}

// "Show more" reveals text above itself, moves down, and reads "Show less".
func expandProbe() shiftProbe {
	var open atomic.Bool
	top := func() int {
		if open.Load() {
			return 250
		}
		return 150
	}
	return shiftProbe{
		screen: func() []*ui.Node {
			nodes := []*ui.Node{{Class: "View", Bounds: ui.Rect{R: 400, B: 800}}, {Depth: 1, Class: "StaticText", Text: "Summary", Bounds: ui.Rect{T: 100, R: 400, B: 150}}}
			label := "Show more"
			if open.Load() {
				nodes = append(nodes, &ui.Node{Depth: 1, Class: "StaticText", Text: "Details", Bounds: ui.Rect{T: 150, R: 400, B: 250}})
				label = "Show less"
			}
			b := btn(label, "toggle", top())
			b.Depth, b.Class = 1, "Button"
			return append(nodes, b)
		},
		onTap: func(f *fakeDriver, x, y int) {
			if y >= top() && y < top()+50 {
				open.Store(!open.Load())
			}
			f.change(true)
		},
		flow: "- tapOn: {id: toggle}",
		check: func(t *testing.T) {
			if !open.Load() {
				t.Errorf("collapsed again: tapped twice")
			}
		},
	}
}

// A delete button in a scrollable list removes its own row; the rows below move up.
func deleteRowProbe() shiftProbe {
	var deleted atomic.Bool
	return shiftProbe{
		screen: func() []*ui.Node {
			nodes := []*ui.Node{{Class: "View", Bounds: ui.Rect{R: 400, B: 800}},
				{Depth: 1, Class: "StaticText", ID: "title", Text: "List", Bounds: ui.Rect{T: 20, R: 400, B: 60}},
				{Depth: 1, Class: "Table", Scrollable: true, Bounds: ui.Rect{T: 100, R: 400, B: 700}}}
			top := 100
			for i := 0; i < 8; i++ {
				if i == 2 && deleted.Load() {
					continue
				}
				b := btn(fmt.Sprintf("Delete %d", i), fmt.Sprintf("del_%d", i), top)
				b.Depth, b.Class = 2, "Button"
				nodes = append(nodes, b)
				top += 60
			}
			return nodes
		},
		onTap: func(f *fakeDriver, x, y int) { deleted.Store(true); f.change(true) },
		flow:  "- tapOn: {id: del_2}\n- tapOn: {id: del_5}",
	}
}

// A tip card's Dismiss button inside a scroll view removes the card; plain text moves up.
func dismissCardProbe() shiftProbe {
	var gone atomic.Bool
	return shiftProbe{
		screen: func() []*ui.Node {
			nodes := []*ui.Node{{Class: "View", Bounds: ui.Rect{R: 400, B: 800}},
				{Depth: 1, Class: "StaticText", ID: "title", Text: "Home", Bounds: ui.Rect{T: 20, R: 400, B: 60}},
				{Depth: 1, Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 100, R: 400, B: 700}}}
			top := 100
			if !gone.Load() {
				nodes = append(nodes, &ui.Node{Depth: 2, Class: "StaticText", Text: "Tip: try this", Bounds: ui.Rect{T: 100, R: 400, B: 150}})
				b := btn("Dismiss", "dismiss_tip", 150)
				b.Depth, b.Class = 2, "Button"
				nodes = append(nodes, b)
				top = 200
			}
			for i := 0; i < 8; i++ {
				nodes = append(nodes, &ui.Node{Depth: 2, Class: "StaticText", Text: fmt.Sprintf("Para %d", i), Bounds: ui.Rect{T: top + 60*i, R: 400, B: top + 60*i + 50}})
			}
			return nodes
		},
		onTap: func(f *fakeDriver, x, y int) { gone.Store(true); f.change(true) },
		flow:  "- tapOn: {id: dismiss_tip}",
	}
}
