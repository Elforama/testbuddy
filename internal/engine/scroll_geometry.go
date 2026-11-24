package engine

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/elforama/testbuddy/internal/ui"
)

func ownList(s *ui.Screen, n *ui.Node, x, y int) *ui.Node {
	if n == nil {
		return nil
	}
	screen := ui.Rect{R: s.Width, B: s.Height}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && !sidewaysShaped(p, screen) && !p.Bounds.Intersect(screen).Empty() {
			if p.Bounds.Contains(x, y) {
				return nil
			}
			return p
		}
	}
	return nil
}

func markTried(s *ui.Screen, x, y int, tried map[string]bool) {
	for _, n := range s.Nodes {
		if n.Scrollable && n.Bounds.Contains(x, y) {
			tried[listKey(n)] = true
		}
	}
}

func listKey(n *ui.Node) string {
	return fmt.Sprintf("%s#%s@%d+%d", n.Class, n.ID, n.Bounds.L, n.Bounds.W())
}

func otherList(s *ui.Screen, tried map[string]bool) *ui.Node {
	screen := ui.Rect{R: s.Width, B: s.Height}
	var best *ui.Node
	for _, c := range s.Nodes {
		if !visibleVerticalList(c, screen) || tried[listKey(c)] {
			continue
		}
		b := c.Bounds.Intersect(screen)
		if best == nil || b.Area() > best.Bounds.Intersect(screen).Area() {
			best = c
		}
	}
	return best
}

func visibleVerticalList(c *ui.Node, screen ui.Rect) bool {
	return c.Scrollable && c.Parent != nil && !sidewaysShaped(c, screen) &&
		c.Bounds.Intersect(screen).H()*5 >= screen.H()
}

func sidewaysShaped(c *ui.Node, screen ui.Rect) bool {
	if strings.Contains(c.Class, "HorizontalScrollView") {
		return true
	}
	b := c.Bounds.Intersect(screen)
	return b.W() > 2*b.H() && b.H() > 0
}

func sidewaysList(s *ui.Screen, n *ui.Node, x, y int, want string) *ui.Node {
	screen := ui.Rect{R: s.Width, B: s.Height}
	sideways := func(c *ui.Node) bool { return c.Scrollable && sidewaysShaped(c, screen) }
	if n != nil {
		for p := n.Parent; p != nil; p = p.Parent {
			if sideways(p) {
				return p
			}
		}
	}
	var lists []*ui.Node
	for _, c := range s.Nodes {
		if sideways(c) && !c.Bounds.Intersect(screen).Empty() {
			lists = append(lists, c)
		}
	}
	if best := listWithSimilarLabels(lists, want); best != nil {
		return best
	}
	if box := scrollContainer(s, x, y); box != nil && sideways(box) {
		return nil
	}
	var largest *ui.Node
	for _, c := range lists {
		if largest == nil || c.Bounds.Intersect(screen).Area() > largest.Bounds.Intersect(screen).Area() {
			largest = c
		}
	}
	return largest
}

func listWithSimilarLabels(lists []*ui.Node, want string) *ui.Node {
	if want == "" {
		return nil
	}
	var best *ui.Node
	bestScore := 2
	for _, c := range lists {
		score := 0
		c.Walk(func(d *ui.Node) bool {
			score = max(score, commonPrefix(d.Label(), want))
			return true
		})
		if score > bestScore {
			best, bestScore = c, score
		} else if score == bestScore {
			best = nil
		}
	}
	return best
}

func commonPrefix(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return i
}

func scrollContainer(s *ui.Screen, x, y int) *ui.Node {
	var best *ui.Node
	for _, n := range s.Nodes {
		if n.Scrollable && n.Bounds.Contains(x, y) && (best == nil || n.IsDescendantOf(best)) {
			best = n
		}
	}
	return best
}

func newTexts(s *ui.Screen, x, y int, seen map[string]bool) int {
	n := 0
	screen := ui.Rect{R: s.Width, B: s.Height}
	add := func(nd *ui.Node) bool {
		if nd.Bounds.Intersect(nd.ClipRect(screen)).Empty() {
			return true
		}
		if t := nd.Label(); t != "" && !seen[t] {
			seen[t] = true
			n++
		}
		return true
	}
	if box := scrollContainer(s, x, y); box != nil {
		box.Walk(add)
	} else {
		for _, nd := range s.Nodes {
			add(nd)
		}
	}
	return n
}

type collectionSpan struct {
	key    string
	lo, hi int
}

func visibleCollectionRows(s *ui.Screen, box *ui.Node) collectionSpan {
	if box == nil || box.Coll == nil || box.Coll.Rows <= 1 {
		return collectionSpan{}
	}
	clip := box.Bounds.Intersect(ui.Rect{R: s.Width, B: s.Height})
	lo, hi := box.Coll.Rows, -1
	box.Walk(func(n *ui.Node) bool {
		if n != box && n.Scrollable {
			return false
		}
		if n.Item != nil && n.Item.Rows >= 0 && n.Item.Rows < box.Coll.Rows && n.Bounds.T > clip.T && n.Bounds.B < clip.B && !n.Bounds.Empty() && !n.Bounds.Intersect(clip).Empty() {
			lo = min(lo, n.Item.Rows)
			hi = max(hi, n.Item.Rows)
		}
		return true
	})
	if hi < lo {
		return collectionSpan{}
	}
	return collectionSpan{key: fmt.Sprintf("%d:%s:%d", s.Windows[box.Window].ID, listKey(box), box.Coll.Rows), lo: lo, hi: hi}
}

func listSignature(s *ui.Screen, x, y int, vertical bool) uint64 {
	return signature(s, scrollContainer(s, x, y), vertical)
}

func screenSignature(s *ui.Screen, vertical bool) uint64 { return signature(s, nil, vertical) }

func signature(s *ui.Screen, box *ui.Node, vertical bool) uint64 {
	h := fnv.New64a()
	add := func(n *ui.Node) bool {
		pos := n.Bounds.L
		if vertical {
			pos = n.Bounds.T
		}
		fmt.Fprintf(h, "%d|%s|%s\x00", pos, n.Text, n.Desc)
		return true
	}
	if box != nil {
		box.Walk(add)
	} else {
		for _, n := range s.Nodes {
			add(n)
		}
	}
	return h.Sum64()
}

func dragStart(s *ui.Screen, platform string, x, y int, dir string) (start int, placed bool) {
	if platform != "ios" {
		return y, true
	}
	box := scrollContainer(s, x, y)
	if box == nil {
		return y, false
	}
	top, bottom := max(box.Bounds.T, 0), min(box.Bounds.B, s.Height)
	if dir == "DOWN" {
		start = top + (bottom-top)*7/10
		return max(start, y), true
	}
	start = top + (bottom-top)*3/10
	return min(start, y), true
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

func flush(b, clip ui.Rect, vertical bool) bool {
	if vertical {
		return abs(b.T-clip.T) <= 1 || abs(b.B-clip.B) <= 1
	}
	return abs(b.L-clip.L) <= 1 || abs(b.R-clip.R) <= 1
}

func fits(b, clip ui.Rect, vertical bool) bool {
	if vertical {
		return b.H() <= clip.H()
	}
	return b.W() <= clip.W()
}

func clearOfKeyboard(s *ui.Screen, screen ui.Rect) ui.Rect {
	for _, w := range s.Windows {
		if w.Type == "keyboard" && w.Bounds.T > screen.T && w.Bounds.T < screen.B &&
			w.Bounds.B >= screen.B-1 && 2*w.Bounds.W() >= screen.W() {
			screen.B = w.Bounds.T
		}
	}
	return screen
}

func clearOfKeyboardSwipe(s *ui.Screen, platform string, screen ui.Rect, x1, y1, x2, y2 int) (sx, sy, ex, ey int, ok bool) {
	if platform != "android" {
		return x1, y1, x2, y2, true
	}
	view := clearOfKeyboard(s, screen)
	if view.B >= screen.B {
		return x1, y1, x2, y2, true
	}
	fit := func(lo, hi int) (int, int) {
		lo = min(lo, hi)
		length := abs(y2 - y1)
		start := clamp(y1, lo, hi)
		if y2 >= y1 {
			start = max(lo, min(start, hi-length))
			return start, min(hi, start+length)
		}
		start = min(hi, max(start, lo+length))
		return start, max(lo, start-length)
	}
	long := func(sy, ey int) bool { return abs(x2-x1)+abs(ey-sy) >= minDrag(screen) }
	lo, hi := view.T+screen.H()*6/100, view.B-1
	if list := scrollContainer(s, x1, min(y1, hi)); list != nil {
		if sy, ey := fit(max(lo, list.Bounds.T), min(hi, list.Bounds.B-1)); long(sy, ey) {
			return x1, sy, x2, ey, true
		}
	}
	sy, ey = fit(lo, hi)
	return x1, sy, x2, ey, long(sy, ey)
}

func minDrag(screen ui.Rect) int { return max(screen.W(), screen.H()) / 50 }

func sameSpot(b, was ui.Rect) bool {
	const slack = 2
	return abs(b.L-was.L) <= slack && abs(b.T-was.T) <= slack && abs(b.R-was.R) <= slack && abs(b.B-was.B) <= slack
}

func centered(b, screen ui.Rect, dir string) bool {
	if dir == "LEFT" || dir == "RIGHT" {
		mid := screen.W() / 2
		return abs(b.CX()-mid) <= screen.W()/5
	}
	mid := screen.H() / 2
	return abs(b.CY()-mid) <= screen.H()/5
}

func centerScrollLimited(n, list *ui.Node, screen ui.Rect, vertical bool) bool {
	if list == nil || list.MoreBefore == list.MoreAfter || !n.IsDescendantOf(list) {
		return false
	}
	after := n.Bounds.CX() > screen.CX()
	if vertical {
		after = n.Bounds.CY() > screen.CY()
	}
	return (after && !list.MoreAfter) || (!after && !list.MoreBefore)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
