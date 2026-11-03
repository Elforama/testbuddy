package look

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// slot is the i-th compact 40 px row (room for 52 rows on a phone screen).
func slot(i int) ui.Rect { return rect(0, 50+i*45, 1080, 90+i*45) }

// textScreen is an Android screen with one TextView per label, top to bottom.
func textScreen(labels ...string) *ui.Screen {
	var ns []*ui.Node
	for i, l := range labels {
		ns = append(ns, nd(1, "android.widget.TextView", slot(i), text(l)))
	}
	return android(ns...)
}

func numbered(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s %d", prefix, i+1)
	}
	return out
}

// The same element keeps its ref across reads; a new one gets the next number, even when
// it appears above the others.
func TestRefsStableAcrossReads(t *testing.T) {
	refs := NewRefs()
	first := Build(textScreen("A", "B"), refs)
	again := Build(textScreen("A", "B"), refs)
	checkLines(t, lines(first), []string{`@1 Text "A"`, `@2 Text "B"`})
	checkLines(t, lines(again), lines(first))

	inserted := Build(textScreen("New", "A", "B"), refs)
	checkLines(t, lines(inserted), []string{`@3 Text "New"`, `@1 Text "A"`, `@2 Text "B"`})
}

// Refs are never reused: a gone element's number stays reserved and comes back with it.
func TestRefsNotReused(t *testing.T) {
	refs := NewRefs()
	Build(textScreen("A", "B"), refs)
	Build(textScreen("A"), refs)
	checkLines(t, lines(Build(textScreen("A", "C"), refs)), []string{`@1 Text "A"`, `@3 Text "C"`})
	checkLines(t, lines(Build(textScreen("B"), refs)), []string{`@2 Text "B"`})
}

// Identity is the id when there is one (so a relabelled element keeps its ref), else the
// kind and label; equal elements are told apart by rank.
func TestRefsIdentity(t *testing.T) {
	refs := NewRefs()
	follow := func(label string) *ui.Screen {
		return android(nd(1, "android.widget.Button", row(0), id("com.example.app:id/follow"), text(label), click))
	}
	checkLines(t, lines(Build(follow("Follow"), refs)), []string{`@1 Button "Follow" #follow`})
	checkLines(t, lines(Build(follow("Following"), refs)), []string{`@1 Button "Following" #follow`})

	// Without an id a new label is a new element; so is a new kind.
	refs = NewRefs()
	Build(textScreen("Save"), refs)
	checkLines(t, lines(Build(textScreen("Saved"), refs)), []string{`@2 Text "Saved"`})
	checkLines(t, lines(Build(android(nd(1, "android.widget.Button", slot(0), text("Save"))), refs)), []string{`@3 Button "Save"`})

	// Equal elements get one ref per rank: when the first goes, the second takes its ref.
	refs = NewRefs()
	checkLines(t, lines(Build(textScreen("Delete", "Delete"), refs)), []string{`@1 Text "Delete"`, `@2 Text "Delete"`})
	checkLines(t, lines(Build(textScreen("Delete"), refs)), []string{`@1 Text "Delete"`})
}

// Each Refs counts on its own.
func TestRefsIndependent(t *testing.T) {
	a, b := NewRefs(), NewRefs()
	Build(textScreen("x", "y", "z"), a)
	checkLines(t, lines(Build(textScreen("z"), b)), []string{`@1 Text "z"`})
	checkLines(t, lines(Build(textScreen("z"), a)), []string{`@3 Text "z"`})
}

// Diff describes a change: "+" new elements, "~" changed ones, "-" gone ones, in that order.
func TestDiffChanges(t *testing.T) {
	wifi := func(on bool, extra string) *ui.Screen {
		opts := []opt{text("Wi-Fi"), click, checkable}
		if on {
			opts = append(opts, checked)
		}
		return android(
			nd(1, "android.widget.TextView", slot(0), text("Settings")),
			nd(1, "android.widget.Switch", slot(1), opts...),
			nd(1, "android.widget.TextView", slot(2), text(extra)),
		)
	}
	cases := []struct {
		name      string
		prev, now *ui.Screen
		want      string
	}{
		{"unchanged", wifi(false, "Old"), wifi(false, "Old"), "screen unchanged\n"},
		{"state changed", wifi(false, "Old"), wifi(true, "Old"), "~ @2 Switch \"Wi-Fi\" checked\n"},
		{"added", textScreen("A", "B", "C"), textScreen("A", "B", "C", "Saved"), "+ @4 Text \"Saved\"\n"},
		{"removed", textScreen("A", "B", "C"), textScreen("A", "B"), "- @3 Text \"C\"\n"},
		{"all three, in order", wifi(false, "Old"), wifi(true, "New"),
			"+ @4 Text \"New\"\n~ @2 Switch \"Wi-Fi\" checked\n- @3 Text \"Old\"\n"},
		{"everything gone", textScreen("A", "B"), textScreen(), "- @1 Text \"A\"\n- @2 Text \"B\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refs := NewRefs()
			prev := Build(c.prev, refs)
			if got := Diff(prev, Build(c.now, refs)); got != c.want {
				t.Errorf("Diff =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

// A relabelled element with an id is a change, not a new element.
func TestDiffRelabelWithID(t *testing.T) {
	refs := NewRefs()
	btn := func(label string) *ui.Screen {
		return android(
			nd(1, "android.widget.TextView", slot(0), text("Profile")),
			nd(1, "android.widget.Button", slot(1), id("com.example.app:id/follow"), text(label), click),
		)
	}
	prev := Build(btn("Follow"), refs)
	if got := Diff(prev, Build(btn("Following"), refs)); got != "~ @2 Button \"Following\" #follow\n" {
		t.Errorf("Diff = %q", got)
	}
}

// The keyboard showing or hiding is its own line.
func TestDiffKeyboard(t *testing.T) {
	refs := NewRefs()
	without := Build(textScreen("A"), refs)
	kb := textScreen("A")
	kb.Keyboard = true
	with := Build(kb, refs)
	if got := Diff(without, with); got != "+ Keyboard\n" {
		t.Errorf("keyboard up: %q", got)
	}
	if got := Diff(with, without); got != "- Keyboard\n" {
		t.Errorf("keyboard down: %q", got)
	}
	kb2 := textScreen("A", "B")
	kb2.Keyboard = true
	if got := Diff(without, Build(kb2, refs)); got != "+ @2 Text \"B\"\n+ Keyboard\n" {
		t.Errorf("keyboard up with a new element: %q", got)
	}
}

// At most 8 gone elements are listed, then a count of the rest.
func TestDiffCapsRemoved(t *testing.T) {
	refs := NewRefs()
	prev := Build(textScreen(append([]string{"K1", "K2"}, numbered("R", 10)...)...), refs)
	got := Diff(prev, Build(textScreen("K1", "K2"), refs))
	gotLines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(gotLines) != 9 || gotLines[0] != `- @3 Text "R 1"` || gotLines[7] != `- @10 Text "R 8"` || gotLines[8] != "- (2 more)" {
		t.Errorf("Diff =\n%s", got)
	}
	// Exactly 8 gone: no count line.
	refs = NewRefs()
	prev = Build(textScreen(append([]string{"K"}, numbered("R", 8)...)...), refs)
	if got := Diff(prev, Build(textScreen("K"), refs)); strings.Contains(got, "more") || strings.Count(got, "\n") != 8 {
		t.Errorf("8 removed:\n%s", got)
	}
}

// When most of the screen is new (more than 12 new elements, over two thirds new, or
// another app) the whole listing is cheaper to read than a diff.
func TestDiffWholeNewScreen(t *testing.T) {
	cases := []struct {
		name      string
		prev, now []string
		whole     bool
	}{
		{"two of three new: still a diff", []string{"A"}, []string{"A", "X", "Y"}, false},
		{"three of four new: whole screen", []string{"A"}, []string{"A", "X", "Y", "Z"}, true},
		{"all new", []string{"A"}, []string{"X"}, true},
		{"12 new among many: diff", numbered("Old", 30), append(numbered("Old", 30), numbered("New", 12)...), false},
		{"13 new among many: whole screen", numbered("Old", 25), append(numbered("Old", 25), numbered("New", 13)...), true},
		{"nothing left: diff", []string{"A", "B"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refs := NewRefs()
			prev := Build(textScreen(c.prev...), refs)
			now := Build(textScreen(c.now...), refs)
			got := Diff(prev, now)
			if isWhole := got == "screen now:\n"+now.Text(); isWhole != c.whole {
				t.Errorf("whole screen = %v, want %v:\n%s", isWhole, c.whole, got)
			}
		})
	}
}

// Another app in front means a whole new screen, even with the same elements; with no
// previous listing there is nothing to diff against.
func TestDiffAppChangeAndFirstRead(t *testing.T) {
	refs := NewRefs()
	prev := Build(textScreen("A", "B"), refs)
	other := textScreen("A", "B")
	other.Windows[0].Package = "com.other.app"
	now := Build(other, refs)
	if got := Diff(prev, now); got != "screen now:\n"+now.Text() {
		t.Errorf("app change: %q", got)
	}
	if got := Diff(nil, prev); got != "screen now:\n"+prev.Text() || !strings.Contains(got, "@1 Text \"A\"") {
		t.Errorf("first read: %q", got)
	}
}

// Similar suggests up to 5 elements whose label or id contains a 4+ letter word of the
// query, case-insensitively, in listing order.
func TestSimilar(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", slot(0), text("Sign in")),
		nd(1, "android.widget.Button", slot(1), text("Sign up"), click),
		nd(1, "android.widget.TextView", slot(2), text("Settings")),
		nd(1, "android.widget.ImageButton", slot(3), id("signin_help"), click),
		nd(1, "android.widget.EditText", slot(4), id("com.example.app:id/password"), hint("Password"), showingHint),
		nd(1, "android.widget.Button", slot(5), text("Logout"), click),
		nd(1, "android.widget.TextView", slot(6), text("Report 2024")),
	)
	l := Build(s, NewRefs())
	cases := []struct {
		query string
		want  []string
	}{
		{"Sign In", []string{"Sign in", "Sign up", "#signin_help"}},
		{"SIGN", []string{"Sign in", "Sign up", "#signin_help"}},
		{"password", []string{"#password"}}, // a field found by its id
		{"Log out", nil},                    // words under 4 letters are ignored
		{"Logout?", []string{"Logout"}},     // punctuation splits words
		{"setting", []string{"Settings"}},   // a word inside a longer label
		{"2024", []string{"Report 2024"}},   // digits count
		{"Nothing like it", nil},
		{"", nil},
		{"ok", nil},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			var got []string
			for _, e := range l.Similar(c.query) {
				if e.Label != "" {
					got = append(got, e.Label)
				} else {
					got = append(got, "#"+e.ID)
				}
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Errorf("Similar(%q) = %v, want %v", c.query, got, c.want)
			}
		})
	}

	many := Build(textScreen(numbered("Item", 8)...), NewRefs())
	if got := many.Similar("item 3"); len(got) != 5 || got[0].Label != "Item 1" {
		t.Errorf("Similar is not capped at 5: %d", len(got))
	}
}
