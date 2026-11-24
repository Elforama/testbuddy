package match

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func drawerScreen(title bool) (s *ui.Screen, chip, drawerItem *ui.Node) {
	top := &ui.Node{Depth: 0, Bounds: rect(0, 0, 1080, 2400), Enabled: true}
	layout := &ui.Node{Depth: 1, Class: "androidx.drawerlayout.widget.DrawerLayout", Bounds: rect(0, 136, 1080, 2400), Enabled: true}
	content := &ui.Node{Depth: 2, Bounds: rect(0, 136, 1080, 2400), Enabled: true}
	chips := &ui.Node{Depth: 3, Class: "android.widget.HorizontalScrollView", Scrollable: true, Bounds: rect(0, 388, 1080, 514), Enabled: true}
	chip = &ui.Node{Depth: 4, Class: "android.widget.Button", Text: "Images", Clickable: true, Bounds: rect(64, 388, 319, 514), Enabled: true}
	audio := &ui.Node{Depth: 4, Class: "android.widget.Button", Text: "Audio", Clickable: true, Bounds: rect(341, 388, 571, 514), Enabled: true}
	drawer := &ui.Node{Depth: 2, Class: "android.widget.LinearLayout", ID: "drawer_roots", Bounds: rect(0, 136, 735, 2400), Enabled: true}
	recent := &ui.Node{Depth: 3, Class: "android.widget.LinearLayout", Clickable: true, Bounds: rect(0, 283, 735, 430), Enabled: true}
	recentTitle := &ui.Node{Depth: 4, Class: "android.widget.TextView", Text: "Recent", Bounds: rect(168, 331, 714, 382), Enabled: true}
	row := &ui.Node{Depth: 3, Class: "android.widget.LinearLayout", Clickable: true, Bounds: rect(0, 430, 735, 577), Enabled: true}
	nodes := []*ui.Node{top, layout, content, chips, chip, audio, drawer, recent, recentTitle, row}
	drawerItem = row
	if title {
		drawerItem = &ui.Node{Depth: 4, Class: "android.widget.TextView", Text: "Images", Bounds: rect(168, 478, 714, 529), Enabled: true}
		nodes = append(nodes, drawerItem)
	} else {
		row.Class, row.Text = "android.widget.Button", "Images"
	}
	s = &ui.Screen{Platform: "android", Width: 1080, Height: 2400, Windows: []*ui.Window{{Type: "app", Active: true, Bounds: rect(0, 0, 1080, 2400), Nodes: nodes}}}
	s.Link()
	return s, chip, drawerItem
}

func widen(s *ui.Screen, w int) *ui.Screen {
	for _, n := range s.Nodes {
		if n.Bounds.R == s.Width {
			n.Bounds.R = w
		}
	}
	s.Windows[0].Bounds.R = w
	s.Width = w
	return s
}

func TestFindPrefersTheMatchNotUnderAModalLayer(t *testing.T) {
	for _, tc := range []struct{ title, wide bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		title := tc.title
		s, chip, item := drawerScreen(title)
		if tc.wide {
			s = widen(s, 3000)
		}
		got, all, err := Find(s, &Selector{Text: "Images"})
		if err != nil {
			t.Fatal(err)
		}
		if got != item {
			t.Errorf("title=%v: picked %s (%s), want the drawer's %s; order %v", title, at(got), nodeName(got), at(item), names(all))
		}
		if len(all) != 2 || all[1] != chip {
			t.Errorf("title=%v: the covered chip should still match, last: %v", title, names(all))
		}
		if got := mustFind(t, s, &Selector{Text: "Images", Index: "0"}); got != chip {
			t.Errorf("title=%v: index 0 picked %s, want the chip (positional)", title, at(got))
		}
		if got := mustFind(t, s, &Selector{Text: "Audio"}); got == nil || got.Text != "Audio" {
			t.Errorf("title=%v: the lone covered chip Audio: %v", title, nodeName(got))
		}
	}
}

func TestChildOfPicksTheReachableParent(t *testing.T) {
	s, chip, item := drawerScreen(false)
	got := mustFind(t, s, &Selector{ChildOf: &Selector{Text: "Images"}})
	if got == nil || !got.IsDescendantOf(item) {
		t.Errorf("childOf Images: %s, want in the drawer row %s (not the chip %s)", at(got), at(item), at(chip))
	}
}

func TestFindRanksARowUnderABarAfterAClearOne(t *testing.T) {
	list := &ui.Node{Depth: 1, Class: "RecyclerView", Scrollable: true, Bounds: rect(0, 0, 1000, 2000), Enabled: true}
	header := &ui.Node{Depth: 2, Text: "Section B", Bounds: rect(0, 0, 1000, 150), Enabled: true}
	under := &ui.Node{Depth: 2, Text: "Item", Clickable: true, Bounds: rect(0, 40, 1000, 180), Enabled: true}
	clear := &ui.Node{Depth: 2, Text: "Item", Clickable: true, Bounds: rect(0, 600, 1000, 740), Enabled: true}
	s := mkScreen(root(), list, header, under, clear)
	if got := mustFind(t, s, &Selector{Text: "Item"}); got != clear {
		t.Errorf("picked %s, want the row clear of the header", at(got))
	}
	label := &ui.Node{Depth: 2, Text: "Item", Bounds: rect(0, 600, 1000, 740), Enabled: true}
	s = mkScreen(root(), list, header, under, label)
	if got := mustFind(t, s, &Selector{Text: "Item"}); got != under {
		t.Errorf("picked %s, want the clickable row under the bar", at(got))
	}
	edge := &ui.Node{Depth: 2, Text: "Item", Clickable: true, Bounds: rect(0, 120, 1000, 400), Enabled: true}
	s = mkScreen(root(), list, header, edge, clear)
	if got := mustFind(t, s, &Selector{Text: "Item"}); got != edge {
		t.Errorf("picked %s, want the first row: the header only overlaps its edge", at(got))
	}
}

func TestFindRanksAMatchUnderAnotherWindowLast(t *testing.T) {
	appOK := &ui.Node{Depth: 0, Text: "OK", Clickable: true, Bounds: rect(400, 1000, 600, 1100), Enabled: true}
	dialog := &ui.Node{Depth: 0, Bounds: rect(100, 700, 900, 1300), Enabled: true}
	dialogOK := &ui.Node{Depth: 1, Text: "OK", Bounds: rect(700, 1200, 850, 1280), Enabled: true}
	s := &ui.Screen{Platform: "android", Width: 1000, Height: 2000, Windows: []*ui.Window{
		{Type: "app", Focused: true, Bounds: rect(100, 700, 900, 1300), Nodes: []*ui.Node{dialog, dialogOK}},
		{Type: "app", Active: true, Bounds: rect(0, 0, 1000, 2000), Nodes: []*ui.Node{appOK}},
	}}
	s.Link()
	if got := mustFind(t, s, &Selector{Text: "OK"}); got != dialogOK {
		t.Errorf("picked %s, want the dialog's OK", at(got))
	}
}
