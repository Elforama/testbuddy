package look

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/elforama/testbuddy/internal/match"
	"github.com/elforama/testbuddy/internal/ui"
)

// actionScreen covers the selector cases: ids, plain and regex-special text, a description,
// an unlabelled icon and an editable field.
func actionScreen() *ui.Screen {
	return android(
		nd(1, "android.widget.Button", row(0), id("com.example.app:id/next"), text("Next"), click),
		nd(1, "android.widget.TextView", row(1), id("com.example.app:id/title"), text("Welcome")),
		nd(1, "android.widget.TextView", row(2), text("Sign in")),
		nd(1, "android.widget.TextView", row(3), text("Price $5 (incl. tax)")),
		nd(1, "android.widget.ImageButton", row(4), desc("Close"), click),
		nd(1, "android.widget.ImageView", rect(980, 950, 1080, 1050), click),
		nd(1, "android.widget.EditText", row(6), id("com.example.app:id/email"), hint("Email"), text("bob@x.io"), editable, click),
		nd(1, "android.widget.EditText", row(7), hint("Phone"), showingHint, desc("Phone number"), editable, click),
	)
}

// SelectorFor prefers the id, then the text (regex-escaped), then a point; for checks an
// element with both id and text is checked on both (unless it is editable).
func TestSelectorFor(t *testing.T) {
	s := actionScreen()
	l := Build(s, NewRefs())
	cases := []struct {
		label         string
		action, check string
	}{
		{"Next", `{id: "next"}`, `{id: "next", text: "Next"}`},
		{"Welcome", `{id: "title"}`, `{id: "title", text: "Welcome"}`},
		{"Sign in", `"Sign in"`, `"Sign in"`},
		{"Price $5 (incl. tax)", `"Price \\$5 \\(incl\\. tax\\)"`, `"Price \\$5 \\(incl\\. tax\\)"`},
		{"Close", `"Close"`, `"Close"`},
		{"", `{point: "95%,41%"}`, `{point: "95%,41%"}`}, // the unlabelled icon: its centre
		{"bob@x.io", `{id: "email"}`, `{id: "email"}`},   // typed text changes: not part of a check
		{"Phone number", `"Phone number"`, `"Phone number"`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			e := byLabel(t, l, c.label)
			if got := SelectorFor(s, e, false); got != c.action {
				t.Errorf("action selector = %s, want %s", got, c.action)
			}
			if got := SelectorFor(s, e, true); got != c.check {
				t.Errorf("check selector = %s, want %s", got, c.check)
			}
		})
	}
}

// With several matches the selector adds the element's index in screen order; clickable-
// first picking means the clickable one of two equal texts needs no index.
func TestSelectorForDuplicates(t *testing.T) {
	s := android(
		nd(1, "android.widget.Button", row(0), id("com.example.app:id/delete"), text("Delete"), click),
		nd(1, "android.widget.Button", row(1), id("com.example.app:id/delete"), text("Delete"), click),
		nd(1, "android.widget.TextView", row(2), text("OK")),
		nd(1, "android.widget.Button", row(3), text("OK"), click),
		nd(1, "android.widget.TextView", row(4), text("Total")),
		nd(1, "android.widget.TextView", row(5), text("Total")),
		nd(1, "android.widget.TextView", row(6), text("Total")),
	)
	l := Build(s, NewRefs())
	want := []string{
		`{id: "delete"}`,
		`{id: "delete", index: 1}`,
		`{text: "OK", index: 0}`,
		`"OK"`,
		`"Total"`,
		`{text: "Total", index: 1}`,
		`{text: "Total", index: 2}`,
	}
	for i, e := range l.Elements {
		if got := SelectorFor(s, e, false); got != want[i] {
			t.Errorf("%s: selector = %s, want %s", e.Line(), got, want[i])
		}
	}
}

// A row without text of its own is found by the texts inside it, skipping any that would
// not find it (here a child that is too far off screen to match).
func TestSelectorForRowsUseInnerTexts(t *testing.T) {
	s := android(
		nd(1, "android.widget.LinearLayout", row(0), click),
		nd(2, "android.widget.TextView", rect(20, 210, 500, 250), text("Wi‑Fi")),
		nd(2, "android.widget.TextView", rect(20, 250, 500, 290), text("Connected")),
		nd(1, "android.widget.LinearLayout", row(1), click),
		nd(2, "android.widget.TextView", rect(0, 2390, 1080, 2590), text("Hidden")), // 5% on screen
		nd(2, "android.widget.TextView", rect(20, 360, 500, 440), text("Shown")),
	)
	l := Build(s, NewRefs())
	if got := SelectorFor(s, byLabel(t, l, "Wi‑Fi, Connected"), false); got != `"Wi‑Fi"` {
		t.Errorf("row selector = %s", got)
	}
	if got := SelectorFor(s, byLabel(t, l, "Hidden, Shown"), false); got != `"Shown"` {
		t.Errorf("row with an unmatchable first text = %s", got)
	}
}

// An element the matcher cannot see (under 10% on screen) falls back to a point for an
// action; a check keeps its text, which is what the assertion is about.
func TestSelectorForUnmatchable(t *testing.T) {
	s := android(nd(1, "android.widget.TextView", rect(0, 2390, 1080, 2590), text("Edge")))
	e := byLabel(t, Build(s, NewRefs()), "Edge")
	if got := SelectorFor(s, e, false); got != `{point: "50%,103%"}` {
		t.Errorf("action = %s", got)
	}
	if got := SelectorFor(s, e, true); got != `"Edge"` {
		t.Errorf("check = %s", got)
	}
	// With no screen size the point is in pixels.
	n := &ui.Node{Bounds: rect(500, 1000, 580, 1400)}
	if got := SelectorFor(&ui.Screen{}, &Element{Node: n}, false); got != `{point: "540,1200"}` {
		t.Errorf("pixel point = %s", got)
	}
}

// iOS identifiers are used as they are (regex-escaped); labels come from the description.
func TestSelectorForIOS(t *testing.T) {
	s := ios(
		nd(2, "Button", rect(0, 50, 100, 90), id("BackButton"), desc("Back")),
		nd(2, "Cell", rect(0, 100, 390, 150), id("item.1"), desc("Item")),
		nd(2, "Cell", rect(0, 150, 390, 200), desc("Wi-Fi, Home")),
		nd(3, "StaticText", rect(20, 160, 100, 190), text("Wi-Fi")),
	)
	l := Build(s, NewRefs())
	for label, want := range map[string]string{
		"Back":        `{id: "BackButton"}`,
		"Item":        `{id: "item\\.1"}`,
		"Wi-Fi, Home": `"Wi-Fi, Home"`,
	} {
		if got := SelectorFor(s, byLabel(t, l, label), false); got != want {
			t.Errorf("%s: %s, want %s", label, got, want)
		}
	}
	if got := SelectorFor(s, byLabel(t, l, "Back"), true); got != `{id: "BackButton", text: "Back"}` {
		t.Errorf("check: %s", got)
	}
}

// parseSelector reads a SelectorFor result the way a flow would.
func parseSelector(t *testing.T, src string) (*match.Selector, bool) {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatalf("selector %s is not valid YAML: %v", src, err)
	}
	switch v := v.(type) {
	case string:
		return &match.Selector{Text: v}, true
	case map[string]any:
		if _, ok := v["point"]; ok {
			return nil, false
		}
		sel := &match.Selector{}
		for k, x := range v {
			switch k {
			case "id":
				sel.ID = x.(string)
			case "text":
				sel.Text = x.(string)
			case "index":
				sel.Index = fmt.Sprint(x)
			default:
				t.Fatalf("selector %s has unexpected key %q", src, k)
			}
		}
		return sel, true
	}
	t.Fatalf("selector %s is a %T", src, v)
	return nil, false
}

// Every selector SelectorFor writes is valid YAML that finds the element again (itself, or
// a node inside it or around it), on every platform. A check (assertion) selector need not
// single out one of several equal elements: finding an equal one (same id and label) is
// enough for an assertion.
func TestSelectorForRoundTrip(t *testing.T) {
	screens := map[string]*ui.Screen{
		"actions": actionScreen(),
		"duplicates": android(
			nd(1, "android.widget.Button", row(0), id("com.example.app:id/delete"), text("Delete"), click),
			nd(1, "android.widget.Button", row(1), id("com.example.app:id/delete"), text("Delete"), click),
			nd(1, "android.widget.TextView", row(2), text("a.b")),
			nd(1, "android.widget.TextView", row(3), text("a.b")),
			nd(1, "android.widget.TextView", row(4), text(`back\slash "quoted" [x]{2}`)),
			nd(1, "android.widget.TextView", row(5), text("Ünïcödé ✓")),
		),
		"ios": ios(
			nd(2, "Cell", rect(0, 100, 390, 150), desc("General")),
			nd(3, "Button", rect(0, 100, 390, 150), desc("General")),
			nd(2, "Switch", rect(0, 150, 390, 200), id("airplane"), desc("Airplane Mode")),
			nd(2, "TextField", rect(0, 200, 390, 250), desc("Search"), hint("Search")),
			nd(2, "StaticText", rect(0, 250, 390, 300), text("Line one\nline two")),
		),
		"web": web(
			nd(1, "h1", rect(100, 50, 600, 100), text("Welcome back")),
			nd(1, "input:email", rect(100, 150, 600, 190), id("email"), hint("Email"), editable, click),
			nd(1, "button", rect(100, 300, 300, 340), text("Log in"), click),
			nd(1, "a", rect(400, 300, 600, 340), text("Log in"), click),
			nd(1, "div[button]", rect(100, 500, 140, 540), desc("Close (x)"), click),
		),
	}
	for name, s := range screens {
		l := Build(s, NewRefs())
		for _, e := range l.Elements {
			for _, check := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/check=%v", name, e.line(false), check), func(t *testing.T) {
					src := SelectorFor(s, e, check)
					sel, ok := parseSelector(t, src)
					if !ok {
						return // a point: nothing to find
					}
					got, _, err := match.Find(s, sel)
					if err != nil || got == nil {
						t.Fatalf("selector %s finds nothing (%v)", src, err)
					}
					related := got == e.Node || got.IsDescendantOf(e.Node) || e.Node.IsDescendantOf(got)
					equal := check && got.ID == e.Node.ID && got.Label() == e.Node.Label()
					if !related && !equal {
						t.Errorf("selector %s finds %s %v, not the element at %v", src, got.ShortClass(), got.Bounds, e.Node.Bounds)
					}
				})
			}
		}
	}
}

// BUG check: a field known only by its placeholder should be selected by that text (the
// matcher matches hints), not by a screen point.
func TestSelectorForHintOnlyField(t *testing.T) {
	s := android(nd(1, "android.widget.EditText", row(0), hint("Email"), showingHint, editable, click))
	l := Build(s, NewRefs())
	if got := SelectorFor(s, l.Elements[0], false); got != `"Email"` {
		t.Errorf("selector = %s, want \"Email\"", got)
	}
}

// ResolveRefs swaps @N for the element's selector, with check selectors on assertion lines.
func TestResolveRefs(t *testing.T) {
	s := actionScreen()
	l := Build(s, NewRefs())
	cases := []struct {
		name, in, want string
	}{
		{"tap", "tapOn: @1", `tapOn: {id: "next"}`},
		{"assertion gets a check selector", "assertVisible: @2", `assertVisible: {id: "title", text: "Welcome"}`},
		{"not-visible assertion", "assertNotVisible: @1", `assertNotVisible: {id: "next", text: "Next"}`},
		{"wait condition line is a check", "- extendedWaitUntil:\n    visible: @2\n    timeout: 5000",
			"- extendedWaitUntil:\n    visible: {id: \"title\", text: \"Welcome\"}\n    timeout: 5000"},
		{"each line on its own", "- tapOn: @2\n- assertVisible: @2\n",
			"- tapOn: {id: \"title\"}\n- assertVisible: {id: \"title\", text: \"Welcome\"}\n"},
		{"text selector", "- tapOn: @3", `- tapOn: "Sign in"`},
		{"inside a map", "- tapOn: {below: @3, index: 0}", `- tapOn: {below: "Sign in", index: 0}`},
		{"two refs on a line", "- swipe: {from: @3, to: @5}", `- swipe: {from: "Sign in", to: "Close"}`},
		{"double-quoted text untouched", `- inputText: "@1"`, `- inputText: "@1"`},
		{"single-quoted text untouched", `- inputText: 'call @1 now'`, `- inputText: 'call @1 now'`},
		{"after a quoted value", `- tapOn: {text: "x", below: @1}`, `- tapOn: {text: "x", below: {id: "next"}}`},
		{"email without digits untouched", "- inputText: user@example.com", "- inputText: user@example.com"},
		{"bare at sign", "- inputText: a @ b @", "- inputText: a @ b @"},
		{"no refs", "- tapOn: Next\n- back", "- tapOn: Next\n- back"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveRefs(c.in, l, s)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("ResolveRefs(%q) =\n%q\nwant\n%q", c.in, got, c.want)
			}
		})
	}
}

// A multi-digit ref is read whole (@12 is not @1 followed by "2").
func TestResolveRefsMultiDigit(t *testing.T) {
	s := textScreen(numbered("Row", 12)...)
	l := Build(s, NewRefs())
	got, err := ResolveRefs("- tapOn: @12\n- tapOn: @1", l, s)
	if err != nil || got != "- tapOn: \"Row 12\"\n- tapOn: \"Row 1\"" {
		t.Errorf("got %q, %v", got, err)
	}
}

// Refs need a listing that has them; text without refs needs no listing at all.
func TestResolveRefsErrors(t *testing.T) {
	s := actionScreen()
	l := Build(s, NewRefs())
	if _, err := ResolveRefs("tapOn: @99", l, s); err == nil || !strings.Contains(err.Error(), "@99 is not on the last listed screen") {
		t.Errorf("unknown ref: %v", err)
	}
	if _, err := ResolveRefs("tapOn: @1", nil, nil); err == nil || !strings.Contains(err.Error(), "no screen listed yet") {
		t.Errorf("no listing: %v", err)
	}
	if got, err := ResolveRefs("tapOn: Next", nil, nil); err != nil || got != "tapOn: Next" {
		t.Errorf("no refs, no listing: %q %v", got, err)
	}
	if got, err := ResolveRefs(`inputText: "me@1.io"`, nil, nil); err != nil || got != `inputText: "me@1.io"` {
		t.Errorf("quoted @ with no listing: %q %v", got, err)
	}
}

// BUG check: "@digits" inside an unquoted word is not a ref (YAML lets plain scalars
// contain "@" after the first character).
func TestResolveRefsInsideWord(t *testing.T) {
	s := actionScreen()
	l := Build(s, NewRefs())
	got, err := ResolveRefs("- inputText: me@123.io", l, s)
	if err != nil || got != "- inputText: me@123.io" {
		t.Errorf("got %q, %v", got, err)
	}
}
