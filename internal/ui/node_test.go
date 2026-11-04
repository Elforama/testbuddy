package ui

import (
	"testing"
)

func rect(l, t, r, b int) Rect { return Rect{L: l, T: t, R: r, B: b} }

// Rect's derived geometry: width, height, centre (integer division), emptiness and area.
func TestRectGeometry(t *testing.T) {
	cases := []struct {
		name         string
		r            Rect
		w, h, cx, cy int
		empty        bool
		area         int
	}{
		{"normal", rect(10, 20, 110, 220), 100, 200, 60, 120, false, 20000},
		{"zero value", Rect{}, 0, 0, 0, 0, true, 0},
		{"odd size rounds centre down", rect(0, 0, 5, 3), 5, 3, 2, 1, false, 15},
		{"zero width line", rect(10, 10, 10, 50), 0, 40, 10, 30, true, 0},
		{"zero height line", rect(10, 10, 50, 10), 40, 0, 30, 10, true, 0},
		{"inverted is empty with no area", rect(100, 100, 0, 0), -100, -100, 50, 50, true, 0},
		{"inverted width only", rect(100, 0, 0, 50), -100, 50, 50, 25, true, 0},
		{"negative coordinates", rect(-50, -20, 50, 20), 100, 40, 0, 0, false, 4000},
		{"one pixel", rect(3, 4, 4, 5), 1, 1, 3, 4, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.W(); got != c.w {
				t.Errorf("W() = %d, want %d", got, c.w)
			}
			if got := c.r.H(); got != c.h {
				t.Errorf("H() = %d, want %d", got, c.h)
			}
			if got := c.r.CX(); got != c.cx {
				t.Errorf("CX() = %d, want %d", got, c.cx)
			}
			if got := c.r.CY(); got != c.cy {
				t.Errorf("CY() = %d, want %d", got, c.cy)
			}
			if got := c.r.Empty(); got != c.empty {
				t.Errorf("Empty() = %v, want %v", got, c.empty)
			}
			if got := c.r.Area(); got != c.area {
				t.Errorf("Area() = %d, want %d", got, c.area)
			}
		})
	}
}

// Contains is half-open: the left/top edges are inside, the right/bottom edges are not.
func TestRectContainsIsHalfOpen(t *testing.T) {
	r := rect(10, 20, 30, 40)
	cases := []struct {
		x, y int
		want bool
	}{
		{10, 20, true},  // top-left corner
		{29, 39, true},  // last pixel inside
		{20, 30, true},  // middle
		{30, 30, false}, // right edge
		{20, 40, false}, // bottom edge
		{9, 30, false},  // left of it
		{20, 19, false}, // above it
		{30, 40, false}, // bottom-right corner
	}
	for _, c := range cases {
		if got := r.Contains(c.x, c.y); got != c.want {
			t.Errorf("Contains(%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if (Rect{}).Contains(0, 0) {
		t.Error("an empty rect contains no point")
	}
}

// String renders the Android bounds notation "[l,t][r,b]".
func TestRectString(t *testing.T) {
	cases := map[string]Rect{
		"[0,0][0,0]":           {},
		"[10,20][30,40]":       rect(10, 20, 30, 40),
		"[-5,-6][7,8]":         rect(-5, -6, 7, 8),
		"[0,0][1080,2400]":     rect(0, 0, 1080, 2400),
		"[100,100][0,0]":       rect(100, 100, 0, 0),
		"[1,2][3,4]":           {1, 2, 3, 4},
		"[99999,1][100000,10]": rect(99999, 1, 100000, 10),
	}
	for want, r := range cases {
		if got := r.String(); got != want {
			t.Errorf("%#v.String() = %q, want %q", r, got, want)
		}
	}
}

// Intersect returns the overlap, normalising any empty result to the zero Rect; Overlaps
// is true only when that overlap has area.
func TestRectIntersectAndOverlaps(t *testing.T) {
	cases := []struct {
		name string
		a, b Rect
		want Rect
	}{
		{"partial overlap", rect(0, 0, 100, 100), rect(50, 50, 150, 150), rect(50, 50, 100, 100)},
		{"contained", rect(0, 0, 100, 100), rect(10, 10, 20, 20), rect(10, 10, 20, 20)},
		{"identical", rect(5, 5, 9, 9), rect(5, 5, 9, 9), rect(5, 5, 9, 9)},
		{"disjoint", rect(0, 0, 10, 10), rect(20, 20, 30, 30), Rect{}},
		{"touching right edge", rect(0, 0, 10, 10), rect(10, 0, 20, 10), Rect{}},
		{"touching bottom edge", rect(0, 0, 10, 10), rect(0, 10, 10, 20), Rect{}},
		{"with empty", rect(0, 0, 10, 10), Rect{}, Rect{}},
		{"clipped to screen", rect(-50, 1900, 500, 2600), rect(0, 0, 1080, 2400), rect(0, 1900, 500, 2400)},
		{"overlapping strip", rect(0, 0, 100, 10), rect(50, -5, 60, 100), rect(50, 0, 60, 10)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Intersect(c.b); got != c.want {
				t.Errorf("Intersect = %v, want %v", got, c.want)
			}
			if got := c.b.Intersect(c.a); got != c.want {
				t.Errorf("Intersect is not symmetric: %v, want %v", got, c.want)
			}
			wantOverlap := c.want != Rect{}
			if got := c.a.Overlaps(c.b); got != wantOverlap {
				t.Errorf("Overlaps = %v, want %v", got, wantOverlap)
			}
		})
	}
}

// Label is the text a person reads: the text, else the accessibility description; a field
// showing its placeholder has no text of its own.
func TestNodeLabel(t *testing.T) {
	cases := []struct {
		name string
		n    Node
		want string
	}{
		{"text wins", Node{Text: "Sign in", Desc: "sign in button"}, "Sign in"},
		{"desc when no text", Node{Desc: "Close"}, "Close"},
		{"nothing", Node{}, ""},
		{"placeholder shown falls back to desc", Node{Text: "Email", Hint: "Email", ShowingHint: true, Desc: "Email field"}, "Email field"},
		{"placeholder shown without desc is empty", Node{Text: "Email", Hint: "Email", ShowingHint: true}, ""},
		{"typed text equal to hint but hint not showing", Node{Text: "Email", Hint: "Email"}, "Email"},
		{"showing hint but text differs from hint", Node{Text: "me@x.io", Hint: "Email", ShowingHint: true}, "me@x.io"},
		{"hint alone is not a label", Node{Hint: "Search"}, ""},
		{"value alone is not a label", Node{Value: "42"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.n.Label(); got != c.want {
				t.Errorf("Label() = %q, want %q", got, c.want)
			}
		})
	}
}

// ShortClass keeps what follows the last dot of the class name.
func TestNodeShortClass(t *testing.T) {
	cases := map[string]string{
		"android.widget.Button":                     "Button",
		"androidx.appcompat.widget.AppCompatButton": "AppCompatButton",
		"Button":                 "Button",
		"":                       "",
		"trailing.":              "",
		".Leading":               "Leading",
		"input:password[button]": "input:password[button]",
		"a.b.c.d":                "d",
	}
	for class, want := range cases {
		n := Node{Class: class}
		if got := n.ShortClass(); got != want {
			t.Errorf("ShortClass(%q) = %q, want %q", class, got, want)
		}
	}
}

func tree() (root, a, a1, a2, b *Node) {
	root = &Node{Text: "root"}
	a = &Node{Text: "a", Parent: root}
	b = &Node{Text: "b", Parent: root}
	a1 = &Node{Text: "a1", Parent: a}
	a2 = &Node{Text: "a2", Parent: a}
	root.Children = []*Node{a, b}
	a.Children = []*Node{a1, a2}
	return
}

// IsDescendantOf is reflexive and follows Parent links only upward.
func TestNodeIsDescendantOf(t *testing.T) {
	root, a, a1, a2, b := tree()
	cases := []struct {
		name   string
		n, anc *Node
		want   bool
	}{
		{"self", a, a, true},
		{"child", a1, a, true},
		{"grandchild", a1, root, true},
		{"parent is not descendant of child", a, a1, false},
		{"sibling", a1, a2, false},
		{"cousin branch", a1, b, false},
		{"root of itself", root, root, true},
		{"nil ancestor", a1, nil, false},
		{"unrelated node", a1, &Node{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.n.IsDescendantOf(c.anc); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// Walk visits in pre-order, and a false return skips that node's subtree but not siblings.
func TestNodeWalk(t *testing.T) {
	root, _, _, _, _ := tree()
	var got []string
	root.Walk(func(n *Node) bool { got = append(got, n.Text); return true })
	if want := []string{"root", "a", "a1", "a2", "b"}; !equal(got, want) {
		t.Errorf("pre-order = %v, want %v", got, want)
	}

	got = nil
	root.Walk(func(n *Node) bool { got = append(got, n.Text); return n.Text != "a" })
	if want := []string{"root", "a", "b"}; !equal(got, want) {
		t.Errorf("skip a's children = %v, want %v", got, want)
	}

	got = nil
	root.Walk(func(n *Node) bool { got = append(got, n.Text); return false })
	if want := []string{"root"}; !equal(got, want) {
		t.Errorf("skip at root = %v, want %v", got, want)
	}

	leaf := &Node{Text: "leaf"}
	count := 0
	leaf.Walk(func(*Node) bool { count++; return true })
	if count != 1 {
		t.Errorf("leaf visited %d times, want 1", count)
	}
}

// Foreground is the active app window's package, else the first app window's, else "".
func TestScreenForeground(t *testing.T) {
	cases := []struct {
		name string
		wins []*Window
		want string
	}{
		{"no windows", nil, ""},
		{"active app", []*Window{{Type: "app", Package: "com.a", Active: true}}, "com.a"},
		{"active app below inactive app", []*Window{
			{Type: "app", Package: "com.top"},
			{Type: "app", Package: "com.active", Active: true},
		}, "com.active"},
		{"no active app: first app", []*Window{
			{Type: "keyboard", Package: "com.ime", Active: true},
			{Type: "app", Package: "com.first"},
			{Type: "app", Package: "com.second"},
		}, "com.first"},
		{"active non-app is ignored", []*Window{
			{Type: "system", Package: "com.android.systemui", Active: true},
			{Type: "app", Package: "com.app"},
		}, "com.app"},
		{"only system windows", []*Window{
			{Type: "system", Package: "com.android.systemui", Active: true},
			{Type: "keyboard", Package: "com.ime"},
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Screen{Windows: c.wins}
			if got := s.Foreground(); got != c.want {
				t.Errorf("Foreground() = %q, want %q", got, c.want)
			}
		})
	}
}

func texts(ns []*Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Text
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Link rebuilds the tree from pre-order depths: parents, children, roots, global indices.
func TestScreenLinkBuildsTree(t *testing.T) {
	root := &Node{Text: "root", Depth: 0}
	header := &Node{Text: "header", Depth: 1}
	title := &Node{Text: "title", Depth: 2}
	list := &Node{Text: "list", Depth: 1}
	row1 := &Node{Text: "row1", Depth: 2}
	row1a := &Node{Text: "row1a", Depth: 3}
	row2 := &Node{Text: "row2", Depth: 2}
	s := &Screen{Windows: []*Window{{Type: "app", Nodes: []*Node{root, header, title, list, row1, row1a, row2}}}}
	s.Link()

	if got := texts(s.Nodes); !equal(got, []string{"root", "header", "title", "list", "row1", "row1a", "row2"}) {
		t.Fatalf("Nodes = %v", got)
	}
	for i, n := range s.Nodes {
		if n.Index != i {
			t.Errorf("%s.Index = %d, want %d", n.Text, n.Index, i)
		}
		if n.Window != 0 {
			t.Errorf("%s.Window = %d, want 0", n.Text, n.Window)
		}
	}
	parents := map[*Node]*Node{root: nil, header: root, title: header, list: root, row1: list, row1a: row1, row2: list}
	for n, want := range parents {
		if n.Parent != want {
			t.Errorf("%s.Parent = %v, want %v", n.Text, n.Parent, want)
		}
	}
	if got := texts(root.Children); !equal(got, []string{"header", "list"}) {
		t.Errorf("root.Children = %v", got)
	}
	if got := texts(list.Children); !equal(got, []string{"row1", "row2"}) {
		t.Errorf("list.Children = %v", got)
	}
	if len(row2.Children) != 0 || len(title.Children) != 0 {
		t.Errorf("leaves have children")
	}
	if got := texts(s.Windows[0].Roots); !equal(got, []string{"root"}) {
		t.Errorf("Roots = %v", got)
	}
}

// A depth jump (0 then 3) still makes the deeper node a child of the nearest shallower one,
// and several depth-0 nodes are separate roots.
func TestScreenLinkDepthJumpsAndMultipleRoots(t *testing.T) {
	r1 := &Node{Text: "r1", Depth: 0}
	deep := &Node{Text: "deep", Depth: 3}
	mid := &Node{Text: "mid", Depth: 1}
	r2 := &Node{Text: "r2", Depth: 0}
	r2c := &Node{Text: "r2c", Depth: 5}
	s := &Screen{Windows: []*Window{{Type: "app", Nodes: []*Node{r1, deep, mid, r2, r2c}}}}
	s.Link()

	if deep.Parent != r1 || mid.Parent != r1 {
		t.Errorf("deep.Parent=%v mid.Parent=%v, want r1", deep.Parent, mid.Parent)
	}
	if got := texts(r1.Children); !equal(got, []string{"deep", "mid"}) {
		t.Errorf("r1.Children = %v", got)
	}
	if r2.Parent != nil || r2c.Parent != r2 {
		t.Errorf("r2.Parent=%v r2c.Parent=%v", r2.Parent, r2c.Parent)
	}
	if got := texts(s.Windows[0].Roots); !equal(got, []string{"r1", "r2"}) {
		t.Errorf("Roots = %v", got)
	}
}

// Nodes are numbered across windows (top-most first); each node records its window, and
// trees never span windows even when depths would allow it.
func TestScreenLinkAcrossWindows(t *testing.T) {
	kb := &Node{Text: "kb", Depth: 0}
	key := &Node{Text: "key", Depth: 1}
	app := &Node{Text: "app", Depth: 1} // depth 1 but first in its window: a root
	btn := &Node{Text: "btn", Depth: 2}
	s := &Screen{Windows: []*Window{
		{Type: "keyboard", Nodes: []*Node{kb, key}},
		{Type: "app", Nodes: []*Node{app, btn}},
	}}
	s.Link()

	if got := texts(s.Nodes); !equal(got, []string{"kb", "key", "app", "btn"}) {
		t.Fatalf("Nodes = %v", got)
	}
	wantWin := map[*Node]int{kb: 0, key: 0, app: 1, btn: 1}
	for n, w := range wantWin {
		if n.Window != w {
			t.Errorf("%s.Window = %d, want %d", n.Text, n.Window, w)
		}
	}
	if app.Parent != nil {
		t.Errorf("app.Parent = %v, want nil (no cross-window parents)", app.Parent.Text)
	}
	if btn.Parent != app || btn.Index != 3 {
		t.Errorf("btn.Parent=%v Index=%d", btn.Parent, btn.Index)
	}
	if got := texts(s.Windows[1].Roots); !equal(got, []string{"app"}) {
		t.Errorf("app window Roots = %v", got)
	}
	if got := texts(kb.Children); !equal(got, []string{"key"}) {
		t.Errorf("kb.Children = %v", got)
	}
}

// Link can be called again (after windows change) without duplicating nodes or children,
// and it clears stale links left on the nodes.
func TestScreenLinkIsRepeatableAndClearsStaleLinks(t *testing.T) {
	stale := &Node{Text: "stale"}
	a := &Node{Text: "a", Depth: 0, Parent: stale, Children: []*Node{stale}, Index: 99, Window: 7}
	b := &Node{Text: "b", Depth: 1}
	s := &Screen{Nodes: []*Node{stale, stale, stale}, Windows: []*Window{{Type: "app", Nodes: []*Node{a, b}, Roots: []*Node{stale}}}}
	s.Link()
	s.Link()

	if got := texts(s.Nodes); !equal(got, []string{"a", "b"}) {
		t.Fatalf("Nodes = %v", got)
	}
	if a.Parent != nil || a.Index != 0 || a.Window != 0 {
		t.Errorf("a: Parent=%v Index=%d Window=%d", a.Parent, a.Index, a.Window)
	}
	if got := texts(a.Children); !equal(got, []string{"b"}) {
		t.Errorf("a.Children = %v", got)
	}
	if got := texts(s.Windows[0].Roots); !equal(got, []string{"a"}) {
		t.Errorf("Roots = %v", got)
	}

	// Re-link after the window's nodes change: b becomes a root.
	b.Depth = 0
	s.Link()
	if b.Parent != nil || len(a.Children) != 0 {
		t.Errorf("after relink: b.Parent=%v a.Children=%v", b.Parent, texts(a.Children))
	}
	if got := texts(s.Windows[0].Roots); !equal(got, []string{"a", "b"}) {
		t.Errorf("Roots after relink = %v", got)
	}
}

// Linking a screen with no windows, or empty windows, leaves no nodes.
func TestScreenLinkEmpty(t *testing.T) {
	s := &Screen{}
	s.Link()
	if len(s.Nodes) != 0 {
		t.Errorf("Nodes = %v", s.Nodes)
	}
	s = &Screen{Windows: []*Window{{Type: "app"}, {Type: "keyboard"}}}
	s.Link()
	if len(s.Nodes) != 0 || s.Windows[0].Roots != nil {
		t.Errorf("Nodes = %v Roots = %v", s.Nodes, s.Windows[0].Roots)
	}
}

func sigScreen(mut func(ns []*Node)) *Screen {
	ns := []*Node{
		{Class: "android.widget.FrameLayout", Depth: 0, Bounds: rect(0, 0, 1080, 2400), Enabled: true},
		{Class: "android.widget.TextView", Text: "Hello", Desc: "greeting", ID: "com.app:id/title", Depth: 1, Bounds: rect(0, 100, 1080, 200), Enabled: true},
		{Class: "android.widget.CheckBox", Text: "Agree", Depth: 1, Bounds: rect(0, 300, 500, 400), Enabled: true, Checkable: true},
	}
	if mut != nil {
		mut(ns)
	}
	s := &Screen{Platform: "android", Width: 1080, Height: 2400, Windows: []*Window{{Type: "app", Nodes: ns}}}
	s.Link()
	return s
}

// Two reads with the same content hash alike, whatever the device's change counter says.
func TestScreenSignatureStable(t *testing.T) {
	a, b := sigScreen(nil), sigScreen(nil)
	b.Seq = 12345
	if a.Signature() != b.Signature() {
		t.Error("identical screens hash differently")
	}
	if a.Signature() != a.Signature() {
		t.Error("Signature is not deterministic")
	}
	empty1, empty2 := &Screen{}, &Screen{Platform: "ios"}
	if empty1.Signature() != empty2.Signature() {
		t.Error("empty screens hash differently")
	}
	if empty1.Signature() == a.Signature() {
		t.Error("an empty screen hashes like a full one")
	}
}

// Each visible attribute (class, text, description, id, bounds, enabled/checked/selected/
// focused state) and the node list itself changes the signature.
func TestScreenSignatureChanges(t *testing.T) {
	base := sigScreen(nil).Signature()
	cases := map[string]func(ns []*Node){
		"text":     func(ns []*Node) { ns[1].Text = "Hello!" },
		"desc":     func(ns []*Node) { ns[1].Desc = "salutation" },
		"id":       func(ns []*Node) { ns[1].ID = "com.app:id/subtitle" },
		"class":    func(ns []*Node) { ns[1].Class = "android.widget.Button" },
		"bounds":   func(ns []*Node) { ns[1].Bounds.B++ },
		"enabled":  func(ns []*Node) { ns[2].Enabled = false },
		"checked":  func(ns []*Node) { ns[2].Checked = true },
		"selected": func(ns []*Node) { ns[2].Selected = true },
		"focused":  func(ns []*Node) { ns[2].Focused = true },
		"order":    func(ns []*Node) { ns[1], ns[2] = ns[2], ns[1] },
		"text moved to desc": func(ns []*Node) {
			ns[1].Text, ns[1].Desc = "", "Hello"
		},
		"text split across fields": func(ns []*Node) {
			ns[1].Text, ns[1].Desc = "Hellogreet", "ing"
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sigScreen(mut).Signature(); got == base {
				t.Errorf("changing %s did not change the signature", name)
			}
		})
	}

	t.Run("extra node", func(t *testing.T) {
		s := sigScreen(nil)
		s.Windows[0].Nodes = append(s.Windows[0].Nodes, &Node{Depth: 1})
		s.Link()
		if s.Signature() == base {
			t.Error("adding a node did not change the signature")
		}
	})
	t.Run("each state flag distinct", func(t *testing.T) {
		seen := map[uint64]string{}
		for name, mut := range map[string]func(ns []*Node){
			"enabled": func(ns []*Node) { ns[2].Enabled = false },
			"checked": func(ns []*Node) { ns[2].Checked = true },
			"both":    func(ns []*Node) { ns[2].Enabled = false; ns[2].Checked = true },
		} {
			sig := sigScreen(mut).Signature()
			if other, ok := seen[sig]; ok {
				t.Errorf("%s and %s hash alike", name, other)
			}
			seen[sig] = name
		}
	})
}

// ClipRect: where a node can show is the screen cut to each scrolling ancestor; a window's
// root (it scrolls the window itself) and ancestors with no bounds are not cuts.
func TestClipRect(t *testing.T) {
	screen := Rect{R: 400, B: 800}
	root := &Node{Scrollable: true, Bounds: Rect{T: -2000, R: 400, B: 3000}} // a page's body, scrolled
	list := &Node{Scrollable: true, Bounds: Rect{T: 100, R: 400, B: 500}, Parent: root}
	inner := &Node{Scrollable: true, Bounds: Rect{L: 50, T: 200, R: 300, B: 700}, Parent: list}
	empty := &Node{Scrollable: true, Parent: inner} // no bounds: no cut
	plain := &Node{Bounds: Rect{T: 0, R: 10, B: 10}, Parent: empty}
	leaf := &Node{Bounds: Rect{L: 60, T: 250, R: 100, B: 270}, Parent: plain}

	if got := leaf.ClipRect(screen); got != (Rect{L: 50, T: 200, R: 300, B: 500}) {
		t.Errorf("nested lists: %v", got)
	}
	if got := root.ClipRect(screen); got != screen {
		t.Errorf("a root: %v", got)
	}
	if got := (&Node{Parent: root}).ClipRect(screen); got != screen {
		t.Errorf("directly in a window's scrolling root: %v (the root is no cut)", got)
	}
	notScrolling := &Node{Bounds: Rect{T: 100, R: 100, B: 200}, Parent: root}
	if got := (&Node{Parent: notScrolling}).ClipRect(screen); got != screen {
		t.Errorf("a container that does not scroll cuts nothing: %v", got)
	}
}
