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
	// Windows above. Only one that takes every touch in its bounds (the focused one: a dialog)
	// is a modal layer. One without focus (picture-in-picture, an autocomplete popup) covers
	// what it draws over, as a bar; the keyboard is a bar.
	{
		el := tappable(0, 400, 1000, 600, 1100)
		dlg := node(0, 100, 700, 900, 1300)
		title := node(1, 150, 750, 850, 850)
		title.Text = "Delete item?"
		s := screen("android", []*ui.Node{dlg, title}, []*ui.Node{el})
		s.Windows[0].Bounds, s.Windows[0].Focused = dlg.Bounds, true
		if c, modal := Covered(s, el); c != dlg || !modal {
			t.Errorf("dialog window: cover %v modal %v", c, modal)
		}
		s.Windows[0].Focused = false // picture-in-picture, a popup
		if c, modal := Covered(s, el); c != dlg || modal {
			t.Errorf("unfocused window: cover %v modal %v, want a cover that is no modal layer", c, modal)
		}
		s.Windows[0].Type, s.Windows[0].Nodes = "keyboard", nil
		if c, modal := Covered(s, el); c == nil || modal {
			t.Errorf("keyboard: cover %v modal %v", c, modal)
		}
	}
	// A BottomSheetDialog's window spans the screen: its touch_outside, over what is under the
	// sheet, is where a tap dismisses the sheet. It is no cover there (a flow taps through it
	// on purpose); the sheet itself is a modal layer.
	{
		under := tappable(0, 100, 300, 900, 400)
		low := tappable(0, 100, 1600, 900, 1700)
		root := node(0, 0, 0, 1000, 2000)
		outside := tappable(1, 0, 0, 1000, 2000)
		outside.ID = "com.google.android.material:id/touch_outside"
		sheet := node(1, 0, 1400, 1000, 2000)
		action := tappable(2, 0, 1550, 1000, 1750)
		s := screen("android", []*ui.Node{root, outside, sheet, action}, []*ui.Node{under, low})
		s.Windows[0].Focused = true
		if c, _ := Covered(s, under); c != nil {
			t.Errorf("under the sheet's touch_outside: cover %v, want none", c)
		}
		if c, modal := Covered(s, low); c != action || !modal {
			t.Errorf("under the sheet: cover %v modal %v", c, modal)
		}
		outside.ID = "" // a dim layer with no name: a label-less, empty node as large as its window
		if c, _ := Covered(s, under); c != nil {
			t.Errorf("under an unnamed scrim: cover %v, want none", c)
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

// Modal is structural (issue #282 review): no share of the screen decides it.
func TestModalIsStructural(t *testing.T) {
	sized := func(w, h int, nodes ...*ui.Node) *ui.Screen {
		s := &ui.Screen{Platform: "android", Width: w, Height: h,
			Windows: []*ui.Window{{Type: "app", Active: true, Bounds: ui.Rect{R: w, B: h}, Nodes: nodes}}}
		s.Link()
		return s
	}
	// A drawer on a wide screen (a tablet, a phone in landscape): a fifth of the screen.
	{
		root := node(0, 0, 0, 3000, 1400)
		list := node(1, 0, 100, 3000, 1400)
		list.Scrollable = true
		el := tappable(2, 64, 388, 319, 514)
		drawer := node(1, 0, 100, 600, 1400)
		roots := node(2, 0, 200, 600, 1400)
		row := tappable(3, 0, 430, 600, 577)
		s := sized(3000, 1400, root, list, el, drawer, roots, row)
		if c, modal := Covered(s, el); c == nil || !modal || Modal(s, el, c) != drawer {
			t.Errorf("wide drawer: cover %v modal %v", c, modal)
		}
	}
	// A sheet over the lower half of a list, in the app's own window.
	{
		root := node(0, 0, 0, 1000, 2000)
		list := node(1, 0, 0, 1000, 2000)
		list.Scrollable = true
		el := tappable(2, 0, 1500, 1000, 1640)
		sheet := node(1, 0, 1000, 1000, 2000)
		content := node(2, 0, 1000, 1000, 2000)
		action := tappable(3, 0, 1450, 1000, 1700)
		s := screen("android", []*ui.Node{root, list, el, sheet, content, action})
		if c, modal := Covered(s, el); c != action || !modal {
			t.Errorf("half-height sheet: cover %v modal %v", c, modal)
		}
	}
	// A portal dialog (react-native-paper's Portal): a host filling the screen, after the
	// content, holding a backdrop that takes touches and the dialog's card.
	{
		root := node(0, 0, 0, 1000, 2000)
		list := node(1, 0, 0, 1000, 2000)
		list.Scrollable = true
		el := tappable(2, 500, 960, 900, 1040)
		host := node(1, 0, 0, 1000, 2000)
		backdrop := tappable(2, 0, 0, 1000, 2000)
		card := node(2, 100, 700, 900, 1300)
		ok := tappable(3, 600, 950, 850, 1050)
		s := screen("android", []*ui.Node{root, list, el, host, backdrop, card, ok})
		if c, modal := Covered(s, el); c == nil || !modal || Modal(s, el, c) != host || Modal(s, el, ok) != host {
			t.Errorf("portal dialog: cover %v modal %v", c, modal)
		}
	}
	// An inset see-through overlay holding a floating button, and one holding a mini-player:
	// bars, so the row under them can still be dragged clear.
	{
		root := node(0, 0, 0, 1000, 2000)
		list := node(1, 0, 100, 1000, 2000)
		list.Scrollable = true
		el := tappable(2, 700, 1800, 1000, 1900)
		overlay := node(1, 0, 100, 1000, 1950)
		fab := tappable(2, 780, 1780, 920, 1920)
		s := screen("android", []*ui.Node{root, list, el, overlay, fab})
		if c, modal := Covered(s, el); c != fab || modal {
			t.Errorf("inset overlay's floating button: cover %v modal %v", c, modal)
		}
		row := tappable(2, 0, 1700, 1000, 1800)
		player := tappable(2, 0, 1680, 1000, 1830)
		s = screen("android", []*ui.Node{root, list, row, overlay, player})
		if c, modal := Covered(s, row); c != player || modal {
			t.Errorf("inset overlay's mini-player: cover %v modal %v", c, modal)
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
