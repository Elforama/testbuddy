package look

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// A control with an id of its own, centred on a control of its kind, is listed: an Android
// ImageButton "Play" #play on a clickable video card (Android ids are not "useful" ids, but
// they are its own). So is a second control with a name of its own and no id.
func TestBuildKeepsInnerControlWithItsOwnIDOrName(t *testing.T) {
	s := android(
		nd(1, "android.view.ViewGroup", rect(0, 200, 1080, 800), desc("Cats video"), click),
		nd(2, "android.widget.ImageButton", rect(440, 400, 640, 600), desc("Play"), click, id("com.example.app:id/play")),
		nd(1, "android.view.ViewGroup", rect(0, 900, 1080, 1500), desc("Dogs video"), click),
		nd(2, "android.widget.ImageButton", rect(440, 1100, 640, 1300), desc("Pause"), click),
	)
	checkLines(t, noRefs(Build(s, NewRefs())), []string{
		`Button "Cats video"`,
		`Button "Play" #play`,
		`Button "Dogs video"`,
		`Button "Pause"`,
	})
}

// On iOS only SF Symbol glyphs (an id with a dot, or the label again) in a labelled button
// are dropped: an image with no id (a photo), a repeated #avatar, and a picker's checkmark on
// a row iOS does not mark selected are kept.
func TestBuildKeepsContentImagesInRows(t *testing.T) {
	s := ios(
		nd(2, "Button", rect(0, 100, 390, 160), desc("Ann"), id("row_ann")),
		nd(3, "Image", rect(10, 105, 60, 155), desc("Ann's photo"), id("avatar")),
		nd(2, "Button", rect(0, 160, 390, 220), desc("Bob"), id("row_bob")),
		nd(3, "Image", rect(10, 165, 60, 215), desc("Bob's photo"), id("avatar")),
		nd(2, "Button", rect(0, 220, 390, 280), desc("Trip"), id("row_trip")),
		nd(3, "Image", rect(10, 225, 60, 275), desc("Sunset")),
		nd(2, "Button", rect(0, 280, 390, 340), desc("Large"), id("size_large")),
		nd(3, "Image", rect(350, 300, 370, 320), desc("Checkmark"), id("checkmark")),
		nd(2, "Button", rect(0, 340, 390, 400), desc("Small"), id("size_small"), selected),
		nd(3, "Image", rect(350, 360, 370, 380), desc("Checkmark"), id("checkmark")),
		nd(2, "Button", rect(0, 400, 390, 460), desc("More"), id("more")),
		nd(3, "Image", rect(350, 420, 370, 440), desc("Forward"), id("chevron.forward")),
	)
	checkLines(t, noRefs(Build(s, NewRefs())), []string{
		`Button "Ann" #row_ann`,
		`Image "Ann's photo"`,
		`Button "Bob" #row_bob`,
		`Image "Bob's photo"`,
		`Button "Trip" #row_trip`,
		`Image "Sunset"`,
		`Button "Large" #size_large`,
		`Image "Checkmark"`,
		`Button "Small" #size_small selected`,
		`Button "More" #more`,
	})
}

// A carousel's cards in a cell below the fold count once, as the cell: the count looked past
// the carousel's own scroll view for off-screen ancestors no more.
func TestOutlineCarouselInOffscreenCellCountsOnce(t *testing.T) {
	carousel := nd(4, "ScrollView", rect(0, 1000, 390, 1200), scrollable)
	s := ios(
		nd(2, "ScrollView", rect(0, 100, 390, 844), id("page"), scrollable),
		nd(3, "Button", rect(0, 100, 390, 200), desc("Top"), id("top")),
		nd(3, "Cell", rect(0, 1000, 390, 1200), desc("Shelf"), id("shelf")),
		carousel,
		nd(5, "Button", rect(0, 1000, 180, 1200), desc("Card 1")),
		nd(5, "Button", rect(200, 1000, 380, 1200), desc("Card 2")),
		nd(5, "Button", rect(400, 1000, 580, 1200), desc("Card 3")),
	)
	checkBody(t, Build(s, NewRefs()), `@1 View #page scrollable (1 more below)
  @2 Button "Top" #top
`)
}

// A cover is named by the listed element at the point it covers: a toolbar holding two
// buttons over a row's right end names the one over it, not the first.
func TestBuildNamesCoverAtThePoint(t *testing.T) {
	s := android(
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 0, 1080, 2400), scrollable),
		nd(2, "android.widget.Button", rect(0, 2200, 1080, 2300), text("Row"), click),
		nd(1, "android.widget.LinearLayout", rect(0, 2150, 1080, 2400), click),
		nd(2, "android.widget.Button", rect(0, 2300, 300, 2400), text("Home"), click),
		nd(2, "android.widget.Button", rect(300, 2150, 1080, 2400), text("Cart"), click),
	)
	l := Build(s, NewRefs())
	row, cart := byLabel(t, l, "Row"), byLabel(t, l, "Cart")
	if want := fmt.Sprintf("@%d", cart.Ref); row.Covered != want {
		t.Errorf("row: %s, want covered by %s", row.Line(), want)
	}
}

// Android 15 edge-to-edge: the list runs under a BottomNavigationView whose container has no
// label and takes no clicks itself; the row behind it is covered by it (named by the tab at
// the covered point).
func TestBuildRowUnderUnlabelledBottomBar(t *testing.T) {
	s := android(
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 0, 1080, 2400), scrollable),
		nd(2, "android.widget.Button", rect(0, 2000, 1080, 2150), text("Row 9"), click),
		nd(2, "android.widget.Button", rect(0, 2200, 1080, 2350), text("Row 10"), click),
		nd(1, "com.google.android.material.bottomnavigation.BottomNavigationView", rect(0, 2160, 1080, 2400)),
		nd(2, "android.widget.FrameLayout", rect(0, 2160, 540, 2400), desc("Home"), click),
		nd(2, "android.widget.FrameLayout", rect(540, 2160, 1080, 2400), desc("Search"), click),
	)
	l := Build(s, NewRefs())
	if e := byLabel(t, l, "Row 10"); !strings.Contains(e.Line(), " covered by @") {
		t.Errorf("row under the bar: %s, want covered by a tab", e.Line())
	}
	if e := byLabel(t, l, "Row 9"); e.Covered != "" {
		t.Errorf("row above the bar: %s", e.Line())
	}
}

// Unnamed scroll areas are matched by place: a list that appears above another does not
// take the other's counts.
func TestDiffMatchesUnnamedScrollAreasByPlace(t *testing.T) {
	list := func(top, n int) []*ui.Node {
		ns := []*ui.Node{nd(2, "ScrollView", rect(0, top, 390, top+300), scrollable)}
		for i := 0; i < n; i++ {
			y := top + i*100
			ns = append(ns, nd(3, "Button", rect(0, y, 390, y+80), desc("L"+string(rune('A'+top/100))+string(rune('a'+i)))))
		}
		return ns
	}
	refs := NewRefs()
	a := Build(ios(list(500, 5)...), refs)
	b := Build(ios(append(list(100, 3), list(500, 5)...)...), refs)
	if d := Diff(a, b); strings.Contains(d, "scrollable (") {
		t.Errorf("diff =\n%s\nwant no count change: the lower list did not move", d)
	}
}
