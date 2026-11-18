package engine

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// pinnedHeaderList is the bench's long list on iOS (bench/flows/scenarios/lists.yaml), as the
// tree read it when `tapOn: "Row 3"` failed after scrolling up to it (seen on main 4cb1f80 and
// in the CI of PR #376): a SwiftUI List of plain style (a UICollectionView) whose section
// header, "Section A", is pinned at the list's top and drawn over the top of Row 3. iOS lists a
// collection's cells by place, so the header's cell comes before Row 3's, and a content view
// as tall as the list's content comes first of all. A tap inside the header goes to the
// header: nothing is picked, nothing changes. Anywhere else on Row 3 picks it.
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
			res.Settle = driver.NoChange // the header took it
			return res
		}
		picked.Store(true)
		f.change(true)
		res.Settle = driver.Changed
		return res
	}
	return picked
}

// lists.yaml's flake: Row 3's middle (y 197) is under Section A's pinned header ([161,201]),
// which the tap took. The header is a cell before Row 3's in the tree, so the iOS cover rules
// (bars, controls drawn after) never saw it. Like Android's sticky headers, it covers the rows
// it is drawn over: the tap goes where Row 3 shows clear of it, and picks it.
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

// The content view as tall as the list's content, before every cell, is no cover: Row 4, under
// no header, is tapped at its middle.
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

// shortList is an iOS list (a Form, a settings list) of w x h points whose content view, listed
// before its cells, is only as tall as its content ([listTop, listTop+56*rows]), with a section
// header pinned at header when header is not empty (listed before the rows, as iOS orders them).
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

// Review: a short list's content view (a Form's, or any list on an iPad) is smaller than half
// the screen, holds its rows and comes before them: it is no cover of any of them. (Taken for
// one by a half-screen rule, every row was "covered", a real pinned header was never found, and
// a slider's re-drag in a short Form stopped.)
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

// ... while a header pinned over a short list's row is still found.
func TestIOSShortListPinnedHeaderIsFound(t *testing.T) {
	t.Parallel()
	header := ui.Rect{R: 402, T: 100, B: 140}
	s, rows := shortList(402, 874, 100, 4, header)
	// Row 1 ([100,156]) lies partly under the header: its middle (128) is under it.
	if cover := coverAt(s, rows[0], 201, 128); cover == nil || cover.Bounds != header {
		t.Errorf("Row 1 at its middle: cover %v, want the header %v", cover, header)
	}
	if cover := coverAt(s, rows[1], 201, 184); cover != nil {
		t.Errorf("Row 2: cover %v, want none", cover)
	}
}
