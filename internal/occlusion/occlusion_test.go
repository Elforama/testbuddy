package occlusion

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func node(depth int, l, t, r, b int) *ui.Node {
	return &ui.Node{Depth: depth, Bounds: ui.Rect{L: l, T: t, R: r, B: b}, Enabled: true}
}

func tappable(depth int, l, t, r, b int) *ui.Node {
	n := node(depth, l, t, r, b)
	n.Clickable = true
	return n
}

func screen(platform string, wins ...[]*ui.Node) *ui.Screen {
	s := &ui.Screen{Platform: platform, Width: 1000, Height: 2000}
	for i, nodes := range wins {
		s.Windows = append(s.Windows, &ui.Window{Type: "app", Active: i == len(wins)-1, Bounds: ui.Rect{R: 1000, B: 2000}, Nodes: nodes})
	}
	s.Link()
	return s
}

// Modal tells a layer over the content as a whole (issue #282) from a bar fixed over it.
func TestModal(t *testing.T) {
	// A drawer: narrower than its container, as tall as the screen, over all of the element.
	{
		root := node(0, 0, 0, 1000, 2000)
		el := tappable(1, 50, 300, 300, 400)
		drawer := node(1, 0, 0, 700, 2000)
		row := tappable(2, 0, 280, 700, 420)
		s := screen("android", []*ui.Node{root, el, drawer, row})
		c, modal := Covered(s, el)
		if c != row || !modal || Modal(s, el, row) != drawer {
			t.Errorf("drawer: cover %v modal %v layer %v", c, modal, Modal(s, el, row))
		}
	}
	// A toolbar, a floating button, a sticky header: bars.
	{
		root := node(0, 0, 0, 1000, 2000)
		list := node(1, 0, 0, 1000, 2000)
		list.Scrollable = true
		header := node(2, 0, 0, 1000, 150)
		el := tappable(2, 0, 50, 1000, 200)
		fab := tappable(1, 800, 1800, 950, 1950)
		low := tappable(2, 0, 1780, 900, 1990)
		s := screen("android", []*ui.Node{root, list, header, el, low, fab})
		if c, modal := Covered(s, el); c != header || modal {
			t.Errorf("sticky header: cover %v modal %v", c, modal)
		}
		if c := At(s, low, 875, 1875); c != fab || Modal(s, low, fab) != nil {
			t.Errorf("floating button: cover %v, layer %v", c, Modal(s, low, fab))
		}
	}
	// A full-size see-through frame laid over the content, holding a bottom bar (as React
	// Native stacks them): the frame fills its container, so it is not taken for a layer.
	{
		root := node(0, 0, 0, 1000, 2000)
		el := tappable(1, 0, 1850, 1000, 1950)
		frame := node(1, 0, 0, 1000, 2000)
		bar := tappable(2, 0, 1800, 1000, 2000)
		s := screen("android", []*ui.Node{root, el, frame, bar})
		if c, modal := Covered(s, el); c != bar || modal {
			t.Errorf("bar in a full-size frame: cover %v modal %v", c, modal)
		}
	}
	// Another app window above (a dialog) is a modal layer; the keyboard is a bar.
	{
		el := tappable(0, 400, 1000, 600, 1100)
		dlg := node(0, 100, 700, 900, 1300)
		s := screen("android", []*ui.Node{dlg}, []*ui.Node{el})
		s.Windows[0].Bounds = dlg.Bounds
		if c, modal := Covered(s, el); c != dlg || !modal {
			t.Errorf("dialog window: cover %v modal %v", c, modal)
		}
		s.Windows[0].Type = "keyboard"
		if c, modal := Covered(s, el); c == nil || modal {
			t.Errorf("keyboard: cover %v modal %v", c, modal)
		}
	}
	// Nothing over it: no cover. The web: never judged from the tree.
	{
		el := tappable(0, 0, 0, 100, 100)
		s := screen("android", []*ui.Node{el})
		if c, modal := Covered(s, el); c != nil || modal {
			t.Errorf("clear: %v %v", c, modal)
		}
		root := node(0, 0, 0, 1000, 2000)
		in := tappable(1, 50, 300, 300, 400)
		drawer := node(1, 0, 0, 700, 2000)
		row := tappable(2, 0, 280, 700, 420)
		if c, _ := Covered(screen("web", []*ui.Node{root, in, drawer, row}), in); c != nil {
			t.Errorf("web: %v", c)
		}
	}
}

// Covered counts most of the element's area as well as its middle: three of its four
// quarters under a cover, its middle clear (a cover with a hole, read as two parts).
func TestCoveredMostOfItsArea(t *testing.T) {
	root := node(0, 0, 0, 1000, 2000)
	el := tappable(1, 100, 100, 500, 500)
	left := tappable(1, 0, 0, 250, 600)    // over its left quarters
	right := tappable(1, 350, 0, 600, 300) // over its top right quarter
	s := screen("android", []*ui.Node{root, el, left, right})
	if c, _ := Covered(s, el); c == nil {
		t.Error("three quarters covered, middle clear: not counted")
	}
	s = screen("android", []*ui.Node{root, el, left})
	if c, _ := Covered(s, el); c != nil {
		t.Errorf("half covered: counted (%v)", c)
	}
}
