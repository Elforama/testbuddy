package engine

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #320: a list drawn under what hides it whole (ui.Node.Buried, the previous screen of a
// stack) is never the list a scroll picks: not the largest list on screen, not the list at a
// point, not a sideways list.
func TestScrollNeverPicksABuriedList(t *testing.T) {
	root := at("FrameLayout", 0, 0, 0, 400, 800)
	top := at("ViewGroup", 1, 0, 0, 400, 800)
	shown := at("RecyclerView", 2, 0, 400, 400, 800)
	shown.Scrollable = true
	prev := at("ViewGroup", 1, 0, 0, 400, 800)
	buried := at("RecyclerView", 2, 0, 0, 400, 800) // larger, and under the middle
	buried.Scrollable = true
	shelf := at("HorizontalScrollView", 2, 0, 100, 400, 200)
	shelf.Scrollable = true
	s := linked("android", root, top, shown, prev, buried, shelf)
	for _, n := range []*ui.Node{prev, buried, shelf} {
		n.Buried = true
	}
	if l := otherList(s, map[string]bool{}); l != shown {
		t.Errorf("otherList = %v, want the list on screen %v", l, shown)
	}
	if visibleVerticalList(buried, ui.Rect{R: 400, B: 800}) {
		t.Error("a buried list counts as a list on screen")
	}
	if c := scrollContainer(s, 200, 300); c != nil {
		t.Errorf("scrollContainer(200, 300) = %v, want none: only a buried list is there", c)
	}
	if l := sidewaysList(s, nil, 200, 400, ""); l != nil {
		t.Errorf("sideways list = %v, want none: the only one is buried", l)
	}
}
