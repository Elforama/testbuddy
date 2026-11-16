package main

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #320: tb hierarchy shows every node, a buried one too (drawn under what hides it
// whole), flagged "buried" so the dump says it is not on screen for a person.
func TestHierarchyJSONFlagsBuried(t *testing.T) {
	root := &ui.Node{Class: "android.widget.FrameLayout", Bounds: ui.Rect{R: 400, B: 800}, Depth: 0}
	top := &ui.Node{Class: "android.view.ViewGroup", Bounds: ui.Rect{R: 400, B: 800}, Depth: 1}
	prev := &ui.Node{Class: "android.view.ViewGroup", Bounds: ui.Rect{R: 400, B: 800}, Depth: 1, Buried: true}
	text := &ui.Node{Class: "android.widget.TextView", Text: "Under", Bounds: ui.Rect{R: 400, B: 100}, Depth: 2, Buried: true}
	s := &ui.Screen{Platform: "android", Width: 400, Height: 800, Windows: []*ui.Window{
		{Type: "app", Package: "com.app", Bounds: ui.Rect{R: 400, B: 800}, Nodes: []*ui.Node{root, top, prev, text}},
	}}
	s.Link()
	r := hierarchyJSON(s).Children[0].Children[0]
	if _, ok := r.Attributes["buried"]; ok {
		t.Errorf("root flagged buried: %v", r.Attributes)
	}
	if _, ok := r.Children[0].Attributes["buried"]; ok {
		t.Errorf("top screen flagged buried: %v", r.Children[0].Attributes)
	}
	p := r.Children[1]
	if p.Attributes["buried"] != "true" || len(p.Children) != 1 || p.Children[0].Attributes["buried"] != "true" {
		t.Errorf("buried screen: %v, its text: %+v", p.Attributes, p.Children)
	}
}
