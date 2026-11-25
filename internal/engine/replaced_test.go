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

func parseCmds(t *testing.T, src string) []*flow.Command {
	t.Helper()
	cmds, err := flow.ParseCommands("", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return cmds
}

func TestIssue421ReplacedInPlaceFailsAfterTheGrace(t *testing.T) {
	old := replacedGrace
	replacedGrace = 150 * time.Millisecond
	t.Cleanup(func() { replacedGrace = old })
	seen := screenOf(t, btn("Search", "", 100), btn("Ask", "", 300))
	now := []*ui.Node{btn("Search for, Roof Repair", "", 100), btn("Ask", "", 300)}
	cases := []struct {
		name string
		seen *ui.Screen
		now  []*ui.Node
		cmds string
		fast bool
	}{
		{"a tap on a label seen in the last look", seen, now, `- tapOn: "Search"`, true},
		{"no last look (a flow)", nil, now, `- tapOn: "Search"`, false},
		{"a later command of the same do", seen, now, "- tapOn: \"Ask\"\n- tapOn: \"Search\"", false},
		{"an assertion waits its time", seen, now, `- assertVisible: "Search"`, false},
		{"a tap inside a runFlow", seen, now, "- runFlow:\n    commands:\n      - tapOn: \"Search\"", false},
		{"a passing label (Saving…)", seen, []*ui.Node{btn("Searching…", "", 100), btn("Ask", "", 300)}, `- tapOn: "Search"`, false},
		{"the last look was another screen", screenOf(t, btn("Search", "", 100), btn("Ask", "", 300), btn("Home", "", 500), btn("Inbox", "", 700)),
			[]*ui.Node{btn("Settings", "", 100), btn("Wi-Fi", "", 300), btn("Bluetooth", "", 500)}, `- tapOn: "Search"`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeDriver{screen: static(c.now...)}
			r := New(f, Options{LookupTimeout: 1500 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond})
			r.SetSeen(c.seen)
			start := time.Now()
			err := r.RunCommands(context.Background(), parseCmds(t, c.cmds), "app", nil)
			took := time.Since(start)
			if err == nil {
				t.Fatal("the step passed")
			}
			if c.fast && (took > time.Second || !contains(err.Error(), `"Search" (in your last look) is gone; at its place now: "Search for, Roof Repair"`)) {
				t.Errorf("took %s, err %v", took, err)
			}
			if !c.fast && (took < 1400*time.Millisecond || contains(err.Error(), "in your last look")) {
				t.Errorf("took %s, err %v: want the full lookup time", took, err)
			}
		})
	}
}

func TestIssue421AFreshReadComesBeforeFailing(t *testing.T) {
	old := replacedGrace
	replacedGrace = 300 * time.Millisecond
	t.Cleanup(func() { replacedGrace = old })
	start := time.Now()
	f := &fakeDriver{screen: func() []*ui.Node {
		if time.Since(start) < 250*time.Millisecond {
			return []*ui.Node{btn("Search for, Roof Repair", "", 100), btn("Ask", "", 300)}
		}
		return []*ui.Node{btn("Search", "", 100), btn("Ask", "", 300)}
	}}
	r := New(f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: 100 * time.Millisecond})
	r.SetSeen(screenOf(t, btn("Search", "", 100), btn("Ask", "", 300)))
	if err := r.RunCommands(context.Background(), parseCmds(t, `- tapOn: "Search"`), "app", nil); err != nil {
		t.Errorf("the label came back silently before the grace ended, but the tap failed: %v", err)
	}
}
