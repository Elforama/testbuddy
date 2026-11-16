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

// Issue #320: the previous screen, drawn under the screen on top that draws over all of it, is
// buried (not on screen for a person); the screen on top is not.
func TestBuriedPreviousScreen(t *testing.T) {
	s, back, nav := stackScreen(true)
	b := Buried(s)
	prev := nav.Parent.Parent
	if !b[prev] || len(b) != 1 {
		t.Errorf("Buried = %v, want only the previous screen %v", b, prev)
	}
	MarkBuried(s)
	for _, n := range s.Nodes {
		if want := n == prev || n.IsDescendantOf(prev); n.Buried != want {
			t.Errorf("%v: Buried %v, want %v", n, n.Buried, want)
		}
	}
	if At(s, back, back.Bounds.CX(), back.Bounds.CY()) != nil {
		t.Error("a buried node is taken for a cover")
	}
}

// What does not bury: drawing orders not reported (tree order says nothing of what is drawn
// over), a cover not visible to the user (issue #319: it hides nothing), and a full-size frame
// drawn over the content that draws only a bar (a see-through frame, as React Native lays one).
func TestNotBuried(t *testing.T) {
	if s, _, _ := stackScreen(false); len(Buried(s)) != 0 {
		t.Errorf("no drawing orders: Buried = %v", Buried(s))
	}
	{
		s, back, _ := stackScreen(true)
		back.Parent.Parent.Visible = false
		if len(Buried(s)) != 0 {
			t.Errorf("cover not visible to the user: Buried = %v", Buried(s))
		}
	}
	{
		root := node(0, 0, 0, 1000, 2000)
		content := node(1, 0, 0, 1000, 2000)
		content.DrawOrder, content.DrawOrderKnown, content.Visible = 0, true, true
		el := tappable(2, 0, 900, 1000, 1000)
		el.Visible = true
		frame := node(1, 0, 0, 1000, 2000)
		frame.DrawOrder, frame.DrawOrderKnown, frame.Visible = 1, true, true
		inner := node(2, 0, 0, 1000, 2000)
		inner.Visible = true
		bar := tappable(3, 0, 1800, 1000, 2000)
		bar.Visible = true
		s := screen("android", []*ui.Node{root, content, el, frame, inner, bar})
		if len(Buried(s)) != 0 {
			t.Errorf("see-through frame holding a bar: Buried = %v", Buried(s))
		}
	}
}
