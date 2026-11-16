// Package occlusion says what is drawn over an element on a screen: what would take a tap
// meant for it (its cover), and whether that cover is a bar that scrolling can bring the
// element clear of, or a modal layer (a drawer, a sheet, a dialog) that nothing but its own
// going can. It is the one definition of "covered": the matcher ranks the matches a person can
// reach before those under a cover (issue #282), and the runner steers its taps by it.
package occlusion

import (
	"strings"

	"github.com/elforama/testbuddy/internal/ui"
)

// IOSTouchTypes are the iOS element types that take touches: one drawn over an element
// covers it.
var IOSTouchTypes = map[string]bool{
	"Button": true, "Link": true, "Cell": true, "TextField": true, "SecureTextField": true,
	"SearchField": true, "TextView": true, "Switch": true, "Toggle": true, "Slider": true,
	"Stepper": true, "SegmentedControl": true, "PickerWheel": true, "ToolbarButton": true,
	"MenuItem": true, "CheckBox": true, "RadioButton": true, "PopUpButton": true,
	"MenuButton": true, "ComboBox": true,
}

// IOSBars are always drawn over content, whatever their place in the tree (which iOS orders
// by position on screen, not as drawn: a navigation bar comes before the list under it).
var IOSBars = map[string]bool{"NavigationBar": true, "TabBar": true, "Toolbar": true}

// At returns what would take a tap at (x, y) instead of n, or nil: the keyboard, a node of
// another app window above n's (a dialog, a popup), a later node of n's own window that takes
// touches there and is neither n's container nor inside it, an iOS bar, or (Android) a sticky
// section header. Not on the web: a page's element order is not its drawing order (screens
// stack through CSS), so it would take another screen's rows for covers; the browser's own
// hit test is the way there.
func At(s *ui.Screen, n *ui.Node, x, y int) *ui.Node {
	if s.Platform == "web" || n.Window < 0 || n.Window >= len(s.Windows) {
		return nil
	}
	// A window above the element's (windows are listed top-most first): the keyboard, or
	// another app window (a dialog, a menu, a sheet shown as a window of its own).
	for i := 0; i < n.Window; i++ {
		w := s.Windows[i]
		if !w.Bounds.Contains(x, y) {
			continue
		}
		switch w.Type {
		case "keyboard":
			if k := keyboardAt(i, w, x, y); k != nil {
				return k
			}
		case "app":
			if c := windowAt(w, x, y); c != nil {
				return c
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
		bar := s.Platform == "ios" && IOSBars[c.Class]
		if !after && !bar {
			continue // drawn before the element, under it
		}
		if Within(c.Bounds, n.Bounds) || 2*c.Bounds.Area() >= screenArea {
			// Inside the element's own frame (a part of it the tree lists apart, as React
			// Native does), or a container as large as half the screen (a transparent overlay
			// would hide everything): no cover.
			continue
		}
		if inOtherList(c, n) {
			continue // another list's content (a closed drawer's menu still placed on screen)
		}
		if bar || TakesTouches(s.Platform, c, x, y) {
			return c
		}
	}
	if s.Platform == "android" {
		return StickyHeader(n, x, y)
	}
	return nil
}

// keyboardAt is the part of a keyboard window (window i) drawn at (x, y), or nil. The window's
// bounds are not what it draws: with a hardware keyboard, Gboard shows only a small floating
// toolbar, in a window still as large as the full keyboard. What the window lists says where
// it draws: a node at the point smaller than the window. A keyboard that lists nothing
// smaller than its window is taken at its window's bounds.
func keyboardAt(i int, w *ui.Window, x, y int) *ui.Node {
	whole := &ui.Node{Class: "Keyboard", Bounds: w.Bounds, Enabled: true, Window: i}
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

// windowAt is the deepest node of app window w drawn at (x, y), or nil when it lists none
// there (a window's bounds can be larger than what it draws), or when what is there is the
// scrim of a window that takes every touch (see Scrim, blocks): a tap there is how a person
// dismisses the window, and a flow that taps through it means just that. (A window without
// focus that draws one plain node, a picture-in-picture video, has no scrim: that is its
// content.)
func windowAt(w *ui.Window, x, y int) *ui.Node {
	var at *ui.Node
	for _, c := range w.Nodes {
		if !c.Bounds.Empty() && c.Bounds.Contains(x, y) {
			at = c // deepest last, in pre-order
		}
	}
	if at != nil && blocks(w) && Scrim(w, at) {
		return nil
	}
	return at
}

// Scrim: n, a node of window w, is the window's "outside": the part a tap on dismisses it (a
// BottomSheetDialog's touch_outside, a dim layer that closes its dialog). It is named so, or
// it is a clickable node with no label and nothing in it that spans its window. One that does
// not take clicks is a veil (a loading dialog, one still mounting): a cover, waited for.
func Scrim(w *ui.Window, n *ui.Node) bool {
	if strings.HasSuffix(n.ID, "touch_outside") {
		return true
	}
	return n.Clickable && n.Label() == "" && len(n.Children) == 0 && 10*n.Bounds.Intersect(w.Bounds).Area() >= 9*w.Bounds.Area()
}

// Veil: cover (of an element on s) is a veil: a node with no label and nothing in it filling a
// window above that takes every touch, and no clicks itself (a clickable one is a scrim, no
// cover). A loading dialog, or one still mounting: it goes when the work is done, as a
// loading overlay does.
func Veil(s *ui.Screen, cover *ui.Node) bool {
	if cover == nil || cover.Window < 0 || cover.Window >= len(s.Windows) {
		return false
	}
	w := s.Windows[cover.Window]
	return blocks(w) && !cover.Clickable && cover.Label() == "" && len(cover.Children) == 0 &&
		10*cover.Bounds.Intersect(w.Bounds).Area() >= 9*w.Bounds.Area()
}

// blocks: app window w, above another, takes every touch within its bounds whatever it draws
// there. The window with input focus (a dialog, a sheet shown as a window) does; a window
// without it (a picture-in-picture window, an autocomplete popup, a floating window) only
// takes the touches on what it draws, as a bar would.
func blocks(w *ui.Window) bool { return w.Type == "app" && w.Focused }

// Within: a lies inside b (give or take a pixel).
func Within(a, b ui.Rect) bool {
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

// TakesTouches: c, or a node in it at (x, y), handles touches. A view that does not (a label,
// a layout) lets them through on Android and iOS alike.
func TakesTouches(platform string, c *ui.Node, x, y int) bool {
	found := false
	c.Walk(func(d *ui.Node) bool {
		if found || !d.Bounds.Contains(x, y) {
			return !found && d == c // a node not at the point: skip its subtree
		}
		switch platform {
		case "ios":
			found = IOSTouchTypes[d.Class]
		default:
			found = d.Clickable || d.LongClickable || d.Editable || d.Checkable || d.Scrollable
		}
		return !found
	})
	return found
}

// Touchable: n itself takes touches.
func Touchable(platform string, n *ui.Node) bool {
	if platform == "ios" {
		return IOSTouchTypes[n.Class]
	}
	return n.Clickable || n.LongClickable || n.Editable || n.Checkable
}

// InIOSBar: n is an iOS bar or in one (bars are drawn over content, whatever their place).
func InIOSBar(n *ui.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if IOSBars[p.Class] {
			return true
		}
	}
	return false
}

// StickyHeader: Android orders a list's children by place, not as drawn, so a sticky section
// header comes before the rows it is drawn over. A row (or header) before the element's own
// row, in the same list, that overlaps it at the point and spans its width is one.
func StickyHeader(n *ui.Node, x, y int) *ui.Node {
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

// VisiblePart is the part of n a tap can reach: on screen, within the scroll views around it
// (iOS frames are not cut to them).
func VisiblePart(s *ui.Screen, n *ui.Node) ui.Rect {
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

// UncoveredPoint is where to tap n: the middle of its visible part, unless something covers
// it there; then the middle of the largest part of it around the cover, if that is no sliver.
// cover is what covers the middle (nil: nothing); ok is false when no part shows clear.
func UncoveredPoint(s *ui.Screen, n *ui.Node) (x, y int, cover *ui.Node, ok bool) {
	v := VisiblePart(s, n)
	if v.Empty() {
		v = n.Bounds
	}
	x, y = v.CX(), v.CY()
	cover = At(s, n, x, y)
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
	if best.Empty() || At(s, n, best.CX(), best.CY()) != nil {
		return x, y, cover, false
	}
	return best.CX(), best.CY(), cover, true
}

// Covered is what covers n where a person would touch it: at the middle of its visible part,
// or else at three of the middles of its four quarters (most of its area), by At's rule; nil
// when n shows clear. modal says the cover is a modal layer (see Modal): n cannot be reached
// until it goes, and no scroll brings n clear of it.
func Covered(s *ui.Screen, n *ui.Node) (cover *ui.Node, modal bool) {
	v := VisiblePart(s, n)
	if v.Empty() {
		v = n.Bounds
	}
	cover = At(s, n, v.CX(), v.CY())
	if cover == nil {
		hits := 0
		for _, q := range []ui.Rect{
			{L: v.L, T: v.T, R: v.CX(), B: v.CY()}, {L: v.CX(), T: v.T, R: v.R, B: v.CY()},
			{L: v.L, T: v.CY(), R: v.CX(), B: v.B}, {L: v.CX(), T: v.CY(), R: v.R, B: v.B},
		} {
			if c := At(s, n, q.CX(), q.CY()); c != nil {
				hits++
				if cover == nil {
					cover = c
				}
			}
		}
		if hits < 3 {
			return nil, false
		}
	}
	return cover, Modal(s, n, cover) != nil
}

// Modal is the modal layer cover belongs to, over n, or nil when cover is a bar. A bar (a
// toolbar, a tab bar, a sticky header or footer, a mini-player, a floating button, the
// keyboard, a picture-in-picture window) is fixed over content that scrolls under it, so a
// drag brings n clear. A modal layer (a drawer, a sheet, a dialog) lies over the content as a
// whole: a drag would move what is under it, or close it through its scrim.
//
// The rule is structural, not a share of the screen (a drawer on a tablet is a fifth of it):
//   - in another app window: modal when that window takes every touch in its bounds (blocks);
//   - in n's own window: the layer is the branch holding cover below the nearest container of
//     both (drawn after n). It is modal when it holds all of what shows of n, draws over most
//     of n (5 of 9 points over n), and neither it nor its only child is a strip across the
//     viewport (Strip). A see-through frame holding a floating button draws little over n; a
//     toolbar or a mini-player, alone or in such a frame, is a strip: no modal layer. A
//     drawer, a sheet (its full-width rows are its content), a portal's backdrop and dialog
//     are. On iOS with the keyboard up, the keyboard listed in the app's window is a bar.
func Modal(s *ui.Screen, n, cover *ui.Node) *ui.Node {
	if cover == nil || s.Platform == "web" {
		return nil
	}
	if cover.Window != n.Window {
		if cover.Window >= 0 && cover.Window < len(s.Windows) && blocks(s.Windows[cover.Window]) {
			return cover
		}
		return nil // the keyboard, or a window that only takes touches on what it draws
	}
	if s.Platform == "ios" && InIOSBar(cover) {
		return nil
	}
	layer := cover
	for layer.Parent != nil && !n.IsDescendantOf(layer.Parent) {
		layer = layer.Parent
	}
	if n.IsDescendantOf(layer) {
		return nil
	}
	v := VisiblePart(s, n)
	if v.Empty() {
		v = n.Bounds
	}
	if !Within(v, layer.Bounds) {
		return nil
	}
	if keyboardInWindow(s, layer, cover) {
		// iOS can list the keyboard's views in the app's own window: a bar (the runner hides
		// the keyboard to reach what is under it).
		return nil
	}
	port := viewport(s, n)
	// The layer is a strip, or holds one alone (a see-through frame holding a bar): a bar laid
	// over the content. Strips deeper in a layer that is not one (a sheet's full-width rows)
	// are its content.
	if Strip(layer.Bounds, port) || (len(layer.Children) == 1 && Strip(layer.Children[0].Bounds, port)) {
		return nil
	}
	drawn := 0
	for _, p := range grid(v) {
		if draws(s.Platform, layer, p[0], p[1]) {
			drawn++
		}
	}
	if drawn < 5 {
		return nil
	}
	return layer
}

// Strip: r is a bar across the viewport: it spans its width (give or take a twentieth) and is
// at most a quarter of its height. A toolbar, a tab bar, a footer, a mini-player are; a drawer
// (Material leaves at least 56 dp beside one), a sheet over a quarter of the list, a dialog
// are not.
func Strip(r, port ui.Rect) bool {
	tol := port.W() / 20
	return r.L <= port.L+tol && r.R >= port.R-tol && 4*r.H() <= port.H()
}

// viewport is what n is seen through: the list (scrolling up and down) around it, on screen,
// else the screen.
func viewport(s *ui.Screen, n *ui.Node) ui.Rect {
	screen := ui.Rect{R: s.Width, B: s.Height}
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Scrollable && p.Bounds.H() >= p.Bounds.W()/2 {
			if r := p.Bounds.Intersect(screen); !r.Empty() {
				return r
			}
		}
	}
	return screen
}

// draws: layer draws something at (x, y): a node in it is there. The layer itself counts only
// when it takes touches (a layer's own frame may be see-through).
func draws(platform string, layer *ui.Node, x, y int) bool {
	found := false
	layer.Walk(func(d *ui.Node) bool {
		if found || !d.Bounds.Contains(x, y) {
			return !found && d == layer
		}
		found = d != layer || Touchable(platform, d)
		return !found
	})
	return found
}

// keyboardInWindow: cover, in layer of the app's own window, is the keyboard as iOS also lists
// it there. The agent moves every Keyboard element (and its keys) into a keyboard window of
// its own, so keyboard views left in the app's window are never typed Keyboard: nothing in a
// node says what it is. What tells:
//   - a keyboard window is listed: the keyboard's own window stays in the app's tree, its host
//     holding the prediction bar, the keys' place and the emoji and dictation buttons (the
//     bench keyboard scenario, on an iPhone simulator). A node from cover up to layer that
//     holds the keyboard window's frame, across the screen, starting no higher above it than
//     a quarter of the keyboard's height (its prediction bar), is that host;
//   - none is listed, yet the keyboard is up (s.Keyboard): a layer across the bottom of the
//     screen and no taller than two thirds of it.
//
// Either way, a layer holding the focused field the keyboard types into (a sheet with a
// search field) is what the keyboard serves, never the keyboard itself.
func keyboardInWindow(s *ui.Screen, layer, cover *ui.Node) bool {
	if s.Platform != "ios" || !s.Keyboard {
		return false
	}
	typing := false
	layer.Walk(func(d *ui.Node) bool {
		typing = typing || (d.Focused && (d.Editable || textTypes[d.Class]))
		return !typing
	})
	if typing {
		return false
	}
	tol := s.Width / 20
	across := func(b ui.Rect) bool { return b.L <= tol && b.R >= s.Width-tol }
	listed := false
	for _, w := range s.Windows {
		if w.Type != "keyboard" || w.Bounds.Empty() {
			continue
		}
		listed = true
		kb := w.Bounds
		for c := cover; c != nil; c = c.Parent {
			if b := c.Bounds; Within(kb, b) && across(b) && b.T >= kb.T-kb.H()/4 {
				return true
			}
			if c == layer {
				break
			}
		}
	}
	if listed {
		return false
	}
	b := layer.Bounds
	return b.B >= s.Height-1 && across(b) && 3*b.H() <= 2*s.Height
}

// textTypes are the iOS element types that take text.
var textTypes = map[string]bool{"TextField": true, "SecureTextField": true, "SearchField": true, "TextView": true}

// grid is 9 points over r: the middles of its ninths.
func grid(r ui.Rect) [][2]int {
	var out [][2]int
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			out = append(out, [2]int{r.L + (2*j+1)*r.W()/6, r.T + (2*i+1)*r.H()/6})
		}
	}
	return out
}
