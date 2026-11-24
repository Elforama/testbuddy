package occlusion

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func stackScreen(known bool) (s *ui.Screen, back, nav *ui.Node) {
	order := func(n *ui.Node, o int) *ui.Node {
		n.DrawOrder, n.DrawOrderKnown = o, known
		return n
	}
	shown := func(n *ui.Node) *ui.Node { n.Visible = true; return n }
	root := shown(node(0, 0, 0, 1000, 2000))
	top := shown(order(node(1, 0, 0, 1000, 2000), 2))
	topHeader := shown(node(2, 0, 0, 1000, 150))
	topBody := shown(node(2, 0, 150, 1000, 2000))
	back = shown(tappable(3, 60, 1350, 940, 1450))
	back.Text = "Back to home"
	prev := shown(order(node(1, 0, 0, 770, 2000), 1))
	prevHeader := shown(node(2, 0, 0, 770, 150))
	prevBody := shown(node(2, 0, 150, 770, 2000))
	nav = shown(tappable(3, 0, 1320, 700, 1420))
	nav.Text = "Navigate to Details"
	s = screen("android", []*ui.Node{root, top, topHeader, topBody, back, prev, prevHeader, prevBody, nav})
	return s, back, nav
}

func TestCoverDrawnUnderIsNoCover(t *testing.T) {
	s, back, _ := stackScreen(true)
	if c, modal := Covered(s, back); c != nil {
		t.Errorf("Covered(Back to home) = %v (modal %v), want nil: the previous screen is drawn under it", c, modal)
	}
}

func TestCoverWithoutDrawingOrderKeepsTreeOrder(t *testing.T) {
	s, back, nav := stackScreen(false)
	if c, _ := Covered(s, back); c != nav {
		t.Errorf("Covered(Back to home) = %v, want the later-listed button %v", c, nav)
	}
}
