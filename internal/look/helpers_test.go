package look

import (
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func rect(l, t, r, b int) ui.Rect { return ui.Rect{L: l, T: t, R: r, B: b} }

// row is the i-th full-width 100 px row of a phone screen, starting at y=200.
func row(i int) ui.Rect { return rect(0, 200+i*150, 1080, 300+i*150) }

type opt func(*ui.Node)

// nd is an enabled node; opts set the rest.
func nd(depth int, class string, b ui.Rect, opts ...opt) *ui.Node {
	n := &ui.Node{Depth: depth, Class: class, Bounds: b, Enabled: true}
	for _, o := range opts {
		o(n)
	}
	return n
}

func text(s string) opt { return func(n *ui.Node) { n.Text = s } }
func desc(s string) opt { return func(n *ui.Node) { n.Desc = s } }
func id(s string) opt   { return func(n *ui.Node) { n.ID = s } }
func hint(s string) opt { return func(n *ui.Node) { n.Hint = s } }
func errText(s string) opt {
	return func(n *ui.Node) { n.Error = s }
}
func click(n *ui.Node)      { n.Clickable = true }
func longClick(n *ui.Node)  { n.LongClickable = true }
func checkable(n *ui.Node)  { n.Checkable = true }
func checked(n *ui.Node)    { n.Checked = true; n.Checkable = true }
func selected(n *ui.Node)   { n.Selected = true }
func focused(n *ui.Node)    { n.Focused = true }
func disabled(n *ui.Node)   { n.Enabled = false }
func scrollable(n *ui.Node) { n.Scrollable = true }
func editable(n *ui.Node)   { n.Editable = true }
func password(n *ui.Node)   { n.Password = true }
func showingHint(n *ui.Node) {
	n.ShowingHint = true
	n.Text = n.Hint
}

const (
	pkg    = "com.example.app"
	phoneW = 1080
	phoneH = 2400
)

// appWin is the active app window holding nodes (pre-order, Depth set).
func appWin(nodes ...*ui.Node) *ui.Window {
	return &ui.Window{ID: 1, Type: "app", Package: pkg, Active: true, Focused: true, Bounds: rect(0, 0, phoneW, phoneH), Nodes: nodes}
}

func screenOf(platform string, w, h int, wins ...*ui.Window) *ui.Screen {
	s := &ui.Screen{Platform: platform, Width: w, Height: h, Windows: wins}
	s.Link()
	return s
}

// android is a 1080x2400 Android screen whose app window holds a full-screen root
// FrameLayout (depth 0) and then nodes (depth 1 and below).
func android(nodes ...*ui.Node) *ui.Screen {
	return screenOf("android", phoneW, phoneH, appWin(append([]*ui.Node{nd(0, "android.widget.FrameLayout", rect(0, 0, phoneW, phoneH))}, nodes...)...))
}

// ios is a 390x844 iOS screen with an Application/Window root.
func ios(nodes ...*ui.Node) *ui.Screen {
	w := &ui.Window{Type: "app", Package: pkg, Active: true, Bounds: rect(0, 0, 390, 844),
		Nodes: append([]*ui.Node{nd(0, "Application", rect(0, 0, 390, 844)), nd(1, "Window", rect(0, 0, 390, 844))}, nodes...)}
	return screenOf("ios", 390, 844, w)
}

// web is a 1280x800 page whose body (depth 0) holds nodes.
func web(nodes ...*ui.Node) *ui.Screen {
	w := &ui.Window{ID: 1, Type: "app", Package: "https://example.com/login", Active: true, Bounds: rect(0, 0, 1280, 800),
		Nodes: append([]*ui.Node{nd(0, "body", rect(0, 0, 1280, 800))}, nodes...)}
	return screenOf("web", 1280, 800, w)
}

// lines are the listing's element lines, with refs.
func lines(l *Listing) []string {
	out := make([]string, len(l.Elements))
	for i, e := range l.Elements {
		out[i] = e.Line()
	}
	return out
}

// noRefs are the listing's element lines without refs.
func noRefs(l *Listing) []string {
	out := make([]string, len(l.Elements))
	for i, e := range l.Elements {
		out[i] = e.line(false)
	}
	return out
}

func checkLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// byLabel finds a listed element by its label.
func byLabel(t *testing.T, l *Listing, label string) *Element {
	t.Helper()
	for _, e := range l.Elements {
		if e.Label == label {
			return e
		}
	}
	t.Fatalf("no element %q in:\n%s", label, l.Text())
	return nil
}
