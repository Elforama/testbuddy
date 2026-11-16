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

// paint sets what the agent reports of n's painting: opaque, or known see-through.
func paint(n *ui.Node, opaque bool) *ui.Node {
	n.Opaque, n.OpaqueKnown = opaque, true
	return n
}

// shownNode is a node visible to the user.
func shownNode(depth, l, t, r, b int) *ui.Node {
	n := node(depth, l, t, r, b)
	n.Visible = true
	return n
}

// ordered sets n's drawing order.
func ordered(n *ui.Node, o int) *ui.Node {
	n.DrawOrder, n.DrawOrderKnown = o, true
	return n
}

// Issue #320: the previous screen, drawn under an opaque screen on top that holds it, is
// buried (not on screen for a person) with all it holds; the screen on top is not.
func TestBuriedUnderOpaqueScreen(t *testing.T) {
	s, back, nav := stackScreen(true)
	top := back.Parent.Parent
	paint(top, true)
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

// What the cover paints decides, not how much of it its nodes fill: a sparse opaque screen (a
// title and a button on a solid background) buries the screen under it.
func TestBuriedUnderSparseOpaqueScreen(t *testing.T) {
	root := shownNode(0, 0, 0, 1000, 2000)
	top := paint(ordered(shownNode(1, 0, 0, 1000, 2000), 2), true)
	title := shownNode(2, 0, 0, 1000, 150)
	button := shownNode(2, 60, 1350, 940, 1450)
	prev := ordered(shownNode(1, 0, 0, 770, 2000), 1)
	text := shownNode(2, 0, 400, 770, 500)
	s := screen("android", []*ui.Node{root, top, title, button, prev, text})
	if b := Buried(s); !b[prev] || len(b) != 1 {
		t.Errorf("Buried = %v, want the previous screen", b)
	}
}

// An opaque node inside the cover that holds the branch buries it, though the cover's own frame
// is see-through (a transparent wrapper around a card with a solid background).
func TestBuriedUnderOpaqueDescendant(t *testing.T) {
	root := shownNode(0, 0, 0, 1000, 2000)
	wrapper := paint(ordered(shownNode(1, 0, 0, 1000, 2000), 2), false)
	card := paint(shownNode(2, 0, 0, 1000, 2000), true)
	prev := ordered(shownNode(1, 0, 0, 770, 2000), 1)
	text := shownNode(2, 0, 400, 770, 500)
	s := screen("android", []*ui.Node{root, wrapper, card, prev, text})
	if b := Buried(s); !b[prev] || len(b) != 1 {
		t.Errorf("Buried = %v, want the previous screen", b)
	}
}

// What does not bury: when in doubt, nothing is.
func TestNotBuried(t *testing.T) {
	// Opacity not reported (an older agent, Compose, a background that is no plain color),
	// however much of the branch the cover's nodes fill.
	if s, _, _ := stackScreen(true); len(Buried(s)) != 0 {
		t.Errorf("opacity unknown: Buried = %v", Buried(s))
	}
	// Drawing orders not reported: tree order says nothing of what is drawn over.
	if s, back, _ := stackScreen(false); paint(back.Parent.Parent, true) != nil && len(Buried(s)) != 0 {
		t.Errorf("no drawing orders: Buried = %v", Buried(s))
	}
	// A cover not visible to the user hides nothing (issue #319).
	{
		s, back, _ := stackScreen(true)
		top := paint(back.Parent.Parent, true)
		top.Visible = false
		if len(Buried(s)) != 0 {
			t.Errorf("cover not visible to the user: Buried = %v", Buried(s))
		}
	}
	// A transparent overlay over the content, holding a safe-area frame inset under the status
	// bar and a floating button (React Native lays such overlays: a portal, a dialog's host, a
	// transparent modal, a map's controls). The safe area fills 97% of the content, and the
	// content shows through it: see-through reported, or not reported at all.
	for _, known := range []bool{true, false} {
		root := shownNode(0, 0, 0, 1000, 2000)
		content := ordered(shownNode(1, 0, 0, 1000, 2000), 0)
		row := shownNode(2, 0, 900, 1000, 1000)
		overlay := ordered(shownNode(1, 0, 0, 1000, 2000), 1)
		safe := shownNode(2, 0, 60, 1000, 2000)
		fab := shownNode(3, 800, 1800, 950, 1950)
		if known {
			paint(overlay, false)
			paint(safe, false)
		}
		s := screen("android", []*ui.Node{root, content, row, overlay, safe, fab})
		if len(Buried(s)) != 0 {
			t.Errorf("transparent inset overlay (opacity known %v): Buried = %v", known, Buried(s))
		}
	}
	// An opaque cover that does not hold all of the branch (a drawer over part of the content).
	{
		root := shownNode(0, 0, 0, 1000, 2000)
		content := ordered(shownNode(1, 0, 0, 1000, 2000), 0)
		drawer := paint(ordered(shownNode(1, 0, 0, 700, 2000), 1), true)
		s := screen("android", []*ui.Node{root, content, drawer})
		if len(Buried(s)) != 0 {
			t.Errorf("drawer over part of the content: Buried = %v", Buried(s))
		}
	}
}
