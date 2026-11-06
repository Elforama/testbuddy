package engine

import (
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
	"testing"
	"time"
)

// A prior lookup saw Target, but Shift pushes it out before its own tap lookup starts.
func TestTapRecoversPreviouslySeenTarget(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		navigated, removed, replacement bool
	}{
		{name: "layout shift"}, {name: "navigation", navigated: true}, {name: "removed", removed: true}, {name: "replacement", replacement: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shifted, scrolled := false, false
			f := &fakeDriver{}
			f.screen = func() []*ui.Node {
				title := "Animations"
				if shifted && tc.navigated {
					title = "Other screen"
				}
				nodes := []*ui.Node{
					{ID: "title", Text: title, Bounds: ui.Rect{R: 400, B: 50}},
					{Class: "ScrollView", ID: "list", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
					{Depth: 1, Class: "Button", ID: "shift", Text: "Shift", Clickable: true, Enabled: true, Bounds: ui.Rect{T: 100, R: 400, B: 150}},
				}
				if !shifted || (scrolled && !tc.removed) {
					top := 650
					label := "Target"
					if shifted {
						top = 450
					}
					if shifted && tc.replacement {
						label = "Different action"
					}
					nodes = append(nodes, &ui.Node{Depth: 1, Class: "Button", ID: "anim_target", Text: label, Clickable: true, Enabled: true, Bounds: ui.Rect{T: top, R: 400, B: top + 50}})
				}
				if shifted {
					nodes = append(nodes, &ui.Node{Depth: 1, Text: "Banner", Bounds: ui.Rect{T: 600, R: 400, B: 800}})
				}
				return nodes
			}
			f.onTap = func(x, y int) {
				if y < 200 {
					shifted = true
					f.change(true)
				}
			}
			f.onSwipe = func(x1, y1, x2, y2 int) { scrolled = true; f.change(true) }
			res := runWith(t, f, Options{LookupTimeout: 600 * time.Millisecond}, "- tapOn: {id: shift}\n- tapOn: {id: anim_target}")
			if tc.navigated || tc.removed || tc.replacement {
				mustFail(t, res, "anim_target")
				if len(f.taps) != 1 {
					t.Fatalf("tapped missing/replaced target: %v", f.taps)
				}
				if tc.navigated && len(f.swipeAt) != 0 {
					t.Fatal("scrolled using history from another screen")
				}
			} else {
				mustPass(t, res)
				if len(f.taps) != 2 || len(f.swipeAt) == 0 || f.taps[1][1] != 475 {
					t.Fatalf("want recovery and tap at current position; taps=%v swipes=%v", f.taps, f.swipeAt)
				}
			}
		})
	}
}

func TestScreenHistoryIsOwnedBoundedAndExpires(t *testing.T) {
	s := &ui.Screen{Width: 400, Height: 800, Windows: []*ui.Window{{Type: "app", Package: "app", Nodes: []*ui.Node{{ID: "title", Text: "Screen", Bounds: ui.Rect{R: 400, B: 50}}, {ID: "list", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}}, {Depth: 1, ID: "target", Text: "Target", Bounds: ui.Rect{T: 600, R: 400, B: 650}}}}}}
	s.Link()
	r := New(&fakeDriver{}, Options{})
	r.rememberScreen(s)
	s.Nodes[2].Text = "Changed"
	if r.history[0].screen.Nodes[2].Text != "Target" {
		t.Fatal("driver mutation altered snapshot")
	}
	for i := 0; i < 40; i++ {
		s.Nodes[2].Bounds.T = i
		r.rememberScreen(s)
	}
	if len(r.history) != historyLimit {
		t.Fatalf("history length %d", len(r.history))
	}
	for i := range r.history {
		r.history[i].at = time.Now().Add(-historyLifetime - time.Second)
	}
	r.rememberScreen(s)
	if len(r.history) != 1 {
		t.Fatalf("expired history retained: %d", len(r.history))
	}
	s.Nodes[0].Text = "Other screen"
	r.rememberScreen(s)
	if len(r.history) != 1 {
		t.Fatal("navigation retained previous screen")
	}
	s.Windows[0].ID = 123
	r.rememberScreen(s)
	if len(r.history) != 1 {
		t.Fatal("window change retained previous screen")
	}
	r.forgetHistory()
	if len(r.history) != 0 {
		t.Fatal("explicit reset retained history")
	}
}

func TestHistoryDoesNotGuess(t *testing.T) {
	for _, name := range []string{"unseen", "expired", "multiple lists", "ambiguous target", "indexed", "window changed"} {
		t.Run(name, func(t *testing.T) {
			s := &ui.Screen{Width: 400, Height: 800, Windows: []*ui.Window{{Type: "app", Package: "app", Nodes: []*ui.Node{
				{ID: "title", Text: "Screen", Bounds: ui.Rect{R: 400, B: 50}},
				{ID: "list", Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
				{Depth: 1, ID: "target", Class: "Button", Clickable: true, Bounds: ui.Rect{T: 600, R: 400, B: 650}},
			}}}}
			if name == "ambiguous target" {
				n := *s.Windows[0].Nodes[2]
				s.Windows[0].Nodes = append(s.Windows[0].Nodes, &n)
			}
			s.Link()
			r := New(&fakeDriver{}, Options{})
			r.rememberScreen(s)
			now := copyScreen(s)
			now.Windows[0].Nodes = now.Windows[0].Nodes[:2]
			sel := &match.Selector{ID: "target"}
			switch name {
			case "unseen":
				sel.ID = "never-seen"
			case "expired":
				r.history[0].at = time.Now().Add(-historyLifetime - time.Second)
			case "multiple lists":
				now.Windows[0].Nodes = append(now.Windows[0].Nodes, &ui.Node{ID: "other", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}})
			case "indexed":
				sel.Index = "0"
			case "window changed":
				now.Windows[0].ID = 2
			}
			now.Link()
			if n, _, _ := r.previouslySeen(sel, now); n != nil {
				t.Fatalf("used unsafe history for %s", name)
			}
		})
	}
}

func TestHistoryIgnoresSystemBarTextChanges(t *testing.T) {
	s := &ui.Screen{Width: 400, Height: 800, Windows: []*ui.Window{
		{Type: "system", Package: "systemui", Nodes: []*ui.Node{{ID: "clock", Text: "10:00", Bounds: ui.Rect{R: 100, B: 20}}}},
		{Type: "app", Package: "app", Active: true, Nodes: []*ui.Node{{ID: "title", Text: "Screen", Bounds: ui.Rect{T: 20, R: 400, B: 50}}}},
	}}
	s.Link()
	r := New(&fakeDriver{}, Options{})
	r.rememberScreen(s)
	s.Windows[0].Nodes[0].Text = "10:01"
	s.Link()
	r.rememberScreen(s)
	if len(r.history) != 2 {
		t.Fatal("system clock change discarded app history")
	}
}
