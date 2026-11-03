package look

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elforama/testbuddy/internal/ui"
)

// The listing is printed as a sparse outline: indentation shows layout only where it tells
// the reader something. A plain vertical stack is the default and gets no header line, so
// a simple screen reads as a flat list. Header lines are:
//
//	row:                 things side by side
//	grid 3 cols:         equal rows of side-by-side things
//	column:              a vertical stack inside a row or grid
//	column scrollable:   any group that scrolls (row scrollable: for a carousel)
//	column #profile:     a group with a useful, unique id
//
// Containers that hold one thing are collapsed, and siblings laid out flat (as in a
// ConstraintLayout) are split into rows by where they sit on screen. Elements inside a
// listed element (the texts in a tappable card) are indented under it.
//
// A scroll area says what it holds out of view: "column scrollable (12 more below):", or
// "(more below)" when only Android's "can scroll further" is known. Off-screen elements
// outside any scroll area (a long web page) are one "(12 more below)" line at the end.
// The same outline is in the JSON listing, as blocks.

// item is a listed element or a group of items, in the outline.
type item struct {
	e      *Element
	node   *ui.Node // the group's container (nil for a row found by position)
	kids   []*item
	box    ui.Rect  // where it is on screen
	scroll *ui.Node // the scroll view it is the content of
	row    bool     // a row found by position, not by a container
}

// outline is one window's items.
type outline struct {
	win   *ui.Window
	items []*item
}

func union(a, b ui.Rect) ui.Rect {
	if a.Empty() {
		return b
	}
	if b.Empty() {
		return a
	}
	return ui.Rect{L: min(a.L, b.L), T: min(a.T, b.T), R: max(a.R, b.R), B: max(a.B, b.B)}
}

// gather builds n's subtree as at most one item: a listed element (with what is listed
// inside it), a group of two or more items, or the one item below a plain wrapper.
func gather(n *ui.Node, kept map[*ui.Node]*Element) *item {
	var kids []*item
	for _, c := range n.Children {
		if it := gather(c, kept); it != nil {
			kids = append(kids, it)
		}
	}
	if e := kept[n]; e != nil {
		return &item{e: e, kids: kids, box: n.Bounds}
	}
	switch {
	case len(kids) == 0:
		return nil
	case len(kids) == 1 && !n.Scrollable:
		return kids[0]
	case len(kids) == 1 && kids[0].e == nil && kids[0].scroll == nil:
		kids[0].scroll = n // a scroll view around one group: that group scrolls
		return kids[0]
	}
	it := &item{node: n, kids: kids}
	if n.Scrollable {
		it.scroll = n
	}
	for _, k := range kids {
		it.box = union(it.box, k.box)
	}
	return it
}

func overlap(a0, a1, b0, b1 int) int { return min(a1, b1) - max(a0, b0) }

// arrange lays out a group's items: "row", "grid" (with its column count) or "column". A
// column's entries may include rows found by position (side-by-side items among stacked ones).
func arrange(kids []*item) (layout string, cols int, entries []*item) {
	if len(kids) < 2 {
		return "column", 0, kids
	}
	sorted := append([]*item(nil), kids...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].box.T < sorted[j].box.T })
	// Bands: items that share most of a horizontal strip.
	var bands [][]*item
	var bandBox []ui.Rect
	for _, k := range sorted {
		if n := len(bands); n > 0 {
			b := bandBox[n-1]
			h := max(1, min(k.box.H(), b.H()))
			if overlap(k.box.T, k.box.B, b.T, b.B)*2 > h {
				bands[n-1] = append(bands[n-1], k)
				bandBox[n-1] = union(b, k.box)
				continue
			}
		}
		bands = append(bands, []*item{k})
		bandBox = append(bandBox, k.box)
	}
	// A band is a row when its items sit side by side without covering each other.
	isRow := make([]bool, len(bands))
	for i, b := range bands {
		sort.SliceStable(b, func(x, y int) bool { return b[x].box.L < b[y].box.L })
		isRow[i] = len(b) > 1
		for j := 1; j < len(b); j++ {
			w := max(1, min(b[j].box.W(), b[j-1].box.W()))
			if overlap(b[j-1].box.L, b[j-1].box.R, b[j].box.L, b[j].box.R)*3 > w {
				isRow[i] = false
				// Items covering each other (an icon inside a field): by their centers.
				sort.SliceStable(b, func(x, y int) bool {
					p, q := b[x].box, b[y].box
					if abs(p.CY()-q.CY()) > max(8, min(p.H(), q.H())/2) {
						return p.CY() < q.CY()
					}
					return p.L < q.L
				})
				break
			}
		}
	}
	if len(bands) == 1 && isRow[0] {
		return "row", 0, bands[0]
	}
	grid := len(bands) > 1
	for i, b := range bands {
		if !isRow[i] || len(b) != len(bands[0]) {
			grid = false
		}
	}
	if grid {
		for _, b := range bands {
			entries = append(entries, b...)
		}
		return "grid", len(bands[0]), entries
	}
	for i, b := range bands {
		if isRow[i] {
			entries = append(entries, &item{kids: b, box: bandBox[i], row: true})
		} else {
			entries = append(entries, b...)
		}
	}
	return "column", 0, entries
}

// Block is one line of the outline: a listed element (Ref, see the listing's elements) or
// a group header (Layout). Children are indented under it.
type Block struct {
	Ref        int      `json:"ref,omitempty"`
	Window     string   `json:"window,omitempty"` // "dialog" or "app", when a system dialog is over the app
	Layout     string   `json:"layout,omitempty"` // row, grid or column
	Cols       int      `json:"cols,omitempty"`   // a grid's columns
	ID         string   `json:"id,omitempty"`
	Scrollable bool     `json:"scrollable,omitempty"`
	More       *More    `json:"more,omitempty"`
	Children   []*Block `json:"children,omitempty"`
	e          *Element
}

// More is how much a scroll area (or the screen) holds out of view, per side: a count of
// elements (Android: of list rows), or -1 when there is more but the count is unknown.
type More struct {
	Above int `json:"above,omitempty"`
	Below int `json:"below,omitempty"`
	Left  int `json:"left,omitempty"`
	Right int `json:"right,omitempty"`
}

func (m *More) empty() bool { return m == nil || *m == More{} }

// String reads "3 more above, 12 more below", or "more below" for an unknown count.
func (m *More) String() string {
	var parts []string
	for _, side := range []struct {
		n    int
		name string
	}{{m.Above, "above"}, {m.Below, "below"}, {m.Left, "to the left"}, {m.Right, "to the right"}} {
		switch {
		case side.n > 0:
			parts = append(parts, fmt.Sprintf("%d more %s", side.n, side.name))
		case side.n < 0:
			parts = append(parts, "more "+side.name)
		}
	}
	return strings.Join(parts, ", ")
}

type outliner struct {
	l       *Listing
	s       *ui.Screen
	idCount map[string]int
	kept    map[*ui.Node]*Element
	hidden  map[*ui.Node][]*ui.Node // scroll view (nil: none) -> top-most off-screen elements
	items   map[*ui.Node][]*ui.Coll // Android list -> its items' places
	shown   map[*ui.Node]bool       // scroll views whose counts are on a line
}

// groupID is a container's id when worth showing: unique and not a generic layout id.
func (o *outliner) groupID(n *ui.Node) string {
	if n == nil || n.ID == "" || o.idCount[n.ID] != 1 || !usefulID(n.ID) || o.l.Platform == "web" {
		return ""
	}
	return ShortID(n.ID)
}

func scrollParent(n *ui.Node) *ui.Node {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable {
			return p
		}
	}
	return nil
}

// sides counts nodes by the side of view they lie on.
func sides(nodes []*ui.Node, view ui.Rect) More {
	var m More
	for _, n := range nodes {
		b := n.Bounds
		switch {
		case b.T >= view.B:
			m.Below++
		case b.B <= view.T:
			m.Above++
		case b.L >= view.R:
			m.Right++
		case b.R <= view.L:
			m.Left++
		}
	}
	return m
}

// more is what scroll view sv holds out of view; horizontal when its content is a row.
func (o *outliner) more(sv *ui.Node, horizontal bool) *More {
	o.shown[sv] = true
	screen := ui.Rect{R: o.s.Width, B: o.s.Height}
	view := sv.Bounds.Intersect(screen)
	if view.Empty() {
		view = screen
	}
	m := sides(o.hidden[sv], view)
	after, before := &m.Below, &m.Above
	if horizontal {
		after, before = &m.Right, &m.Left
	}
	// An Android list's size (on the scroll view or a list inside it) and the places of the
	// items on screen; without places, the rows on screen when at one end of the list.
	coll := sv
	if coll.Coll == nil {
		coll = o.listIn(sv)
	}
	if coll != nil && coll.Coll.Rows > 0 && coll.Coll.Cols > 0 {
		c := coll.Coll
		if c.Rows == 1 && c.Cols > 1 {
			horizontal = true
			after, before = &m.Right, &m.Left
		}
		total := c.Rows
		if horizontal {
			total = c.Cols
		}
		if places := o.items[coll]; len(places) > 0 {
			lo, hi := -1, -1
			for _, it := range places {
				i := it.Rows
				if horizontal {
					i = it.Cols
				}
				if lo < 0 || i < lo {
					lo = i
				}
				hi = max(hi, i)
			}
			// Not every row reports its place (Android Settings): the rows on screen run on
			// from the first one that does.
			hi = min(total-1, max(hi, lo+len(coll.Children)-1))
			*before, *after = lo, max(0, total-1-hi)
		} else if shown := len(coll.Children); c.Rows == 1 || c.Cols == 1 {
			switch {
			case !sv.MoreBefore && sv.MoreAfter:
				*before, *after = 0, max(0, total-shown)
			case sv.MoreBefore && !sv.MoreAfter:
				*before, *after = max(0, total-shown), 0
			}
		}
	}
	if o.l.Platform == "android" {
		// Whether it can scroll further wins: a row may be an invisible spacer, or partly
		// cut off.
		switch {
		case !sv.MoreAfter:
			*after = 0
		case *after == 0:
			*after = -1
		}
		switch {
		case !sv.MoreBefore:
			*before = 0
		case *before == 0:
			*before = -1
		}
	}
	if m.empty() {
		return nil
	}
	return &m
}

// listIn is the Android list inside scroll view sv (RecyclerViews in some apps report
// their size on a child), or nil.
func (o *outliner) listIn(sv *ui.Node) *ui.Node {
	var found *ui.Node
	for _, c := range sv.Children {
		c.Walk(func(n *ui.Node) bool {
			if found != nil || n.Scrollable {
				return false
			}
			if n.Coll != nil {
				found = n
				return false
			}
			return true
		})
	}
	return found
}

// group makes a group's blocks: under a header block when its layout differs from ctx
// (the layout it sits in) or it scrolls or has an id; otherwise in line.
func (o *outliner) group(g *item, ctx string) []*Block {
	layout, cols, entries := arrange(g.kids)
	if g.row {
		layout, cols, entries = "row", 0, g.kids
	}
	id := o.groupID(g.node)
	var kids []*Block
	inner := layout
	if layout == ctx && layout != "grid" && g.scroll == nil && id == "" {
		inner = ctx
	}
	for _, e := range entries {
		kids = append(kids, o.item(e, inner)...)
	}
	if inner == ctx && layout != "grid" && g.scroll == nil && id == "" {
		return kids
	}
	b := &Block{Layout: layout, ID: id, Children: kids}
	if layout == "grid" {
		b.Cols = cols
	}
	if g.scroll != nil {
		b.Scrollable = true
		b.More = o.more(g.scroll, layout == "row")
	}
	return []*Block{b}
}

func (o *outliner) item(it *item, ctx string) []*Block {
	if it.e == nil {
		return o.group(it, ctx)
	}
	b := &Block{Ref: it.e.Ref, e: it.e}
	if len(it.kids) > 0 {
		b.Children = o.group(&item{kids: it.kids, box: it.box}, "column")
	}
	if it.e.Node.Scrollable {
		horizontal := len(b.Children) == 1 && b.Children[0].Layout == "row"
		b.More = o.more(it.e.Node, horizontal)
	}
	return []*Block{b}
}

// buildOutline groups the listed elements by the containers they sit in, per window, and
// counts what scroll areas hold out of view. offscreen are elements Build left out for
// lying off screen.
func (l *Listing) buildOutline(s *ui.Screen, offscreen []*ui.Node) {
	o := &outliner{l: l, s: s, idCount: map[string]int{}, kept: map[*ui.Node]*Element{},
		hidden: map[*ui.Node][]*ui.Node{}, items: map[*ui.Node][]*ui.Coll{}, shown: map[*ui.Node]bool{}}
	for _, n := range s.Nodes {
		if n.ID != "" {
			o.idCount[n.ID]++
		}
		if n.Item != nil {
			for p := n.Parent; p != nil; p = p.Parent {
				if p.Coll != nil {
					o.items[p] = append(o.items[p], n.Item)
					break
				}
			}
		}
	}
	for _, e := range l.Elements {
		o.kept[e.Node] = e
	}
	// Count an off-screen element once: not when inside another one, or inside a listed one.
	off := map[*ui.Node]bool{}
	for _, n := range offscreen {
		off[n] = true
	}
	for _, n := range offscreen {
		inside := false
		for p := n.Parent; p != nil && !inside; p = p.Parent {
			inside = off[p] || o.kept[p] != nil
		}
		if !inside {
			sv := scrollParent(n)
			o.hidden[sv] = append(o.hidden[sv], n)
		}
	}

	l.Blocks, l.More = nil, nil
	var wins []*Block
	for _, w := range s.Windows {
		var items []*item
		for _, r := range w.Roots {
			if it := gather(r, o.kept); it != nil {
				items = append(items, it)
			}
		}
		if len(items) == 0 {
			continue
		}
		root := &item{kids: items}
		if len(items) == 1 {
			root = items[0]
		}
		var blocks []*Block
		if root.e != nil && root.e.Node.Parent == nil && len(root.kids) > 0 {
			// A labelled window root (an iOS Application): its contents are the screen,
			// not something inside it.
			blocks = append([]*Block{{Ref: root.e.Ref, e: root.e}}, o.group(&item{kids: root.kids}, "column")...)
		} else {
			blocks = o.item(root, "column")
		}
		name := w.Type // app, system, overlay
		if name == "system" {
			name = "dialog"
		}
		wins = append(wins, &Block{Window: name, Children: blocks})
	}
	// Off-screen elements outside any scroll area, or in one with no line of its own.
	var rest []*ui.Node
	for sv, nodes := range o.hidden {
		if sv == nil || !o.shown[sv] {
			rest = append(rest, nodes...)
		}
	}
	if m := sides(rest, ui.Rect{R: s.Width, B: s.Height}); !m.empty() {
		l.More = &m
	}
	if len(wins) == 1 {
		l.Blocks = wins[0].Children
	} else {
		l.Blocks = wins
	}
}

func writeBlocks(b *strings.Builder, blocks []*Block, depth int) {
	for _, k := range blocks {
		b.WriteString(strings.Repeat("  ", depth))
		var line string
		switch {
		case k.e != nil:
			line = k.e.Line()
			if k.e.New {
				line = "*" + line
			}
			if !k.More.empty() {
				line += " (" + k.More.String() + ")"
			}
		case k.Window != "":
			line = k.Window + ":"
		default:
			line = k.Layout
			if k.Layout == "grid" {
				line = fmt.Sprintf("grid %d cols", k.Cols)
			}
			if k.ID != "" {
				line += " #" + k.ID
			}
			if k.Scrollable {
				line += " scrollable"
			}
			if !k.More.empty() {
				line += " (" + k.More.String() + ")"
			}
			line += ":"
		}
		b.WriteString(line)
		b.WriteByte('\n')
		writeBlocks(b, k.Children, depth+1)
	}
}

// writeOutline prints the elements as an outline; false if there is none.
func (l *Listing) writeOutline(b *strings.Builder) bool {
	if l.Blocks == nil {
		return false
	}
	writeBlocks(b, l.Blocks, 0)
	if !l.More.empty() {
		b.WriteString("(" + l.More.String() + ")\n")
	}
	return true
}

// MarkNew flags the elements that were not on prev, the screen last shown. Nothing is
// flagged on a first look, in another app, or when everything is new.
func (l *Listing) MarkNew(prev *Listing) {
	for _, e := range l.Elements {
		e.New = false
	}
	if prev == nil || prev.App != l.App {
		return
	}
	old := map[int]bool{}
	for _, e := range prev.Elements {
		old[e.Ref] = true
	}
	n := 0
	for _, e := range l.Elements {
		if !old[e.Ref] {
			e.New = true
			n++
		}
	}
	if n == len(l.Elements) {
		for _, e := range l.Elements {
			e.New = false
		}
	}
}
