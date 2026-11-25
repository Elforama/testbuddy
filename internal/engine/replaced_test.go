package engine

import (
	"context"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

func screenOf(t *testing.T, nodes ...*ui.Node) *ui.Screen {
	t.Helper()
	s, err := (&fakeDriver{screen: static(nodes...)}).Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func tapCmd(t *testing.T, text string) *flow.Command {
	t.Helper()
	cmds, err := flow.ParseCommands("", []byte("- tapOn: \""+text+"\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	return cmds[0]
}

func TestIssue421InPlaceOf(t *testing.T) {
	was := &ui.Node{Text: "Search", ID: "search", Bounds: ui.Rect{L: 0, T: 100, R: 80, B: 150}}
	wide := &ui.Node{Text: "Search for, Roof Repair", Bounds: ui.Rect{L: 0, T: 100, R: 300, B: 150}}
	sameID := &ui.Node{Text: "Search for, Roof Repair", ID: "search", Bounds: ui.Rect{L: 0, T: 100, R: 500, B: 150}}
	page := &ui.Node{Text: "Results", Bounds: ui.Rect{L: 0, T: 0, R: 1080, B: 2000}}
	below := &ui.Node{Text: "Recent searches", Bounds: ui.Rect{L: 0, T: 160, R: 80, B: 210}}
	cases := []struct {
		name  string
		nodes []*ui.Node
		want  *ui.Node
	}{
		{"a wider label in place", []*ui.Node{wide}, wide},
		{"the same id, much wider", []*ui.Node{sameID}, sameID},
		{"a page around it is not its replacement", []*ui.Node{page}, nil},
		{"an element below is not in its place", []*ui.Node{below}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, n := range c.nodes {
				n.Visible = true
			}
			got := inPlaceOf(was, screenOf(t, c.nodes...))
			if (got == nil) != (c.want == nil) || (got != nil && got.Label() != c.want.Label()) {
				t.Errorf("inPlaceOf = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIssue421ReplacedInPlaceFailsAfterTheGrace(t *testing.T) {
	old := replacedGrace
	replacedGrace = 150 * time.Millisecond
	t.Cleanup(func() { replacedGrace = old })
	seen := screenOf(t, btn("Search", "", 100), btn("Ask", "", 300))
	cases := []struct {
		name     string
		seen     *ui.Screen
		cmds     int
		fast     bool
		inMsgFor string
	}{
		{"seen in the last look", seen, 1, true, `"Search" (in your last look) is gone; at its place now:  "Search for, Roof Repair"`},
		{"no last look (a flow)", nil, 1, false, ""},
		{"a later command of the same do", seen, 2, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeDriver{screen: static(btn("Search for, Roof Repair", "", 100), btn("Ask", "", 300))}
			r := New(f, Options{LookupTimeout: 1500 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond})
			r.SetSeen(c.seen)
			cmds := []*flow.Command{tapCmd(t, "Search")}
			if c.cmds == 2 {
				cmds = []*flow.Command{tapCmd(t, "Ask"), tapCmd(t, "Search")}
			}
			start := time.Now()
			err := r.RunCommands(context.Background(), cmds, "app", nil)
			took := time.Since(start)
			if err == nil {
				t.Fatal("the tap passed")
			}
			if c.fast && (took > time.Second || !contains(err.Error(), c.inMsgFor)) {
				t.Errorf("took %s, err %v", took, err)
			}
			if !c.fast && (took < 1400*time.Millisecond || contains(err.Error(), "in your last look")) {
				t.Errorf("took %s, err %v: want the full lookup time", took, err)
			}
		})
	}
}
