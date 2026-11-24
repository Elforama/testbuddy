package look

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

func slot(i int) ui.Rect { return rect(0, 50+i*45, 1080, 90+i*45) }

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

func TestRefsStableAcrossReads(t *testing.T) {
	refs := NewRefs()
	first := Build(textScreen("A", "B"), refs)
	again := Build(textScreen("A", "B"), refs)
	checkLines(t, lines(first), []string{`@1 Text "A"`, `@2 Text "B"`})
	checkLines(t, lines(again), lines(first))

	inserted := Build(textScreen("New", "A", "B"), refs)
	checkLines(t, lines(inserted), []string{`@3 Text "New"`, `@1 Text "A"`, `@2 Text "B"`})
}

func TestRefsNotReused(t *testing.T) {
	refs := NewRefs()
	Build(textScreen("A", "B"), refs)
	Build(textScreen("A"), refs)
	checkLines(t, lines(Build(textScreen("A", "C"), refs)), []string{`@1 Text "A"`, `@3 Text "C"`})
	checkLines(t, lines(Build(textScreen("B"), refs)), []string{`@2 Text "B"`})
}

func TestRefsIdentity(t *testing.T) {
	refs := NewRefs()
	follow := func(label string) *ui.Screen {
		return android(nd(1, "android.widget.Button", row(0), id("com.example.app:id/follow"), text(label), click))
	}
	checkLines(t, lines(Build(follow("Follow"), refs)), []string{`@1 Button "Follow" #follow`})
	checkLines(t, lines(Build(follow("Following"), refs)), []string{`@1 Button "Following" #follow`})

	refs = NewRefs()
	Build(textScreen("Save"), refs)
	checkLines(t, lines(Build(textScreen("Saved"), refs)), []string{`@2 Text "Saved"`})
	checkLines(t, lines(Build(android(nd(1, "android.widget.Button", slot(0), text("Save"))), refs)), []string{`@3 Button "Save"`})

	refs = NewRefs()
	checkLines(t, lines(Build(textScreen("Delete", "Delete"), refs)), []string{`@1 Text "Delete"`, `@2 Text "Delete"`})
	checkLines(t, lines(Build(textScreen("Delete"), refs)), []string{`@1 Text "Delete"`})
}

func TestRefsIndependent(t *testing.T) {
	a, b := NewRefs(), NewRefs()
	Build(textScreen("x", "y", "z"), a)
	checkLines(t, lines(Build(textScreen("z"), b)), []string{`@1 Text "z"`})
	checkLines(t, lines(Build(textScreen("z"), a)), []string{`@3 Text "z"`})
}

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
		{"all three, in order", wifi(false, "Old"), android(
			nd(1, "android.widget.TextView", slot(0), text("Settings")),
			nd(1, "android.widget.Switch", slot(1), text("Wi-Fi"), click, checked),
			nd(1, "android.widget.Button", slot(2), text("New"), click),
		), "+ @4 Button \"New\"\n~ @2 Switch \"Wi-Fi\" checked\n- @3 Text \"Old\"\n"},
		{"text changed in place", wifi(false, "Status: old"), wifi(false, "Status: new"), "~ @4 Text \"Status: new\"\n"},
		{"another text in its place", wifi(false, "Inbox"), wifi(false, "Archive"), "+ @4 Text \"Archive\"\n- @3 Text \"Inbox\"\n"},
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

func TestDiffCapsRemoved(t *testing.T) {
	refs := NewRefs()
	prev := Build(textScreen(append([]string{"K1", "K2"}, numbered("R", 10)...)...), refs)
	got := Diff(prev, Build(textScreen("K1", "K2"), refs))
	gotLines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(gotLines) != 9 || gotLines[0] != `- @3 Text "R 1"` || gotLines[7] != `- @10 Text "R 8"` || gotLines[8] != "- (2 more)" {
		t.Errorf("Diff =\n%s", got)
	}
	refs = NewRefs()
	prev = Build(textScreen(append([]string{"K"}, numbered("R", 8)...)...), refs)
	if got := Diff(prev, Build(textScreen("K"), refs)); strings.Contains(got, "more") || strings.Count(got, "\n") != 8 {
		t.Errorf("8 removed:\n%s", got)
	}
}

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
		{"password", []string{"#password"}},
		{"Log out", nil},
		{"Logout?", []string{"Logout"}},
		{"setting", []string{"Settings"}},
		{"2024", []string{"Report 2024"}},
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
