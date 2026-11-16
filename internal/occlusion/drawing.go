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

// Buried returns the nodes of s drawn under a sibling that hides them whole (issue #320): the
// previous screen of a navigation stack that stays mounted under the one on top, a tab's page
// drawn under the page shown. Each is the top of a branch: all under it is buried too (and a
// branch under a later sibling of one of its containers is buried with that container).
//
// A branch b is buried by a sibling o (same parent) when:
//   - both report their drawing order, and o's is higher: o is drawn after b, over it (the
//     tree's order says nothing about it: Android lists a parent's children in its own order);
//   - o reports itself visible to the user (a node not visible to the user hides nothing,
//     issue #319);
//   - o, or a node in it (visible, as all between them), says it paints opaque all of its
//     bounds (ui.Node.Opaque), and those bounds hold all of what of b is in its window.
//
// What o's nodes fill tells nothing: a transparent overlay holding a safe-area frame is as
// full as a screen. Where opacity is not reported (an older agent, Android before 17, a
// background that is no plain color, Compose's nodes), nothing is buried: when in doubt, what
// is listed stays seen.
func Buried(s *ui.Screen) map[*ui.Node]bool {
	var out map[*ui.Node]bool
	screen := ui.Rect{R: s.Width, B: s.Height}
	for _, w := range s.Windows {
		area := screen.Intersect(w.Bounds)
		if area.Empty() {
			area = screen
		}
		for _, p := range w.Nodes {
			if len(p.Children) < 2 {
				continue
			}
			for _, b := range p.Children {
				if !b.DrawOrderKnown {
					continue
				}
				v := b.Bounds.Intersect(area)
				if v.Empty() {
					continue // off screen: nothing to hide
				}
				for _, o := range p.Children {
					if o == b || !o.DrawOrderKnown || o.DrawOrder <= b.DrawOrder || !o.Visible || !Within(v, o.Bounds) {
						continue
					}
					if paintsOver(o, v) {
						if out == nil {
							out = map[*ui.Node]bool{}
						}
						out[b] = true
						break
					}
				}
			}
		}
	}
	return out
}

// paintsOver: o, or a node in it reached through visible nodes only, paints opaque bounds
// that hold v.
func paintsOver(o *ui.Node, v ui.Rect) bool {
	found := false
	o.Walk(func(d *ui.Node) bool {
		if found || !d.Visible || !d.Bounds.Overlaps(v) {
			return false
		}
		if d.OpaqueKnown && d.Opaque && Within(v, d.Bounds) {
			found = true
		}
		return !found
	})
	return found
}

// MarkBuried marks every node of the branches drawn under another that hides them (Buried):
// what a person cannot see is neither found nor listed, nor taken for a cover (ui.Node.Buried).
// They stay in the tree: what was under a cover a tap was made in is how the runner tells that
// the cover, gone from the tree, is still drawn over its next target (engine's heldCover).
func MarkBuried(s *ui.Screen) {
	for b := range Buried(s) {
		b.Walk(func(d *ui.Node) bool {
			d.Buried = true
			return true
		})
	}
}
