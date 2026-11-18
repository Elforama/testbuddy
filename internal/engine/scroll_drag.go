package engine

import (
	"sort"
	"strings"

	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

// dragRoom is where an up-and-down drag of a list goes (issue #372): the list (box), what shows
// of it (room: within view, above an Android keyboard, below a bar drawn over its top edge and a
// section header pinned there, above one over its bottom edge), where a drag may not start
// (blocked: what else is drawn over the list and takes touches, a list of its own in it, a web
// view or a map: a drag there is a tap on it, or scrolls it instead), and how far toward the
// room's edges a drag's ends go (lo, hi: inset 10% from them, off the screen's top 6% and bottom
// 10%, the system's edge gestures).
type dragRoom struct {
	box     *ui.Node
	room    ui.Rect
	blocked []ui.Rect
	margin  int
	lo, hi  int
}

// listRoom is the dragRoom of the list under (x, y); false when there is none, or it shows less
// than a fifth of the screen.
func listRoom(s *ui.Screen, view ui.Rect, x, y int) (dragRoom, bool) {
	screen := ui.Rect{R: s.Width, B: s.Height}
	box := verticalListAt(s, x, y)
	if box == nil {
		return dragRoom{}, false
	}
	room := box.Bounds.Intersect(box.ClipRect(screen)).Intersect(view)
	if room.H()*5 < screen.H() {
		return dragRoom{}, false
	}
	// A bar along an edge (half the list's width or more) leaves less of it to see; anything
	// else drawn over it is only kept clear of where a drag starts.
	var blocked []ui.Rect
	for _, c := range coversOverList(s, box, room) {
		switch {
		case c.W()*2 < room.W():
			blocked = append(blocked, c)
		case c.CY() < room.CY():
			room.T = max(room.T, c.B)
		default:
			room.B = min(room.B, c.T)
		}
	}
	if hdr := pinnedHeader(box, room); hdr != nil {
		room.T = max(room.T, hdr.Bounds.B)
	}
	if room.H()*5 < screen.H() {
		return dragRoom{}, false
	}
	blocked = append(blocked, ownScrollers(box, room)...)
	margin := minDrag(screen)
	inset := max(room.H()/10, margin)
	return dragRoom{box: box, room: room, blocked: blocked, margin: margin,
		lo: max(room.T+inset, screen.H()*6/100), hi: min(room.B-inset, screen.H()*9/10)}, true
}

// clear: a drag may start at (px, py), a margin away from what is blocked.
func (r dragRoom) clear(px, py int) bool {
	for _, c := range r.blocked {
		if px >= c.L-r.margin && px < c.R+r.margin && py >= c.T-r.margin && py < c.B+r.margin {
			return false
		}
	}
	return true
}

// start places a drag of travel from about sy, moving the finger by sign (-1 up the screen, +1
// down): in a column of the room where both its ends are clear, else where its start is, else
// with its start moved past what blocks it toward its end (and its travel shortened to stay
// within lo-hi). ok is false when no start is clear. x is the column to try first.
func (r dragRoom) start(x, sy, travel, sign int) (sx, sy2, travel2 int, ok bool) {
	xs := []int{r.room.CX(), r.room.L + r.room.W()/4, r.room.L + r.room.W()*3/4}
	if x > r.room.L && x < r.room.R {
		xs = append([]int{x}, xs...)
	}
	for _, both := range []bool{true, false} {
		for _, cx := range xs {
			if r.clear(cx, sy) && (!both || r.clear(cx, sy+sign*travel)) {
				return cx, sy, travel, true
			}
		}
	}
	sx = xs[0]
	for range r.blocked {
		moved := false
		for _, c := range r.blocked {
			if !r.clear(sx, sy) && sx >= c.L-r.margin && sx < c.R+r.margin && sy >= c.T-r.margin && sy < c.B+r.margin {
				if sign > 0 {
					sy = c.B + r.margin
				} else {
					sy = c.T - r.margin - 1
				}
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	if sign > 0 {
		travel = min(travel, r.hi-sy)
	} else {
		travel = min(travel, sy-r.lo)
	}
	return sx, sy, travel, r.clear(sx, sy) && travel > 0
}

// longDrag sizes an up-or-down scrollUntilVisible drag to the list it scrolls (issue #372): the
// list under (x, y), the drag's start as placed so far. The drag runs across its room (dragRoom)
// from near one edge to near the other, so one drag moves close to a whole list of content,
// where the drag from the screen's middle moved 40% of the screen whatever the list's size.
//
// It never steps over content: the travel stays below the room's height less the tallest row
// that shows (one at most half the room tall: a taller one is brought in whole by the partial
// drag, as before) and a margin, so every row is whole in at least one read, if the drag lands
// alone. Two drags can land in one update (an app a frame or two behind): that goes unseen
// unless the list can tell, so only there is the long drag sent as it is: an Android
// collection that numbers its rows (the gap check sees a skip) and the web (its drags are an
// instant scrollBy). Anywhere else it needs at least two rows to check it by (dragGuard, when
// guarded: the caller has not seen a check fail), and stops short of carrying the second of
// them out of the room; without them it stays under half the room (the cap placeVertical
// keeps for collections). The content still stops where the finger lifts: the drag's own shape
// (ActionOpts.Drag) is not changed, only its length.
//
// ok is false, for the caller to drag as it did (y to ey), when there is no such list, it shows
// less than a fifth of the screen, or the long drag would be no longer than that one.
func longDrag(s *ui.Screen, view ui.Rect, x, y, ey int, dir string, guarded bool) (sx, sy, tx, ty int, g *dragGuard, ok bool) {
	r, found := listRoom(s, view, x, y)
	if !found {
		return
	}
	h := r.room.H()
	inset := max(h/10, r.margin)
	tallest := 0
	r.box.Walk(func(d *ui.Node) bool {
		if d != r.box && d.Bounds.H()*2 <= h && !d.Bounds.Intersect(r.room).Empty() {
			tallest = max(tallest, d.Bounds.H())
		}
		return true
	})
	travel := min(h-2*inset, h-tallest-r.margin)
	limit := travel
	down := dir != "UP"
	if !(s.Platform == "web" || (s.Platform == "android" && r.box.Coll != nil && r.box.Coll.Rows > 1 &&
		visibleCollectionRows(s, r.box).key != "")) {
		limit = h * 45 / 100
		var anchors map[string]ui.Rect
		if guarded {
			anchors = listAnchors(s, r.box, r.room)
		}
		if reach := anchorReach(anchors, r.room, down, r.margin); len(anchors) >= 2 && min(travel, reach) > limit {
			travel = min(travel, reach)
			g = &dragGuard{anchors: anchors, down: down, room: r.room, margin: r.margin}
		} else {
			travel = min(travel, limit)
		}
	}
	travel = min(travel, r.hi-r.lo)
	base := abs(ey - y)
	if travel <= base {
		return 0, 0, 0, 0, nil, false
	}
	sign, from := -1, r.hi // a drag up the screen, from near the room's bottom
	if !down {
		sign, from = 1, r.lo
	}
	sx, sy, travel, ok = r.start(x, from, travel, sign)
	if !ok || travel <= base {
		return 0, 0, 0, 0, nil, false
	}
	if g != nil && travel <= limit {
		g = nil
	}
	if g != nil {
		g.x, g.sy, g.ty, g.travel = sx, sy, sy+sign*travel, travel
	}
	return sx, sy, sx, sy + sign*travel, g, true
}

// dragGuard checks a long drag that the list could not show a skip of (longDrag, issue #372)
// by its anchors: rows whole in the room in the read before it, by label and id, each the only
// one so named on screen (a label repeated, or a sticky header, says nothing of where the list
// went). The drag counts when the next read that shows the list moved has two of them again,
// each the only one so named, its edge (the one the content moves away from: the other may be
// cut at the list's edge) in the room, moved the drag's way by at least half the drag (less, at
// a list's end, once the app has settled: less cannot skip) and at most the room less its
// height and a margin (so the rows between showed), and both moved alike
// (a label repeated about once a screen can match one by chance, not two moving together).
// Else the content may have moved by two drags at once, and the rows in between never showed.
type dragGuard struct {
	anchors map[string]ui.Rect
	down    bool // the content moves up the screen (scrolling DOWN)
	room    ui.Rect
	margin  int
	x       int // the drag: (x, sy) to (x, ty)
	sy, ty  int
	travel  int
	waited  bool // a read that did not confirm it was followed by a wait for the app to settle
	back    bool // a drag back after a drag that failed its check
	tries   int  // drags back sent for that drag
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

// anchorReach is the longest drag that keeps the edge of the second anchor nearest the room's
// far side in the room (a margin in): the edge the content moves away from, as confirmed
// measures. 0 with fewer than two anchors.
func anchorReach(anchors map[string]ui.Rect, room ui.Rect, down bool, margin int) int {
	var edges []int
	for _, a := range anchors {
		if down {
			edges = append(edges, a.B-room.T)
		} else {
			edges = append(edges, room.B-a.T)
		}
	}
	if len(edges) < 2 {
		return 0
	}
	sort.Sort(sort.Reverse(sort.IntSlice(edges)))
	return edges[1] - margin
}

// confirmed: s shows the drag landed as one (dragGuard). A drag back with fewer than two
// anchors cannot be checked: that it moved the list is all there is to go by.
func (g *dragGuard) confirmed(s *ui.Screen) bool {
	if g.back && len(g.anchors) < 2 {
		return true
	}
	counts := keyCounts(s)
	var shifts []int
	for _, n := range s.Nodes {
		k := anchorKey(n)
		was, ok := g.anchors[k]
		if !ok || counts[k] != 1 || n.Bounds.Empty() {
			continue
		}
		edge, moved := n.Bounds.B, was.B-n.Bounds.B
		if !g.down {
			edge, moved = n.Bounds.T, n.Bounds.T-was.T
		}
		// Less than half the drag: a list at its end, or an app still showing the drag; once
		// the app has settled (waited), the end (the rest cannot come with the next drag).
		if edge < g.room.T || edge > g.room.B || moved < 0 || (moved*2 < g.travel && !g.waited) ||
			moved > g.room.H()-was.H()-g.margin {
			continue
		}
		shifts = append(shifts, moved)
	}
	sort.Ints(shifts)
	for i := 1; i < len(shifts); i++ {
		if shifts[i]-shifts[i-1] <= max(4, g.margin/4) {
			return true
		}
	}
	return false
}

// dragBack is the drag back after g failed its check: from where g ended to where it began; or,
// g being a drag back that failed its own, that drag once more. It goes across the room r of the
// list as s shows it, starting clear (dragRoom.start), checked by the anchors s shows; nil when
// no start is clear.
func (g *dragGuard) dragBack(s *ui.Screen, r dragRoom) *dragGuard {
	down, from, tries := !g.down, g.ty, 1
	if g.back {
		down, from, tries = g.down, g.sy, g.tries+1
	}
	sign := 1 // the finger moves down the screen: the content too
	if down {
		sign = -1
	}
	x, sy, travel, ok := r.start(g.x, from, g.travel, sign)
	if !ok || travel < r.margin {
		return nil
	}
	return &dragGuard{anchors: listAnchors(s, r.box, r.room), down: down, room: r.room, margin: r.margin,
		x: x, sy: sy, ty: sy + sign*travel, travel: travel, back: true, tries: tries}
}

// pinnedHeader is a row of box near the top of its room drawn over other rows (a sticky
// section header): of the rows (box's children, half its width or more) whose top is within the
// room's top tenth, the one overlapping the most others (ties: the one reaching lowest), or nil.
// What it is drawn over does not show.
func pinnedHeader(box *ui.Node, room ui.Rect) *ui.Node {
	wide := func(n *ui.Node) bool { return !n.Bounds.Empty() && n.Bounds.W()*2 >= room.W() }
	var best *ui.Node
	most := 0
	for _, d := range box.Children {
		b := d.Bounds
		if !wide(d) || b.H()*2 > room.H() || b.T < room.T-2 || b.T > room.T+room.H()/10 {
			continue
		}
		n := 0
		for _, e := range box.Children {
			if e != d && wide(e) && min(b.B, e.Bounds.B)-max(b.T, e.Bounds.T) > 2 {
				n++
			}
		}
		if n > 0 && (n > most || (n == most && b.B > best.Bounds.B)) {
			best, most = d, n
		}
	}
	return best
}

// ownScrollers are the parts of room where a drag would move something in box other than box:
// a list of its own that scrolls up and down, a web view, a map.
func ownScrollers(box *ui.Node, room ui.Rect) []ui.Rect {
	var out []ui.Rect
	box.Walk(func(d *ui.Node) bool {
		if d == box {
			return true
		}
		screen := ui.Rect{R: 1 << 30, B: 1 << 30} // its own shape, wherever it is
		c := d.Class
		if (d.Scrollable && !sidewaysShaped(d, screen)) || strings.Contains(c, "WebView") || strings.Contains(c, "MapView") || c == "Map" {
			if r := d.Bounds.Intersect(room); !r.Empty() {
				out = append(out, r)
			}
			return false
		}
		return true
	})
	return out
}

// verticalListAt is the list an up-and-down drag at (x, y) scrolls: the innermost scrolling node
// there that is not a sideways list (a shelf in a page: an up-and-down drag there moves the
// page), or the list holding it when it shows less than half as much (a list of its own in a
// row), or nil.
func verticalListAt(s *ui.Screen, x, y int) *ui.Node {
	screen := ui.Rect{R: s.Width, B: s.Height}
	vertical := func(n *ui.Node) bool {
		return n.Scrollable && n.Bounds.Contains(x, y) && !sidewaysShaped(n, screen)
	}
	var best *ui.Node
	for _, n := range s.Nodes {
		if vertical(n) && (best == nil || n.IsDescendantOf(best)) {
			best = n
		}
	}
	if best == nil {
		return nil
	}
	for p := best.Parent; p != nil; p = p.Parent {
		if vertical(p) && best.Bounds.Intersect(screen).H()*2 < p.Bounds.Intersect(screen).H() {
			best = p
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
