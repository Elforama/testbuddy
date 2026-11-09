package look

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// Stable selectors: ways to single out an element that its own id or text does not,
// without a match index (which breaks as soon as a row is added above it). Every candidate
// is checked with the matcher on the screen it was built from.

const (
	maxAncestors     = 6  // containers tried, nearest first
	maxChildAnchors  = 3  // labelled children tried per container
	maxAboveAnchors  = 5  // unique titles above the element tried, nearest first
	maxAboveExamined = 12 // labelled nodes above examined for uniqueness
)

// related: m is n, or one holds the other (a selector that finds either taps the element).
func related(m, n *ui.Node) bool { return m == n || n.IsDescendantOf(m) || m.IsDescendantOf(n) }

// findsAt is the position of n (or a node related to it) among sel's matches on s.
func findsAt(s *ui.Screen, sel *match.Selector, n *ui.Node) (int, bool) {
	_, all, err := match.Find(s, sel)
	if err != nil {
		return 0, false
	}
	for i, m := range all {
		if related(m, n) {
			return i, true
		}
	}
	return 0, false
}

// only reports whether sel finds exactly one node on s, and that it is n.
func only(s *ui.Screen, sel *match.Selector, n *ui.Node) bool {
	_, all, err := match.Find(s, sel)
	return err == nil && len(all) == 1 && all[0] == n
}

// render writes a selector as flow-style YAML: a bare text as a quoted string, else a map
// with its keys in a fixed order.
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

// nodeLabel is the text a node is found by, or "" for none or one that changes (a field's
// typed text).
func nodeLabel(n *ui.Node) string {
	if n.Editable {
		return ""
	}
	if n.Text != "" && !n.ShowingHint {
		return n.Text
	}
	return n.Desc
}

// containerAnchors are selectors that find container a and nothing else: its id, its
// label, or a uniquely labelled child it holds (not n's side of it).
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

// placed finds the element by one of its own selectors (bases) narrowed to a container
// (childOf), else to below a unique title above it. It returns nil when neither picks the
// element first.
func placed(s *ui.Screen, n *ui.Node, bases []*match.Selector) *match.Selector {
	first := func(sel *match.Selector) bool {
		i, ok := findsAt(s, sel, n)
		return ok && i == 0
	}
	a := n.Parent
	for depth := 0; a != nil && depth < maxAncestors; depth, a = depth+1, a.Parent {
		for _, anchor := range containerAnchors(s, a, n) {
			for _, b := range bases {
				sel := *b
				sel.ChildOf = anchor
				if first(&sel) {
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

// titlesAbove are selectors for unique labelled nodes above n (their tops above n's top,
// which is what `below` compares), nearest first.
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

// actionableNode is a node people tap or type in.
func actionableNode(n *ui.Node) bool {
	return n.Clickable || n.LongClickable || n.Editable || n.Checkable
}

// soleInContainer finds an element with nothing of its own to find it by (no id, no text)
// as the only actionable node in a container that can be singled out: `{childOf: …}`.
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
			return nil // a wider container holds the others too
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
