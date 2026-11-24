package look

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elforama/testbuddy/internal/ui"
)

type item struct {
	e      *Element
	node   *ui.Node
	kids   []*item
	box    ui.Rect
	scroll *ui.Node
	row    bool
}

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
		kids[0].scroll = n
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

func arrange(kids []*item) (layout string, cols int, entries []*item) {
	if len(kids) < 2 {
		return "column", 0, kids
	}
	sorted := append([]*item(nil), kids...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].box.T < sorted[j].box.T })
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
	isRow := make([]bool, len(bands))
	for i, b := range bands {
		sort.SliceStable(b, func(x, y int) bool { return b[x].box.L < b[y].box.L })
		isRow[i] = len(b) > 1
		for j := 1; j < len(b); j++ {
			w := max(1, min(b[j].box.W(), b[j-1].box.W()))
			if overlap(b[j-1].box.L, b[j-1].box.R, b[j].box.L, b[j].box.R)*3 > w {
				isRow[i] = false
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

type Block struct {
	Ref        int      `json:"ref,omitempty"`
	Window     string   `json:"window,omitempty"`
	Layout     string   `json:"layout,omitempty"`
	Cols       int      `json:"cols,omitempty"`
	ID         string   `json:"id,omitempty"`
	Scrollable bool     `json:"scrollable,omitempty"`
	Scroll     string   `json:"scroll,omitempty"`
	More       *More    `json:"more,omitempty"`
	Children   []*Block `json:"children,omitempty"`
	e          *Element
	box        ui.Rect
}

type More struct {
	Above int `json:"above,omitempty"`
	Below int `json:"below,omitempty"`
	Left  int `json:"left,omitempty"`
	Right int `json:"right,omitempty"`
}

func (m *More) empty() bool { return m == nil || *m == More{} }

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
	hidden  map[*ui.Node][]*ui.Node
	items   map[*ui.Node][]*ui.Coll
	shown   map[*ui.Node]bool
}

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
	if h, ok := o.l.hints[sv]; ok {
		for _, side := range []struct{ got, hint *int }{{&m.Above, &h.Above}, {&m.Below, &h.Below}, {&m.Left, &h.Left}, {&m.Right, &h.Right}} {
			if *side.got == 0 && *side.hint < 0 {
				*side.got = -1
			}
		}
	}
	if o.l.Platform == "android" {
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
	b := &Block{Layout: layout, ID: id, Children: kids, box: g.box}
	if g.scroll != nil {
		b.box = g.scroll.Bounds
	}
	if layout == "grid" {
		b.Cols = cols
	}
	if g.scroll != nil {
		b.Scrollable = true
		b.Scroll = o.l.scrollDir(g.scroll)
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
	off := map[*ui.Node]bool{}
	for _, n := range offscreen {
		off[n] = true
	}
	for _, n := range offscreen {
		inside, listed := false, true
		for p := n.Parent; p != nil && !inside; p = p.Parent {
			inside = off[p] || (listed && o.kept[p] != nil && !p.Scrollable)
			if p.Scrollable {
				listed = false
			}
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
			blocks = append([]*Block{{Ref: root.e.Ref, e: root.e}}, o.group(&item{kids: root.kids}, "column")...)
		} else {
			blocks = o.item(root, "column")
		}
		name := w.Type
		if name == "system" {
			name = "dialog"
		}
		wins = append(wins, &Block{Window: name, Children: blocks})
	}
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

type scrollArea struct {
	name string
	line string
	more string
	box  ui.Rect
}

func (l *Listing) scrollAreas() []scrollArea {
	var out []scrollArea
	var walk func([]*Block)
	walk = func(bs []*Block) {
		for _, b := range bs {
			a := scrollArea{}
			switch {
			case b.e != nil && b.e.Node.Scrollable:
				a = scrollArea{name: fmt.Sprintf("@%d", b.e.Ref), line: b.e.Line(), box: b.e.Node.Bounds}
			case b.e == nil && b.Scrollable:
				a = scrollArea{line: b.Layout + " scrollable", box: b.box}
				if b.ID != "" {
					a.name, a.line = "#"+b.ID, b.Layout+" #"+b.ID+" scrollable"
				}
			default:
				walk(b.Children)
				continue
			}
			if !b.More.empty() {
				a.more = b.More.String()
			}
			out = append(out, a)
			walk(b.Children)
		}
	}
	walk(l.Blocks)
	screen := scrollArea{name: "screen", line: "screen"}
	if !l.More.empty() {
		screen.more = l.More.String()
	}
	return append(out, screen)
}

func moreChanges(prev, l *Listing) []string {
	was := prev.scrollAreas()
	var out []string
	for _, a := range l.scrollAreas() {
		var match *scrollArea
		best := 0
		for i := range was {
			w := &was[i]
			switch {
			case a.name != "" && w.name == a.name:
				match, best = w, 1<<62
			case a.name == "" && w.name == "":
				if o := a.box.Intersect(w.box).Area(); o > best {
					match, best = w, o
				}
			}
		}
		if match == nil || match.more == a.more {
			continue
		}
		m := a.more
		if m == "" {
			m = "nothing more out of view"
		}
		out = append(out, "~ "+a.line+" ("+m+")")
	}
	return out
}

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
