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

func TestModal(t *testing.T) {
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
		s.Windows[0].Focused = false
		if c, modal := Covered(s, el); c != dlg || modal {
			t.Errorf("unfocused window: cover %v modal %v, want a cover that is no modal layer", c, modal)
		}
		s.Windows[0].Type, s.Windows[0].Nodes = "keyboard", nil
		if c, modal := Covered(s, el); c == nil || modal {
			t.Errorf("keyboard: cover %v modal %v", c, modal)
		}
	}
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
		outside.ID = ""
		if c, _ := Covered(s, under); c != nil {
			t.Errorf("under an unnamed scrim: cover %v, want none", c)
		}
	}
	{
		under := tappable(0, 100, 300, 900, 400)
		veil := node(0, 0, 0, 1000, 2000)
		s := screen("android", []*ui.Node{veil}, []*ui.Node{under})
		s.Windows[0].Focused = true
		if c, modal := Covered(s, under); c != veil || !modal {
			t.Errorf("under a veil: cover %v modal %v, want the veil, a modal layer", c, modal)
		}
	}
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

func TestModalIsStructural(t *testing.T) {
	sized := func(w, h int, nodes ...*ui.Node) *ui.Screen {
		s := &ui.Screen{Platform: "android", Width: w, Height: h,
			Windows: []*ui.Window{{Type: "app", Active: true, Bounds: ui.Rect{R: w, B: h}, Nodes: nodes}}}
		s.Link()
		return s
	}
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

func TestModalSheetOfFlatRows(t *testing.T) {
	root := node(0, 0, 0, 1000, 2000)
	list := node(1, 0, 0, 1000, 2000)
	list.Scrollable = true
	el := tappable(2, 0, 1400, 1000, 1500)
	sheet := node(1, 0, 1000, 1000, 2000)
	nodes := []*ui.Node{root, list, el, sheet}
	for i := 0; i < 5; i++ {
		nodes = append(nodes, tappable(2, 0, 1000+200*i, 1000, 1200+200*i))
	}
	s := screen("android", nodes)
	if c, modal := Covered(s, el); c == nil || !modal || Modal(s, el, c) != sheet {
		t.Errorf("sheet of flat rows: cover %v modal %v", c, modal)
	}
}

func TestIOSKeyboardUpSheetStaysModal(t *testing.T) {
	typed := func(class string, depth, l, t, r, b int) *ui.Node {
		n := node(depth, l, t, r, b)
		n.Class = class
		return n
	}
	app := func() []*ui.Node {
		table := typed("Table", 2, 0, 0, 393, 852)
		table.Scrollable = true
		return []*ui.Node{typed("Application", 0, 0, 0, 393, 852), typed("Window", 1, 0, 0, 393, 852), table,
			typed("Cell", 3, 0, 380, 393, 440)}
	}
	ios := func(kbWindow bool, nodes []*ui.Node) (*ui.Screen, *ui.Node) {
		s := &ui.Screen{Platform: "ios", Width: 393, Height: 852, Keyboard: true}
		if kbWindow {
			s.Windows = append(s.Windows, &ui.Window{Type: "keyboard", Bounds: ui.Rect{T: 516, R: 393, B: 852}, Nodes: []*ui.Node{
				typed("Keyboard", 0, 0, 516, 393, 852), typed("Key", 1, 0, 520, 39, 562)}})
		}
		s.Windows = append(s.Windows, &ui.Window{Type: "app", Active: true, Focused: true, Bounds: ui.Rect{R: 393, B: 852}, Nodes: nodes})
		s.Link()
		return s, nodes[3]
	}
	sheet := typed("Other", 2, 0, 200, 393, 852)
	nodes := append(app(), sheet, typed("SearchField", 3, 16, 216, 377, 252),
		typed("Table", 3, 0, 360, 393, 852), typed("Cell", 4, 0, 360, 393, 460))
	s, el := ios(true, nodes)
	if c, modal := Covered(s, el); c == nil || !modal {
		t.Errorf("sheet with a search field, keyboard up: cover %v modal %v, want a modal layer", c, modal)
	}
	s, el = ios(false, nodes)
	if c, modal := Covered(s, el); c == nil || !modal {
		t.Errorf("sheet, keyboard up, no keyboard window: cover %v modal %v, want a modal layer", c, modal)
	}
	field := typed("SearchField", 3, 16, 446, 377, 482)
	field.Focused, field.Editable = true, true
	medium := append(app(), typed("Other", 2, 0, 430, 393, 852), field,
		typed("Table", 3, 0, 490, 393, 852), typed("Cell", 4, 0, 490, 393, 590))
	medium[3].Bounds = ui.Rect{T: 500, R: 393, B: 560}
	s, el = ios(false, medium)
	if c, modal := Covered(s, el); c == nil || !modal {
		t.Errorf("medium-detent sheet with a focused search field, no keyboard window: cover %v modal %v, want a modal layer", c, modal)
	}
	field = typed("SearchField", 3, 16, 506, 377, 540)
	field.Focused, field.Editable = true, true
	low := append(app(), typed("Other", 2, 0, 500, 393, 852), field,
		typed("Table", 3, 0, 540, 393, 852), typed("Cell", 4, 0, 540, 393, 640))
	low[3].Bounds = ui.Rect{T: 560, R: 393, B: 620}
	s, el = ios(true, low)
	if c, modal := Covered(s, el); c == nil || !modal {
		t.Errorf("sheet at the keyboard host's place with a focused search field: cover %v modal %v, want a modal layer", c, modal)
	}
}

func TestIOSKeyboardHostInTheAppWindowIsABar(t *testing.T) {
	typed := func(class string, depth, l, t, r, b int) *ui.Node {
		n := node(depth, l, t, r, b)
		n.Class = class
		return n
	}
	button := func(depth, l, t, r, b int) *ui.Node {
		n := typed("Button", depth, l, t, r, b)
		n.Clickable = true
		return n
	}
	submit := button(4, 20, 798, 98, 832)
	submit.ID, submit.Desc = "kb_submit", "Submit"
	bottom := typed("TextField", 4, 20, 748, 382, 782)
	bottom.ID, bottom.Text, bottom.Clickable = "kb_bottom", "low", true
	app := []*ui.Node{
		typed("Application", 0, 0, 0, 402, 874),
		typed("Window", 1, 0, 0, 402, 874),
		typed("Other", 2, 0, 0, 402, 874),
		typed("Other", 3, 0, 0, 402, 874),
		bottom, submit,
		typed("Window", 1, 0, 0, 402, 874),
		typed("Other", 2, 0, 0, 402, 874),
		typed("Other", 3, 0, 546, 402, 874),
		typed("Other", 4, 0, 546, 402, 590),
		button(5, 0, 546, 134, 590), button(5, 134, 546, 268, 590), button(5, 268, 546, 402, 590),
		typed("Other", 4, 0, 590, 402, 816),
		typed("Other", 4, 0, 590, 402, 874),
		button(5, 8, 806, 77, 874),
		button(5, 325, 805, 394, 874),
	}
	key := typed("Key", 2, 5, 597, 44, 651)
	key.Clickable = true
	s := &ui.Screen{Platform: "ios", Width: 402, Height: 874, Keyboard: true, Windows: []*ui.Window{
		{Type: "keyboard", Bounds: ui.Rect{T: 590, R: 402, B: 816}, Nodes: []*ui.Node{
			typed("Keyboard", 0, 0, 590, 402, 816), typed("Other", 1, 0, 590, 402, 816), key}},
		{Type: "app", Active: true, Focused: true, Bounds: ui.Rect{R: 402, B: 874}, Nodes: app},
	}}
	s.Link()
	if c, modal := Covered(s, submit); c == nil || modal {
		t.Errorf("Submit under the keyboard's host in the app's tree: cover %v modal %v, want a bar", c, modal)
	}
}

func TestCoveredMostOfItsArea(t *testing.T) {
	root := node(0, 0, 0, 1000, 2000)
	el := tappable(1, 100, 100, 500, 500)
	left := tappable(1, 0, 0, 250, 600)
	right := tappable(1, 350, 0, 600, 300)
	s := screen("android", []*ui.Node{root, el, left, right})
	if c, _ := Covered(s, el); c == nil {
		t.Error("three quarters covered, middle clear: not counted")
	}
	s = screen("android", []*ui.Node{root, el, left})
	if c, _ := Covered(s, el); c != nil {
		t.Errorf("half covered: counted (%v)", c)
	}
}
