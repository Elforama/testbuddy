package match

import (
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func TestTextMatches(t *testing.T) {
	cases := []struct {
		pat, val string
		want     bool
	}{
		{"Log", "Login", false},
		{"Log.*", "Login", true},
		{"login", "LOGIN", true},
		{"Hello (world)", "Hello (world)", true},
		{"Price ($5", "Price ($5", true},
		{"a b", "a\nb", true},
		{"Line.*end", "Line\nmid\nend", true},
		{"Sign In", "Sign in", true},
		{"(?=Sign).*", "Sign up", true},
	}
	for _, c := range cases {
		if got := TextMatches(c.pat, c.val); got != c.want {
			t.Errorf("TextMatches(%q, %q) = %v, want %v", c.pat, c.val, got, c.want)
		}
	}
}

func screen(nodes ...*ui.Node) *ui.Screen {
	s := &ui.Screen{Width: 1000, Height: 2000, Windows: []*ui.Window{{Type: "app", Nodes: nodes}}}
	s.Link()
	return s
}

func n(depth int, text, id string, l, t, r, b int, click bool) *ui.Node {
	return &ui.Node{Depth: depth, Text: text, ID: id, Bounds: ui.Rect{L: l, T: t, R: r, B: b}, Clickable: click, Enabled: true}
}

func TestFindDeepestAndClickableFirst(t *testing.T) {
	s := screen(
		n(0, "", "", 0, 0, 1000, 2000, false),
		n(1, "OK", "com.app:id/ok_button", 0, 100, 500, 200, true),
		n(2, "OK", "", 10, 110, 490, 190, false),
		n(1, "OK", "", 0, 900, 500, 1000, true),
		n(1, "Far", "", 0, 5000, 100, 5100, false),
	)
	got, _, _ := Find(s, &Selector{Text: "OK"})
	if got != s.Nodes[3] {
		t.Fatalf("got %+v", got)
	}
	got, _, _ = Find(s, &Selector{ID: "ok_button"})
	if got != s.Nodes[1] {
		t.Fatalf("id after slash: got %+v", got)
	}
	got, _, _ = Find(s, &Selector{Text: "Far"})
	if got != nil {
		t.Fatalf("off-screen node matched")
	}
	got, _, _ = Find(s, &Selector{Text: "OK", Index: "-1"})
	if got != s.Nodes[3] {
		t.Fatalf("index -1: got %+v", got)
	}
	got, _, _ = Find(s, &Selector{Text: "OK", Below: &Selector{ID: "ok_button"}})
	if got != s.Nodes[3] {
		t.Fatalf("below: got %+v", got)
	}
}
