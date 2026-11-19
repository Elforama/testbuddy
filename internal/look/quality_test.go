package look

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// Fixtures here follow `tb hierarchy --json` dumps of the bench app on an iOS 26 simulator
// (402x874 screens shrunk to the 390x844 of the ios helper where it does not matter).

// asVal sets an iOS value shown as the node's text (a slider's "50%", a switch's "0").
func asVal(v string) opt { return func(n *ui.Node) { n.Text, n.Value = v, v } }

// iosBar is an iOS scroll bar: an Other labelled with its axis and pages, holding its knob.
func iosBar(depth int, b ui.Rect, label, pct string) []*ui.Node {
	return []*ui.Node{
		nd(depth, "Other", b, desc(label), asVal(pct)),
		nd(depth+1, "Other", rect(b.R-3, b.B-7, b.R, b.B)),
	}
}

func nodes(groups ...[]*ui.Node) []*ui.Node {
	var out []*ui.Node
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// A scroll view listed for its id (iOS Controls' #page) says what it holds below the fold,
// as a group header does: the elements out of view were counted, then swallowed as being
// "inside a listed element" (the scroll view itself).
func TestOutlineListedScrollViewCountsHidden(t *testing.T) {
	s := ios(
		nd(2, "ScrollView", rect(0, 148, 390, 844), id("page"), scrollable),
		nd(3, "Other", rect(0, 148, 390, 1600)),
		nd(4, "Button", rect(20, 160, 140, 190), desc("One"), id("tab_one")),
		nd(4, "StaticText", rect(20, 200, 140, 220), desc("Tab: One"), id("tabs_status")),
		nd(4, "Button", rect(20, 900, 140, 930), desc("Start"), id("progress_go")),
		nd(4, "StaticText", rect(20, 940, 140, 960), desc("Progress: 0%"), id("progress_status")),
		nd(4, "Button", rect(20, 1000, 140, 1030), desc("Hold me"), id("longpress_button")),
		nd(5, "StaticText", rect(20, 1000, 140, 1030), desc("Hold me")),
	)
	checkBody(t, Build(s, NewRefs()), `@1 View #page scrollable (3 more below)
  @2 Button "One" #tab_one
  @3 Text "Tab: One" #tabs_status
`)
}

// iOS scroll bars ("Vertical scroll bar, 2 pages" "0%") are not listed: they become the
// scroll area's hint of what it holds out of view, the only one a lazily loaded list gives
// (its rows below the fold are not in the tree).
func TestOutlineIOSScrollBarsAreHints(t *testing.T) {
	list := func(bar string, pct string) *ui.Screen {
		return ios(nodes(
			[]*ui.Node{
				nd(2, "CollectionView", rect(0, 127, 390, 844), id("scenarios"), scrollable),
				nd(3, "Other", rect(0, -1368, 390, 2545)),
				nd(3, "Cell", rect(0, 127, 390, 191)),
				nd(4, "Button", rect(0, 127, 390, 191), desc("Journey"), id("scenario_journey")),
				nd(5, "StaticText", rect(20, 149, 82, 169), desc("Journey")),
				nd(3, "Cell", rect(0, 191, 390, 255)),
				nd(4, "Button", rect(0, 191, 390, 255), desc("Lists"), id("scenario_lists")),
			},
			iosBar(3, rect(369, 127, 389, 812), bar, pct),
			iosBar(3, rect(369, 127, 389, 812), bar, pct),
		)...)
	}
	for _, c := range []struct {
		bar, pct, more string
	}{
		{"Vertical scroll bar, 2 pages", "0%", " (more below)"},
		{"Vertical scroll bar, 41 pages", "25%", " (more above, more below)"},
		{"Vertical scroll bar, 41 pages", "100%", " (more above)"},
		{"Vertical scroll bar, 1 page", "0%", ""},
		{"Horizontal scroll bar, 3 pages", "50%", " (more to the left, more to the right)"},
	} {
		l := Build(list(c.bar, c.pct), NewRefs())
		want := `@1 View #scenarios scrollable` + c.more + `
  @2 Button "Journey" #scenario_journey
  @3 Button "Lists" #scenario_lists
`
		if got := body(l); got != want {
			t.Errorf("%s %s: listing =\n%s\nwant\n%s", c.bar, c.pct, got, want)
		}
		for _, e := range l.Elements {
			if strings.Contains(e.Label, "scroll bar") {
				t.Errorf("%s: scroll bar listed: %s", c.bar, e.Line())
			}
		}
	}
	// A known count wins over the bar's "more".
	s := ios(nodes(
		[]*ui.Node{
			nd(2, "ScrollView", rect(0, 148, 390, 844), id("page"), scrollable),
			nd(3, "Button", rect(20, 160, 140, 190), desc("One"), id("one")),
			nd(3, "Button", rect(20, 900, 140, 930), desc("Two"), id("two")),
		},
		iosBar(3, rect(369, 148, 389, 812), "Vertical scroll bar, 4 pages", "0%"),
	)...)
	checkBody(t, Build(s, NewRefs()), `@1 View #page scrollable (1 more below)
  @2 Button "One" #one
`)
	// An alert's own scroll views say "2 pages" for a one-line title: no hint there.
	a := ios(nodes(
		[]*ui.Node{
			nd(2, "Alert", rect(41, 388, 361, 514), desc("Delete item?")),
			nd(3, "ScrollView", rect(41, 388, 361, 434), scrollable),
			nd(4, "StaticText", rect(71, 410, 331, 430), desc("Delete item?"), id("dialog_title")),
		},
		iosBar(4, rect(328, 388, 358, 434), "Vertical scroll bar, 2 pages", "0%"),
		iosBar(4, rect(41, 408, 361, 431), "Horizontal scroll bar, 1 page", "0%"),
		[]*ui.Node{
			nd(3, "ScrollView", rect(41, 434, 361, 514), scrollable),
			nd(4, "Button", rect(57, 450, 197, 498), desc("Cancel")),
			nd(4, "Button", rect(205, 450, 345, 498), desc("Delete")),
		},
		iosBar(4, rect(328, 434, 358, 514), "Vertical scroll bar, 1 page", "0%"),
	)...)
	got := body(Build(a, NewRefs()))
	if strings.Contains(got, "scroll bar") || strings.Contains(got, "more") {
		t.Errorf("alert listing =\n%s\nwant no scroll bars and no hint", got)
	}
}

// Wrappers and twins that only repeat what is listed: a switch's inner switch, a segmented
// control around its segments, glyph images in labelled buttons, a control's caption beside
// it, and iOS's full-screen Application and Toolbar.
func TestBuildDropsDuplicatesAndWrappers(t *testing.T) {
	s := ios(
		nd(2, "Toolbar", rect(0, 0, 390, 844), desc("Toolbar"), id("Toolbar")),
		nd(2, "SegmentedControl", rect(20, 221, 370, 252)),
		nd(3, "Button", rect(20, 221, 136, 253), desc("S"), id("seg_s")),
		nd(3, "Button", rect(136, 221, 253, 253), desc("M"), id("seg_m"), selected),
		nd(3, "Button", rect(253, 221, 370, 253), desc("L"), id("seg_l")),
		nd(2, "Button", rect(20, 296, 78, 316), desc("Red"), id("radio_red")),
		nd(3, "StaticText", rect(20, 296, 78, 316), desc("Red")),
		nd(3, "Image", rect(21, 298, 38, 315), desc("Circle"), id("circle")),
		nd(2, "Button", rect(20, 324, 94, 345), desc("Green"), id("radio_green"), selected),
		nd(3, "StaticText", rect(20, 324, 94, 345), desc("Green")),
		nd(3, "Image", rect(21, 326, 38, 343), desc("Large Filled Circle Inside A Circle"), id("largecircle.fill.circle")),
		nd(2, "Button", rect(20, 353, 81, 373), desc("Blue"), id("radio_blue")),
		nd(3, "StaticText", rect(20, 353, 81, 373), desc("Blue")),
		nd(3, "Image", rect(21, 354, 38, 371), desc("Circle"), id("circle")),
		nd(2, "Switch", rect(310, 510, 370, 538), desc("Wi-Fi"), id("switch_wifi"), asVal("0")),
		nd(3, "Switch", rect(310, 510, 372, 538), asVal("0")),
		nd(2, "StaticText", rect(20, 788, 104, 809), desc("Pick a date")),
		nd(2, "DatePicker", rect(257, 781, 370, 816), desc("Pick a date"), id("date_open")),
		nd(3, "Button", rect(257, 781, 370, 816), desc("Date Picker"), asVal("Nov 18, 2026")),
	)
	s.Nodes[0].Desc = "Buddy Bench" // the Application
	checkLines(t, noRefs(Build(s, NewRefs())), []string{
		`Button "S" #seg_s`,
		`Button "M" #seg_m selected`,
		`Button "L" #seg_l`,
		`Button "Red" #radio_red`,
		`Button "Green" #radio_green selected`,
		`Button "Blue" #radio_blue`,
		`Switch "Wi-Fi" #switch_wifi unchecked`,
		`View "Pick a date" #date_open`,
		`Button "Date Picker" value="Nov 18, 2026"`,
	})
	// Kept: an image with an id of its own in a button, a control's caption that differs, and
	// a segmented control whose segments are not all listed apart.
	k := ios(
		nd(2, "Button", rect(20, 100, 370, 160), desc("Profile"), id("profile")),
		nd(3, "Image", rect(20, 100, 60, 160), desc("Avatar"), id("profile_photo")),
		nd(2, "StaticText", rect(20, 200, 104, 220), desc("Due")),
		nd(2, "DatePicker", rect(257, 195, 370, 225), desc("Pick a date"), id("date_open")),
	)
	checkLines(t, noRefs(Build(k, NewRefs())), []string{
		`Button "Profile" #profile`,
		`Image "Avatar" #profile_photo`,
		`Text "Due"`,
		`View "Pick a date" #date_open`,
	})
}

// A slider's or progress view's value is shown as its value, not as its name.
func TestBuildValueKinds(t *testing.T) {
	s := ios(
		nd(2, "Slider", rect(20, 591, 370, 622), id("slider_volume"), asVal("50%")),
		nd(2, "ProgressIndicator", rect(20, 655, 370, 659), id("anim_progress"), asVal("42%")),
		nd(3, "ProgressIndicator", rect(20, 645, 370, 669), asVal("42%")),
		nd(2, "Slider", rect(20, 700, 370, 730), asVal("10%")),
	)
	l := Build(s, NewRefs())
	checkLines(t, noRefs(l), []string{
		`Slider #slider_volume value="50%"`,
		`Progress #anim_progress value="42%"`,
		`Slider value="10%"`,
	})
	// The element without an id keeps its ref when its value moves.
	refs := NewRefs()
	a := Build(s, refs)
	s.Nodes[len(s.Nodes)-1].Text, s.Nodes[len(s.Nodes)-1].Value = "20%", "20%"
	b := Build(s, refs)
	if a.Elements[2].Ref != b.Elements[2].Ref {
		t.Errorf("slider ref %d -> %d after its value moved", a.Elements[2].Ref, b.Elements[2].Ref)
	}
	if d := Diff(a, b); d != "~ @3 Slider value=\"20%\"\n" {
		t.Errorf("diff = %q", d)
	}
	// The web: a range input (role slider) with its position as its value.
	w := web(nd(1, "input:range[slider]", rect(20, 100, 400, 130), desc("Volume"), id("volume"), click,
		func(n *ui.Node) { n.Value = "50" }))
	checkLines(t, noRefs(Build(w, NewRefs())), []string{`Slider "Volume" #volume value="50"`})
}

// An element only partly on screen, or covered by the keyboard or a dialog, says so.
func TestBuildMarksPartlyOffscreenAndCovered(t *testing.T) {
	s := ios(
		nd(2, "ScrollView", rect(0, 127, 390, 844), id("scenarios"), scrollable),
		nd(3, "Button", rect(0, 703, 390, 767), desc("Good accessibility"), id("scenario_a11y_good")),
		nd(3, "Button", rect(0, 831, 390, 895), desc("Animations"), id("scenario_animations")),
	)
	checkBody(t, Build(s, NewRefs()), `@1 View #scenarios scrollable
  @2 Button "Good accessibility" #scenario_a11y_good
  @3 Button "Animations" #scenario_animations partly off-screen
`)

	// Android: the keyboard over the Send button; a dialog window over the screen.
	kb := &ui.Window{ID: 2, Type: "keyboard", Package: "com.google.android.inputmethod.latin", Bounds: rect(0, 1500, phoneW, phoneH),
		Nodes: []*ui.Node{nd(0, "android.widget.FrameLayout", rect(0, 1500, phoneW, phoneH))}}
	app := appWin(
		nd(0, "android.widget.FrameLayout", rect(0, 0, phoneW, phoneH)),
		nd(1, "android.widget.EditText", rect(40, 200, 1040, 300), text("hi"), editable, focused, id("com.example.app:id/msg")),
		nd(1, "android.widget.Button", rect(40, 1800, 1040, 1900), text("Send"), click, id("com.example.app:id/send")),
	)
	l := Build(screenOf("android", phoneW, phoneH, kb, app), NewRefs())
	send := byLabel(t, l, "Send")
	if send.Covered != "keyboard" || !strings.HasSuffix(send.Line(), ` covered by keyboard`) {
		t.Errorf("send: %s (covered %q)", send.Line(), send.Covered)
	}
	if msg := byLabel(t, l, "hi"); msg.Covered != "" {
		t.Errorf("field: %s", msg.Line())
	}

	dialog := &ui.Window{ID: 3, Type: "app", Package: pkg, Active: true, Focused: true, Bounds: rect(100, 900, 980, 1500),
		Nodes: []*ui.Node{
			nd(0, "android.widget.FrameLayout", rect(100, 900, 980, 1500)),
			nd(1, "android.widget.Button", rect(600, 1350, 940, 1450), text("OK"), click),
		}}
	under := appWin(
		nd(0, "android.widget.FrameLayout", rect(0, 0, phoneW, phoneH)),
		nd(1, "android.widget.Button", rect(200, 1000, 880, 1200), text("Under"), click),
		nd(1, "android.widget.Button", rect(200, 2000, 880, 2100), text("Below"), click),
	)
	under.Focused = false
	l = Build(screenOf("android", phoneW, phoneH, dialog, under), NewRefs())
	if e := byLabel(t, l, "Under"); e.Covered != "modal" || !strings.HasSuffix(e.Line(), " covered by modal") {
		t.Errorf("under: %s", e.Line())
	}
	if e := byLabel(t, l, "Below"); e.Covered != "" {
		t.Errorf("below: %s", e.Line())
	}
	if e := byLabel(t, l, "OK"); e.Covered != "" {
		t.Errorf("ok: %s", e.Line())
	}
	b, _ := json.Marshal(byLabel(t, l, "Under"))
	if !strings.Contains(string(b), `"covered":"modal"`) {
		t.Errorf("JSON %s", b)
	}
}

// iOS's long list scrolled down: rows under the pinned section header are covered by it, the
// header itself is not covered by the rows (listed after it), and neither is a row partly out
// of the list by the list's content view (as large as the list).
func TestBuildMarksRowsUnderPinnedHeader(t *testing.T) {
	cell := func(b ui.Rect, cls, label, ident string) []*ui.Node {
		return []*ui.Node{nd(3, "Cell", b), nd(4, cls, b, desc(label), id(ident))}
	}
	s := ios(nodes(
		[]*ui.Node{nd(2, "CollectionView", rect(0, 161, 390, 844), scrollable), nd(3, "Other", rect(0, 161, 390, 844))},
		cell(rect(0, 116, 390, 172), "Button", "Row 114", "row_114"),
		cell(rect(0, 161, 390, 201), "StaticText", "Section E", "section_E"),
		cell(rect(0, 172, 390, 228), "Button", "Row 115", "row_115"),
		cell(rect(0, 228, 390, 284), "Button", "Row 116", "row_116"),
		cell(rect(0, 820, 390, 876), "Button", "Row 127", "row_127"),
	)...)
	checkBody(t, Build(s, NewRefs()), `column scrollable:
  @1 Button "Row 114" #row_114 partly off-screen covered by @2
  @2 Text "Section E" #section_E
  @3 Button "Row 115" #row_115 covered by @2
  @4 Button "Row 116" #row_116
  @5 Button "Row 127" #row_127 partly off-screen
`)
}

// A diff names an element with no id that changed its text as changed (~), not as one gone
// and one new: it is found by its place under the nearest container with an id.
func TestDiffNoIDValueChangeIsAChange(t *testing.T) {
	screen := func(status string, checkedRadio int) *ui.Screen {
		radio := func(i int, label string) *ui.Node {
			n := nd(2, "android.widget.RadioButton", rect(40, 300+i*100, 1040, 380+i*100), text(label), click, checkable)
			n.Checked = i == checkedRadio
			return n
		}
		return android(
			nd(1, "android.widget.LinearLayout", rect(0, 200, 1080, 800), id("com.example.app:id/colors")),
			nd(2, "android.widget.TextView", rect(40, 200, 1040, 280), text(status)),
			radio(0, "Red"), radio(1, "Green"), radio(2, "Blue"),
		)
	}
	refs := NewRefs()
	before := Build(screen("Color: none", -1), refs)
	after := Build(screen("Color: Green", 1), refs)
	got := Diff(before, after)
	want := "~ @5 Text \"Color: Green\"\n~ @3 Radio \"Green\" checked\n"
	if got != want {
		t.Errorf("diff =\n%s\nwant\n%s", got, want)
	}
}

// A scroll that moves what a scroll area holds out of view reports the new counts compactly.
func TestDiffReportsMoreCounts(t *testing.T) {
	screen := func(top int) *ui.Screen {
		var ns []*ui.Node
		ns = append(ns, nd(2, "ScrollView", rect(0, 148, 390, 844), id("page"), scrollable))
		for i := 0; i < 12; i++ {
			y := top + i*100
			ns = append(ns, nd(3, "Button", rect(20, y, 370, y+80), desc("Item "+string(rune('A'+i))), id("item_"+string(rune('a'+i)))))
		}
		return ios(ns...)
	}
	refs := NewRefs()
	a := Build(screen(160), refs)
	b := Build(screen(60), refs)
	d := Diff(a, b)
	if !strings.Contains(d, "~ @1 View #page scrollable (1 more above, 4 more below)\n") {
		t.Errorf("diff =\n%s\nwant a line with the new count", d)
	}
}

// The JSON listing drops what the text drops and carries the new fields.
func TestJSONMatchesText(t *testing.T) {
	s := ios(nodes(
		[]*ui.Node{
			nd(2, "ScrollView", rect(0, 148, 390, 844), id("page"), scrollable),
			nd(3, "Slider", rect(20, 591, 370, 622), id("slider_volume"), asVal("50%")),
			nd(3, "Button", rect(0, 831, 390, 895), desc("Animations"), id("scenario_animations")),
		},
		iosBar(3, rect(369, 148, 389, 812), "Vertical scroll bar, 4 pages", "0%"),
	)...)
	s.Nodes[0].Desc = "Buddy Bench"
	l := Build(s, NewRefs())
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"value":"50%"`, `"offscreen":true`, `"scroll":"vertical"`, `"more":{"below":-1}`, `"tappable":true`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON lacks %s:\n%s", want, got)
		}
	}
	for _, not := range []string{"scroll bar", "Buddy Bench"} {
		if strings.Contains(got, not) {
			t.Errorf("JSON has %q:\n%s", not, got)
		}
	}
}
