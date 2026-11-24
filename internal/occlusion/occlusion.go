package occlusion

import (
	"strings"

	"github.com/elforama/testbuddy/internal/ui"
)

var IOSTouchTypes = map[string]bool{
	"Button": true, "Link": true, "Cell": true, "TextField": true, "SecureTextField": true,
	"SearchField": true, "TextView": true, "Switch": true, "Toggle": true, "Slider": true,
	"Stepper": true, "SegmentedControl": true, "PickerWheel": true, "ToolbarButton": true,
	"MenuItem": true, "CheckBox": true, "RadioButton": true, "PopUpButton": true,
	"MenuButton": true, "ComboBox": true,
}

var IOSBars = map[string]bool{"NavigationBar": true, "TabBar": true, "Toolbar": true}

func At(s *ui.Screen, n *ui.Node, x, y int) *ui.Node {
	if s.Platform == "web" {
		return webAt(s, n, x, y)
	}
	if n.Window < 0 || n.Window >= len(s.Windows) {
		return nil
	}
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
		if !reaches(c.Bounds, edgeOf(s), x, y) || c.IsDescendantOf(n) || n.IsDescendantOf(c) {
			continue
		}
		bar := s.Platform == "ios" && IOSBars[c.Class]
		over := after
		if under, known := DrawOrder(c, n); known {
			over = !under
		} else if over && (s.Platform == "android" || s.Platform == "ios") && pinnedOver(n, c, x, y, edgeOf(s)) {
			over = false
		}
		if !over && !bar {
			continue
		}
		if Within(c.Bounds, n.Bounds) || 2*c.Bounds.Area() >= screenArea {
			continue
		}
		if inOtherList(c, n) {
			continue
		}
		if bar || TakesTouches(s.Platform, c, x, y) {
			return c
		}
	}
	if s.Platform == "android" || s.Platform == "ios" {
		return stickyHeader(n, x, y, edgeOf(s))
	}
	return nil
}

func webAt(s *ui.Screen, n *ui.Node, x, y int) *ui.Node {
	if len(n.Covers) == 0 {
		return nil
	}
	for _, h := range n.Covers {
		if h.X == x && h.Y == y {
			return h.By
		}
	}
	for _, p := range samples(hitPart(s, n)) {
		if p[0] == x && p[1] == y {
			return nil
		}
	}
	for _, h := range n.Covers {
		if h.By != nil && h.By.Bounds.Contains(x, y) {
			return h.By
		}
	}
	return nil
}

func hitPart(s *ui.Screen, n *ui.Node) ui.Rect {
	v := VisiblePart(s, n)
	if v.Empty() {
		v = n.Bounds
	}
	return v
}

func SamplePoints(s *ui.Screen, n *ui.Node) (pts [5][2]int, ok bool) {
	v := VisiblePart(s, n)
	if v.Empty() || n.Bounds.Intersect(n.ClipRect(ui.Rect{R: s.Width, B: s.Height})).Empty() {
		return pts, false
	}
	return samples(v), true
}

func samples(v ui.Rect) [5][2]int {
	return [5][2]int{
		{v.CX(), v.CY()},
		{(v.L + v.CX()) / 2, (v.T + v.CY()) / 2}, {(v.CX() + v.R) / 2, (v.T + v.CY()) / 2},
		{(v.L + v.CX()) / 2, (v.CY() + v.B) / 2}, {(v.CX() + v.R) / 2, (v.CY() + v.B) / 2},
	}
}

func keyboardAt(i int, w *ui.Window, x, y int) *ui.Node {
	whole := &ui.Node{Class: "Keyboard", Bounds: w.Bounds, Enabled: true, Window: i}
	if len(w.Nodes) == 0 {
		return whole
	}
	var at *ui.Node
	parts := false
	for _, k := range w.Nodes {
		if 10*k.Bounds.Area() >= 9*w.Bounds.Area() || k.Bounds.Empty() {
			continue
		}
		parts = true
		if k.Bounds.Contains(x, y) {
			at = k
		}
	}
	if !parts {
		return whole
	}
	if at == nil {
		return nil
	}
	c := *at
	c.Class = "Keyboard " + at.ShortClass()
	return &c
}

func windowAt(w *ui.Window, x, y int) *ui.Node {
	var at *ui.Node
	for _, c := range w.Nodes {
		if !c.Bounds.Empty() && c.Bounds.Contains(x, y) {
			at = c
		}
	}
	if at != nil && blocks(w) && Scrim(w, at) {
		return nil
	}
	return at
}

func Scrim(w *ui.Window, n *ui.Node) bool {
	if strings.HasSuffix(n.ID, "touch_outside") {
		return true
	}
	return n.Clickable && n.Label() == "" && len(n.Children) == 0 && 10*n.Bounds.Intersect(w.Bounds).Area() >= 9*w.Bounds.Area()
}

func Veil(s *ui.Screen, cover *ui.Node) bool {
	if cover == nil {
		return false
	}
	if s.Platform == "web" {
		taps := false
		cover.Walk(func(d *ui.Node) bool {
			taps = taps || Touchable(s.Platform, d)
			return !taps
		})
		return !taps
	}
	if cover.Window < 0 || cover.Window >= len(s.Windows) {
		return false
	}
	w := s.Windows[cover.Window]
	return blocks(w) && !cover.Clickable && cover.Label() == "" && len(cover.Children) == 0 &&
		10*cover.Bounds.Intersect(w.Bounds).Area() >= 9*w.Bounds.Area()
}

func blocks(w *ui.Window) bool { return w.Type == "app" && w.Focused }

func Within(a, b ui.Rect) bool {
	return a.L >= b.L-1 && a.T >= b.T-1 && a.R <= b.R+1 && a.B <= b.B+1
}

func inOtherList(c, n *ui.Node) bool {
	for p := c.Parent; p != nil; p = p.Parent {
		if p.Scrollable && !n.IsDescendantOf(p) {
			return true
		}
	}
	return false
}

func TakesTouches(platform string, c *ui.Node, x, y int) bool {
	found := false
	c.Walk(func(d *ui.Node) bool {
		if found || !d.Bounds.Contains(x, y) {
			return !found && d == c
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

func Touchable(platform string, n *ui.Node) bool {
	if platform == "ios" {
		return IOSTouchTypes[n.Class]
	}
	return n.Clickable || n.LongClickable || n.Editable || n.Checkable
}

func InIOSBar(n *ui.Node) bool {
	for p := n; p != nil; p = p.Parent {
		if IOSBars[p.Class] {
			return true
		}
	}
	return false
}

func StickyHeader(n *ui.Node, x, y int) *ui.Node { return stickyHeader(n, x, y, 0) }

func stickyHeader(n *ui.Node, x, y, e int) *ui.Node {
	row := n
	for row.Parent != nil && !row.Parent.Scrollable {
		row = row.Parent
	}
	list := row.Parent
	if list == nil || row.Bounds.W()*2 < list.Bounds.W() {
		return nil
	}
	for _, c := range list.Children {
		if c == row {
			break
		}
		if reaches(c.Bounds, e, x, y) && c.Bounds.L <= n.Bounds.L && c.Bounds.R >= n.Bounds.R && !holdsRow(row, c) && !contentView(list, row, c) {
			return c
		}
	}
	return nil
}

func edgeOf(s *ui.Screen) int {
	if s.Platform == "ios" {
		return 1
	}
	return 0
}

func grown(r ui.Rect, e int) ui.Rect {
	return ui.Rect{L: r.L - e, T: r.T - e, R: r.R + e, B: r.B + e}
}

func reaches(r ui.Rect, e, x, y int) bool { return !r.Empty() && grown(r, e).Contains(x, y) }

func holdsRow(row, c *ui.Node) bool { return Within(row.Bounds, c.Bounds) }

func contentView(list, row, c *ui.Node) bool {
	return Within(list.Bounds, c.Bounds) && Within(row.Bounds.Intersect(list.Bounds), c.Bounds)
}

func pinnedOver(n, c *ui.Node, x, y, e int) bool {
	h := stickyHeader(c, x, y, e)
	if h == nil || (h != n && !n.IsDescendantOf(h)) || h.Parent == nil {
		return false
	}
	return h.Bounds.T >= h.Parent.Bounds.T-1
}

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
	c := grown(cover.Bounds, edgeOf(s))
	parts := []ui.Rect{
		{L: v.L, T: v.T, R: v.R, B: min(v.B, c.T)},
		{L: v.L, T: max(v.T, c.B), R: v.R, B: v.B},
		{L: v.L, T: v.T, R: min(v.R, c.L), B: v.B},
		{L: max(v.L, c.R), T: v.T, R: v.R, B: v.B},
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

func Covered(s *ui.Screen, n *ui.Node) (cover *ui.Node, modal bool) {
	pts := samples(hitPart(s, n))
	at := func(i int) *ui.Node { return At(s, n, pts[i][0], pts[i][1]) }
	if s.Platform == "web" {
		at = func(i int) *ui.Node {
			for _, h := range n.Covers {
				if h.Point == i {
					return h.By
				}
			}
			return nil
		}
	}
	cover = at(0)
	if cover == nil {
		hits := 0
		for i := 1; i < len(pts); i++ {
			if c := at(i); c != nil {
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

func Modal(s *ui.Screen, n, cover *ui.Node) *ui.Node {
	if cover == nil {
		return nil
	}
	if s.Platform == "web" {
		return webModal(s, n, cover)
	}
	if cover.Window != n.Window {
		if cover.Window >= 0 && cover.Window < len(s.Windows) && blocks(s.Windows[cover.Window]) {
			return cover
		}
		return nil
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
		return nil
	}
	port := viewport(s, n)
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

func webModal(s *ui.Screen, n, cover *ui.Node) *ui.Node {
	v := hitPart(s, n)
	port := viewport(s, n)
	for _, h := range n.Covers {
		if h.By != cover {
			continue
		}
		for _, l := range h.Layers {
			switch l.Kind {
			case "modal":
				return l.Node
			case "fixed", "sticky":
				if Within(v, l.Node.Bounds) && !Strip(l.Node.Bounds, port) {
					return l.Node
				}
			}
		}
	}
	return nil
}

func Lasting(s *ui.Screen, n, cover *ui.Node) bool {
	if s.Platform != "web" {
		return true
	}
	for _, h := range n.Covers {
		if h.By != cover {
			continue
		}
		for _, l := range h.Layers {
			if l.Kind == "modal" {
				return true
			}
		}
	}
	return false
}

func Positioned(n, cover *ui.Node) bool {
	for _, h := range n.Covers {
		if h.By != cover {
			continue
		}
		for _, l := range h.Layers {
			if l.Kind == "fixed" || l.Kind == "sticky" {
				return true
			}
		}
	}
	return false
}

func Strip(r, port ui.Rect) bool {
	tol := port.W() / 20
	return r.L <= port.L+tol && r.R >= port.R-tol && 4*r.H() <= port.H()
}

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

var textTypes = map[string]bool{"TextField": true, "SecureTextField": true, "SearchField": true, "TextView": true}

func grid(r ui.Rect) [][2]int {
	var out [][2]int
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			out = append(out, [2]int{r.L + (2*j+1)*r.W()/6, r.T + (2*i+1)*r.H()/6})
		}
	}
	return out
}
