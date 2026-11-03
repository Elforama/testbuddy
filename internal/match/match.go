// Package match finds the element a selector names on a screen, with the flow format's
// exact semantics: full-match, case-insensitive
// Java-style regexes over text/hint/accessibility text/error; deepest matches; relative
// filters intersected in hierarchy order; index by position or clickable-first.
package match

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dlclark/regexp2/v2"

	"github.com/elforama/testbuddy/internal/ui"
)

// Selector is a selector with every ${...} already evaluated.
type Selector struct {
	Text, ID, Index, CSS string

	Width, Height, Tolerance *int
	Enabled, Selected        *bool
	Checked, Focused         *bool
	Traits                   []string

	Below, Above, LeftOf, RightOf *Selector
	ContainsChild, ChildOf        *Selector
	ContainsDescendants           []*Selector

	// CSSNodes are the screen's node indices the CSS selector matched (web only; filled by
	// the engine from the page before matching). nil with CSS set matches nothing.
	CSSNodes map[int]bool
}

// Walk visits the selector and every nested selector.
func (s *Selector) Walk(fn func(*Selector)) {
	if s == nil {
		return
	}
	fn(s)
	for _, n := range []*Selector{s.Below, s.Above, s.LeftOf, s.RightOf, s.ContainsChild, s.ChildOf} {
		n.Walk(fn)
	}
	for _, n := range s.ContainsDescendants {
		n.Walk(fn)
	}
}

// Describe renders the selector for messages ("Text matching regex: Login").
func (s *Selector) Describe() string {
	var parts []string
	if s.Text != "" {
		parts = append(parts, fmt.Sprintf("%q", s.Text))
	}
	if s.ID != "" {
		parts = append(parts, "id: "+s.ID)
	}
	if s.CSS != "" {
		parts = append(parts, "css: "+s.CSS)
	}
	if s.Width != nil || s.Height != nil {
		parts = append(parts, fmt.Sprintf("size: %vx%v", deref(s.Width), deref(s.Height)))
	}
	for _, f := range []struct {
		n string
		b *bool
	}{{"enabled", s.Enabled}, {"selected", s.Selected}, {"checked", s.Checked}, {"focused", s.Focused}} {
		if f.b != nil {
			parts = append(parts, fmt.Sprintf("%s: %v", f.n, *f.b))
		}
	}
	if len(s.Traits) > 0 {
		parts = append(parts, "traits: "+strings.Join(s.Traits, " "))
	}
	for _, r := range []struct {
		n string
		s *Selector
	}{{"below", s.Below}, {"above", s.Above}, {"left of", s.LeftOf}, {"right of", s.RightOf},
		{"containing child", s.ContainsChild}, {"child of", s.ChildOf}} {
		if r.s != nil {
			parts = append(parts, r.n+" "+r.s.Describe())
		}
	}
	for _, d := range s.ContainsDescendants {
		parts = append(parts, "containing "+d.Describe())
	}
	if s.Index != "" {
		parts = append(parts, "index: "+s.Index)
	}
	if len(parts) == 0 {
		return "any element"
	}
	return strings.Join(parts, ", ")
}

func deref(p *int) any {
	if p == nil {
		return "*"
	}
	return *p
}

// ---- regex ----

var (
	reMu    sync.Mutex
	reCache = map[string]*regexp2.Regexp{}
)

const reOpts = regexp2.IgnoreCase | regexp2.Singleline | regexp2.Multiline | regexp2.Unicode

// compile returns a full-match regex for pattern, or an escaped literal when the pattern
// does not compile.
func compile(pattern string) *regexp2.Regexp {
	reMu.Lock()
	defer reMu.Unlock()
	if re, ok := reCache[pattern]; ok {
		return re
	}
	re, err := regexp2.Compile(`\A(?:`+pattern+`)\z`, reOpts)
	if err != nil {
		re = regexp2.MustCompile(`\A(?:`+regexp2.Escape(pattern)+`)\z`, reOpts)
	}
	reCache[pattern] = re
	return re
}

func full(re *regexp2.Regexp, s string) bool {
	ok, _ := re.MatchString(s)
	return ok
}

// TextMatches is the format's text rule for one attribute value.
func TextMatches(pattern, value string) bool {
	re := compile(pattern)
	if full(re, value) || pattern == value {
		return true
	}
	if strings.Contains(value, "\n") {
		v := strings.ReplaceAll(value, "\n", " ")
		return full(re, v) || pattern == v
	}
	return false
}

func nodeTextMatches(pattern string, n *ui.Node) bool {
	for _, v := range []string{n.Text, n.Hint, n.Desc, n.Error} {
		if v != "" && TextMatches(pattern, v) {
			return true
		}
	}
	return false
}

func idMatches(pattern string, n *ui.Node) bool {
	if n.ID == "" {
		return false
	}
	re := compile(pattern)
	if full(re, n.ID) {
		return true
	}
	if i := strings.LastIndex(n.ID, "/"); i >= 0 {
		return full(re, n.ID[i+1:])
	}
	return false
}

// ---- visibility ----

// Visible prunes nodes less than 10% on screen that have no surviving children
// (out-of-bounds filtering). It returns the kept nodes in pre-order.
func Visible(s *ui.Screen) []*ui.Node {
	screen := ui.Rect{R: s.Width, B: s.Height}
	keep := make(map[*ui.Node]bool, len(s.Nodes))
	var visit func(n *ui.Node) bool
	visit = func(n *ui.Node) bool {
		child := false
		for _, c := range n.Children {
			if visit(c) {
				child = true
			}
		}
		k := child || VisibleShare(n.Bounds, screen) >= 0.1
		if k {
			keep[n] = true
		}
		return k
	}
	for _, w := range s.Windows {
		for _, r := range w.Roots {
			visit(r)
		}
	}
	out := make([]*ui.Node, 0, len(keep))
	for _, n := range s.Nodes {
		if keep[n] {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return s.Nodes
	}
	return out
}

// VisibleShare is the fraction of r that lies on screen. A rectangle with exactly one zero
// dimension counts as visible (the reference computes NaN there, which passes the check).
func VisibleShare(r, screen ui.Rect) float64 {
	area := r.W() * r.H()
	if area == 0 {
		if r.W() == 0 && r.H() == 0 {
			return 0
		}
		return 1
	}
	if area < 0 {
		return 0
	}
	return float64(r.Intersect(screen).Area()) / float64(area)
}

// ---- filtering ----

type finder struct {
	all []*ui.Node // candidates: visible nodes in pre-order
	pos map[*ui.Node]int
}

// Find returns the element the selector picks, or nil. It also returns every candidate
// match in pick order (for diagnostics).
func Find(s *ui.Screen, sel *Selector) (*ui.Node, []*ui.Node, error) {
	f := &finder{all: Visible(s)}
	f.pos = make(map[*ui.Node]int, len(f.all))
	for i, n := range f.all {
		f.pos[n] = i
	}
	cands := f.all
	if sel.ChildOf != nil {
		parent, _, err := f.pick(f.all, sel.ChildOf)
		if err != nil {
			return nil, nil, err
		}
		if parent == nil {
			return nil, nil, nil
		}
		cands = f.subtree(parent)
	}
	matches, err := f.filter(cands, sel)
	if err != nil {
		return nil, nil, err
	}
	matches, err = order(matches, sel)
	if err != nil || len(matches) == 0 {
		return nil, matches, err
	}
	first := matches[0]
	if first.Bounds.Empty() && first.Bounds.W() == 0 && first.Bounds.H() == 0 {
		return nil, matches, nil
	}
	return first, matches, nil
}

func (f *finder) subtree(parent *ui.Node) []*ui.Node {
	var out []*ui.Node
	for _, n := range f.all {
		if n.IsDescendantOf(parent) {
			out = append(out, n)
		}
	}
	return out
}

// pick resolves a nested selector to its single first match.
func (f *finder) pick(cands []*ui.Node, sel *Selector) (*ui.Node, []*ui.Node, error) {
	if sel.ChildOf != nil {
		parent, _, err := f.pick(f.all, sel.ChildOf)
		if err != nil || parent == nil {
			return nil, nil, err
		}
		cands = f.subtree(parent)
	}
	m, err := f.filter(cands, sel)
	if err != nil {
		return nil, nil, err
	}
	m, err = order(m, sel)
	if err != nil || len(m) == 0 {
		return nil, m, err
	}
	return m[0], m, nil
}

// filter applies basic then relative filters, keeping hierarchy order.
func (f *finder) filter(cands []*ui.Node, sel *Selector) ([]*ui.Node, error) {
	basic := f.basic(cands, sel)
	for _, rel := range []struct {
		anchor *Selector
		pred   func(n, a *ui.Node) bool
	}{
		{sel.Below, func(n, a *ui.Node) bool { return n.Bounds.T > a.Bounds.T }},
		{sel.Above, func(n, a *ui.Node) bool { return n.Bounds.T < a.Bounds.T }},
		{sel.RightOf, func(n, a *ui.Node) bool { return n.Bounds.L > a.Bounds.L }},
		{sel.LeftOf, func(n, a *ui.Node) bool { return n.Bounds.L < a.Bounds.L }},
	} {
		if rel.anchor == nil {
			continue
		}
		anchors, err := f.anchors(rel.anchor)
		if err != nil {
			return nil, err
		}
		basic = keep(basic, func(n *ui.Node) bool {
			for _, a := range anchors {
				if a != n && rel.pred(n, a) {
					return true
				}
			}
			return false
		})
	}
	if sel.ContainsChild != nil {
		kids, err := f.filter(f.all, sel.ContainsChild)
		if err != nil {
			return nil, err
		}
		set := toSet(kids)
		basic = keep(basic, func(n *ui.Node) bool {
			for _, c := range n.Children {
				if set[c] {
					return true
				}
			}
			return false
		})
	}
	for _, d := range sel.ContainsDescendants {
		desc, err := f.filter(f.all, d)
		if err != nil {
			return nil, err
		}
		set := toSet(desc)
		basic = keep(basic, func(n *ui.Node) bool {
			found := false
			for _, c := range n.Children {
				c.Walk(func(x *ui.Node) bool {
					if set[x] {
						found = true
					}
					return !found
				})
				if found {
					break
				}
			}
			return found
		})
	}
	return basic, nil
}

// anchors are all matches of a relative selector (only the indexed one if it has an index).
func (f *finder) anchors(sel *Selector) ([]*ui.Node, error) {
	m, err := f.filter(f.all, sel)
	if err != nil {
		return nil, err
	}
	if sel.Index != "" {
		m, err = order(m, sel)
		if err != nil || len(m) == 0 {
			return nil, err
		}
		return m[:1], nil
	}
	return m, nil
}

func hasBasic(sel *Selector) bool {
	return sel.Text != "" || sel.ID != "" || sel.Width != nil || sel.Height != nil || len(sel.Traits) > 0 ||
		sel.Enabled != nil || sel.Selected != nil || sel.Checked != nil || sel.Focused != nil || sel.CSS != ""
}

// basic applies the attribute filters and keeps the deepest matches.
func (f *finder) basic(cands []*ui.Node, sel *Selector) []*ui.Node {
	if !hasBasic(sel) {
		return cands
	}
	match := map[*ui.Node]bool{}
	var list []*ui.Node
	for _, n := range cands {
		if sel.Text != "" && !nodeTextMatches(sel.Text, n) {
			continue
		}
		if sel.ID != "" && !idMatches(sel.ID, n) {
			continue
		}
		if sel.CSS != "" && !sel.CSSNodes[n.Index] {
			continue
		}
		tol := 0
		if sel.Tolerance != nil {
			tol = *sel.Tolerance
		}
		if sel.Width != nil && abs(n.Bounds.W()-*sel.Width) > tol {
			continue
		}
		if sel.Height != nil && abs(n.Bounds.H()-*sel.Height) > tol {
			continue
		}
		if sel.Enabled != nil && n.Enabled != *sel.Enabled {
			continue
		}
		if sel.Selected != nil && n.Selected != *sel.Selected {
			continue
		}
		if sel.Checked != nil && n.Checked != *sel.Checked {
			continue
		}
		if sel.Focused != nil && n.Focused != *sel.Focused {
			continue
		}
		if !traitsMatch(sel.Traits, n) {
			continue
		}
		match[n] = true
		list = append(list, n)
	}
	// deepest: drop matches that have a matching descendant
	var out []*ui.Node
	for _, n := range list {
		deeper := false
		for _, c := range n.Children {
			c.Walk(func(x *ui.Node) bool {
				if match[x] {
					deeper = true
				}
				return !deeper
			})
			if deeper {
				break
			}
		}
		if !deeper {
			out = append(out, n)
		}
	}
	return out
}

func traitsMatch(traits []string, n *ui.Node) bool {
	for _, t := range traits {
		switch t {
		case "text":
			if n.Text == "" && n.Desc == "" {
				return false
			}
		case "long-text":
			if len([]rune(n.Text)) <= 200 {
				return false
			}
		case "square":
			if n.Bounds.H() == 0 || math.Abs(1-float64(n.Bounds.W())/float64(n.Bounds.H())) >= 0.03 {
				return false
			}
		}
	}
	return true
}

// order picks by index (position order) or clickable-first.
func order(m []*ui.Node, sel *Selector) ([]*ui.Node, error) {
	if sel.Index != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(sel.Index), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, &InvalidIndex{Value: sel.Index}
		}
		idx := int(f)
		sorted := append([]*ui.Node(nil), m...)
		sort.SliceStable(sorted, func(i, j int) bool {
			a, b := sorted[i].Bounds, sorted[j].Bounds
			ae, be := a == (ui.Rect{}), b == (ui.Rect{})
			if ae != be {
				return !ae
			}
			if a.T != b.T {
				return a.T < b.T
			}
			return a.L < b.L
		})
		if idx < 0 {
			idx = len(sorted) + idx
		}
		if idx < 0 || idx >= len(sorted) {
			return nil, nil
		}
		return []*ui.Node{sorted[idx]}, nil
	}
	out := append([]*ui.Node(nil), m...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Clickable && !out[j].Clickable })
	return out, nil
}

// InvalidIndex is a bad index value; it always fails the command, even an optional one.
type InvalidIndex struct{ Value string }

func (e *InvalidIndex) Error() string { return fmt.Sprintf("invalid index %q", e.Value) }

func keep(ns []*ui.Node, pred func(*ui.Node) bool) []*ui.Node {
	var out []*ui.Node
	for _, n := range ns {
		if pred(n) {
			out = append(out, n)
		}
	}
	return out
}

func toSet(ns []*ui.Node) map[*ui.Node]bool {
	m := make(map[*ui.Node]bool, len(ns))
	for _, n := range ns {
		m[n] = true
	}
	return m
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
