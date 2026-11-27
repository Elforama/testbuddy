package look

import (
	"strings"

	"github.com/elforama/testbuddy/internal/ui"
)

type Row struct {
	Text string `json:"text"`
	Cut  bool   `json:"-"`
}

func (l *Listing) mainScroll(screen ui.Rect) (*Block, ui.Rect) {
	var best *Block
	var bestView ui.Rect
	var walk func([]*Block)
	walk = func(bs []*Block) {
		for _, b := range bs {
			var box ui.Rect
			scrolls, dir, sideways := false, b.Scroll, b.Layout == "row"
			if b.e != nil && b.e.Node.Scrollable {
				scrolls, dir, box = true, l.scrollDir(b.e.Node), b.e.Node.Bounds
				sideways = len(b.Children) == 1 && b.Children[0].e == nil && b.Children[0].Layout == "row"
			} else if b.e == nil && b.Scrollable {
				scrolls, box = true, b.box
			}
			view := box.Intersect(screen)
			if scrolls && dir != "horizontal" && (dir == "vertical" || !sideways) && view.Area() > bestView.Area() {
				best, bestView = b, view
			}
			walk(b.Children)
		}
	}
	walk(l.Blocks)
	if best == nil {
		return nil, screen
	}
	return best, bestView
}

func (l *Listing) rowBlocks(list *Block) []*Block {
	var rows []*Block
	if list != nil {
		rows = list.Children
	} else {
		for _, b := range l.Blocks {
			if b.Window != "" {
				rows = append(rows, b.Children...)
			} else {
				rows = append(rows, b)
			}
		}
	}
	for len(rows) == 1 && rows[0].e == nil && rows[0].Layout != "row" && !rows[0].Scrollable {
		rows = rows[0].Children
	}
	return rows
}

func blockElements(b *Block, into []*Element) []*Element {
	if b.e != nil {
		into = append(into, b.e)
	}
	for _, c := range b.Children {
		into = blockElements(c, into)
	}
	return into
}

func (l *Listing) Rows() ([]Row, ui.Rect) {
	list, view := l.mainScroll(ui.Rect{R: l.Width, B: l.Height})
	var rows [][]*Element
	holds := map[*ui.Node]int{}
	for _, b := range l.rowBlocks(list) {
		if els := blockElements(b, nil); len(els) > 0 {
			rows = append(rows, els)
			for p := els[0].Node.Parent; p != nil; p = p.Parent {
				holds[p]++
			}
		}
	}
	atEdge := func(b ui.Rect) bool { return b.T <= view.T || b.B >= view.B }
	out := make([]Row, 0, len(rows))
	for _, els := range rows {
		parts := make([]string, len(els))
		cut := false
		for i, e := range els {
			parts[i] = e.contents
			cut = cut || atEdge(e.Node.Bounds)
		}
		for p := els[0].Node.Parent; p != nil && !cut; p = p.Parent {
			cut = holds[p] == 1 && atEdge(p.Bounds)
		}
		out = append(out, Row{Text: strings.Join(parts, "  "), Cut: cut})
	}
	return out, view
}

func sharedRows(before, after []Row) int {
	for k := min(len(before), len(after)); k > 0; k-- {
		same := true
		for i := 0; i < k && same; i++ {
			same = before[len(before)-k+i].Text == after[i].Text
		}
		if same {
			return k
		}
	}
	return 0
}

func JoinRows(before, after []Row) ([]Row, bool) {
	if len(before) == 0 {
		return after, false
	}
	b, a := before, after
	for len(b) > 0 && b[len(b)-1].Cut {
		b = b[:len(b)-1]
	}
	for len(a) > 0 && a[0].Cut {
		a = a[1:]
	}
	if k := sharedRows(b, a); k > 0 {
		return append(b[:len(b):len(b)], a[k:]...), true
	}
	k := sharedRows(before, after)
	return append(before[:len(before):len(before)], after[k:]...), k > 0
}
