package match

import (
	"errors"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func TestFindAnyElement(t *testing.T) {
	a := leaf(1, "A", rect(0, 0, 100, 100))
	b := leaf(1, "B", rect(0, 100, 100, 200))
	b.Clickable = true
	c := leaf(1, "C", rect(0, 200, 100, 300))
	c.Clickable = true
	s := mkScreen(root(), a, b, c)
	got, all, err := Find(s, &Selector{})
	if err != nil || got != b {
		t.Fatalf("got %s, %v; want B", nodeName(got), err)
	}
	if len(all) != 4 || all[0] != b || all[1] != c {
		t.Errorf("pick order = %v, want clickable first", names(all))
	}

	s = mkScreen(root(), leaf(1, "plain", rect(0, 0, 10, 10)))
	if got := mustFind(t, s, &Selector{}); got != s.Nodes[0] {
		t.Errorf("no clickable: got %s, want the first node", nodeName(got))
	}
}

func TestFindDeepestMatch(t *testing.T) {
	card := &ui.Node{Depth: 1, ID: "card_row", Desc: "Settings", Bounds: rect(0, 0, 1000, 300), Clickable: true, Enabled: true}
	inner := &ui.Node{Depth: 2, Bounds: rect(0, 0, 1000, 300), Enabled: true}
	label := &ui.Node{Depth: 3, ID: "title_row", Text: "Settings", Bounds: rect(10, 10, 500, 100), Enabled: true}
	other := &ui.Node{Depth: 1, ID: "footer_row", Text: "Other", Bounds: rect(0, 500, 1000, 600), Enabled: true}
	s := mkScreen(root(), card, inner, label, other)

	got, all, err := Find(s, &Selector{Text: "Settings"})
	if err != nil || got != label {
		t.Fatalf("text: got %s, want the inner label (deepest)", nodeName(got))
	}
	if len(all) != 1 {
		t.Errorf("text matches = %v, want only the deepest", names(all))
	}

	_, all, _ = Find(s, &Selector{ID: ".*_row"})
	if want := []string{"Settings", "Other"}; !sameNames(names(all), want) {
		t.Errorf("id matches = %v, want %v (card dropped: its label matches too)", names(all), want)
	}
}

func TestFindClickableFirstIsStable(t *testing.T) {
	plain1 := leaf(1, "Save", rect(0, 0, 100, 100))
	click1 := leaf(1, "Save", rect(0, 100, 100, 200))
	click1.Clickable = true
	plain2 := leaf(1, "Save", rect(0, 200, 100, 300))
	click2 := leaf(1, "Save", rect(0, 300, 100, 400))
	click2.Clickable = true
	s := mkScreen(root(), plain1, click1, plain2, click2)
	_, all, _ := Find(s, &Selector{Text: "Save"})
	want := []*ui.Node{click1, click2, plain1, plain2}
	for i := range want {
		if all[i] != want[i] {
			t.Fatalf("pick order position %d = %v, want click1, click2, plain1, plain2", i, all[i].Bounds)
		}
	}
}

func TestFindIndexPositionOrder(t *testing.T) {
	bottom := leaf(1, "Item", rect(0, 900, 100, 1000))
	topRight := leaf(1, "Item", rect(500, 100, 600, 200))
	topLeft := leaf(1, "Item", rect(0, 100, 100, 200))
	middle := leaf(1, "Item", rect(0, 500, 100, 600))
	bottom.Clickable = true
	s := mkScreen(root(), bottom, topRight, topLeft, middle)
	cases := []struct {
		index string
		want  *ui.Node
	}{
		{"0", topLeft},
		{"1", topRight},
		{"2", middle},
		{"3", bottom},
		{"4", nil},
		{"100", nil},
		{"-1", bottom},
		{"-2", middle},
		{"-4", topLeft},
		{"-5", nil},
		{"1.9", topRight},
		{" 2 ", middle},
		{"2.0", middle},
		{"-0", topLeft},
		{"1e0", topRight},
	}
	for _, c := range cases {
		t.Run(c.index, func(t *testing.T) {
			got, _, err := Find(s, &Selector{Text: "Item", Index: c.index})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != c.want {
				t.Errorf("index %q = %s, want %s", c.index, nodeName(got), nodeName(c.want))
			}
		})
	}
}

func TestFindInvalidIndex(t *testing.T) {
	s := mkScreen(root(), leaf(1, "A", rect(0, 0, 100, 100)), leaf(1, "B", rect(0, 200, 100, 300)))
	for _, bad := range []string{"abc", "one", "NaN", "Inf", "-Inf", "+Inf", "1,5", "#1"} {
		for name, sel := range map[string]*Selector{
			"own":      {Text: "A", Index: bad},
			"childOf":  {Text: "A", ChildOf: &Selector{Text: "B", Index: bad}},
			"below":    {Text: "B", Below: &Selector{Text: "A", Index: bad}},
			"nested":   {ChildOf: &Selector{Text: "A", ChildOf: &Selector{Index: bad}}},
			"rightOf":  {RightOf: &Selector{Index: bad}},
			"anchored": {Above: &Selector{Text: "B", Below: &Selector{Index: bad}}},
		} {
			t.Run(bad+"/"+name, func(t *testing.T) {
				got, _, err := Find(s, sel)
				var inv *InvalidIndex
				if !errors.As(err, &inv) {
					t.Fatalf("err = %v, want *InvalidIndex", err)
				}
				if inv.Value != bad || got != nil {
					t.Errorf("Value = %q, got %s", inv.Value, nodeName(got))
				}
			})
		}
	}
	if msg := (&InvalidIndex{Value: "abc"}).Error(); msg != `invalid index "abc"` {
		t.Errorf("Error() = %q", msg)
	}
}

func TestFindIndexZeroBoundsLast(t *testing.T) {
	ghost := &ui.Node{Depth: 1, Text: "Item", Enabled: true}
	child := leaf(2, "child", rect(0, 0, 100, 100))
	real := leaf(1, "Item", rect(0, 500, 100, 600))
	s := mkScreen(root(), ghost, child, real)

	if got := mustFind(t, s, &Selector{Text: "Item", Index: "0"}); got != real {
		t.Errorf("index 0 = %s, want the node with bounds", nodeName(got))
	}
	got, all, err := Find(s, &Selector{Text: "Item", Index: "1"})
	if err != nil || got != nil || len(all) != 1 || all[0] != ghost {
		t.Errorf("index 1 = %s, %v, %v; want nil with the ghost as the candidate", nodeName(got), names(all), err)
	}
	got, all, _ = Find(s, &Selector{Text: "Item"})
	if got != nil || len(all) != 2 {
		t.Errorf("no index = %s, %v; want nil (zero-size first pick)", nodeName(got), names(all))
	}
}

func TestFindZeroSizeFirstPick(t *testing.T) {
	dot := &ui.Node{Depth: 1, Text: "Dot", Bounds: rect(50, 50, 50, 50), Enabled: true}
	s := mkScreen(root(), dot, leaf(2, "kid", rect(0, 0, 100, 100)))
	got, all, err := Find(s, &Selector{Text: "Dot"})
	if got != nil || err != nil || len(all) != 1 {
		t.Errorf("got %s, %v, %v", nodeName(got), names(all), err)
	}
	line := &ui.Node{Depth: 1, Text: "Divider", Bounds: rect(0, 500, 1000, 500), Enabled: true}
	s = mkScreen(root(), line)
	if got := mustFind(t, s, &Selector{Text: "Divider"}); got != line {
		t.Errorf("zero-height line not found")
	}
}

func TestFindStateFilters(t *testing.T) {
	on := &ui.Node{Depth: 1, Text: "Opt", Bounds: rect(0, 0, 100, 100), Enabled: true, Selected: true, Checked: true, Focused: true}
	off := &ui.Node{Depth: 1, Text: "Opt", Bounds: rect(0, 200, 100, 300)}
	s := mkScreen(root(), on, off)
	cases := []struct {
		name string
		sel  Selector
		want *ui.Node
	}{
		{"enabled true", Selector{Text: "Opt", Enabled: ptr(true)}, on},
		{"enabled false", Selector{Text: "Opt", Enabled: ptr(false)}, off},
		{"selected true", Selector{Text: "Opt", Selected: ptr(true)}, on},
		{"selected false", Selector{Text: "Opt", Selected: ptr(false)}, off},
		{"checked true", Selector{Text: "Opt", Checked: ptr(true)}, on},
		{"checked false", Selector{Text: "Opt", Checked: ptr(false)}, off},
		{"focused true", Selector{Text: "Opt", Focused: ptr(true)}, on},
		{"focused false", Selector{Text: "Opt", Focused: ptr(false)}, off},
		{"combined mismatch", Selector{Text: "Opt", Enabled: ptr(true), Checked: ptr(false)}, nil},
		{"state alone", Selector{Checked: ptr(true)}, on},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustFind(t, s, &c.sel); got != c.want {
				t.Errorf("got %s, want %s", nodeName(got), nodeName(c.want))
			}
		})
	}
}

func TestFindSizeFilters(t *testing.T) {
	small := &ui.Node{Depth: 1, ID: "small", Bounds: rect(0, 0, 100, 50), Enabled: true}
	big := &ui.Node{Depth: 1, ID: "big", Bounds: rect(0, 100, 400, 300), Enabled: true}
	s := mkScreen(root(), small, big)
	cases := []struct {
		name string
		sel  Selector
		want *ui.Node
	}{
		{"exact width", Selector{Width: ptr(100)}, small},
		{"exact height", Selector{Height: ptr(200)}, big},
		{"both", Selector{Width: ptr(400), Height: ptr(200)}, big},
		{"off by one, no tolerance", Selector{Width: ptr(101)}, nil},
		{"within tolerance", Selector{Width: ptr(105), Tolerance: ptr(5)}, small},
		{"just outside tolerance", Selector{Width: ptr(106), Tolerance: ptr(5)}, nil},
		{"tolerance below", Selector{Height: ptr(45), Tolerance: ptr(5)}, small},
		{"width matches, height does not", Selector{Width: ptr(100), Height: ptr(200)}, nil},
		{"tolerance alone filters nothing", Selector{ID: "big", Tolerance: ptr(1)}, big},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustFind(t, s, &c.sel); got != c.want {
				t.Errorf("got %s, want %s", nodeName(got), nodeName(c.want))
			}
		})
	}
}

func TestTraitsMatch(t *testing.T) {
	long := strings.Repeat("é", 201)
	cases := []struct {
		name   string
		traits []string
		n      ui.Node
		want   bool
	}{
		{"no traits", nil, ui.Node{}, true},
		{"text: text", []string{"text"}, ui.Node{Text: "x"}, true},
		{"text: desc", []string{"text"}, ui.Node{Desc: "x"}, true},
		{"text: hint does not count", []string{"text"}, ui.Node{Hint: "x"}, false},
		{"text: none", []string{"text"}, ui.Node{}, false},
		{"long-text: 201 runes", []string{"long-text"}, ui.Node{Text: long}, true},
		{"long-text: 200 runes", []string{"long-text"}, ui.Node{Text: strings.Repeat("a", 200)}, false},
		{"long-text: counts runes not bytes", []string{"long-text"}, ui.Node{Text: strings.Repeat("é", 150)}, false},
		{"square: exact", []string{"square"}, ui.Node{Bounds: rect(0, 0, 100, 100)}, true},
		{"square: 2.9% off", []string{"square"}, ui.Node{Bounds: rect(0, 0, 100, 103)}, true},
		{"square: 3.8% off", []string{"square"}, ui.Node{Bounds: rect(0, 0, 100, 104)}, false},
		{"square: wide", []string{"square"}, ui.Node{Bounds: rect(0, 0, 200, 100)}, false},
		{"square: no height", []string{"square"}, ui.Node{Bounds: rect(0, 0, 100, 0)}, false},
		{"unknown trait ignored", []string{"sparkly"}, ui.Node{}, true},
		{"all traits required", []string{"text", "square"}, ui.Node{Text: "x", Bounds: rect(0, 0, 10, 20)}, false},
		{"all traits satisfied", []string{"text", "square"}, ui.Node{Text: "x", Bounds: rect(0, 0, 20, 20)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := traitsMatch(c.traits, &c.n); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestFindByTraits(t *testing.T) {
	icon := &ui.Node{Depth: 1, ID: "icon", Bounds: rect(0, 0, 100, 100), Enabled: true}
	banner := &ui.Node{Depth: 1, ID: "banner", Text: "Hi", Bounds: rect(0, 200, 1000, 300), Enabled: true}
	s := mkScreen(root(), icon, banner)
	if got := mustFind(t, s, &Selector{Traits: []string{"square"}}); got != icon {
		t.Errorf("square = %s", nodeName(got))
	}
	if got := mustFind(t, s, &Selector{Traits: []string{"text"}}); got != banner {
		t.Errorf("text = %s", nodeName(got))
	}
}

func TestFindCSS(t *testing.T) {
	a := &ui.Node{Depth: 1, Class: "button", Text: "Buy", Bounds: rect(0, 0, 100, 100), Enabled: true}
	b := &ui.Node{Depth: 1, Class: "button", Text: "Sell", Bounds: rect(0, 200, 100, 300), Enabled: true}
	s := &ui.Screen{Platform: "web", Width: 1000, Height: 2000, Windows: []*ui.Window{{Type: "app", Nodes: []*ui.Node{root(), a, b}}}}
	s.Link()

	if got := mustFind(t, s, &Selector{CSS: "button.sell", CSSNodes: map[int]bool{b.Index: true}}); got != b {
		t.Errorf("css = %s, want Sell", nodeName(got))
	}
	if got := mustFind(t, s, &Selector{CSS: "button", CSSNodes: map[int]bool{a.Index: true, b.Index: true}, Text: "Sell"}); got != b {
		t.Errorf("css+text = %s, want Sell", nodeName(got))
	}
	if got := mustFind(t, s, &Selector{CSS: "button"}); got != nil {
		t.Errorf("css with no results = %s, want nil", nodeName(got))
	}
	if got := mustFind(t, s, &Selector{CSS: "button", CSSNodes: map[int]bool{}}); got != nil {
		t.Errorf("css with empty results = %s, want nil", nodeName(got))
	}
	if got := mustFind(t, s, &Selector{CSS: "button", CSSNodes: map[int]bool{a.Index: true}, Text: "Sell"}); got != nil {
		t.Errorf("css and text disagree = %s, want nil", nodeName(got))
	}
}

func grid() (*ui.Screen, map[string]*ui.Node) {
	m := map[string]*ui.Node{
		"TL": leaf(1, "TL", rect(100, 100, 300, 300)),
		"TR": leaf(1, "TR", rect(600, 100, 800, 300)),
		"BL": leaf(1, "BL", rect(100, 600, 300, 800)),
		"BR": leaf(1, "BR", rect(600, 600, 800, 800)),
	}
	return mkScreen(root(), m["TL"], m["TR"], m["BL"], m["BR"]), m
}

func TestFindRelativePosition(t *testing.T) {
	s, m := grid()
	cell := &Selector{Text: "[TB][LR]"}
	cases := []struct {
		name string
		sel  Selector
		want []string
	}{
		{"below TL", Selector{Text: cell.Text, Below: &Selector{Text: "TL"}}, []string{"BL", "BR"}},
		{"above BR", Selector{Text: cell.Text, Above: &Selector{Text: "BR"}}, []string{"TL", "TR"}},
		{"rightOf TL", Selector{Text: cell.Text, RightOf: &Selector{Text: "TL"}}, []string{"TR", "BR"}},
		{"leftOf BR", Selector{Text: cell.Text, LeftOf: &Selector{Text: "BR"}}, []string{"TL", "BL"}},
		{"below and rightOf", Selector{Text: cell.Text, Below: &Selector{Text: "TL"}, RightOf: &Selector{Text: "BL"}}, []string{"BR"}},
		{"same row is not below", Selector{Text: "TR", Below: &Selector{Text: "TL"}}, nil},
		{"same column is not rightOf", Selector{Text: "BL", RightOf: &Selector{Text: "TL"}}, nil},
		{"nothing above the top row", Selector{Text: cell.Text, Above: &Selector{Text: "TL"}}, nil},
		{"anchor missing", Selector{Text: cell.Text, Below: &Selector{Text: "Nope"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, all, err := Find(s, &c.sel)
			if err != nil {
				t.Fatal(err)
			}
			if !sameNames(names(all), c.want) {
				t.Errorf("matches = %v, want %v", names(all), c.want)
			}
			if len(c.want) > 0 && got != m[c.want[0]] {
				t.Errorf("got %s, want %s (hierarchy order)", nodeName(got), c.want[0])
			}
		})
	}
}

func TestFindRelativeAnyAnchorButSelf(t *testing.T) {
	a1 := leaf(1, "A1", rect(0, 100, 100, 200))
	a2 := leaf(1, "A2", rect(0, 200, 100, 300))
	a3 := leaf(1, "A3", rect(0, 300, 100, 400))
	s := mkScreen(root(), a1, a2, a3)
	_, all, _ := Find(s, &Selector{Text: "A.", Below: &Selector{Text: "A."}})
	if want := []string{"A2", "A3"}; !sameNames(names(all), want) {
		t.Errorf("matches = %v, want %v", names(all), want)
	}
	_, all, _ = Find(s, &Selector{Text: "A2", Below: &Selector{Text: "A2"}})
	if len(all) != 0 {
		t.Errorf("a node is below itself: %v", names(all))
	}
}

func TestFindRelativeIndexedAnchor(t *testing.T) {
	rows := []*ui.Node{
		leaf(1, "Row", rect(0, 100, 100, 150)),
		leaf(1, "X", rect(0, 160, 100, 190)),
		leaf(1, "Row", rect(0, 200, 100, 250)),
		leaf(1, "X", rect(0, 260, 100, 290)),
		leaf(1, "Row", rect(0, 300, 100, 350)),
		leaf(1, "X", rect(0, 360, 100, 390)),
	}
	s := mkScreen(append([]*ui.Node{root()}, rows...)...)
	cases := []struct {
		index string
		want  *ui.Node
	}{
		{"", rows[1]},
		{"0", rows[1]},
		{"1", rows[3]},
		{"2", rows[5]},
		{"-1", rows[5]},
		{"3", nil},
	}
	for _, c := range cases {
		t.Run("index="+c.index, func(t *testing.T) {
			got := mustFind(t, s, &Selector{Text: "X", Below: &Selector{Text: "Row", Index: c.index}})
			if got != c.want {
				t.Errorf("got %s, want %s", at(got), at(c.want))
			}
		})
	}
}

func TestFindRelativeNested(t *testing.T) {
	s, m := grid()
	got := mustFind(t, s, &Selector{
		Text:   "[TB][LR]",
		Below:  &Selector{Text: "[TB][LR]", RightOf: &Selector{Text: "TL"}, Above: &Selector{Text: "BR"}},
		LeftOf: &Selector{Text: "BR"},
	})
	if got != m["BL"] {
		t.Errorf("got %s, want BL", nodeName(got))
	}
}

func TestFindRelativeOnly(t *testing.T) {
	s, m := grid()
	m["BR"].Clickable = true
	got, all, _ := Find(s, &Selector{Below: &Selector{Text: "TR"}})
	if got != m["BR"] {
		t.Errorf("got %s, want the clickable BR", nodeName(got))
	}
	if !sameNames(names(all), []string{"BR", "BL"}) {
		t.Errorf("matches = %v", names(all))
	}
}

func cards() (*ui.Screen, map[string]*ui.Node) {
	m := map[string]*ui.Node{
		"card1":  {Depth: 1, ID: "card1", Bounds: rect(0, 0, 1000, 400), Enabled: true},
		"title1": leaf(2, "Title", rect(10, 10, 500, 100)),
		"buy1":   leaf(2, "Buy", rect(10, 200, 500, 300)),
		"card2":  {Depth: 1, ID: "card2", Bounds: rect(0, 500, 1000, 900), Enabled: true},
		"title2": leaf(2, "Title", rect(10, 510, 500, 600)),
		"wrap2":  {Depth: 2, Bounds: rect(0, 650, 1000, 850), Enabled: true},
		"buy2":   leaf(3, "Buy", rect(10, 700, 500, 800)),
		"loose":  leaf(1, "Buy", rect(10, 1000, 500, 1100)),
	}
	m["buy1"].Clickable = true
	m["buy2"].Clickable = true
	s := mkScreen(root(), m["card1"], m["title1"], m["buy1"], m["card2"], m["title2"], m["wrap2"], m["buy2"], m["loose"])
	return s, m
}

func TestFindChildOf(t *testing.T) {
	s, m := cards()
	cases := []struct {
		name string
		sel  Selector
		want *ui.Node
	}{
		{"first card", Selector{Text: "Buy", ChildOf: &Selector{ID: "card1"}}, m["buy1"]},
		{"second card, any depth", Selector{Text: "Buy", ChildOf: &Selector{ID: "card2"}}, m["buy2"]},
		{"parent missing", Selector{Text: "Buy", ChildOf: &Selector{ID: "card3"}}, nil},
		{"no match inside", Selector{Text: "Sell", ChildOf: &Selector{ID: "card1"}}, nil},
		{"parent by regex picks first", Selector{Text: "Buy", ChildOf: &Selector{ID: "card."}}, m["buy1"]},
		{"parent by index", Selector{Text: "Buy", ChildOf: &Selector{ID: "card.", Index: "1"}}, m["buy2"]},
		{"nested childOf", Selector{Text: "Buy", ChildOf: &Selector{ChildOf: &Selector{ID: "card2"}, Index: "-1"}}, m["buy2"]},
		{"nested childOf parent missing", Selector{Text: "Buy", ChildOf: &Selector{ChildOf: &Selector{ID: "none"}}}, nil},
		{"index over every Buy reaches the loose one", Selector{Text: "Buy", Index: "2"}, m["loose"]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustFind(t, s, &c.sel); got != c.want {
				t.Errorf("got %s, want %s", nodeName(got), nodeName(c.want))
			}
		})
	}
}

func TestFindChildOfIncludesParent(t *testing.T) {
	s, m := cards()
	if got := mustFind(t, s, &Selector{ID: "card2", ChildOf: &Selector{ID: "card2"}}); got != m["card2"] {
		t.Errorf("got %s, want card2", nodeName(got))
	}
}

func TestFindContainsChild(t *testing.T) {
	s, m := cards()
	cases := []struct {
		name string
		sel  Selector
		want []string
	}{
		{"direct child", Selector{ContainsChild: &Selector{Text: "Title"}}, []string{"#card1", "#card2"}},
		{"grandchild does not count", Selector{ID: "card2", ContainsChild: &Selector{Text: "Buy"}}, nil},
		{"wrapper holds it directly", Selector{ContainsChild: &Selector{Text: "Buy"}}, []string{nodeName(s.Nodes[0]), "#card1", m["wrap2"].Bounds.String()}},
		{"with id", Selector{ID: "card1", ContainsChild: &Selector{Text: "Buy"}}, []string{"#card1"}},
		{"nothing contains it", Selector{ContainsChild: &Selector{Text: "Nope"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, all, err := Find(s, &c.sel)
			if err != nil {
				t.Fatal(err)
			}
			if !sameNames(names(all), c.want) {
				t.Errorf("matches = %v, want %v", names(all), c.want)
			}
		})
	}
}

func TestFindContainsDescendants(t *testing.T) {
	s, m := cards()
	cases := []struct {
		name string
		sel  Selector
		want *ui.Node
	}{
		{"one at depth", Selector{ID: "card.", ContainsDescendants: []*Selector{{Text: "Buy"}}}, m["card1"]},
		{"all required", Selector{ID: "card.", ContainsDescendants: []*Selector{{Text: "Title"}, {Text: "Buy"}}}, m["card1"]},
		{"grandchild found", Selector{ID: "card2", ContainsDescendants: []*Selector{{Text: "Buy"}}}, m["card2"]},
		{"one missing", Selector{ID: "card.", ContainsDescendants: []*Selector{{Text: "Title"}, {Text: "Sell"}}}, nil},
		{"self does not count", Selector{Text: "Title", ContainsDescendants: []*Selector{{Text: "Title"}}}, nil},
		{"nested relation", Selector{ID: "card.", ContainsDescendants: []*Selector{{Text: "Buy", Below: &Selector{Text: "Title", Index: "1"}}}}, m["card2"]},
		{"bare: the innermost container", Selector{ContainsDescendants: []*Selector{{Text: "Title"}, {Text: "Buy"}}}, m["card1"]},
		{"bare, deeper descendant", Selector{ContainsDescendants: []*Selector{{Text: "Buy", Below: &Selector{Text: "Title", Index: "1"}}}}, m["wrap2"]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustFind(t, s, &c.sel); got != c.want {
				t.Errorf("got %s, want %s", nodeName(got), nodeName(c.want))
			}
		})
	}
}

func TestFindContainsPropagatesErrors(t *testing.T) {
	s, _ := cards()
	for name, sel := range map[string]*Selector{
		"containsChild":       {ContainsChild: &Selector{Below: &Selector{Index: "x"}}},
		"containsDescendants": {ContainsDescendants: []*Selector{{Above: &Selector{Index: "x"}}}},
	} {
		if _, _, err := Find(s, sel); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestFindSkipsOffScreen(t *testing.T) {
	on := leaf(1, "Row", rect(0, 1900, 1000, 2000))
	off := leaf(1, "Row", rect(0, 2100, 1000, 2200))
	sliver := leaf(1, "Edge", rect(0, 1995, 1000, 2095))
	s := mkScreen(root(), on, off, sliver)
	_, all, _ := Find(s, &Selector{Text: "Row"})
	if len(all) != 1 || all[0] != on {
		t.Errorf("matches = %v, want only the on-screen row", names(all))
	}
	if got := mustFind(t, s, &Selector{Text: "Edge"}); got != nil {
		t.Errorf("a 5%%-visible element matched")
	}
}

func TestFindChildOfInsideRelativeAnchor(t *testing.T) {
	s, m := cards()
	got := mustFind(t, s, &Selector{Text: "Buy", Below: &Selector{Text: "Title", ChildOf: &Selector{ID: "card2"}}})
	if got != m["buy2"] {
		t.Errorf("got %s, want buy2 %s", at(got), at(m["buy2"]))
	}
}

func TestFindIndexInsideContainsChild(t *testing.T) {
	s, m := cards()
	got := mustFind(t, s, &Selector{ID: "card.", ContainsChild: &Selector{Text: "Title", Index: "1"}})
	if got != m["card2"] {
		t.Errorf("got %s, want card2", nodeName(got))
	}
	if _, _, err := Find(s, &Selector{ContainsChild: &Selector{Text: "Title", Index: "x"}}); err == nil {
		t.Error("invalid index in containsChild raised no error")
	}
}

func TestVisibleClipsToScrollingAncestors(t *testing.T) {
	header := leaf(1, "Header", rect(0, 0, 1000, 300))
	list := &ui.Node{Depth: 1, Bounds: rect(0, 300, 1000, 1500), Scrollable: true, Enabled: true}
	gone := leaf(2, "Row 1", rect(0, 150, 1000, 280))
	half := leaf(2, "Row 2", rect(0, 280, 1000, 420))
	shown := leaf(2, "Row 3", rect(0, 420, 1000, 560))
	below := leaf(2, "Row 9", rect(0, 1520, 1000, 1660))
	s := mkScreen(root(), header, list, gone, half, shown, below)
	vis := map[*ui.Node]bool{}
	for _, n := range Visible(s) {
		vis[n] = true
	}
	if vis[gone] || vis[below] || !vis[half] || !vis[shown] {
		t.Errorf("visible: gone %v below %v half %v shown %v", vis[gone], vis[below], vis[half], vis[shown])
	}
	if n, _, _ := Find(s, &Selector{Text: "Row 1"}); n != nil {
		t.Error("a row scrolled out of its list was found")
	}
	empty := &ui.Node{Depth: 1, Scrollable: true, Enabled: true}
	row := leaf(2, "Row", rect(0, 100, 1000, 200))
	mkScreen(root(), empty, row)
	if c := row.ClipRect(rect(0, 0, 1000, 2000)); c != rect(0, 0, 1000, 2000) {
		t.Errorf("clip under an empty scroller = %v", c)
	}
	if c := gone.ClipRect(rect(0, 0, 1000, 2000)); c != rect(0, 300, 1000, 1500) {
		t.Errorf("clip in the list = %v", c)
	}
}

func TestVisibleScrolledPage(t *testing.T) {
	body := &ui.Node{Depth: 0, Bounds: rect(0, -2050, 1000, -1337), Scrollable: true, Enabled: true}
	section := &ui.Node{Depth: 1, Bounds: rect(0, -2050, 1000, 713), Enabled: true}
	item := leaf(2, "Item 30", rect(0, 400, 1000, 450))
	s := mkScreen(body, section, item)
	if n := mustFind(t, s, &Selector{Text: "Item 30"}); n != item {
		t.Errorf("found %v on a scrolled page", n)
	}
}

func TestFindListedOffScreen(t *testing.T) {
	w := &ui.Window{Type: "app", Active: true, Bounds: ui.Rect{R: 400, B: 800}, Nodes: []*ui.Node{
		{Text: "Row 1", Bounds: ui.Rect{T: 100, R: 400, B: 150}},
		{Text: "Row 99", Bounds: ui.Rect{T: 5000, R: 400, B: 5050}},
	}}
	s := &ui.Screen{Platform: "android", Width: 400, Height: 800, Windows: []*ui.Window{w}}
	s.Link()
	sel := &Selector{Text: "Row 99"}
	if n, _, _ := Find(s, sel); n != nil {
		t.Errorf("Find found an off-screen row: %+v", n)
	}
	n, err := FindListed(s, sel)
	if err != nil || n == nil || n.Text != "Row 99" {
		t.Errorf("FindListed = %+v, %v", n, err)
	}
	if n, err := FindListed(s, &Selector{Text: "Row 7"}); n != nil || err != nil {
		t.Errorf("missing: %+v, %v", n, err)
	}
}
