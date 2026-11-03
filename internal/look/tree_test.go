package look

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// body is the listing without its header line.
func body(l *Listing) string {
	_, rest, _ := strings.Cut(l.Text(), "\n")
	return rest
}

func checkBody(t *testing.T, l *Listing, want string) {
	t.Helper()
	if got := body(l); got != want {
		t.Errorf("listing =\n%s\nwant\n%s", got, want)
	}
}

// A profile row (avatar, name over email, edit button) above a row of two buttons:
// rows and the column inside a row get header lines, wrappers holding one thing do not.
func TestOutlineRowsAndColumns(t *testing.T) {
	s := android(
		nd(1, "android.widget.LinearLayout", rect(0, 200, 1080, 600)),
		nd(2, "android.widget.TextView", rect(40, 200, 1040, 260), text("Account")),
		nd(2, "android.widget.LinearLayout", rect(0, 300, 1080, 420)),
		nd(3, "android.widget.ImageView", rect(40, 300, 160, 420), desc("Avatar")),
		nd(3, "android.widget.LinearLayout", rect(180, 300, 800, 420)),
		nd(4, "android.widget.FrameLayout", rect(180, 300, 800, 350)), // wrapper: collapsed
		nd(5, "android.widget.TextView", rect(180, 300, 800, 350), text("Jane Doe")),
		nd(4, "android.widget.TextView", rect(180, 360, 800, 410), text("jane@example.com")),
		nd(3, "android.widget.Button", rect(840, 320, 1040, 400), text("Edit"), click),
		nd(1, "android.widget.LinearLayout", rect(0, 2200, 1080, 2300)),
		nd(2, "android.widget.Button", rect(40, 2200, 520, 2300), text("Cancel"), click),
		nd(2, "android.widget.Button", rect(560, 2200, 1040, 2300), text("Save"), click),
	)
	checkBody(t, Build(s, NewRefs()), `@1 Text "Account"
row:
  @2 Image "Avatar"
  column:
    @3 Text "Jane Doe"
    @4 Text "jane@example.com"
  @5 Button "Edit"
row:
  @6 Button "Cancel"
  @7 Button "Save"
`)
}

// A scrolling list of tappable cards: the list is marked scrollable and each card's texts
// sit under the card.
func TestOutlineScrollListOfCards(t *testing.T) {
	s := android(
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable),
		nd(2, "android.view.ViewGroup", rect(0, 200, 1080, 400), click, id("com.example.app:id/card")),
		nd(3, "android.widget.TextView", rect(40, 220, 700, 280), text("Order #1")),
		nd(3, "android.widget.TextView", rect(800, 220, 1040, 280), text("$12")),
		nd(2, "android.view.ViewGroup", rect(0, 420, 1080, 620), click, id("com.example.app:id/card")),
		nd(3, "android.widget.TextView", rect(40, 440, 700, 500), text("Order #2")),
		nd(3, "android.widget.TextView", rect(800, 440, 1040, 500), text("$30")),
	)
	checkBody(t, Build(s, NewRefs()), `column scrollable:
  @1 Button "Order #1, $12"
  @2 Button "Order #2, $30"
`)
}

// Texts inside a button that are not part of its label are indented under it, laid out
// as they sit.
func TestOutlineInsideButton(t *testing.T) {
	s := android(
		nd(1, "android.view.ViewGroup", rect(0, 200, 1080, 400), desc("Wi-Fi"), click),
		nd(2, "android.widget.TextView", rect(40, 220, 500, 280), text("Home network")),
		nd(2, "android.widget.Switch", rect(900, 220, 1040, 280), checked, click),
	)
	checkBody(t, Build(s, NewRefs()), `@1 Button "Wi-Fi"
  row:
    @2 Text "Home network"
    @3 Switch checked
`)
}

// Siblings laid out flat in one container (a ConstraintLayout, a React Native view) are
// split into rows by position; equal rows make a grid.
func TestOutlineRowsByPosition(t *testing.T) {
	s := android(
		nd(1, "androidx.constraintlayout.widget.ConstraintLayout", rect(0, 0, 1080, 2400)),
		nd(2, "android.widget.TextView", rect(40, 200, 1040, 260), text("Checkout")),
		nd(2, "android.widget.TextView", rect(40, 300, 500, 360), text("Total")),
		nd(2, "android.widget.TextView", rect(800, 305, 1040, 355), text("$42")),
		nd(2, "android.widget.Button", rect(40, 400, 1040, 500), text("Pay"), click),
	)
	checkBody(t, Build(s, NewRefs()), `@1 Text "Checkout"
row:
  @2 Text "Total"
  @3 Text "$42"
@4 Button "Pay"
`)
	g := android(
		nd(1, "android.widget.GridLayout", rect(0, 200, 1080, 600)),
		nd(2, "android.widget.Button", rect(0, 200, 360, 400), text("1"), click),
		nd(2, "android.widget.Button", rect(360, 200, 720, 400), text("2"), click),
		nd(2, "android.widget.Button", rect(720, 200, 1080, 400), text("3"), click),
		nd(2, "android.widget.Button", rect(0, 400, 360, 600), text("4"), click),
		nd(2, "android.widget.Button", rect(360, 400, 720, 600), text("5"), click),
		nd(2, "android.widget.Button", rect(720, 400, 1080, 600), text("6"), click),
	)
	checkBody(t, Build(g, NewRefs()), `grid 3 cols:
  @1 Button "1"
  @2 Button "2"
  @3 Button "3"
  @4 Button "4"
  @5 Button "5"
  @6 Button "6"
`)
}

// On iOS a container with an identifier is listed itself, with its contents under it;
// items that cover each other (a button over a row) do not make a row.
func TestOutlineCarouselIDAndOverlap(t *testing.T) {
	s := ios(
		nd(2, "ScrollView", rect(0, 100, 390, 300), id("featured")),
		nd(3, "Button", rect(10, 100, 190, 300), desc("Paris")),
		nd(3, "Button", rect(200, 100, 380, 300), desc("Rome")),
		nd(2, "Other", rect(0, 320, 390, 800), id("results")),
		nd(3, "StaticText", rect(20, 330, 370, 360), text("3 results")),
		nd(3, "Button", rect(20, 370, 370, 420), desc("Lisbon")),
		nd(3, "Button", rect(300, 380, 370, 410), desc("Add")),
	)
	s.Nodes[2].Scrollable = true
	checkBody(t, Build(s, NewRefs()), `@1 View #featured scrollable
  row:
    @2 Button "Paris"
    @3 Button "Rome"
@4 View #results
  @5 Text "3 results"
  @6 Button "Lisbon"
  @7 Button "Add"
`)
}

// A plain vertical screen stays a flat list: no header lines at all.
func TestOutlinePlainStackIsFlat(t *testing.T) {
	s := android(
		nd(1, "android.widget.ScrollView", rect(0, 0, 1080, 2400)),
		nd(2, "android.widget.LinearLayout", rect(0, 0, 1080, 2400)),
		nd(3, "android.widget.TextView", row(0), text("One")),
		nd(3, "android.widget.LinearLayout", rect(0, 350, 1080, 600)),
		nd(4, "android.widget.TextView", row(1), text("Two")),
		nd(4, "android.widget.TextView", row(2), text("Three")),
	)
	checkBody(t, Build(s, NewRefs()), `@1 Text "One"
@2 Text "Two"
@3 Text "Three"
`)
}

// An icon button laid over the end of a field (show password) is listed after the field,
// not beside it in a row.
func TestOutlineIconInsideField(t *testing.T) {
	s := ios(
		nd(2, "Other", rect(0, 260, 390, 400)),
		nd(3, "StaticText", rect(20, 276, 76, 290), text("Password")),
		nd(3, "SecureTextField", rect(36, 312, 354, 332), desc("Password"), id("pw")),
		nd(3, "Button", rect(322, 291, 366, 335), desc("Show password"), id("eye")),
	)
	checkBody(t, Build(s, NewRefs()), `@1 Text "Password"
@2 SecureField "Password" #pw
@3 Button "Show password" #eye
`)
}

// iOS and web report elements outside the visible area: a scroll area counts them by side,
// once each (not the text inside an off-screen card again).
func TestOutlineMoreFromOffscreenElements(t *testing.T) {
	s := ios(
		nd(2, "ScrollView", rect(0, 100, 390, 844), scrollable),
		nd(3, "Button", rect(0, -200, 390, -100), desc("Earlier")),
		nd(3, "Button", rect(0, 100, 390, 200), desc("One")),
		nd(3, "Button", rect(0, 200, 390, 300), desc("Two")),
		nd(3, "Cell", rect(0, 900, 390, 1000), desc("Three")),
		nd(4, "StaticText", rect(20, 910, 200, 990), text("Three")),
		nd(3, "Button", rect(0, 1000, 390, 1100), desc("Four")),
	)
	l := Build(s, NewRefs())
	checkBody(t, l, `column scrollable (1 more above, 2 more below):
  @1 Button "One"
  @2 Button "Two"
`)
	b, _ := json.Marshal(l.Blocks)
	if want := `[{"layout":"column","scrollable":true,"more":{"above":1,"below":2},"children":[{"ref":1},{"ref":2}]}]`; string(b) != want {
		t.Errorf("blocks JSON = %s\nwant %s", b, want)
	}
}

// Off-screen elements outside any scroll area (a long web page) are one line at the end.
func TestOutlineMoreOnPage(t *testing.T) {
	s := web(
		nd(1, "h1", rect(100, 50, 600, 100), text("Docs")),
		nd(1, "p", rect(100, 900, 600, 950), text("Below the fold")),
		nd(1, "a", rect(100, 1000, 600, 1050), text("Next page"), click),
	)
	checkBody(t, Build(s, NewRefs()), `@1 Heading "Docs"
(2 more below)
`)
}

// Android sends no off-screen nodes: a list's size and its items' rows give the count, and
// "can scroll further" alone gives "more" without one.
func TestOutlineMoreAndroidList(t *testing.T) {
	list := nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable)
	list.Coll = &ui.Coll{Rows: 50, Cols: 1}
	list.MoreAfter, list.MoreBefore = true, true
	a := nd(2, "android.widget.TextView", row(0), text("Item 10"), click)
	a.Item = &ui.Coll{Rows: 10, Cols: 0}
	b := nd(2, "android.widget.TextView", row(1), text("Item 11"), click)
	b.Item = &ui.Coll{Rows: 11, Cols: 0}
	checkBody(t, Build(android(list, a, b), NewRefs()), `column scrollable (10 more above, 38 more below):
  @1 Button "Item 10"
  @2 Button "Item 11"
`)

	carousel := nd(1, "android.widget.HorizontalScrollView", rect(0, 200, 1080, 400), scrollable)
	carousel.MoreAfter = true
	s := android(carousel,
		nd(2, "android.widget.LinearLayout", rect(0, 200, 1080, 400)),
		nd(3, "android.widget.Button", rect(0, 200, 500, 400), text("Paris"), click),
		nd(3, "android.widget.Button", rect(540, 200, 1040, 400), text("Rome"), click))
	checkBody(t, Build(s, NewRefs()), `row scrollable (more to the right):
  @1 Button "Paris"
  @2 Button "Rome"
`)
}

// Elements that were not on the screen shown before get a "*"; a first look, another app
// or an all-new screen marks nothing.
func TestMarkNew(t *testing.T) {
	refs := NewRefs()
	first := Build(android(
		nd(1, "android.widget.TextView", row(0), text("Cart")),
		nd(1, "android.widget.Button", row(1), text("Add"), click),
	), refs)
	first.MarkNew(nil)
	checkBody(t, first, "@1 Text \"Cart\"\n@2 Button \"Add\"\n")

	second := Build(android(
		nd(1, "android.widget.TextView", row(0), text("Cart")),
		nd(1, "android.widget.TextView", row(1), text("Apples")),
		nd(1, "android.widget.Button", row(2), text("Add"), click),
	), refs)
	second.MarkNew(first)
	checkBody(t, second, "@1 Text \"Cart\"\n*@3 Text \"Apples\"\n@2 Button \"Add\"\n")
	if b, _ := json.Marshal(second.ByRef(3)); !strings.Contains(string(b), `"new":true`) {
		t.Errorf("element JSON lacks new: %s", b)
	}
	if d := Diff(first, second); d != "+ @3 Text \"Apples\"\n" {
		t.Errorf("Diff = %q", d)
	}

	other := Build(android(nd(1, "android.widget.TextView", row(0), text("Login"))), refs)
	other.MarkNew(second)
	if strings.Contains(other.Text(), "*") {
		t.Errorf("all-new screen marked:\n%s", other.Text())
	}
}

// A list that reports its size (here on a child of the scroll view, as Android Settings
// does) but not its items' places: at the top of the list, the rows on screen give the count.
func TestOutlineMoreAndroidListAtTop(t *testing.T) {
	sv := nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable)
	sv.MoreAfter = true
	list := nd(2, "android.widget.LinearLayout", rect(0, 200, 1080, 2200))
	list.Coll = &ui.Coll{Rows: 19, Cols: 1}
	s := android(sv, list,
		nd(3, "android.widget.LinearLayout", row(0), text("Network"), click),
		nd(3, "android.widget.LinearLayout", row(1), text("Apps"), click),
		nd(3, "android.widget.LinearLayout", row(2), text("Battery"), click))
	checkBody(t, Build(s, NewRefs()), `column scrollable (16 more below):
  @1 Button "Network"
  @2 Button "Apps"
  @3 Button "Battery"
`)
}

// When only some rows report their place, the rows on screen still count as shown.
func TestOutlineMoreAndroidSparsePlaces(t *testing.T) {
	sv := nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), scrollable)
	sv.Coll = &ui.Coll{Rows: 19, Cols: 1}
	sv.MoreAfter = true
	a := nd(2, "android.widget.LinearLayout", row(0), text("Network"), click)
	a.Item = &ui.Coll{Rows: 0}
	b := nd(2, "android.widget.LinearLayout", row(1), text("Apps"), click)
	c := nd(2, "android.widget.LinearLayout", row(2), text("Battery"), click)
	c.Item = &ui.Coll{Rows: 2}
	d := nd(2, "android.widget.LinearLayout", row(3), text("Sound"), click)
	checkBody(t, Build(android(sv, a, b, c, d), NewRefs()), `column scrollable (15 more below):
  @1 Button "Network"
  @2 Button "Apps"
  @3 Button "Battery"
  @4 Button "Sound"
`)
}

// A row scrolled out of its list but still inside the screen (behind a header) is out of
// view: counted, not listed, the same as the matcher sees it.
func TestOutlineRowScrolledOutOfListInsideScreen(t *testing.T) {
	s := ios(
		nd(2, "StaticText", rect(0, 50, 390, 100), text("Header")),
		nd(2, "ScrollView", rect(0, 100, 390, 500), scrollable),
		nd(3, "Button", rect(0, 40, 390, 90), desc("Scrolled away")),
		nd(3, "Button", rect(0, 100, 390, 200), desc("One")),
		nd(3, "Button", rect(0, 200, 390, 300), desc("Two")),
		nd(3, "Button", rect(0, 520, 390, 620), desc("Below the list")),
	)
	checkBody(t, Build(s, NewRefs()), `@1 Text "Header"
column scrollable (1 more above, 1 more below):
  @2 Button "One"
  @3 Button "Two"
`)
}
