package match

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #300, the web half of #282: a fixed drawer repeats "Images" from the list under its
// backdrop. The list's row comes first in the page, but the browser's hit test reports the
// backdrop over it (ui.Node.Covers): the drawer's item is the one a person can reach.
func TestFindOnTheWebPrefersTheMatchTheHitTestFindsClear(t *testing.T) {
	body := &ui.Node{Depth: 0, Class: "body", Bounds: rect(0, 0, 1000, 2000), Enabled: true}
	row := &ui.Node{Depth: 1, Class: "div", Text: "Images", Clickable: true, Bounds: rect(0, 300, 1000, 360), Enabled: true}
	backdrop := &ui.Node{Depth: 1, Class: "div", ID: "backdrop", Bounds: rect(0, 0, 1000, 2000), Enabled: true}
	drawer := &ui.Node{Depth: 1, Class: "nav", Bounds: rect(0, 0, 300, 2000), Enabled: true}
	item := &ui.Node{Depth: 2, Class: "div", Text: "Images", Clickable: true, Bounds: rect(0, 300, 300, 360), Enabled: true}
	s := &ui.Screen{Platform: "web", Width: 1000, Height: 2000, Windows: []*ui.Window{{Type: "app", Active: true, Focused: true,
		Bounds: rect(0, 0, 1000, 2000), Nodes: []*ui.Node{body, row, backdrop, drawer, item}}}}
	s.Link()
	row.Covers = []ui.Hit{{Point: 0, X: 500, Y: 330, By: backdrop, Layers: []ui.HitLayer{{Node: backdrop, Kind: "fixed"}}}}
	got, all, err := Find(s, &Selector{Text: "Images"})
	if err != nil {
		t.Fatal(err)
	}
	if got != item || len(all) != 2 || all[1] != row {
		t.Errorf("picked %s, order %v: want the drawer's item, then the row under the backdrop", at(got), names(all))
	}
	// A selector on the row alone still finds it (the runner decides what to do about the cover).
	if got := mustFind(t, s, &Selector{Text: "Images", Index: "0"}); got == nil {
		t.Error("index 0 found nothing")
	}
}
