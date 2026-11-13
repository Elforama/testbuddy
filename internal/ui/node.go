// Package ui is the platform-neutral screen model: a tree of nodes read from a device.
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

// Coll is a list's size (Rows, Cols; -1 unknown) or an item's row and column in one.
type Coll struct{ Rows, Cols int }

// Node is one element on screen.
type Node struct {
	Text  string // Android text / iOS title or value
	Desc  string // Android content-description / iOS label (accessibilityText in selectors)
	ID    string // Android resource-id / iOS accessibilityIdentifier
	Hint  string // placeholder
	Value string // iOS value
	Error string
	State string // Android stateDescription
	Class string // Android class name / iOS element type

	Bounds        Rect
	PreciseBounds *[4]float64 // optional sub-point edges (left, top, right, bottom)

	Clickable, LongClickable, Enabled, Focused, Checked, Checkable, Selected bool
	Scrollable, Password, Editable, Visible, ShowingHint                     bool

	// Android scroll containers: whether it can scroll further (forward = down or right),
	// and a list's size / an item's row and column in it, when the app reports them.
	MoreAfter, MoreBefore bool
	Coll, Item            *Coll

	// DrawOrder is the node's place among its siblings as drawn (Android: later is drawn on
	// top, elevation and custom drawing order counted), when the platform reports it
	// (DrawOrderKnown). The tree's order is by place on screen, not as drawn.
	DrawOrder      int
	DrawOrderKnown bool

	Window   int // index of its window in Screen.Windows
	Depth    int // depth within its window
	Index    int // pre-order position in Screen.Nodes
	Parent   *Node
	Children []*Node
}

// Label is what a person reads on the node: its text, else its accessibility description.
func (n *Node) Label() string {
	if n.Text != "" && !(n.ShowingHint && n.Text == n.Hint) {
		return n.Text
	}
	return n.Desc
}

// ShortClass is the last segment of the class name.
func (n *Node) ShortClass() string {
	c := n.Class
	if i := strings.LastIndexAny(c, "."); i >= 0 {
		c = c[i+1:]
	}
	return c
}

// IsDescendantOf reports whether n is anc or inside it.
func (n *Node) IsDescendantOf(anc *Node) bool {
	for p := n; p != nil; p = p.Parent {
		if p == anc {
			return true
		}
	}
	return false
}

// Walk visits n and its subtree in pre-order. Returning false skips a node's children.
func (n *Node) Walk(fn func(*Node) bool) {
	if !fn(n) {
		return
	}
	for _, c := range n.Children {
		c.Walk(fn)
	}
}

// Window is one window (an app, the keyboard, a system dialog).
type Window struct {
	ID      int
	Type    string // app, keyboard, system, overlay
	Package string
	Title   string
	Active  bool
	Focused bool
	Bounds  Rect
	Nodes   []*Node // this window's nodes, pre-order with Depth set (input to Link)
	Roots   []*Node
}

// Screen is one read of the device's screen.
type Screen struct {
	Platform string
	Width    int
	Height   int
	Seq      int64 // the device's change counter when it was read
	Keyboard bool
	Windows  []*Window // top-most first
	Nodes    []*Node   // every node, pre-order, windows top-most first
	// Tracked: the driver promises that, from this read on, every change that can move an
	// element on it bumps Seq: nothing was moving without sending events when it was read (web:
	// no running animation that can move a box). Android and iOS cannot promise it: a view
	// slides or springs there without an event (issue #80).
	Tracked bool
}

// Foreground is the package or bundle id of the active app window.
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

// Link fills Parent, Children, Index and Window from each node's Depth, given nodes per window in pre-order.
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

// Signature hashes what the screen shows (structure, text, bounds, state), to tell whether
// two reads differ.
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

// ClipRect is the part of the screen where n can show: the screen, cut to every scrolling
// ancestor's bounds (a row scrolled out of its list is not on screen, though inside the
// screen). Ignored: ancestors with empty bounds, and a window's root, whose scrolling is
// the window's own (a web page's body: its box moves up off the screen as the page scrolls,
// while what shows is the window).
func (n *Node) ClipRect(screen Rect) Rect {
	r := screen
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && p.Parent != nil && !p.Bounds.Empty() {
			r = r.Intersect(p.Bounds)
		}
	}
	return r
}
