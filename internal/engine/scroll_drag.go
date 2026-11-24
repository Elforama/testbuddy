package engine

import (
	"sort"
	"strings"

	"github.com/elforama/testbuddy/internal/occlusion"
	"github.com/elforama/testbuddy/internal/ui"
)

type dragRoom struct {
	box     *ui.Node
	room    ui.Rect
	blocked []ui.Rect
	margin  int
	lo, hi  int
}

func listRoom(s *ui.Screen, view ui.Rect, x, y int, own string) (dragRoom, bool) {
	screen := ui.Rect{R: s.Width, B: s.Height}
	box := verticalListAt(s, x, y, own)
	if box == nil {
		return dragRoom{}, false
	}
	room := box.Bounds.Intersect(box.ClipRect(screen)).Intersect(view)
	if room.H()*5 < screen.H() {
		return dragRoom{}, false
	}
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

func (r dragRoom) clear(px, py int) bool {
	for _, c := range r.blocked {
		if px >= c.L-r.margin && px < c.R+r.margin && py >= c.T-r.margin && py < c.B+r.margin {
			return false
		}
	}
	return true
}

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

func longDrag(s *ui.Screen, view ui.Rect, x, y, ey int, dir string, own string, guarded bool) (sx, sy, tx, ty int, g *dragGuard, ok bool) {
	r, found := listRoom(s, view, x, y, own)
	if !found {
		return
	}
	h := r.room.H()
	inset := max(h/10, r.margin)
	travel := min(h-2*inset, h-tallestRow(r.box, r.room)-r.margin)
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
	sign, from := -1, r.hi
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

type dragGuard struct {
	anchors map[string]ui.Rect
	down    bool
	room    ui.Rect
	margin  int
	x       int
	sy, ty  int
	travel  int
	waited  bool
	back    bool
	tries   int
}

func anchorKey(n *ui.Node) string {
	if l := n.Label(); l != "" {
		return l + "#" + n.ID
	}
	return ""
}

func keyCounts(s *ui.Screen) map[string]int {
	counts := map[string]int{}
	for _, n := range s.Nodes {
		if k := anchorKey(n); k != "" {
			counts[k]++
		}
	}
	return counts
}

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

func (g *dragGuard) confirmed(s *ui.Screen) (shift int, ok bool) {
	if g.back && len(g.anchors) < 2 {
		return g.travel, true
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
		if edge < g.room.T || edge > g.room.B || moved < 0 || (moved*2 < g.travel && !g.waited) ||
			moved > g.room.H()-was.H()-g.margin {
			continue
		}
		shifts = append(shifts, moved)
	}
	sort.Ints(shifts)
	for i := 1; i < len(shifts); i++ {
		if shifts[i]-shifts[i-1] <= max(4, g.margin/4) {
			return shifts[i-1], true
		}
	}
	return 0, false
}

func shortCap(s *ui.Screen, view ui.Rect, x, y int, own string, missing int) int {
	room, margin, tallest := view, minDrag(ui.Rect{R: s.Width, B: s.Height}), 0
	if r, ok := listRoom(s, view, x, y, own); ok {
		room, tallest = r.room, tallestRow(r.box, r.room)
	}
	return max(margin, room.H()-missing-tallest-margin)
}

func tallestRow(box *ui.Node, room ui.Rect) int {
	tallest := 0
	box.Walk(func(d *ui.Node) bool {
		if d != box && d.Bounds.H()*2 <= room.H() && !d.Bounds.Intersect(room).Empty() {
			tallest = max(tallest, d.Bounds.H())
		}
		return true
	})
	return tallest
}

func (g *dragGuard) dragBack(s *ui.Screen, r dragRoom) *dragGuard {
	down, from, tries := !g.down, g.ty, 1
	if g.back {
		down, from, tries = g.down, g.sy, g.tries+1
	}
	sign := 1
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

func ownScrollers(box *ui.Node, room ui.Rect) []ui.Rect {
	var out []ui.Rect
	box.Walk(func(d *ui.Node) bool {
		if d == box {
			return true
		}
		screen := ui.Rect{R: 1 << 30, B: 1 << 30}
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

func verticalListAt(s *ui.Screen, x, y int, own string) *ui.Node {
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
	for p := best.Parent; p != nil && (own == "" || listKey(best) != own); p = p.Parent {
		if vertical(p) && best.Bounds.Intersect(screen).H()*2 < p.Bounds.Intersect(screen).H() {
			best = p
		}
	}
	return best
}

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

func takesTouchesAnywhere(platform string, c *ui.Node) bool {
	found := false
	c.Walk(func(d *ui.Node) bool {
		found = found || occlusion.Touchable(platform, d) || d.Scrollable
		return !found
	})
	return found
}
