package engine

import (
	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

// longDrag sizes an up-or-down scrollUntilVisible drag to the list it scrolls (issue #372): the
// list under (x, y), the drag's start as placed so far, of which what shows (within view: above
// an Android keyboard) and is not under a bar drawn over it is the room. The drag runs from
// near one edge of that room to near the other, inset from both, so one drag moves close to a
// whole list of content, where the drag from the screen's middle moved 40% of the screen
// whatever the list's size.
//
// It never steps over content: the travel stays below the room's height less the tallest row
// that shows (one at most half the room tall: a taller one is brought in whole by the partial
// drag, as before) and a margin, so every row is whole in at least one read. In a collection
// whose rows Android does not number, or an iOS collection view, two drags can arrive in one
// update (the cap placeVertical keeps): there a long drag goes only when guarded (the caller has
// not seen one fail) with rows to check it by, and the caller checks it (g, dragGuard); else it
// stays under half the room. It starts clear of anything
// drawn over the list that takes touches (a floating button, a bottom bar: a drag that starts
// there is a tap on it), and away from the screen's edges (the system's edge gestures). The
// content still stops where the finger lifts: the drag's own shape (ActionOpts.Drag) is not
// changed, only its length.
//
// ok is false, for the caller to drag as it did (y to ey), when there is no such list, it shows
// less than a fifth of the screen, or the long drag would be no longer than that one.
func longDrag(s *ui.Screen, view ui.Rect, x, y, ey int, dir string, guarded bool) (sx, sy, tx, ty int, g *dragGuard, ok bool) {
	screen := ui.Rect{R: s.Width, B: s.Height}
	box := verticalListAt(s, x, y)
	if box == nil {
		return
	}
	room := box.Bounds.Intersect(box.ClipRect(screen)).Intersect(view)
	if room.H()*5 < screen.H() {
		return
	}
	// What is drawn over the list: a bar along an edge (half its width or more) leaves less of
	// it to see; anything else is only kept clear of where the drag starts.
	var small []ui.Rect
	for _, c := range coversOverList(s, box, room) {
		switch {
		case c.W()*2 < room.W():
			small = append(small, c)
		case c.CY() < room.CY():
			room.T = max(room.T, c.B)
		default:
			room.B = min(room.B, c.T)
		}
	}
	h := room.H()
	if h*5 < screen.H() {
		return
	}
	margin := minDrag(screen)
	inset := max(h/10, margin)
	tallest := 0
	box.Walk(func(d *ui.Node) bool {
		if d != box && d.Bounds.H()*2 <= h && !d.Bounds.Intersect(room).Empty() {
			tallest = max(tallest, d.Bounds.H())
		}
		return true
	})
	travel := min(h-2*inset, h-tallest-margin)
	limit := travel
	if (s.Platform == "android" && box.Coll != nil && box.Coll.Rows > 1 && visibleCollectionRows(s, box).key == "") ||
		(s.Platform == "ios" && box.Class == "CollectionView") {
		// Two drags landing in one update would skip rows no row number tells of. A long drag
		// goes only with rows to check it by (dragGuard); else, or once one failed, the cap.
		limit = h * 45 / 100
		if guarded {
			if anchors := listAnchors(s, box, room); len(anchors) > 0 {
				g = &dragGuard{anchors: anchors, down: dir != "UP"}
			}
		}
		if g == nil {
			travel = min(travel, limit)
		}
	}
	// Within the room, inset from its edges, and off the screen's edges: the top 6% (a touch
	// there pulls the notification shade down) and the bottom 10% (the home gesture), as far
	// as the drags from the screen's middle ever went.
	lo, hi := max(room.T+inset, screen.H()*6/100), min(room.B-inset, screen.H()*9/10)
	travel = min(travel, hi-lo)
	base := abs(ey - y)
	if travel <= base {
		return
	}
	clear := func(px, py int) bool {
		for _, c := range small {
			if px >= c.L-margin && px < c.R+margin && py >= c.T-margin && py < c.B+margin {
				return false
			}
		}
		return true
	}
	// Toward the edge the content heads away from: dragging DOWN starts near the room's bottom.
	end := func(start int) int {
		if dir == "UP" {
			return start + travel
		}
		return start - travel
	}
	// The drag from (px, py): checked when longer than the cap of a list that needs one.
	planned := func(px, py int) (int, int, int, int, *dragGuard, bool) {
		if g != nil && travel > limit {
			g.x, g.sy, g.ty, g.travel = px, py, end(py), travel
		} else {
			g = nil
		}
		return px, py, px, end(py), g, true
	}
	sy = hi
	if dir == "UP" {
		sy = lo
	}
	xs := []int{room.CX(), room.L + room.W()/4, room.L + room.W()*3/4}
	if x > room.L && x < room.R {
		xs = append([]int{x}, xs...)
	}
	// A column whose start and end are both clear, else one whose start is.
	for _, both := range []bool{true, false} {
		for _, cx := range xs {
			if clear(cx, sy) && (!both || clear(cx, end(sy))) {
				return planned(cx, sy)
			}
		}
	}
	// Covered across every column: start past what covers it, toward the drag's end.
	sx = xs[0]
	for range small {
		moved := false
		for _, c := range small {
			if !clear(sx, sy) && sx >= c.L-margin && sx < c.R+margin && sy >= c.T-margin && sy < c.B+margin {
				if dir == "UP" {
					sy = c.B + margin
				} else {
					sy = c.T - margin - 1
				}
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	if dir == "UP" {
		travel = min(travel, hi-sy)
	} else {
		travel = min(travel, sy-lo)
	}
	if !clear(sx, sy) || travel <= base {
		return 0, 0, 0, 0, nil, false
	}
	return planned(sx, sy)
}

// dragGuard checks a long drag of a list that does not number its rows (longDrag, issue #372)
// by its anchors: rows whole in the read before it, by label and id, each the only one so named
// on screen (a label repeated, or a sticky header, says nothing of where the list went). The
// drag counts when the next read that shows the list moved has one of them again, the only one
// so named, moved the drag's way by about its length (half to one and a half times): then the
// content between the reads all showed. Else it may have moved by two drags at once, and the
// rows in between never showed.
type dragGuard struct {
	anchors map[string]ui.Rect
	down    bool // the drag goes up the screen: the content moves up (scrolling DOWN)
	x       int  // the drag: (x, sy) to (x, ty)
	sy, ty  int
	travel  int
	waited  bool // a read that did not confirm it was followed by a wait for the app to settle
}

// anchorKey names a row across reads: its label and id ("" when it has no label).
func anchorKey(n *ui.Node) string {
	if l := n.Label(); l != "" {
		return l + "#" + n.ID
	}
	return ""
}

// keyCounts counts the nodes on screen by anchorKey.
func keyCounts(s *ui.Screen) map[string]int {
	counts := map[string]int{}
	for _, n := range s.Nodes {
		if k := anchorKey(n); k != "" {
			counts[k]++
		}
	}
	return counts
}

// listAnchors is what in box shows whole within room and is the only node so named on screen,
// by anchorKey, with its bounds.
func listAnchors(s *ui.Screen, box *ui.Node, room ui.Rect) map[string]ui.Rect {
	counts := keyCounts(s)
	anchors := map[string]ui.Rect{}
	box.Walk(func(d *ui.Node) bool {
		if k := anchorKey(d); d != box && k != "" && counts[k] == 1 && !d.Bounds.Empty() &&
			d.Bounds.T >= room.T && d.Bounds.B <= room.B {
			anchors[k] = d.Bounds
		}
		return true
	})
	return anchors
}

// confirmed: s has one of the anchors moved the drag's way by about its length. The edge
// measured is the one the content moves away from (the bottom for a drag up the screen): the
// other may be cut off at the list's edge.
func (g *dragGuard) confirmed(s *ui.Screen) bool {
	counts := keyCounts(s)
	for _, n := range s.Nodes {
		k := anchorKey(n)
		was, ok := g.anchors[k]
		if !ok || counts[k] != 1 || n.Bounds.Empty() {
			continue
		}
		moved := n.Bounds.T - was.T
		if g.down {
			moved = was.B - n.Bounds.B
		}
		if moved*2 >= g.travel && moved*2 <= g.travel*3 {
			return true
		}
	}
	return false
}

// verticalListAt is the innermost scrolling node at (x, y) that is not a sideways list (a
// shelf in a page: an up-and-down drag there moves the page), or nil.
func verticalListAt(s *ui.Screen, x, y int) *ui.Node {
	screen := ui.Rect{R: s.Width, B: s.Height}
	var best *ui.Node
	for _, n := range s.Nodes {
		if n.Scrollable && n.Bounds.Contains(x, y) && !sidewaysShaped(n, screen) && (best == nil || n.IsDescendantOf(best)) {
			best = n
		}
	}
	return best
}

// coversOverList is what is drawn over the list box that a touch there would reach instead of
// it, cut to room: on the web, what the browser's hit test found over the list or its rows;
// elsewhere, a window above the list's (smaller than half the screen: a bar, a keyboard, a
// popup), and in its own window a node outside the list, drawn over it (by the drawing order
// where reported, else listed after it, or an iOS bar) that takes touches. A node as large as
// half the screen is a layout around the screen, not a cover (as occlusion.At has it).
func coversOverList(s *ui.Screen, box *ui.Node, room ui.Rect) []ui.Rect {
	var out []ui.Rect
	add := func(r ui.Rect) {
		if r = r.Intersect(room); !r.Empty() {
			out = append(out, r)
		}
	}
	half := s.Width * s.Height / 2
	if s.Platform == "web" {
		seen := map[*ui.Node]bool{}
		box.Walk(func(d *ui.Node) bool {
			for _, h := range d.Covers {
				if h.By != nil && !seen[h.By] && h.By != box && !h.By.IsDescendantOf(box) && !box.IsDescendantOf(h.By) && h.By.Bounds.Area() < half {
					seen[h.By] = true
					add(h.By.Bounds)
				}
			}
			return true
		})
		return out
	}
	if box.Window < 0 || box.Window >= len(s.Windows) {
		return nil
	}
	for i := 0; i < box.Window; i++ {
		if w := s.Windows[i]; w.Bounds.Area() < half {
			add(w.Bounds)
		}
	}
	after := false
	for _, c := range s.Windows[box.Window].Nodes {
		if c == box {
			after = true
			continue
		}
		if c.Bounds.Area() >= half || c.IsDescendantOf(box) || box.IsDescendantOf(c) || !c.Bounds.Overlaps(room) {
			continue
		}
		bar := s.Platform == "ios" && occlusion.IOSBars[c.Class]
		over := after
		if under, known := drawOrder(c, box); known {
			over = !under
		}
		if bar || (over && takesTouchesAnywhere(s.Platform, c)) {
			add(c.Bounds)
		}
	}
	return out
}

// takesTouchesAnywhere: c, or a node in it, handles touches (occlusion.TakesTouches, at any
// point). A bar counts whole, not only its buttons: what it is drawn over does not show.
func takesTouchesAnywhere(platform string, c *ui.Node) bool {
	found := false
	c.Walk(func(d *ui.Node) bool {
		found = found || occlusion.Touchable(platform, d) || d.Scrollable
		return !found
	})
	return found
}
