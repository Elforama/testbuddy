package look

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// A typical Android sign-in screen: header line, one line per element with kind, label,
// id, hint and state words, refs in reading order.
func TestBuildAndroidSignIn(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", row(0), text("Sign in")),
		nd(1, "android.widget.EditText", row(1), id("com.example.app:id/email"), hint("Email"), showingHint, click, focused),
		nd(1, "android.widget.EditText", row(2), id("com.example.app:id/password"), hint("Password"), showingHint, click, password),
		nd(1, "android.widget.Button", row(3), id("com.example.app:id/next"), text("Next"), click, disabled),
		nd(1, "android.widget.CheckBox", row(4), text("Remember me"), click, checkable),
	)
	l := Build(s, NewRefs())
	want := "android 1080x2400 com.example.app, 5 elements\n" +
		"@1 Text \"Sign in\"\n" +
		"@2 Field #email hint=\"Email\" focused\n" +
		"@3 SecureField #password hint=\"Password\"\n" +
		"@4 Button \"Next\" #next disabled\n" +
		"@5 CheckBox \"Remember me\" unchecked\n"
	if got := l.Text(); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
	if l.Platform != "android" || l.Width != 1080 || l.Height != 2400 || l.App != pkg || l.Screen != s {
		t.Errorf("listing header fields: %+v", l)
	}
	e := l.ByRef(4)
	if e == nil || e.Node != s.Nodes[4] || e.Bounds != [4]int{0, 650, 1080, 750} {
		t.Errorf("ByRef(4) = %+v", e)
	}
	if l.ByRef(99) != nil || l.ByRef(0) != nil {
		t.Error("ByRef found a ref that is not listed")
	}
}

// An iOS settings screen: control types, cells that only wrap their button are dropped,
// and static text repeated inside a listed control is not listed again.
func TestBuildIOSSettings(t *testing.T) {
	s := ios(
		nd(2, "NavigationBar", rect(0, 47, 390, 91), id("Settings")),
		nd(3, "StaticText", rect(150, 55, 240, 80), text("Settings")),
		nd(2, "Other", rect(0, 100, 390, 800)), // unlabelled container: skipped
		nd(3, "Cell", rect(0, 100, 390, 150), desc("General")),
		nd(4, "Button", rect(0, 100, 390, 150), desc("General")),
		nd(5, "StaticText", rect(20, 110, 200, 140), text("General")),
		nd(3, "Cell", rect(0, 150, 390, 200), desc("Wi-Fi, Home")),
		nd(4, "StaticText", rect(20, 160, 100, 190), text("Wi-Fi")),
		nd(4, "StaticText", rect(300, 160, 370, 190), text("Home")),
		nd(3, "Switch", rect(0, 200, 390, 250), desc("Airplane Mode"), id("airplane"), checked),
		nd(3, "Button", rect(0, 250, 390, 300), desc("Back"), id("BackButton")),
		nd(3, "TextField", rect(0, 300, 390, 350), desc("Search"), hint("Search"), focused),
		nd(3, "Other", rect(0, 350, 390, 400), desc("Banner")),
	)
	l := Build(s, NewRefs())
	checkLines(t, lines(l), []string{
		`@1 View #Settings`,
		`@2 Text "Settings"`,
		`@4 Button "General"`,
		`@5 Cell "Wi-Fi, Home"`,
		`@6 Switch "Airplane Mode" #airplane checked`,
		`@7 Button "Back" #BackButton`,
		`@8 Field "Search" hint="Search" focused`,
		`@9 View "Banner"`,
	})
}

// A web login page: kinds from tags and roles, ids shown when useful, plain containers
// without a label left out even when they have an id.
func TestBuildWebPage(t *testing.T) {
	s := web(
		nd(1, "div", rect(0, 0, 1280, 800), id("main")),
		nd(2, "h1", rect(100, 50, 600, 100), text("Welcome back")),
		nd(2, "input:email", rect(100, 150, 600, 190), id("email"), hint("Email"), editable, click, focused),
		nd(2, "input:password", rect(100, 200, 600, 240), id("pw"), hint("Password"), editable, click, password),
		nd(2, "input:checkbox", rect(100, 250, 120, 270), desc("Remember me"), click),
		nd(2, "button", rect(100, 300, 300, 340), text("Log in"), click),
		nd(2, "a", rect(400, 300, 600, 340), text("Forgot password?"), click),
		nd(2, "select", rect(100, 400, 300, 440), text("English"), click),
		nd(2, "div[button]", rect(100, 500, 140, 540), desc("Close"), click),
		nd(2, "p", rect(100, 600, 900, 640), text("By signing in you agree."), id("legal")),
		nd(2, "div", rect(100, 700, 900, 740)), // empty: left out
	)
	l := Build(s, NewRefs())
	if !strings.HasPrefix(l.Text(), "web 1280x800 https://example.com/login, 9 elements\n") {
		t.Errorf("header: %q", l.Text())
	}
	checkLines(t, lines(l), []string{
		`@1 Heading "Welcome back"`,
		`@2 Field #email hint="Email" focused`,
		`@3 SecureField #pw hint="Password"`,
		`@4 CheckBox "Remember me" unchecked`,
		`@5 Button "Log in"`,
		`@6 Link "Forgot password?"`,
		`@7 Select "English"`,
		`@8 Button "Close"`,
		`@9 Text "By signing in you agree." #legal`,
	})
}

// State words: checked/unchecked for anything checkable, selected, focused only on fields,
// disabled only on things one acts on, scrollable, and the error text.
func TestBuildStateWords(t *testing.T) {
	cases := []struct {
		name string
		n    *ui.Node
		want string
	}{
		{"switch off", nd(1, "android.widget.Switch", row(0), text("Wi-Fi"), click, checkable), `Switch "Wi-Fi" unchecked`},
		{"switch on", nd(1, "android.widget.Switch", row(0), text("Wi-Fi"), click, checked), `Switch "Wi-Fi" checked`},
		{"switch kind without checkable flag", nd(1, "android.widget.Switch", row(0), text("Wi-Fi")), `Switch "Wi-Fi" unchecked`},
		{"radio", nd(1, "android.widget.RadioButton", row(0), text("Small"), click), `Radio "Small" unchecked`},
		{"checkable row", nd(1, "android.widget.LinearLayout", row(0), desc("Agree"), checkable), `Button "Agree" unchecked`},
		{"selected tab", nd(1, "android.widget.TextView", row(0), text("Home"), click, selected), `Button "Home" selected`},
		{"focused field", nd(1, "android.widget.EditText", row(0), text("hi"), focused), `Field "hi" focused`},
		{"focused button is not marked", nd(1, "android.widget.Button", row(0), text("Go"), click, focused), `Button "Go"`},
		{"disabled button", nd(1, "android.widget.Button", row(0), text("Pay"), disabled), `Button "Pay" disabled`},
		{"disabled clickable view", nd(1, "android.view.View", row(0), desc("Pay"), click, disabled), `Button "Pay" disabled`},
		{"disabled field", nd(1, "android.widget.EditText", row(0), text("x"), disabled), `Field "x" disabled`},
		{"disabled text is not marked", nd(1, "android.widget.TextView", row(0), text("Note"), disabled), `Text "Note"`},
		{"scrollable named list", nd(1, "androidx.recyclerview.widget.RecyclerView", row(0), id("feed"), scrollable), `View #feed scrollable`},
		{"error", nd(1, "android.widget.EditText", row(0), id("com.example.app:id/email"), text("bob"), errText("Enter a valid email")), `Field "bob" #email error="Enter a valid email"`},
		{"long error is clipped", nd(1, "android.widget.EditText", row(0), errText(strings.Repeat("e", 70))), `Field error="` + strings.Repeat("e", 59) + `…"`},
		{"all states in order", nd(1, "android.widget.CheckBox", row(0), text("All"), click, checked, selected, disabled, scrollable, errText("bad")),
			`CheckBox "All" checked selected disabled scrollable error="bad"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := Build(android(c.n), NewRefs())
			checkLines(t, noRefs(l), []string{c.want})
		})
	}
}

// Fields: the placeholder is shown as hint= (not as the label); typed text is the label;
// an accessibility label equal to the hint is still the label.
func TestBuildFieldLabelsAndHints(t *testing.T) {
	cases := []struct {
		name string
		n    *ui.Node
		want string
	}{
		{"placeholder showing", nd(1, "android.widget.EditText", row(0), hint("Email"), showingHint), `Field hint="Email"`},
		{"typed text", nd(1, "android.widget.EditText", row(0), hint("Email"), text("bob@x.io")), `Field "bob@x.io" hint="Email"`},
		{"text equal to hint", nd(1, "android.widget.EditText", row(0), hint("Email"), text("Email")), `Field hint="Email"`},
		{"desc wins over hint", nd(1, "android.widget.EditText", row(0), hint("Email"), showingHint, desc("Email address")), `Field "Email address" hint="Email"`},
		{"empty field", nd(1, "android.widget.EditText", row(0)), `Field`},
		{"long hint clipped", nd(1, "android.widget.EditText", row(0), hint(strings.Repeat("h", 50))), `Field hint="` + strings.Repeat("h", 39) + `…"`},
		{"hint on a non-field is ignored", nd(1, "android.widget.Button", row(0), text("Go"), hint("tooltip"), click), `Button "Go"`},
		{"password field", nd(1, "android.widget.EditText", row(0), hint("PIN"), showingHint, password), `SecureField hint="PIN"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkLines(t, noRefs(Build(android(c.n), NewRefs())), []string{c.want})
		})
	}
}

// Ids are shown only when unique on screen; Android layout ids only on fields and things one
// acts on; React Native testIDs and iOS identifiers on anything.
func TestBuildIDs(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", row(0), id("com.example.app:id/title"), text("Title")),
		nd(1, "android.widget.Button", row(1), id("com.example.app:id/delete"), text("Delete"), click),
		nd(1, "android.widget.Button", row(2), id("com.example.app:id/delete"), text("Delete"), click),
		nd(1, "android.widget.Button", row(3), id("com.example.app:id/save"), text("Save"), click),
		nd(1, "android.view.ViewGroup", row(4), id("home_screen")),
		nd(1, "android.widget.TextView", row(5), id("price_label"), text("$5")),
		nd(1, "android.view.ViewGroup", row(6), id("com.example.app:id/container")), // layout id, nothing else: left out
		nd(1, "android.widget.TextView", row(7), id("dup"), text("A")),
		nd(1, "android.widget.TextView", row(8), id("dup"), text("B")),
	)
	checkLines(t, lines(Build(s, NewRefs())), []string{
		`@1 Text "Title"`,
		`@2 Button "Delete"`,
		`@3 Button "Delete"`,
		`@4 Button "Save" #save`,
		`@5 View #home_screen`,
		`@6 Text "$5" #price_label`,
		`@7 Text "A"`,
		`@8 Text "B"`,
	})
}

// A node is listed only if it has a readable label, is a field, can be acted on, or has a
// useful id: decorative images and glyph-only text are left out.
func TestBuildWhatGetsALine(t *testing.T) {
	s := android(
		nd(1, "android.widget.ImageView", row(0)),                        // decorative
		nd(1, "android.widget.ImageView", row(1), desc("Logo")),          // labelled image
		nd(1, "android.widget.TextView", row(2), text("")),              // icon glyph
		nd(1, "android.widget.TextView", row(3), text("›"), click),       // glyph, but clickable
		nd(1, "android.widget.ImageView", row(4), click),                 // unlabelled button
		nd(1, "android.widget.TextView", row(5), text("   ")),            // blank
		nd(1, "android.widget.LinearLayout", row(6)),                     // plain layout
		nd(1, "android.widget.TextView", row(7), text("Price: 5")),       // text
		nd(1, "android.widget.TextView", row(8), id("glyph"), text("★")), // glyph with a testID
	)
	checkLines(t, lines(Build(s, NewRefs())), []string{
		`@1 Image "Logo"`,
		`@2 Button "›"`,
		`@3 Button`,
		`@4 Text "Price: 5"`,
		`@5 Text "★" #glyph`,
	})
}

// An actionable element without a label of its own is labelled by up to four texts inside
// it; those texts are then not listed again.
func TestBuildInnerTextLabels(t *testing.T) {
	s := android(
		nd(1, "android.widget.LinearLayout", row(0), click),
		nd(2, "android.widget.TextView", rect(20, 210, 500, 250), text("Wi‑Fi")),
		nd(2, "android.widget.TextView", rect(20, 250, 500, 290), text("Connected")),
		nd(1, "android.widget.LinearLayout", row(1), click),
		nd(2, "android.widget.TextView", rect(0, 350, 100, 400), text("a")),
		nd(2, "android.widget.LinearLayout", rect(100, 350, 500, 400)),
		nd(3, "android.widget.TextView", rect(100, 350, 200, 400), text("b")),
		nd(3, "android.widget.TextView", rect(200, 350, 300, 400), text("c")),
		nd(2, "android.widget.TextView", rect(500, 350, 600, 400), text("d")),
		nd(2, "android.widget.TextView", rect(600, 350, 700, 400), text("e")),
		nd(1, "android.widget.LinearLayout", row(2)), // not actionable: no label from inside
		nd(2, "android.widget.TextView", rect(0, 500, 500, 600), text("Inside")),
	)
	checkLines(t, lines(Build(s, NewRefs())), []string{
		`@1 Button "Wi‑Fi, Connected"`,
		`@2 Button "a, b, c, d"`,
		`@3 Text "e"`,
		`@4 Text "Inside"`,
	})
}

// A label already shown by the nearest listed ancestor is dropped for text, views and
// images (React Native repeats a button's text in a child), but kept for controls and for
// children with a useful id (and then the wrapper that only repeats it goes instead).
func TestBuildDropsRepeatedLabels(t *testing.T) {
	s := android(
		nd(1, "android.view.ViewGroup", row(0), desc("Sign in"), click),
		nd(2, "android.widget.TextView", row(0), text("Sign in")),
		nd(1, "android.view.ViewGroup", row(1), desc("Sign up now"), click),
		nd(2, "android.widget.TextView", row(1), text("Sign up")), // contained in the parent's label
		nd(1, "android.view.ViewGroup", row(2), desc("Pay"), click),
		nd(2, "android.widget.TextView", row(2), text("Pay $5")), // text containing the parent's label
		nd(1, "android.view.ViewGroup", row(3), desc("Next"), click),
		nd(2, "android.widget.TextView", row(3), id("next_label"), text("Next")), // useful id: kept
		nd(1, "android.view.ViewGroup", row(4), desc("Card"), click),
		nd(2, "android.widget.Button", row(4), text("Card"), click), // a control: kept
		nd(1, "android.view.ViewGroup", row(5), desc("Profile"), click),
		nd(2, "android.widget.TextView", row(5), text("Edit")), // different text: kept
		nd(2, "android.widget.ImageView", row(5), desc("Profile")),
	)
	checkLines(t, lines(Build(s, NewRefs())), []string{
		`@1 Button "Sign in"`,
		`@2 Button "Sign up now"`,
		`@3 Button "Pay"`,
		`@5 Text "Next" #next_label`, // @4, the button around it, only repeated it
		`@7 Button "Card"`,           // likewise @6
		`@8 Button "Profile"`,
		`@9 Text "Edit"`,
	})
}

// The repeat check uses the nearest *listed* ancestor, skipping unlisted containers.
func TestBuildRepeatedLabelThroughUnlistedContainer(t *testing.T) {
	s := android(
		nd(1, "android.view.ViewGroup", row(0), desc("Sign in"), click),
		nd(2, "android.widget.FrameLayout", row(0)), // not listed
		nd(3, "android.widget.TextView", row(0), text("Sign in")),
		nd(1, "android.widget.FrameLayout", row(1)), // not listed, and nothing listed above it
		nd(2, "android.widget.TextView", row(1), text("Alone")),
	)
	checkLines(t, noRefs(Build(s, NewRefs())), []string{`Button "Sign in"`, `Text "Alone"`})
}

// A container (cell, view or button) whose label only repeats its single listed child is
// dropped in favour of the child; with an id, more than one child, or extra text it stays.
func TestBuildDropsWrappers(t *testing.T) {
	cases := []struct {
		name  string
		nodes []*ui.Node
		want  []string
	}{
		{"cell around its button", []*ui.Node{
			nd(3, "Cell", rect(0, 100, 390, 150), desc("General")),
			nd(4, "Button", rect(0, 100, 390, 150), desc("General")),
		}, []string{`@2 Button "General"`}},
		{"cell repeating the label twice", []*ui.Node{
			nd(3, "Cell", rect(0, 100, 390, 150), desc("General, General")),
			nd(4, "Button", rect(0, 100, 390, 150), desc("General")),
		}, []string{`@2 Button "General"`}},
		{"view around its switch", []*ui.Node{
			nd(3, "Other", rect(0, 100, 390, 150), desc("Wi-Fi")),
			nd(4, "Switch", rect(300, 100, 390, 150), desc("Wi-Fi")),
		}, []string{`@2 Switch "Wi-Fi" unchecked`}},
		{"cell with an id stays", []*ui.Node{
			nd(3, "Cell", rect(0, 100, 390, 150), id("general_cell"), desc("General")),
			nd(4, "Button", rect(0, 100, 390, 150), desc("General")),
		}, []string{`@1 Cell "General" #general_cell`, `@2 Button "General"`}},
		{"cell with more text stays", []*ui.Node{
			nd(3, "Cell", rect(0, 100, 390, 150), desc("General, About")),
			nd(4, "Button", rect(0, 100, 390, 150), desc("General")),
		}, []string{`@1 Cell "General, About"`, `@2 Button "General"`}},
		{"cell with two controls stays", []*ui.Node{
			nd(3, "Cell", rect(0, 100, 390, 150), desc("Wi-Fi")),
			nd(4, "Switch", rect(300, 100, 390, 150), desc("Wi-Fi")),
			nd(4, "Button", rect(250, 100, 300, 150), desc("More Info")),
		}, []string{`@1 Cell "Wi-Fi"`, `@3 Button "More Info"`, `@2 Switch "Wi-Fi" unchecked`}},
		{"text kinds are not wrappers", []*ui.Node{
			nd(3, "Link", rect(0, 100, 390, 150), desc("Help")),
			nd(4, "Button", rect(0, 100, 390, 150), desc("Help")),
		}, []string{`@1 Link "Help"`, `@2 Button "Help"`}},
		{"nested wrappers keep the innermost", []*ui.Node{
			nd(3, "Cell", rect(0, 100, 390, 150), desc("General")),
			nd(4, "Other", rect(0, 100, 390, 150), desc("General")), // a repeated view: dropped while listing
			nd(5, "Button", rect(0, 100, 390, 150), desc("General")),
		}, []string{`@2 Button "General"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checkLines(t, lines(Build(ios(c.nodes...), NewRefs())), c.want)
		})
	}
}

// Elements are listed in reading order: top to bottom, and left to right within a row
// (tops within 8 px count as one row); refs keep their tree order.
func TestBuildReadingOrder(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(600, 300, 900, 400), text("row2 right")),
		nd(1, "android.widget.TextView", rect(0, 306, 500, 400), text("row2 left (6px lower)")),
		nd(1, "android.widget.TextView", rect(0, 100, 500, 200), text("row1")),
		nd(1, "android.widget.TextView", rect(0, 320, 500, 400), text("row3 (20px lower)")),
	)
	checkLines(t, lines(Build(s, NewRefs())), []string{
		`@3 Text "row1"`,
		`@2 Text "row2 left (6px lower)"`,
		`@1 Text "row2 right"`,
		`@4 Text "row3 (20px lower)"`,
	})
}

// Long labels are clipped to 80 characters and whitespace is collapsed.
func TestBuildClipsLabels(t *testing.T) {
	long := strings.Repeat("word ", 30)
	s := android(
		nd(1, "android.widget.TextView", row(0), text(long)),
		nd(1, "android.widget.TextView", row(1), text("  two\n\nlines  ")),
	)
	l := Build(s, NewRefs())
	if got := l.Elements[0].Label; len([]rune(got)) != 80 || !strings.HasSuffix(got, "…") {
		t.Errorf("clipped label = %q (%d runes)", got, len([]rune(got)))
	}
	if got := l.Elements[1].Label; got != "two lines" {
		t.Errorf("collapsed label = %q", got)
	}
}

// The window root container, nodes off screen (but not their on-screen children) and
// unlabelled iOS "Other" views are not listed.
func TestBuildSkipsContainersAndOffScreen(t *testing.T) {
	s := android(
		nd(1, "android.widget.TextView", rect(0, 2500, 1080, 2600), text("Below the fold")),
		nd(1, "android.widget.HorizontalScrollView", rect(-2000, 100, -100, 200), desc("Carousel"), click),
		nd(2, "android.widget.TextView", rect(10, 100, 300, 200), text("Peeking in")),
		nd(1, "android.widget.TextView", rect(1000, 2350, 1200, 2450), text("Partly on")),
	)
	checkLines(t, noRefs(Build(s, NewRefs())), []string{`Text "Peeking in"`, `Text "Partly on"`})

	labelledRoot := screenOf("android", phoneW, phoneH, appWin(
		nd(0, "android.widget.FrameLayout", rect(0, 0, phoneW, phoneH), desc("Main")),
		nd(1, "android.widget.TextView", row(0), text("Hi")),
	))
	checkLines(t, noRefs(Build(labelledRoot, NewRefs())), []string{`View "Main"`, `Text "Hi"`})

	leafRoot := screenOf("android", phoneW, phoneH, appWin(nd(0, "android.widget.TextView", row(0), text("Only"))))
	checkLines(t, noRefs(Build(leafRoot, NewRefs())), []string{`Text "Only"`})

	iosScreen := ios(
		nd(2, "Other", rect(0, 100, 390, 200)),
		nd(3, "StaticText", rect(0, 100, 390, 200), text("Inside other")),
		nd(2, "Other", rect(0, 200, 390, 300), id("hero")),
	)
	checkLines(t, noRefs(Build(iosScreen, NewRefs())), []string{`Text "Inside other"`, `View #hero`})
}

// The keyboard is one "Keyboard" line: a keyboard window, an iOS Keyboard element or an
// Android input-method view all count, and their keys are not listed.
func TestBuildKeyboard(t *testing.T) {
	field := nd(1, "android.widget.EditText", row(0), hint("Message"), showingHint, focused)
	kbWin := &ui.Window{Type: "keyboard", Package: "com.google.android.inputmethod.latin", Bounds: rect(0, 1600, 1080, 2400),
		Nodes: []*ui.Node{nd(0, "android.widget.FrameLayout", rect(0, 1600, 1080, 2400)), nd(1, "android.widget.Button", rect(0, 1600, 100, 1700), text("q"), click)}}
	s := screenOf("android", phoneW, phoneH, kbWin, appWin(nd(0, "android.widget.FrameLayout", rect(0, 0, phoneW, phoneH)), field))
	l := Build(s, NewRefs())
	if want := "android 1080x2400 com.example.app, 1 elements\n@1 Field hint=\"Message\" focused\nKeyboard\n"; l.Text() != want {
		t.Errorf("keyboard window:\n%s", l.Text())
	}

	iosKB := ios(
		nd(2, "TextField", rect(0, 100, 390, 150), hint("To"), focused),
		nd(2, "Keyboard", rect(0, 500, 390, 844)),
		nd(3, "Key", rect(0, 500, 40, 550), desc("q")),
	)
	l = Build(iosKB, NewRefs())
	if !l.Keyboard || len(l.Elements) != 1 {
		t.Errorf("iOS keyboard element: keyboard=%v\n%s", l.Keyboard, l.Text())
	}

	ime := android(
		nd(1, "android.widget.FrameLayout", rect(0, 1600, 1080, 2400), id("com.google.android.inputmethod.latin:id/keyboard_holder")),
		nd(2, "android.widget.Button", rect(0, 1600, 100, 1700), text("q"), click),
	)
	l = Build(ime, NewRefs())
	if !l.Keyboard || len(l.Elements) != 0 {
		t.Errorf("input-method view: keyboard=%v\n%s", l.Keyboard, l.Text())
	}

	flagged := android(nd(1, "android.widget.TextView", row(0), text("x")))
	flagged.Keyboard = true
	if l := Build(flagged, NewRefs()); !l.Keyboard || !strings.HasSuffix(l.Text(), "Keyboard\n") {
		t.Errorf("screen keyboard flag not carried over")
	}
}

// Status bars (as elements, or as thin system windows at the screen edges) are dropped.
func TestBuildDropsBars(t *testing.T) {
	status := &ui.Window{Type: "system", Package: "com.android.systemui", Bounds: rect(0, 0, 1080, 63),
		Nodes: []*ui.Node{nd(0, "android.widget.FrameLayout", rect(0, 0, 1080, 63)), nd(1, "android.widget.TextView", rect(0, 0, 200, 63), text("12:30"))}}
	nav := &ui.Window{Type: "system", Package: "com.android.systemui", Bounds: rect(0, 2337, 1080, 2400),
		Nodes: []*ui.Node{nd(0, "android.widget.FrameLayout", rect(0, 2337, 1080, 2400)), nd(1, "android.widget.ImageView", rect(500, 2337, 580, 2400), desc("Home"), click)}}
	app := appWin(nd(0, "android.widget.FrameLayout", rect(0, 0, phoneW, phoneH)), nd(1, "android.widget.TextView", row(0), text("Content")))
	l := Build(screenOf("android", phoneW, phoneH, status, nav, app), NewRefs())
	checkLines(t, noRefs(l), []string{`Text "Content"`})
	if l.Dialog != "" {
		t.Errorf("systemui bars reported as a dialog: %q", l.Dialog)
	}

	iosBar := ios(
		nd(2, "StatusBar", rect(0, 0, 390, 47)),
		nd(3, "StaticText", rect(10, 10, 60, 40), text("9:41")),
		nd(2, "StaticText", rect(0, 100, 390, 150), text("Inbox")),
	)
	checkLines(t, noRefs(Build(iosBar, NewRefs())), []string{`Text "Inbox"`})
}

// A system window on top of the app (not systemui) is reported as a dialog, and its
// elements are listed with the app's.
func TestBuildSystemDialog(t *testing.T) {
	dialog := &ui.Window{Type: "system", Package: "com.google.android.permissioncontroller", Bounds: rect(60, 800, 1020, 1600),
		Nodes: []*ui.Node{
			nd(0, "android.widget.FrameLayout", rect(60, 800, 1020, 1600)),
			nd(1, "android.widget.TextView", rect(100, 850, 980, 1000), text("Allow Example to send you notifications?")),
			nd(1, "android.widget.Button", rect(100, 1300, 980, 1400), text("Allow"), click),
		}}
	app := appWin(nd(0, "android.widget.FrameLayout", rect(0, 0, phoneW, phoneH)), nd(1, "android.widget.TextView", row(0), text("Home")))
	l := Build(screenOf("android", phoneW, phoneH, dialog, app), NewRefs())
	if l.Dialog != "com.google.android.permissioncontroller" {
		t.Errorf("Dialog = %q", l.Dialog)
	}
	want := "android 1080x2400 com.example.app, 3 elements\n" +
		"system dialog: com.google.android.permissioncontroller\n" +
		"@3 Text \"Home\"\n" +
		"@1 Text \"Allow Example to send you notifications?\"\n" +
		"@2 Button \"Allow\"\n"
	if got := l.Text(); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}

	// A system window that is not the top one, or the only window, is not a dialog.
	below := Build(screenOf("android", phoneW, phoneH, app, dialog), NewRefs())
	alone := Build(screenOf("android", phoneW, phoneH, dialog), NewRefs())
	if below.Dialog != "" || alone.Dialog != "" {
		t.Errorf("below=%q alone=%q, want no dialog", below.Dialog, alone.Dialog)
	}
	if alone.App != "" || !strings.HasPrefix(alone.Text(), "android 1080x2400, 2 elements\n") {
		t.Errorf("no app window: header %q", strings.SplitN(alone.Text(), "\n", 2)[0])
	}
}

// An empty screen lists nothing.
func TestBuildEmpty(t *testing.T) {
	l := Build(&ui.Screen{Platform: "ios", Width: 390, Height: 844}, NewRefs())
	if got := l.Text(); got != "ios 390x844, 0 elements\n" {
		t.Errorf("Text() = %q", got)
	}
}

// The JSON listing carries refs, kinds, labels, ids, states and bounds, but not the nodes.
func TestListingJSON(t *testing.T) {
	s := android(nd(1, "android.widget.Button", row(0), id("com.example.app:id/ok"), text("OK"), click, disabled))
	b, err := json.Marshal(Build(s, NewRefs()))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"platform":"android","width":1080,"height":2400,"app":"com.example.app","elements":[{"ref":1,"kind":"Button","label":"OK","id":"ok","states":["disabled"],"bounds":[0,200,1080,300]}],"keyboard":false}`
	if string(b) != want {
		t.Errorf("JSON =\n%s\nwant\n%s", b, want)
	}
}
