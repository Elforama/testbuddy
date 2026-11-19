package occlusion

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// iosLongList is the bench's iOS long list scrolled to row 120 (a `tb hierarchy --json` dump,
// iOS 26 simulator, 402x874): a SwiftUI List whose content view is as large as the list and
// listed among its cells, a section header pinned at the list's top over Row 115, and Row 127
// cut off at the list's bottom.
func iosLongList() (s *ui.Screen, nodes map[string]*ui.Node) {
	nodes = map[string]*ui.Node{}
	mk := func(name, class string, depth, l, t, r, b int) *ui.Node {
		n := &ui.Node{Class: class, Depth: depth, Bounds: ui.Rect{L: l, T: t, R: r, B: b}, Enabled: true, Desc: name}
		if class == "Other" || class == "Cell" || class == "CollectionView" {
			n.Desc = ""
		}
		if name != "" {
			nodes[name] = n
		}
		return n
	}
	list := mk("list", "CollectionView", 1, 0, 161, 402, 874)
	list.Scrollable = true
	app := []*ui.Node{
		mk("", "Other", 0, 0, 0, 402, 874),
		list,
		mk("content", "Other", 2, 0, 161, 402, 874),
		mk("", "Cell", 2, 0, 116, 402, 172), mk("Row 114", "Button", 3, 0, 116, 402, 172),
		mk("header", "Cell", 2, 0, 161, 402, 201), mk("Section E", "StaticText", 3, 0, 161, 402, 201),
		mk("See all", "Button", 3, 320, 165, 400, 197),
		mk("", "Cell", 2, 0, 172, 402, 228), mk("Row 115", "Button", 3, 0, 172, 402, 228),
		mk("", "Cell", 2, 0, 228, 402, 284), mk("Row 116", "Button", 3, 0, 228, 402, 284),
		mk("", "Cell", 2, 0, 850, 402, 906), mk("Row 127", "Button", 3, 0, 850, 402, 906),
	}
	// The content view comes after the first cells in the real tree; here it is listed before
	// Row 127 either way.
	s = &ui.Screen{Platform: "ios", Width: 402, Height: 874, Windows: []*ui.Window{
		{Type: "app", Active: true, Focused: true, Bounds: ui.Rect{R: 402, B: 874}, Nodes: app},
	}}
	s.Link()
	return s, nodes
}

// CI run 44430794044 (iOS lists.yaml): Row 3's middle (201, 201) lies on the bottom edge of the
// pinned "Section A" header, [0,161][402,201] in the tree. iOS frames are fractional and
// rounded there: the header really covers that point and took the tap, three times. A cover's
// edges count 1 pt wider, so the tap goes to the middle of what shows below the header.
func TestTapPointClearsACoverEdgeRoundedAway(t *testing.T) {
	mk := func(class, label string, depth, l, t, r, b int) *ui.Node {
		return &ui.Node{Class: class, Desc: label, Depth: depth, Bounds: ui.Rect{L: l, T: t, R: r, B: b}, Enabled: true}
	}
	list := mk("CollectionView", "", 1, 0, 161, 402, 874)
	list.Scrollable = true
	row2 := mk("Button", "Row 2", 3, 0, 117, 402, 173)
	header := mk("StaticText", "Section A", 3, 0, 161, 402, 201)
	row3 := mk("Button", "Row 3", 3, 0, 173, 402, 229)
	row4 := mk("Button", "Row 4", 3, 0, 229, 402, 285)
	s := &ui.Screen{Platform: "ios", Width: 402, Height: 874, Windows: []*ui.Window{{
		Type: "app", Active: true, Focused: true, Bounds: ui.Rect{R: 402, B: 874}, Nodes: []*ui.Node{
			mk("Other", "", 0, 0, 0, 402, 874), list,
			mk("Cell", "", 2, 0, 117, 402, 173), row2,
			mk("Cell", "", 2, 0, 161, 402, 201), header,
			mk("Cell", "", 2, 0, 173, 402, 229), row3,
			mk("Cell", "", 2, 0, 229, 402, 285), row4,
		}}}}
	s.Link()
	x, y, cover, ok := UncoveredPoint(s, row3)
	if !ok || cover == nil || y < 203 || y > 227 {
		t.Errorf("Row 3: tap at (%d, %d) cover %v ok %v, want below the header (y 203..227)", x, y, cover, ok)
	}
	// A row nothing covers keeps its middle, its neighbours' edges touching it or not.
	if x, y, cover, ok := UncoveredPoint(s, row4); !ok || cover != nil || x != 201 || y != 257 {
		t.Errorf("Row 4: tap at (%d, %d) cover %v ok %v, want its middle (201, 257)", x, y, cover, ok)
	}
}

// Android's bounds are exact pixels: a row whose middle lies on a bar's edge is clear there,
// and its tap stays its middle (the 1 pt margin is iOS's alone).
func TestExactBoundsHaveNoEdgeMargin(t *testing.T) {
	root := node(0, 0, 0, 1000, 2000)
	list := node(1, 0, 0, 1000, 2000)
	list.Scrollable = true
	header := node(2, 0, 0, 1000, 200) // pinned, listed first
	header.Clickable = true
	row := tappable(2, 0, 100, 1000, 300) // middle (500, 200): the header's bottom edge
	s := screen("android", []*ui.Node{root, list, header, row})
	if c := At(s, row, 500, 200); c != nil {
		t.Errorf("android: the header's edge covers the row's middle: %v", c.Bounds)
	}
	small := tappable(1, 0, 600, 100, 606)    // a 6 px element
	touching := tappable(1, 0, 606, 100, 700) // its neighbour, drawn after it, touching
	s = screen("android", []*ui.Node{root, small, touching})
	if x, y, c, ok := UncoveredPoint(s, small); c != nil || !ok || x != 50 || y != 603 {
		t.Errorf("android 6 px element: tap (%d, %d) cover %v ok %v, want its middle, clear", x, y, c, ok)
	}
}

// A row cut off at the list's bottom is not covered by the list's content view (as large as
// the list, it does not hold the whole row): it was ranked as covered and steered clear of.
func TestListContentViewDoesNotCoverACutOffRow(t *testing.T) {
	s, n := iosLongList()
	if c, _ := Covered(s, n["Row 127"]); c != nil {
		t.Errorf("Row 127 covered by %v %q %v, want clear", c.Class, c.Desc, c.Bounds)
	}
}

// A pinned section header, and a button in it, are not covered by the row scrolled under it
// (listed after the header, as iOS lists a list's children by place): no scroll clears that.
// The row under the header is covered by it.
func TestRowDoesNotCoverItsPinnedHeader(t *testing.T) {
	s, n := iosLongList()
	for _, name := range []string{"Section E", "See all"} {
		if c, _ := Covered(s, n[name]); c != nil {
			t.Errorf("%s covered by %v %q %v, want clear", name, c.Class, c.Desc, c.Bounds)
		}
	}
	if c, modal := Covered(s, n["Row 115"]); c != n["header"] || modal {
		t.Errorf("Row 115: cover %v modal %v, want the pinned header, a bar", c, modal)
	}
	if c, _ := Covered(s, n["Row 116"]); c != nil {
		t.Errorf("Row 116 covered by %v", c)
	}
}
