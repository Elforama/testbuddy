package engine

import (
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
