package engine

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// AND-003: local result evidence must not use a newly inserted label, an
// ambiguous/reused ID, another window, or another interactive control.
func TestAdjacentTapResultRequiresExistingUnambiguousLabel(t *testing.T) {
	for _, kind := range []string{"changed", "unchanged", "new", "duplicate", "interactive", "other window", "not adjacent"} {
		t.Run(kind, func(t *testing.T) {
			screen := func(top int, text string) (*ui.Screen, *ui.Node) {
				n := btn("Tap", "action", top)
				n.Depth = 1
				s := &ui.Screen{Width: 400, Height: 800, Windows: []*ui.Window{{ID: 7, Type: "app", Package: "example", Nodes: []*ui.Node{
					{Class: "ViewGroup", Bounds: ui.Rect{R: 400, B: 800}}, n,
					{Depth: 1, ID: "result", Class: "TextView", Text: text, Bounds: ui.Rect{T: top + 60, R: 400, B: top + 100}},
				}}}}
				s.Link()
				return s, n
			}
			before, was := screen(100, "Count: 0")
			after, now := screen(300, "Count: 1")
			switch kind {
			case "unchanged":
				after.Nodes[2].Text = "Count: 0"
			case "new":
				before.Nodes[2].ID = "previous-label"
			case "duplicate":
				after.Windows[0].Nodes = append(after.Windows[0].Nodes, &ui.Node{Depth: 1, ID: "result", Text: "Other", Class: "TextView"})
				after.Link()
			case "interactive":
				after.Nodes[2].Clickable = true
			case "other window":
				after.Windows[0].ID++
			case "not adjacent":
				label := after.Nodes[2]
				after.Windows[0].Nodes = append(after.Windows[0].Nodes[:2], &ui.Node{Depth: 1, Class: "ViewGroup"}, label)
				after.Link()
			}
			if got := adjacentTapResultChanged(was, before, now, after); got != (kind == "changed") {
				t.Fatalf("changed result evidence = %v", got)
			}
		})
	}
}
