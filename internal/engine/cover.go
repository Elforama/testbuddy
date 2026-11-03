package engine

import (
	"context"
	"os"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// Covers. Something drawn over an element where a tap would land takes the tap instead: a
// bar fixed over a list (a tab bar, a toolbar, a sticky footer), a floating button, the
// keyboard. A plain tap at the element's center would hit it. tb looks for a cover there first;
// when there is one it drags the element clear in its list, as a person would, and failing
// that taps the largest part of the element that shows.

var debug = os.Getenv("TB_DEBUG") != ""

// maxCoverClears is how many drags may try to bring an element clear of a cover.
const maxCoverClears = 2

// iOS element types that take touches: one drawn over an element covers it.
var iosTouchTypes = map[string]bool{
	"Button": true, "Link": true, "Cell": true, "TextField": true, "SecureTextField": true,
	"SearchField": true, "TextView": true, "Switch": true, "Toggle": true, "Slider": true,
	"Stepper": true, "SegmentedControl": true, "PickerWheel": true, "ToolbarButton": true,
	"MenuItem": true, "CheckBox": true, "RadioButton": true, "PopUpButton": true,
	"MenuButton": true, "ComboBox": true,
}

// iOS bars: always drawn over content, whatever their place in the tree (which iOS orders by
// position on screen, not as drawn: a navigation bar comes before the list under it).
var iosBars = map[string]bool{"NavigationBar": true, "TabBar": true, "Toolbar": true}

// coverAt returns what would take a tap at (x, y) instead of n, or nil. Not on the web: a
// page's element order is not its drawing order (screens stack through CSS), so it would take
// another screen's rows for covers; the browser's own hit test is the way there.
func coverAt(s *ui.Screen, n *ui.Node, x, y int) *ui.Node {
	if s.Platform == "web" || n.Window < 0 || n.Window >= len(s.Windows) {
		return nil
	}
	// A keyboard window above the element's (windows are listed top-most first).
	for i := 0; i < n.Window; i++ {
		if w := s.Windows[i]; w.Type == "keyboard" && w.Bounds.Contains(x, y) {
			if k := keyboardAt(w, x, y); k != nil {
				return k
			}
		}
	}
	screenArea := s.Width * s.Height
	win := s.Windows[n.Window]
	after := false
	for _, c := range win.Nodes {
		if c == n {
			after = true
			continue
		}
		if !c.Bounds.Contains(x, y) || c.IsDescendantOf(n) || n.IsDescendantOf(c) {
			continue
		}
		bar := s.Platform == "ios" && iosBars[c.Class]
		if !after && !bar {
			continue // drawn before the element, under it
		}
		if within(c.Bounds, n.Bounds) || 2*c.Bounds.Area() >= screenArea {
			// Inside the element's own frame (a part of it the tree lists apart, as React
			// Native does), or a container as large as half the screen (a transparent overlay
			// would hide everything): no cover.
			continue
		}
		if inOtherList(c, n) {
			continue // another list's content (a closed drawer's menu still placed on screen)
		}
		if bar || takesTouches(s.Platform, c, x, y) {
			return c
		}
	}
	if s.Platform == "android" {
		return stickyHeader(n, x, y)
	}
	return nil
}

// keyboardAt is the part of a keyboard window drawn at (x, y), or nil. The window's bounds
// are not what it draws: with a hardware keyboard, Gboard shows only a small floating
// toolbar, in a window still as large as the full keyboard. What the window lists says where
// it draws: a node at the point smaller than the window. A keyboard that lists nothing
// smaller than its window is taken at its window's bounds.
func keyboardAt(w *ui.Window, x, y int) *ui.Node {
	whole := &ui.Node{Class: "Keyboard", Bounds: w.Bounds, Enabled: true}
	if len(w.Nodes) == 0 {
		return whole
	}
	var at *ui.Node
	parts := false // it lists something smaller than itself
	for _, k := range w.Nodes {
		if 10*k.Bounds.Area() >= 9*w.Bounds.Area() || k.Bounds.Empty() {
			continue
		}
		parts = true
		if k.Bounds.Contains(x, y) {
			at = k // deepest last, in pre-order
		}
	}
	if !parts {
		return whole // only a node as large as the window: it draws all of it
	}
	if at == nil {
		return nil
	}
	c := *at
	c.Class = "Keyboard " + at.ShortClass()
	return &c
}

func within(a, b ui.Rect) bool {
	return a.L >= b.L-1 && a.T >= b.T-1 && a.R <= b.R+1 && a.B <= b.B+1
}

// inOtherList: c is inside a scroll view that does not also hold n.
func inOtherList(c, n *ui.Node) bool {
	for p := c.Parent; p != nil; p = p.Parent {
		if p.Scrollable && !n.IsDescendantOf(p) {
			return true
		}
	}
	return false
}

// takesTouches: c, or a node in it at (x, y), handles touches. A view that does not (a label,
// a layout) lets them through on Android and iOS alike.
func takesTouches(platform string, c *ui.Node, x, y int) bool {
	found := false
	c.Walk(func(d *ui.Node) bool {
		if found || !d.Bounds.Contains(x, y) {
			return !found && d == c // a node not at the point: skip its subtree
		}
		switch platform {
		case "ios":
			found = iosTouchTypes[d.Class]
		default:
			found = d.Clickable || d.LongClickable || d.Editable || d.Checkable || d.Scrollable
		}
		return !found
	})
	return found
}

// stickyHeader: Android orders a list's children by place, not as drawn, so a sticky section
// header comes before the rows it is drawn over. A row (or header) before the element's own
// row, in the same list, that overlaps it at the point and spans its width is one.
func stickyHeader(n *ui.Node, x, y int) *ui.Node {
	row := n
	for row.Parent != nil && !row.Parent.Scrollable {
		row = row.Parent
	}
	list := row.Parent
	if list == nil || row.Bounds.W()*2 < list.Bounds.W() {
		return nil // not in a list, or a cell of a sideways one (its cells may overlap a little)
	}
	for _, c := range list.Children {
		if c == row {
			break
		}
		if c.Bounds.Contains(x, y) && c.Bounds.L <= n.Bounds.L && c.Bounds.R >= n.Bounds.R {
			return c
		}
	}
	return nil
}

// visiblePart is the part of n a tap can reach: on screen, within the scroll views around it
// (iOS frames are not cut to them).
func visiblePart(s *ui.Screen, n *ui.Node) ui.Rect {
	r := n.Bounds.Intersect(ui.Rect{R: s.Width, B: s.Height})
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && !r.Empty() {
			if c := r.Intersect(p.Bounds); !c.Empty() {
				r = c
			}
		}
	}
	return r
}

// uncoveredPoint is where to tap n: the middle of its visible part, unless something covers
// it there; then the middle of the largest part of it around the cover, if that is no sliver.
// cover is what covers the middle (nil: nothing); ok is false when no part shows clear.
func uncoveredPoint(s *ui.Screen, n *ui.Node) (x, y int, cover *ui.Node, ok bool) {
	v := visiblePart(s, n)
	if v.Empty() {
		v = n.Bounds
	}
	x, y = v.CX(), v.CY()
	cover = coverAt(s, n, x, y)
	if cover == nil {
		return x, y, nil, true
	}
	c := cover.Bounds
	parts := []ui.Rect{
		{L: v.L, T: v.T, R: v.R, B: min(v.B, c.T)}, // above it
		{L: v.L, T: max(v.T, c.B), R: v.R, B: v.B}, // below
		{L: v.L, T: v.T, R: min(v.R, c.L), B: v.B}, // left of it
		{L: max(v.L, c.R), T: v.T, R: v.R, B: v.B}, // right of it
	}
	least := max(s.Width/60, 4)
	best := ui.Rect{}
	for _, p := range parts {
		if p.W() >= least && p.H() >= least && p.Area() > best.Area() {
			best = p
		}
	}
	if best.Empty() || coverAt(s, n, best.CX(), best.CY()) != nil {
		return x, y, cover, false
	}
	return best.CX(), best.CY(), cover, true
}

// clearCover drags n's list so n comes clear of cover: a bar along the top or bottom moves the
// content down or up, by just enough plus a margin, from the middle of the list's part that
// shows clear of the cover. It returns false when no drag can: n is in no list that scrolls up
// and down, or the cover is in that list too (it moves along), or the list shows too little.
func (r *Runner) clearCover(ctx context.Context, s *ui.Screen, n, cover *ui.Node) (bool, error) {
	c := cover.Bounds
	if c.W() < c.H() {
		return false, nil // a bar along a side
	}
	var list *ui.Node
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && p.Bounds.H() >= p.Bounds.W()/2 {
			list = p
			break
		}
	}
	if list == nil || cover.IsDescendantOf(list) {
		return false, nil
	}
	lo, hi := max(list.Bounds.T, 0), min(list.Bounds.B, s.Height)
	bottom := c.CY() > s.Height/2
	if bottom {
		hi = min(hi, c.T)
	} else {
		lo = max(lo, c.B)
	}
	if hi-lo < s.Width/6 {
		return false, nil
	}
	margin := s.Width / 12
	shift := c.B - n.Bounds.T + margin // the cover is above: content down
	if bottom {
		shift = -(n.Bounds.B - c.T + margin)
	}
	x := clamp(list.Bounds.CX(), 0, s.Width-1)
	mid := (lo + hi) / 2
	half := (hi - lo) / 2
	r.invalidate()
	if _, err := r.Drv.Swipe(ctx, x, mid, x, mid+clamp(shift, -half, half), 0, driver.ActionOpts{NoSettle: true, Drag: true}); err != nil {
		return false, err
	}
	r.Drv.WaitSettled(ctx, 2*time.Second)
	return true, nil
}

// clearAndPoint brings n (found by sel on s) clear of anything covering it and says where to
// tap it. Up to maxCoverClears drags of its list, each followed by finding it again; then its
// largest uncovered part; failing that its center.
func (r *Runner) clearAndPoint(ctx context.Context, sel *match.Selector, s *ui.Screen, n *ui.Node) (x, y int, err error) {
	x, y, cover, ok := uncoveredPoint(s, n)
	if cover != nil {
		r.tracef("covered at its middle by %s", nodeDesc(cover))
	}
	for i := 0; cover != nil && i < maxCoverClears; i++ {
		was := n.Bounds
		dragged, err := r.clearCover(ctx, s, n, cover)
		if err != nil || !dragged {
			if err == nil {
				r.tracef("no drag can bring it clear (not in a list that scrolls, or the cover moves with it)")
			}
			break
		}
		r.tracef("dragged its list to bring it clear of the cover")
		n2, s2, _, ferr := r.find(ctx, sel, time.Second)
		if ferr != nil {
			return 0, 0, ferr
		}
		n, s = n2, s2
		x, y, cover, ok = uncoveredPoint(s, n)
		if n.Bounds == was {
			break // the list moves no further
		}
	}
	if cover != nil && !ok {
		v := visiblePart(s, n)
		r.tracef("no part of it shows clear of %s: tapping its middle anyway", nodeDesc(cover))
		return v.CX(), v.CY(), nil
	}
	if cover != nil {
		r.tracef("tapping its largest uncovered part, at (%d, %d)", x, y)
	}
	return x, y, nil
}
