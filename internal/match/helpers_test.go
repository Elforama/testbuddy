package match

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func rect(l, t, r, b int) ui.Rect { return ui.Rect{L: l, T: t, R: r, B: b} }

func ptr[T any](v T) *T { return &v }

func mkScreen(nodes ...*ui.Node) *ui.Screen {
	s := &ui.Screen{Platform: "android", Width: 1000, Height: 2000, Windows: []*ui.Window{{Type: "app", Active: true, Nodes: nodes}}}
	s.Link()
	return s
}

func leaf(depth int, text string, b ui.Rect) *ui.Node {
	return &ui.Node{Depth: depth, Text: text, Bounds: b, Enabled: true}
}

func root() *ui.Node { return &ui.Node{Depth: 0, Bounds: rect(0, 0, 1000, 2000), Enabled: true} }

func mustFind(t *testing.T, s *ui.Screen, sel *Selector) *ui.Node {
	t.Helper()
	got, _, err := Find(s, sel)
	if err != nil {
		t.Fatalf("Find(%s): %v", sel.Describe(), err)
	}
	return got
}

func nodeName(n *ui.Node) string {
	if n == nil {
		return "<nil>"
	}
	if n.Text != "" {
		return n.Text
	}
	if n.ID != "" {
		return "#" + n.ID
	}
	return n.Bounds.String()
}

func names(ns []*ui.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = nodeName(n)
	}
	return out
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func at(n *ui.Node) string {
	if n == nil {
		return "<nil>"
	}
	return n.Bounds.String()
}
