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
// drawn under the page shown. Each is the top of a branch: all under it is buried too.
//
// A branch b is buried by a sibling o (same parent) when:
//   - both report their drawing order, and o's is higher: o is drawn after b, over it (the
//     tree's order says nothing about it: Android lists a parent's children in its own order);
//   - o reports itself visible to the user, and is at least half the screen: a screen, a page,
//     a sheet, not a bar or a header (a node not visible to the user hides nothing, issue #319);
//   - o holds all of what of b is on screen, and draws over nearly all of it: nodes of o, each
//     visible and smaller than o, cover 90% of it. o's own frame does not count, nor a node in
//     it as large as it: a full-size frame may be see-through (React Native lays one over the
//     content to hold a bottom bar), its content is what shows it is not.
//
// Only on screens that report drawing orders (Android).
func Buried(s *ui.Screen) map[*ui.Node]bool {
	var out map[*ui.Node]bool
	screenArea := s.Width * s.Height
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
					if o == b || !o.DrawOrderKnown || o.DrawOrder <= b.DrawOrder || !o.Visible ||
						2*o.Bounds.Intersect(area).Area() < screenArea || !Within(v, o.Bounds) {
						continue
					}
					if drawsOver(o, v) {
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

// drawsOver: the nodes of o (visible, smaller than o) cover 90% of v, on a 16 by 16 grid of
// points in it.
func drawsOver(o *ui.Node, v ui.Rect) bool {
	var parts []ui.Rect
	o.Walk(func(d *ui.Node) bool {
		if d != o && d.Visible && !Within(o.Bounds, d.Bounds) && d.Bounds.Overlaps(v) {
			parts = append(parts, d.Bounds)
		}
		return true
	})
	if len(parts) == 0 {
		return false
	}
	inside := 0
	for i := 0; i < 16; i++ {
		for k := 0; k < 16; k++ {
			x := v.L + (2*i+1)*v.W()/32
			y := v.T + (2*k+1)*v.H()/32
			for _, r := range parts {
				if r.Contains(x, y) {
					inside++
					break
				}
			}
		}
	}
	return inside >= 16*16*9/10
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
