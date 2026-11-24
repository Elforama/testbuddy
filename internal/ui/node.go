package ui

import (
	"fmt"
	"strings"
)

type Rect struct{ L, T, R, B int }

func (r Rect) W() int      { return r.R - r.L }
func (r Rect) H() int      { return r.B - r.T }
func (r Rect) CX() int     { return (r.L + r.R) / 2 }
func (r Rect) CY() int     { return (r.T + r.B) / 2 }
func (r Rect) Empty() bool { return r.R <= r.L || r.B <= r.T }
func (r Rect) Area() int {
	if r.Empty() {
		return 0
	}
	return r.W() * r.H()
}
func (r Rect) Contains(x, y int) bool { return x >= r.L && x < r.R && y >= r.T && y < r.B }
func (r Rect) String() string         { return fmt.Sprintf("[%d,%d][%d,%d]", r.L, r.T, r.R, r.B) }

func (r Rect) Intersect(o Rect) Rect {
	out := Rect{max(r.L, o.L), max(r.T, o.T), min(r.R, o.R), min(r.B, o.B)}
	if out.Empty() {
		return Rect{}
	}
	return out
}

func (r Rect) Overlaps(o Rect) bool { return !r.Intersect(o).Empty() }

type Coll struct{ Rows, Cols int }

type Node struct {
	Text  string
	Desc  string
	ID    string
	Hint  string
	Value string
	Error string
	State string
	Class string

	Bounds        Rect
	PreciseBounds *[4]float64

	Clickable, LongClickable, Enabled, Focused, Checked, Checkable, Selected bool
	Scrollable, Password, Editable, Visible, ShowingHint                     bool

	MoreAfter, MoreBefore bool
	Coll, Item            *Coll

	DrawOrder      int
	DrawOrderKnown bool

	Covers []Hit

	Modal, UnderModal bool

	Window   int
	Depth    int
	Index    int
	Parent   *Node
	Children []*Node

	Ref int
}

type Hit struct {
	Point  int
	X, Y   int
	By     *Node
	Layers []HitLayer
}

type HitLayer struct {
	Node *Node
	Kind string
}

func (n *Node) Label() string {
	if n.Text != "" && !(n.ShowingHint && n.Text == n.Hint) {
		return n.Text
	}
	return n.Desc
}

func (n *Node) ShortClass() string {
	c := n.Class
	if i := strings.LastIndexAny(c, "."); i >= 0 {
		c = c[i+1:]
	}
	return c
}

func (n *Node) IsDescendantOf(anc *Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p == anc {
			return true
		}
	}
	return false
}

func (n *Node) Walk(fn func(*Node) bool) {
	if !fn(n) {
		return
	}
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

type Window struct {
	ID      int
	Type    string
	Package string
	Title   string
	Active  bool
	Focused bool
	Bounds  Rect
	Nodes   []*Node
	Roots   []*Node
}

type Screen struct {
	Platform string
	Width    int
	Height   int
	Seq      int64
	Read     int64
	Keyboard bool
	Unread   int
	Windows  []*Window
	Nodes    []*Node
}

func (s *Screen) Foreground() string {
	for _, w := range s.Windows {
		if w.Active && w.Type == "app" {
			return w.Package
		}
	}
	for _, w := range s.Windows {
		if w.Type == "app" {
			return w.Package
		}
	}
	return ""
}

func (s *Screen) Link() {
	s.Nodes = s.Nodes[:0]
	for wi, w := range s.Windows {
		var stack []*Node
		w.Roots = nil
		for _, n := range w.Nodes {
			n.Window = wi
			n.Children = nil
			for len(stack) > 0 && stack[len(stack)-1].Depth >= n.Depth {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				n.Parent = nil
				w.Roots = append(w.Roots, n)
			} else {
				p := stack[len(stack)-1]
				n.Parent = p
				p.Children = append(p.Children, n)
			}
			stack = append(stack, n)
			n.Index = len(s.Nodes)
			s.Nodes = append(s.Nodes, n)
		}
	}
}

func (s *Screen) Signature() uint64 {
	h := uint64(14695981039346656037)
	mix := func(str string) {
		for i := 0; i < len(str); i++ {
			h ^= uint64(str[i])
			h *= 1099511628211
		}
		h ^= 0xff
		h *= 1099511628211
	}
	for _, n := range s.Nodes {
		mix(n.Class)
		mix(n.Text)
		mix(n.Desc)
		mix(n.ID)
		mix(n.Bounds.String())
		flags := 0
		for i, b := range []bool{n.Enabled, n.Checked, n.Selected, n.Focused} {
			if b {
				flags |= 1 << i
			}
		}
		mix(string(rune('a' + flags)))
	}
	return h
}

func (n *Node) ClipRect(screen Rect) Rect {
	r := screen
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && p.Parent != nil && !p.Bounds.Empty() {
			r = r.Intersect(p.Bounds)
		}
	}
	return r
}
