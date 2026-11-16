package occlusion

import "github.com/elforama/testbuddy/internal/ui"

// DrawOrder: whether c is drawn under n (under), and whether the drawing order tells (known).
// Below the nearest node holding both, the branch holding n is drawn after the one holding c
// when its drawing order is higher (Android reports each node's place among its siblings as
// drawn: elevation, translationZ and a parent's own child drawing order, React Native's zIndex,
// count). An order not reported, or the same for both (drawn at once), or c and n in one
// branch, tells nothing.
func DrawOrder(c, n *ui.Node) (under, known bool) {
	bc, bn := c, n
	for bc.Parent != nil && !n.IsDescendantOf(bc.Parent) {
		bc = bc.Parent
	}
	for bn.Parent != nil && bn.Parent != bc.Parent {
		bn = bn.Parent
	}
	if bc.Parent != bn.Parent || bc == bn || !bc.DrawOrderKnown || !bn.DrawOrderKnown || bn.DrawOrder == bc.DrawOrder {
		return false, false // no common container found, or no order to tell
	}
	return bn.DrawOrder > bc.DrawOrder, true
}
