package match

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// VisibleShare is the fraction of a rectangle on screen; a rectangle with exactly one zero
// dimension counts as fully visible, a zero-size or inverted one as not visible.
func TestVisibleShare(t *testing.T) {
	scr := rect(0, 0, 1000, 2000)
	cases := []struct {
		name string
		r    ui.Rect
		want float64
	}{
		{"fully on screen", rect(0, 0, 100, 100), 1},
		{"the whole screen", scr, 1},
		{"half off the right", rect(950, 0, 1050, 100), 0.5},
		{"quarter on at a corner", rect(-50, -50, 50, 50), 0.25},
		{"exactly 10% on", rect(0, 1910, 100, 2810), 0.1},
		{"fully off below", rect(0, 2100, 100, 2200), 0},
		{"fully off left", rect(-200, 0, -100, 100), 0},
		{"bigger than the screen", rect(-1000, 0, 2000, 2000), 1.0 / 3},
		{"zero size", rect(10, 10, 10, 10), 0},
		{"zero value", ui.Rect{}, 0},
		{"zero width line", rect(10, 10, 10, 500), 1},
		{"zero height line", rect(10, 10, 500, 10), 1},
		{"zero height line off screen still counts", rect(10, 5000, 500, 5000), 1},
		{"inverted width", rect(100, 0, 0, 100), 0},
		{"inverted height", rect(0, 100, 100, 0), 0},
		{"inverted both", rect(100, 100, 0, 0), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := VisibleShare(c.r, scr)
			if d := got - c.want; d > 1e-9 || d < -1e-9 {
				t.Errorf("VisibleShare(%v) = %v, want %v", c.r, got, c.want)
			}
		})
	}
}

// Visible keeps nodes at least 10% on screen, and any node with a kept descendant, in
// pre-order.
func TestVisiblePrunesOffScreen(t *testing.T) {
	scroller := &ui.Node{Depth: 1, Text: "list", Bounds: rect(0, 0, 1000, 5000)} // 40% visible
	onRow := leaf(2, "on", rect(0, 100, 1000, 200))
	offRow := leaf(2, "off", rect(0, 3000, 1000, 3100))
	tenth := leaf(2, "tenth", rect(0, 1910, 100, 2810))   // exactly 10%
	sliver := leaf(2, "sliver", rect(0, 1920, 100, 2820)) // under 10%
	offParent := &ui.Node{Depth: 1, Text: "offParent", Bounds: rect(0, 4000, 1000, 6000)}
	visibleKid := leaf(2, "kid", rect(0, 0, 50, 50)) // drawn outside its parent
	hiddenKid := leaf(2, "hiddenKid", rect(0, 4100, 50, 4150))
	s := mkScreen(root(), scroller, onRow, offRow, tenth, sliver, offParent, visibleKid, hiddenKid)

	got := names(Visible(s))
	want := []string{nodeName(s.Nodes[0]), "list", "on", "tenth", "offParent", "kid"}
	if !sameNames(got, want) {
		t.Errorf("Visible = %v, want %v", got, want)
	}
}

// When nothing qualifies (e.g. an unknown screen size), every node is a candidate.
func TestVisibleFallsBackToAllNodes(t *testing.T) {
	s := mkScreen(leaf(0, "a", rect(0, 3000, 100, 3100)), leaf(1, "b", rect(0, 4000, 100, 4100)))
	if got := Visible(s); len(got) != 2 {
		t.Errorf("Visible = %v, want both nodes", names(got))
	}
	s = &ui.Screen{Windows: []*ui.Window{{Type: "app", Nodes: []*ui.Node{leaf(0, "x", rect(0, 0, 10, 10))}}}}
	s.Link()
	if got := Visible(s); len(got) != 1 {
		t.Errorf("zero-size screen: Visible = %v, want the node", names(got))
	}
	if got := Visible(&ui.Screen{Width: 100, Height: 100}); len(got) != 0 {
		t.Errorf("empty screen: Visible = %v", names(got))
	}
}

// Visible looks at every window, keeping their order.
func TestVisibleAcrossWindows(t *testing.T) {
	s := &ui.Screen{Width: 1000, Height: 2000, Windows: []*ui.Window{
		{Type: "keyboard", Nodes: []*ui.Node{leaf(0, "key", rect(0, 1500, 100, 1600)), leaf(0, "offkey", rect(0, 2500, 100, 2600))}},
		{Type: "app", Nodes: []*ui.Node{leaf(0, "app", rect(0, 0, 1000, 1000))}},
	}}
	s.Link()
	if got := names(Visible(s)); !sameNames(got, []string{"key", "app"}) {
		t.Errorf("Visible = %v", got)
	}
}

// Find works across windows: a keyboard window's nodes are candidates like the app's.
func TestFindAcrossWindows(t *testing.T) {
	done := leaf(0, "Done", rect(800, 1500, 1000, 1600))
	done.Clickable = true
	s := &ui.Screen{Width: 1000, Height: 2000, Windows: []*ui.Window{
		{Type: "keyboard", Nodes: []*ui.Node{done}},
		{Type: "app", Nodes: []*ui.Node{leaf(0, "Done", rect(0, 0, 100, 100))}},
	}}
	s.Link()
	got, all, _ := Find(s, &Selector{Text: "Done"})
	if got != done || len(all) != 2 {
		t.Errorf("got %s among %v", nodeName(got), names(all))
	}
}
