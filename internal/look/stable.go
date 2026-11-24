package look

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

const (
	maxAncestors     = 6
	maxChildAnchors  = 3
	maxAboveAnchors  = 5
	maxAboveExamined = 12
)

func related(m, n *ui.Node) bool { return m == n || n.IsDescendantOf(m) || m.IsDescendantOf(n) }

func findsAt(s *ui.Screen, sel *match.Selector, n *ui.Node) (i int, alone, ok bool) {
	_, all, err := match.Find(s, sel)
	if err != nil {
		return 0, false, false
	}
	at := -1
	alone = true
	for j, m := range all {
		if !related(m, n) {
			alone = false
		} else if at < 0 {
			at = j
		}
	}
	if at < 0 {
		return 0, false, false
	}
	return at, alone, true
}

func only(s *ui.Screen, sel *match.Selector, n *ui.Node) bool {
	_, all, err := match.Find(s, sel)
	return err == nil && len(all) == 1 && all[0] == n
}

func render(sel *match.Selector) string {
	var parts []string
	if sel.ID != "" {
		parts = append(parts, "id: "+strconv.Quote(sel.ID))
	}
	if sel.Text != "" {
		parts = append(parts, "text: "+strconv.Quote(sel.Text))
	}
	if sel.ContainsChild != nil {
		parts = append(parts, "containsChild: "+render(sel.ContainsChild))
	}
	if sel.Below != nil {
		parts = append(parts, "below: "+render(sel.Below))
	}
	if sel.ChildOf != nil {
		parts = append(parts, "childOf: "+render(sel.ChildOf))
	}
	if sel.Index != "" {
		parts = append(parts, "index: "+sel.Index)
	}
	if len(parts) == 1 && sel.Text != "" {
		return strconv.Quote(sel.Text)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func nodeLabel(n *ui.Node) string {
	if n.Editable {
		return ""
	}
	if n.Text != "" && !n.ShowingHint {
		return n.Text
	}
	return n.Desc
}

func containerAnchors(s *ui.Screen, a, n *ui.Node) []*match.Selector {
	var out []*match.Selector
	if a.ID != "" {
		if sel := (&match.Selector{ID: regexp.QuoteMeta(ShortID(a.ID))}); only(s, sel, a) {
			out = append(out, sel)
		}
	}
	if l := nodeLabel(a); l != "" {
		if sel := (&match.Selector{Text: regexp.QuoteMeta(l)}); only(s, sel, a) {
			out = append(out, sel)
		}
	}
	tried := 0
	for _, c := range a.Children {
		if tried == maxChildAnchors {
			break
		}
		l := nodeLabel(c)
		if l == "" || related(c, n) {
			continue
		}
		tried++
		title := &match.Selector{Text: regexp.QuoteMeta(l)}
		if !only(s, title, c) {
			continue
		}
		if sel := (&match.Selector{ContainsChild: title}); only(s, sel, a) {
			out = append(out, sel)
		}
	}
	return out
}

func placed(s *ui.Screen, n *ui.Node, bases []*match.Selector) *match.Selector {
	alone := func(sel *match.Selector) bool {
		_, alone, ok := findsAt(s, sel, n)
		return ok && alone
	}
	first := func(sel *match.Selector) bool {
		i, _, ok := findsAt(s, sel, n)
		return ok && i == 0
	}
	a := n.Parent
	for depth := 0; a != nil && depth < maxAncestors; depth, a = depth+1, a.Parent {
		for _, anchor := range containerAnchors(s, a, n) {
			for _, b := range bases {
				sel := *b
				sel.ChildOf = anchor
				if alone(&sel) {
					return &sel
				}
			}
		}
	}
	for _, anchor := range titlesAbove(s, n) {
		for _, b := range bases {
			sel := *b
			sel.Below = anchor
			if first(&sel) {
				return &sel
			}
		}
	}
	return nil
}

func titlesAbove(s *ui.Screen, n *ui.Node) []*match.Selector {
	var cands []*ui.Node
	for _, a := range s.Nodes {
		if a.Bounds.T < n.Bounds.T && !related(a, n) && nodeLabel(a) != "" && !a.Bounds.Empty() {
			cands = append(cands, a)
		}
	}
	gap := func(a *ui.Node) int { return abs(n.Bounds.T - a.Bounds.B) }
	sort.SliceStable(cands, func(i, j int) bool { return gap(cands[i]) < gap(cands[j]) })
	var out []*match.Selector
	for i, a := range cands {
		if i == maxAboveExamined || len(out) == maxAboveAnchors {
			break
		}
		if sel := (&match.Selector{Text: regexp.QuoteMeta(nodeLabel(a))}); only(s, sel, a) {
			out = append(out, sel)
		}
	}
	return out
}

func actionableNode(n *ui.Node) bool {
	return n.Clickable || n.LongClickable || n.Editable || n.Checkable
}

func soleInContainer(s *ui.Screen, n *ui.Node) *match.Selector {
	a := n.Parent
	for depth := 0; a != nil && depth < maxAncestors; depth, a = depth+1, a.Parent {
		alone := true
		a.Walk(func(d *ui.Node) bool {
			if d != a && actionableNode(d) && !related(d, n) {
				alone = false
			}
			return alone
		})
		if !alone {
			return nil
		}
		for _, anchor := range containerAnchors(s, a, n) {
			sel := &match.Selector{ChildOf: anchor}
			if got, _, err := match.Find(s, sel); err == nil && got != nil && (got == n || got.IsDescendantOf(n)) {
				return sel
			}
		}
	}
	return nil
}
