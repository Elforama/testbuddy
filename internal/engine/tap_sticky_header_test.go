package engine

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func pinnedHeaderList(f *fakeDriver, header ui.Rect) (picked *atomic.Bool) {
	picked = &atomic.Bool{}
	f.screen = func() []*ui.Node {
		status := "Nothing picked"
		if picked.Load() {
			status = "Picked Row 3"
		}
		node := func(class string, depth int, text, id string, r ui.Rect) *ui.Node {
			return &ui.Node{Class: class, Depth: depth, Text: text, ID: id, Bounds: r, Enabled: true}
		}
		list := node("CollectionView", 0, "", "", ui.Rect{R: 402, T: 161, B: 874})
		list.Scrollable = true
		return []*ui.Node{
			node("StaticText", 0, status, "picked", ui.Rect{L: 20, T: 133, R: 135, B: 153}),
			list,
			node("Other", 1, "", "", ui.Rect{R: 402, T: -1431, B: 1457}),
			node("Cell", 1, "", "", ui.Rect{R: 402, T: 113, B: 169}),
			node("Button", 2, "Row 2", "row_2", ui.Rect{R: 402, T: 113, B: 169}),
			node("Cell", 1, "", "", header),
			node("StaticText", 2, "Section A", "section_A", header),
			node("Cell", 1, "", "", ui.Rect{R: 402, T: 169, B: 225}),
			node("Button", 2, "Row 3", "row_3", ui.Rect{R: 402, T: 169, B: 225}),
			node("Cell", 1, "", "", ui.Rect{R: 402, T: 225, B: 281}),
			node("Button", 2, "Row 4", "row_4", ui.Rect{R: 402, T: 225, B: 281}),
		}
	}
	f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
		f.mu.Lock()
		at := f.taps[n-1]
		f.mu.Unlock()
		if header.Contains(at[0], at[1]) {
			res.Settle = driver.NoChange
			return res
		}
		picked.Store(true)
		f.change(true)
		res.Settle = driver.Changed
		return res
	}
	return picked
}

func TestIOSTapAvoidsAPinnedSectionHeader(t *testing.T) {
	t.Parallel()
	header := ui.Rect{R: 402, T: 161, B: 201}
	f := &fakeDriver{plat: "ios", size: [2]int{402, 874}, tapSettle: driver.NoChange}
	picked := pinnedHeaderList(f, header)
	mustPass(t, runWith(t, f, quick, "- tapOn: \"Row 3\"\n- assertVisible: \"Picked Row 3\""))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.taps) == 0 || header.Contains(f.taps[0][0], f.taps[0][1]) || !picked.Load() {
		t.Errorf("taps %v: want the first on Row 3 clear of the header %v", f.taps, header)
	}
}

func TestIOSListContentViewIsNoPinnedHeader(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", size: [2]int{402, 874}}
	pinnedHeaderList(f, ui.Rect{R: 402, T: 161, B: 201})
	mustPass(t, runWith(t, f, quick, "- tapOn: \"Row 4\""))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.taps) != 1 || f.taps[0] != [2]int{201, 253} {
		t.Errorf("taps %v, want one at Row 4's middle (201, 253)", f.taps)
	}
}

func shortList(w, h, listTop, rows int, header ui.Rect) (*ui.Screen, []*ui.Node) {
	node := func(class string, depth int, text string, r ui.Rect) *ui.Node {
		return &ui.Node{Class: class, Depth: depth, Text: text, Bounds: r, Enabled: true}
	}
	list := node("CollectionView", 0, "", ui.Rect{R: w, T: listTop, B: h})
	list.Scrollable = true
	nodes := []*ui.Node{list, node("Other", 1, "", ui.Rect{R: w, T: listTop, B: listTop + 56*rows})}
	if !header.Empty() {
		nodes = append(nodes, node("Cell", 1, "", header), node("StaticText", 2, "Section A", header))
	}
	var buttons []*ui.Node
	for i := 0; i < rows; i++ {
		r := ui.Rect{R: w, T: listTop + 56*i, B: listTop + 56*(i+1)}
		b := node("Button", 2, fmt.Sprintf("Row %d", i+1), r)
		nodes = append(nodes, node("Cell", 1, "", r), b)
		buttons = append(buttons, b)
	}
	win := &ui.Window{Type: "app", Package: "app", Active: true, Nodes: nodes, Bounds: ui.Rect{R: w, B: h}}
	s := &ui.Screen{Platform: "ios", Width: w, Height: h, Windows: []*ui.Window{win}}
	s.Link()
	return s, buttons
}

func TestIOSShortListContentViewIsNoPinnedHeader(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		w, h int
	}{{"iPhone", 402, 874}, {"iPad", 1032, 1376}} {
		s, rows := shortList(c.w, c.h, 100, 4, ui.Rect{})
		for _, r := range rows {
			if cover := coverAt(s, r, r.Bounds.CX(), r.Bounds.CY()); cover != nil {
				t.Errorf("%s: %s covered by %s %v", c.name, r.Text, cover.Class, cover.Bounds)
			}
		}
	}
}

func TestIOSShortListPinnedHeaderIsFound(t *testing.T) {
	t.Parallel()
	header := ui.Rect{R: 402, T: 100, B: 140}
	s, rows := shortList(402, 874, 100, 4, header)
	if cover := coverAt(s, rows[0], 201, 128); cover == nil || cover.Bounds != header {
		t.Errorf("Row 1 at its middle: cover %v, want the header %v", cover, header)
	}
	if cover := coverAt(s, rows[1], 201, 184); cover != nil {
		t.Errorf("Row 2: cover %v, want none", cover)
	}
}
