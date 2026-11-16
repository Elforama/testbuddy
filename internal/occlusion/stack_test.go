package occlusion

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// stackScreen is a navigation stack that keeps its previous screen mounted (issue #320): a
// container holding two full-height screens, the one on top listed first (drawing order 2),
// the previous one listed after it (drawing order 1), shifted left and drawn under it. Each
// screen has a header and a body; "Back to home" is on the top screen, "Navigate to Details"
// on the previous one, under it. known: whether the agent reported the drawing orders.
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

// Issue #320: a node listed after the element but drawn under it (the previous screen of a
// stack) is no cover. Before, tree order stood for drawing order, and the previous screen's
// button was taken for a modal layer over "Back to home".
func TestCoverDrawnUnderIsNoCover(t *testing.T) {
	s, back, _ := stackScreen(true)
	if c, modal := Covered(s, back); c != nil {
		t.Errorf("Covered(Back to home) = %v (modal %v), want nil: the previous screen is drawn under it", c, modal)
	}
}

// Without drawing orders (older agents), tree order still decides: the later screen covers.
func TestCoverWithoutDrawingOrderKeepsTreeOrder(t *testing.T) {
	s, back, nav := stackScreen(false)
	if c, _ := Covered(s, back); c != nav {
		t.Errorf("Covered(Back to home) = %v, want the later-listed button %v", c, nav)
	}
}
