package engine

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// clickable is a clickable node with bounds and depth.
func clickable(depth, l, t, r, b int) *ui.Node {
	n := at("View", depth, l, t, r, b)
	n.Clickable = true
	return n
}

// coverAt: what takes a tap at a point instead of the element, case by case.
func TestCoverAt(t *testing.T) {
	type tc struct {
		name  string
		s     *ui.Screen
		n     *ui.Node
		x, y  int
		cover bool
	}
	var cases []tc
	{
		// Covers stick out of the element: one inside its frame is a part of it.
		el, fab := clickable(0, 0, 100, 400, 200), clickable(0, 300, 150, 380, 250)
		cases = append(cases, tc{"later clickable over it", linked("android", el, fab), el, 340, 175, true})
		cases = append(cases, tc{"later clickable, elsewhere", linked("android", el, fab), el, 50, 175, false})
	}
	{
		under, el := clickable(0, 300, 150, 380, 250), clickable(0, 0, 100, 400, 200)
		cases = append(cases, tc{"earlier: drawn under it", linked("android", under, el), el, 340, 175, false})
	}
	{
		el, label := clickable(0, 0, 100, 400, 200), at("Text", 0, 300, 150, 380, 250)
		cases = append(cases, tc{"a label takes no touches", linked("android", el, label), el, 340, 175, false})
	}
	{
		el := clickable(0, 0, 100, 400, 200)
		box := at("Frame", 0, 250, 140, 390, 260)
		btnIn := clickable(1, 300, 150, 380, 250)
		cases = append(cases, tc{"a layout with a button at the point", linked("android", el, box, btnIn), el, 340, 175, true})
		cases = append(cases, tc{"a layout with its button elsewhere", linked("android", el, box, btnIn), el, 260, 175, false})
	}
	for _, flag := range []string{"long", "edit", "check", "scroll"} {
		el, c := clickable(0, 0, 100, 400, 200), at("View", 0, 300, 150, 380, 250)
		switch flag {
		case "long":
			c.LongClickable = true
		case "edit":
			c.Editable = true
		case "check":
			c.Checkable = true
		case "scroll":
			c.Scrollable = true
		}
		cases = append(cases, tc{"android " + flag, linked("android", el, c), el, 340, 175, true})
	}
	{
		parent := clickable(0, 0, 100, 400, 200)
		el := clickable(1, 0, 100, 400, 200)
		child := clickable(2, 300, 120, 380, 180)
		cases = append(cases, tc{"its own child", linked("android", parent, el, child), el, 340, 150, false})
		cases = append(cases, tc{"its parent", linked("android", parent, el, child), child, 340, 150, false})
	}
	{
		el := clickable(0, 0, 100, 400, 200)
		part := clickable(0, 10, 110, 390, 190) // inside its frame: listed apart (React Native)
		cases = append(cases, tc{"a part of it", linked("android", el, part), el, 200, 150, false})
	}
	{
		el := clickable(0, 0, 100, 400, 200)
		overlay := clickable(0, 0, 0, 400, 500) // half the screen or more
		cases = append(cases, tc{"a screen-sized overlay", linked("android", el, overlay), el, 200, 150, false})
	}
	{
		el := clickable(0, 0, 100, 400, 200)
		drawer := at("Drawer", 0, 380, 0, 400, 800) // a closed drawer's list, its rows still placed
		drawer.Scrollable = true
		item := clickable(1, 300, 150, 400, 250)
		cases = append(cases, tc{"another list's row", linked("android", el, drawer, item), el, 340, 175, false})
	}
	{
		nav := at("NavigationBar", 0, 0, 0, 400, 100)
		list := at("Table", 0, 0, 0, 400, 800)
		list.Scrollable = true
		cell := at("Cell", 1, 0, 50, 400, 150)
		cases = append(cases, tc{"iOS bar before it", linked("ios", nav, list, cell), cell, 200, 75, true})
		cases = append(cases, tc{"iOS bar, not at the point", linked("ios", nav, list, cell), cell, 200, 125, false})
		cases = append(cases, tc{"android: an earlier bar is drawn under", linked("android", nav, list, cell), cell, 200, 75, false})
	}
	{
		el := at("Cell", 0, 0, 100, 400, 200)
		b := at("Button", 0, 300, 150, 380, 250)
		txt := at("StaticText", 0, 300, 150, 380, 250)
		cases = append(cases, tc{"iOS button over it", linked("ios", el, b), el, 340, 175, true})
		cases = append(cases, tc{"iOS text over it", linked("ios", el, txt), el, 340, 175, false})
	}
	{
		el, fab := clickable(0, 0, 100, 400, 200), clickable(0, 300, 150, 380, 250)
		cases = append(cases, tc{"web: never", linked("web", el, fab), el, 340, 175, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := coverAt(c.s, c.n, c.x, c.y)
			if (got != nil) != c.cover {
				t.Errorf("cover %v, want %v", got, c.cover)
			}
		})
	}
}

// The keyboard window above the element's window covers it; a node in another window is
// out of range.
func TestCoverAtKeyboard(t *testing.T) {
	el := clickable(0, 0, 600, 400, 700)
	app := &ui.Window{Type: "app", Nodes: []*ui.Node{el}, Bounds: ui.Rect{R: 400, B: 800}}
	kb := &ui.Window{Type: "keyboard", Bounds: ui.Rect{T: 650, R: 400, B: 800}}
	s := &ui.Screen{Platform: "android", Width: 400, Height: 800, Windows: []*ui.Window{kb, app}}
	s.Link()
	if c := coverAt(s, el, 200, 675); c == nil || c.Class != "Keyboard" || c.Bounds != kb.Bounds {
		t.Errorf("cover %+v, want the keyboard", c)
	}
	if c := coverAt(s, el, 200, 625); c != nil {
		t.Errorf("above the keyboard: %+v", c)
	}
	stray := clickable(0, 0, 0, 10, 10)
	stray.Window = 7
	if c := coverAt(s, stray, 1, 1); c != nil {
		t.Errorf("node outside the windows: %+v", c)
	}
}

// A sticky section header (before the row in its list, spanning it) covers the row on Android.
func TestStickyHeaderWideList(t *testing.T) {
	list := at("Recycler", 0, 0, 0, 400, 300) // wider than tall
	list.Scrollable = true
	header := at("Header", 1, 0, 0, 400, 60)
	row := clickable(1, 0, 40, 400, 90)
	s := linked("android", list, header, row)
	if c := coverAt(s, row, 200, 50); c != header {
		t.Errorf("cover %+v, want the header", c)
	}
	if c := coverAt(s, row, 200, 80); c != nil {
		t.Errorf("below the header: %+v", c)
	}
	// A header narrower than the row is not one.
	narrow := at("Header", 1, 0, 0, 200, 60)
	s = linked("android", list, narrow, row)
	if c := coverAt(s, row, 100, 50); c != nil {
		t.Errorf("narrow header: %+v", c)
	}
	// Not in a list: none.
	if c := stickyHeader(clickable(0, 0, 0, 10, 10), 1, 1); c != nil {
		t.Errorf("no list: %+v", c)
	}
	// The row's own subtree climbs to the list's child.
	inner := clickable(2, 10, 45, 100, 85)
	rowBox := at("Row", 1, 0, 40, 400, 90)
	s = linked("android", list, header, rowBox, inner)
	if c := stickyHeader(inner, 50, 50); c != header {
		t.Errorf("nested row: %+v", c)
	}
}

// A sticky header in an ordinary, full-height list covers the row under it too.
func TestStickyHeaderTallList(t *testing.T) {
	list := at("Recycler", 0, 0, 0, 400, 800)
	list.Scrollable = true
	header := at("Header", 1, 0, 0, 400, 60)
	row := clickable(1, 0, 40, 400, 90)
	s := linked("android", list, header, row)
	if c := coverAt(s, row, 200, 50); c != header {
		t.Errorf("cover %+v, want the header", c)
	}
}

// visiblePart cuts the element to the screen and to the scroll views around it.
func TestVisiblePart(t *testing.T) {
	list := at("List", 0, 0, 100, 400, 600)
	list.Scrollable = true
	row := clickable(1, 0, 550, 400, 650) // half below the list's bottom
	off := clickable(1, 0, 750, 400, 900) // below the screen, outside the list
	s := linked("ios", list, row, off)
	if v := visiblePart(s, row); v != (ui.Rect{T: 550, R: 400, B: 600}) {
		t.Errorf("row %v", v)
	}
	if v := visiblePart(s, off); v != (ui.Rect{T: 750, R: 400, B: 800}) {
		t.Errorf("off-list row %v: cut to the screen, the list does not hold any of it", v)
	}
	gone := clickable(0, 0, 900, 400, 950)
	if v := visiblePart(linked("ios", gone), gone); !v.Empty() {
		t.Errorf("off screen: %v", v)
	}
}

// uncoveredPoint: the middle, or the middle of the biggest part around a cover, or nothing.
func TestUncoveredPoint(t *testing.T) {
	// Nothing over it: the middle.
	el := clickable(0, 0, 100, 400, 200)
	x, y, cover, ok := uncoveredPoint(linked("android", el), el)
	if x != 200 || y != 150 || cover != nil || !ok {
		t.Errorf("plain: %d,%d %v %v", x, y, cover, ok)
	}
	// A bar over its lower half: the middle of the upper half.
	el = clickable(0, 0, 100, 400, 200)
	bar := clickable(0, 0, 140, 400, 300)
	x, y, cover, ok = uncoveredPoint(linked("android", el, bar), el)
	if x != 200 || y != 120 || cover != bar || !ok {
		t.Errorf("bar below: %d,%d %v %v", x, y, cover, ok)
	}
	// A button over its right part: the left part.
	el = clickable(0, 0, 100, 400, 200)
	b := clickable(0, 150, 90, 400, 210)
	x, y, cover, ok = uncoveredPoint(linked("android", el, b), el)
	if x != 75 || y != 150 || cover != b || !ok {
		t.Errorf("button right: %d,%d %v %v", x, y, cover, ok)
	}
	// Covered all over: no part shows.
	el = clickable(0, 0, 100, 400, 200)
	all := clickable(0, 0, 50, 400, 250)
	x, y, cover, ok = uncoveredPoint(linked("android", el, all), el)
	if x != 200 || y != 150 || cover != all || ok {
		t.Errorf("all covered: %d,%d %v %v", x, y, cover, ok)
	}
	// What shows is a sliver: not good enough.
	el = clickable(0, 0, 100, 400, 200)
	almost := clickable(0, 0, 102, 400, 250)
	if _, _, _, ok = uncoveredPoint(linked("android", el, almost), el); ok {
		t.Error("a 2 px sliver counted as clear")
	}
	// The best part is itself covered by a second cover.
	el = clickable(0, 0, 100, 400, 200)
	low := clickable(0, 0, 140, 400, 300)
	high := clickable(0, 0, 50, 400, 141)
	if _, _, _, ok = uncoveredPoint(linked("android", el, low, high), el); ok {
		t.Error("the part above was covered too")
	}
	// Off screen entirely: its own bounds' middle.
	el = clickable(0, 0, 900, 400, 1000)
	if x, y, _, _ = uncoveredPoint(linked("android", el), el); x != 200 || y != 950 {
		t.Errorf("off screen: %d,%d", x, y)
	}
}

// clearCover drags the element's list just clear of a bar, or says it cannot.
func TestClearCover(t *testing.T) {
	ctx := context.Background()
	list := at("List", 0, 0, 0, 400, 800)
	list.Scrollable = true
	row := clickable(1, 0, 700, 400, 750)
	footer := clickable(0, 0, 720, 400, 800)
	header := clickable(0, 0, 0, 400, 80)
	s := linked("android", list, row, footer, header)

	f := &fakeDriver{screen: static()}
	r := New(f, quick)
	if ok, err := r.clearCover(ctx, s, row, footer); !ok || err != nil {
		t.Fatalf("footer: %v %v", ok, err)
	}
	// The list's clear part is 0-720 (middle 360); the row must rise 750-720+400/12 = 63 px.
	if f.swipeAt[0] != [4]int{200, 360, 200, 297} || !f.swipes[0].Drag || !f.swipes[0].NoSettle {
		t.Errorf("drag %v %+v", f.swipeAt[0], f.swipes[0])
	}
	if len(f.waitDur) != 1 {
		t.Errorf("settle waits %v", f.waitDur)
	}
	// A bar at the top moves the content down.
	top := clickable(1, 0, 40, 400, 90)
	s2 := linked("android", list, top, header)
	if ok, _ := r.clearCover(ctx, s2, top, header); !ok {
		t.Fatal("header: no drag")
	}
	// Clear part 80-800 (middle 440); shift 80-40+33 = 73.
	if f.swipeAt[1] != [4]int{200, 440, 200, 513} {
		t.Errorf("drag %v", f.swipeAt[1])
	}

	for _, tc := range []struct {
		name     string
		s        *ui.Screen
		n, cover *ui.Node
	}{
		{"a bar along the side", s, row, clickable(0, 350, 0, 400, 800)},
		{"not in a list", linked("android", clickable(0, 0, 700, 400, 750)), clickable(0, 0, 700, 400, 750), footer},
		{"the cover moves with the list", s, row, list},
	} {
		if ok, err := r.clearCover(ctx, tc.s, tc.n, tc.cover); ok || err != nil {
			t.Errorf("%s: %v %v", tc.name, ok, err)
		}
	}
	// A list that shows too little clear of the cover.
	short := at("List", 0, 0, 500, 400, 800)
	short.Scrollable = true
	r2 := clickable(1, 0, 700, 400, 750)
	tall := clickable(0, 0, 520, 400, 800)
	s3 := linked("android", short, r2, tall)
	if ok, _ := r.clearCover(ctx, s3, r2, tall); ok {
		t.Error("a 20 px clear part was dragged")
	}
	// A cover inside the list itself.
	inList := clickable(1, 0, 720, 400, 800)
	s4 := linked("android", list, row, inList)
	if ok, _ := r.clearCover(ctx, s4, row, inList); ok {
		t.Error("dragged a list whose cover moves along")
	}
	// A drag the device fails.
	f.fail("Swipe", errors.New("no touch"))
	if _, err := r.clearCover(ctx, s, row, footer); err == nil {
		t.Error("swipe error lost")
	}
}

// A row at a list's end that stays under the footer is tapped where it shows best; the drag
// that moved nothing is not repeated.
func TestTapCoveredAtListEnd(t *testing.T) {
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		list := &ui.Node{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		row := &ui.Node{Text: "Last", Depth: 1, Bounds: ui.Rect{T: 740, R: 400, B: 790}, Enabled: true, Clickable: true}
		footer := &ui.Node{Text: "Pay", Bounds: ui.Rect{T: 700, R: 400, B: 800}, Enabled: true, Clickable: true}
		return []*ui.Node{list, row, footer}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {} // the list is at its end
	mustPass(t, runWith(t, f, quick, "- tapOn: Last"))
	if len(f.swipeAt) != 1 {
		t.Errorf("%d drags, want 1", len(f.swipeAt))
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 765} {
		t.Errorf("taps %v, want the row's middle as the last resort", f.taps)
	}
}

// An element that vanishes after the clearing drag fails the tap.
func TestTapCoveredVanishes(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var gone bool
	f.screen = func() []*ui.Node {
		if gone {
			return nil
		}
		list := &ui.Node{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		row := &ui.Node{Text: "Row", Depth: 1, Bounds: ui.Rect{T: 700, R: 400, B: 750}, Enabled: true, Clickable: true}
		footer := &ui.Node{Text: "Pay", Bounds: ui.Rect{T: 700, R: 400, B: 800}, Enabled: true, Clickable: true}
		return []*ui.Node{list, row, footer}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		f.mu.Lock() // the screen func runs under f.mu
		gone = true
		f.mu.Unlock()
		f.change(true)
	}
	mustFail(t, runWith(t, f, quick, "- tapOn: Row"), "Element not found")
}

// TB_DEBUG logs covers and dialogs to stderr.
func TestDebugLogging(t *testing.T) {
	defer func(d bool, e *os.File) { debug, os.Stderr = d, e }(debug, os.Stderr)
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skip(err)
	}
	defer null.Close()
	debug, os.Stderr = true, null
	TestTapAvoidsKeyboard(t)
	TestAnswersAndroidErrorDialog(t)
}

// loadingCover: a spinner, or a layer holding one, over the element takes its taps (Android).
func TestLoadingCover(t *testing.T) {
	const pb = "android.widget.ProgressBar"
	{
		// A see-through overlay laid over the whole screen, its spinner stretched across it.
		root := at("Frame", 0, 0, 0, 400, 800)
		key := clickable(1, 50, 300, 150, 400)
		veil := at("ViewGroup", 1, 0, 0, 400, 800)
		spin := at(pb, 2, 0, 0, 400, 800)
		s := linked("android", root, key, veil, spin)
		if c := loadingCover(s, key); c != veil {
			t.Errorf("overlay with a spinner: cover %v", c)
		}
		if loadingCover(linked("ios", root, key, veil, spin), key) != nil {
			t.Error("iOS: not judged from the tree")
		}
	}
	{
		// A small spinner in a layer that covers the element: that layer holds a spinner.
		key := clickable(0, 50, 300, 150, 400)
		layer := at("ViewGroup", 0, 0, 0, 400, 800)
		spin := at(pb, 1, 180, 380, 220, 420)
		if c := loadingCover(linked("android", key, layer, spin), key); c != layer {
			t.Errorf("layer holding a spinner: cover %v", c)
		}
	}
	{
		// The element's own spinner (a button showing progress), and a container of the
		// element that holds a spinner elsewhere (a list loading more): no cover.
		list := at("RecyclerView", 0, 0, 0, 400, 800)
		btn := clickable(1, 50, 300, 150, 400)
		own := at(pb, 2, 60, 310, 80, 330)
		more := at(pb, 1, 180, 700, 220, 740)
		if c := loadingCover(linked("android", list, btn, own, more), btn); c != nil {
			t.Errorf("own and container spinners: cover %v", c)
		}
	}
	{
		// A spinner elsewhere on screen, not over the element: no cover.
		key := clickable(0, 50, 300, 150, 400)
		spin := at(pb, 0, 180, 700, 220, 740)
		if c := loadingCover(linked("android", key, spin), key); c != nil {
			t.Errorf("spinner elsewhere: cover %v", c)
		}
	}
}

// sidewaysList: a sideways scrollUntilVisible drags the sideways list, not the middle of the
// screen, when the middle has none.
func TestSidewaysList(t *testing.T) {
	scroll := func(n *ui.Node) *ui.Node { n.Scrollable = true; return n }
	{
		// A vertical screen with a carousel above its middle.
		page := scroll(at("ScrollView", 0, 0, 0, 400, 800))
		cards := scroll(at("android.widget.HorizontalScrollView", 1, 0, 100, 400, 250))
		card := at("View", 2, 500, 110, 650, 240) // listed, off screen to the right
		s := linked("android", page, cards, card)
		if got := sidewaysList(s, nil, 200, 400, ""); got != cards {
			t.Errorf("carousel above the middle: %v", got)
		}
		if got := sidewaysList(s, card, 200, 400, ""); got != cards {
			t.Errorf("the element's own list: %v", got)
		}
	}
	{
		// A wide list across the middle: dragged where it is, as before.
		page := scroll(at("ScrollView", 0, 0, 300, 400, 500))
		if got := sidewaysList(linked("ios", page), nil, 200, 400, ""); got != nil {
			t.Errorf("sideways list at the middle: %v", got)
		}
	}
	{
		// No sideways list at all: the middle, as before.
		page := scroll(at("ScrollView", 0, 0, 0, 400, 800))
		if got := sidewaysList(linked("android", page), nil, 200, 400, ""); got != nil {
			t.Errorf("no sideways list: %v", got)
		}
	}
}

// A tap under a loading overlay (a see-through layer with a spinner) waits for it to go, then
// taps the element: a tap on the overlay would be lost.
func TestTapWaitsOutLoadingOverlay(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var veil atomic.Bool
	veil.Store(true)
	f.screen = func() []*ui.Node {
		root := at("Frame", 0, 0, 0, 400, 800)
		key := clickable(1, 50, 300, 150, 400)
		key.Text = "1"
		nodes := []*ui.Node{root, key}
		if veil.Load() {
			nodes = append(nodes, at("ViewGroup", 1, 0, 0, 400, 800), at("android.widget.ProgressBar", 2, 0, 0, 400, 800))
		}
		return nodes
	}
	var tappedUnder atomic.Bool
	f.onTap = func(x, y int) { tappedUnder.Store(veil.Load()) }
	go func() {
		time.Sleep(300 * time.Millisecond)
		veil.Store(false)
		f.change(true)
	}()
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: \"1\""))
	if tappedUnder.Load() || len(f.taps) != 1 {
		t.Errorf("taps %v, under the overlay: %v", f.taps, tappedUnder.Load())
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Errorf("took %s, want the overlay's 300 ms", d)
	}
}

// A keyboard window covers only what it draws: Gboard's floating toolbar (shown with a
// hardware keyboard) sits in a window as large as the screen. A keyboard that lists nothing
// smaller than its window covers all of it.
func TestCoverAtKeyboardParts(t *testing.T) {
	screen := func(kb *ui.Window, el *ui.Node) *ui.Screen {
		app := &ui.Window{Type: "app", Bounds: ui.Rect{R: 1080, B: 2400}, Nodes: []*ui.Node{el}}
		s := &ui.Screen{Platform: "android", Width: 1080, Height: 2400, Windows: []*ui.Window{kb, app}}
		s.Link()
		return s
	}
	next := clickable(0, 42, 902, 1038, 1033)
	toolbar := &ui.Window{Type: "keyboard", Bounds: ui.Rect{T: 63, R: 1080, B: 2400}, Nodes: []*ui.Node{
		at("FrameLayout", 0, 0, 63, 1080, 2400),
		at("FrameLayout", 1, 911, 863, 1080, 2400),
		clickable(2, 922, 873, 1048, 999), // voice typing
	}}
	if c := coverAt(screen(toolbar, next), next, 540, 967); c != nil {
		t.Errorf("toolbar: the button's middle is clear, got cover %v", c.Bounds)
	}
	if c := coverAt(screen(toolbar, next), next, 980, 930); c == nil || c.Bounds != (ui.Rect{L: 922, T: 873, R: 1048, B: 999}) {
		t.Errorf("toolbar: its voice button covers (980, 930), got %v", c)
	}
	full := &ui.Window{Type: "keyboard", Bounds: ui.Rect{T: 1500, R: 1080, B: 2400}, Nodes: []*ui.Node{at("KeyboardView", 0, 0, 1500, 1080, 2400)}}
	low := clickable(0, 0, 1600, 1080, 1700)
	if c := coverAt(screen(full, low), low, 540, 1650); c == nil {
		t.Error("a keyboard listing only its whole view covers its window")
	}
	empty := &ui.Window{Type: "keyboard", Bounds: ui.Rect{T: 1500, R: 1080, B: 2400}}
	if c := coverAt(screen(empty, low), low, 540, 1650); c == nil {
		t.Error("a keyboard listing nothing covers its window")
	}
}

// Lazy shelves list only the cards that show: a sideways scroll to "Card 6-12" drags the shelf
// whose cards look like it, not the largest or the middle one.
func TestSidewaysListBySimilarItems(t *testing.T) {
	scroll := func(n *ui.Node) *ui.Node { n.Scrollable = true; return n }
	page := scroll(at("ScrollView", 0, 0, 0, 400, 800))
	shelf5 := scroll(at("ScrollView", 1, 0, 300, 400, 420))
	c51 := at("Button", 2, 0, 310, 140, 410)
	c51.Text = "Card 5-1"
	shelf6 := scroll(at("ScrollView", 1, 0, 600, 400, 720))
	c61 := at("Button", 2, 0, 610, 140, 710)
	c61.Text = "Card 6-1"
	s := linked("ios", page, shelf5, c51, shelf6, c61)
	if got := sidewaysList(s, nil, 200, 400, "Card 6-12"); got != shelf6 {
		t.Errorf("for Card 6-12: %v", got)
	}
	if got := sidewaysList(s, nil, 200, 400, "Card 5-9"); got != shelf5 {
		t.Errorf("for Card 5-9: %v", got)
	}
	// Equally alike everywhere: as before, the shelf at the middle (shelf 5 holds y=400).
	if got := sidewaysList(s, nil, 200, 400, "Card 7-1"); got != nil {
		t.Errorf("for an unlike target: %v, want nil (the middle's shelf)", got)
	}
	// Up and down, a shelf is not the element's list: the page is.
	if got := ownList(s, c61, 200, 400); got != nil {
		t.Errorf("ownList for a card = %v, want the page (at the middle: nil)", got)
	}
}

// hitFake is a web-like driver whose hit test reports a cover while veiled is set.
type hitFake struct {
	*fakeDriver
	veiled atomic.Bool
	checks atomic.Int64
}

func (h *hitFake) CoveredAt(ctx context.Context, index, x, y int) (string, error) {
	h.checks.Add(1)
	if h.veiled.Load() {
		return "#veil", nil
	}
	return "", nil
}

// A web tap whose point a veil covers waits for the veil to go (the browser's hit test says
// so), then taps: the veil would swallow the tap.
func TestTapWaitsForHitClear(t *testing.T) {
	t.Parallel()
	h := &hitFake{fakeDriver: &fakeDriver{screen: static(btn("Under", "under", 300)), plat: "web"}}
	h.veiled.Store(true)
	var tappedVeiled atomic.Bool
	h.onTap = func(x, y int) { tappedVeiled.Store(h.veiled.Load()) }
	go func() {
		time.Sleep(300 * time.Millisecond)
		h.veiled.Store(false)
		h.change(true)
	}()
	start := time.Now()
	mustPass(t, runWith(t, h, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if tappedVeiled.Load() || len(h.taps) != 1 {
		t.Errorf("taps %v, under the veil %v", h.taps, tappedVeiled.Load())
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 2*time.Second {
		t.Errorf("took %s, want the veil's 300 ms", d)
	}
	// Not covered: one check, no wait.
	h2 := &hitFake{fakeDriver: &fakeDriver{screen: static(btn("Go", "", 300)), plat: "web"}}
	mustPass(t, runWith(t, h2, quick, "- tapOn: Go"))
	if h2.checks.Load() != 1 {
		t.Errorf("%d hit checks for a clear tap", h2.checks.Load())
	}
}

// A web tap target pushed out of view (a banner grew above it) while something else moved into
// its place is not tapped "anyway": that would tap the other element. The step fails on the
// target instead.
func TestTapDoesNotHitWhatTookTheTargetsPlace(t *testing.T) {
	t.Parallel()
	h := &hitFake{fakeDriver: &fakeDriver{plat: "web"}}
	var reads atomic.Int64
	h.screen = func() []*ui.Node {
		if reads.Add(1) > 1 { // moved right after it was found
			h.veiled.Store(true)                            // the hit test at Stop's old place reaches Target
			return []*ui.Node{btn("Target", "target", 300)} // Stop is below the view now
		}
		return []*ui.Node{btn("Stop", "stop", 300)}
	}
	res := runWith(t, h, Options{LookupTimeout: 600 * time.Millisecond, OptionalTimeout: 300 * time.Millisecond}, "- tapOn: {id: stop}")
	for _, tap := range h.taps {
		t.Errorf("tapped %v: Target took Stop's place", tap)
	}
	if res.Err == nil {
		t.Error("passed without tapping Stop")
	}
}

// iOS: a tap on a button that got no reaction at all was taken by something tb cannot see (a
// veil hidden from accessibility). When the screen changes (the veil goes), the button is
// tapped again, and that tap reaches it.
func TestIOSSwallowedTapIsMadeAgain(t *testing.T) {
	t.Parallel()
	under := btn("Under", "under", 300)
	under.Class = "Button"
	f := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
	var veiled atomic.Bool
	veiled.Store(true)
	var reached atomic.Int64
	f.onTap = func(x, y int) {
		if !veiled.Load() {
			reached.Add(1)
		}
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		veiled.Store(false)
		f.mu.Lock()
		f.tapSettle = driver.Changed
		f.mu.Unlock()
		f.change(true) // the veil went
	}()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if reached.Load() != 1 {
		t.Errorf("taps %v, %d reached the button: want exactly one", f.taps, reached.Load())
	}
	// A button that does not react while nothing else changes is tapped once.
	g := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
	mustPass(t, runWith(t, g, quick, "- tapOn: Under"))
	if len(g.taps) != 1 {
		t.Errorf("no change anywhere: %d taps, want 1", len(g.taps))
	}
}

// iOS, on a screen that never stops moving: the tap lands (the counter goes up) but its
// reaction is filtered out with the screen's endless motion, so it reports no change. What the
// screen lists changed, so it is not tapped again (that would tap twice).
func TestIOSNoReactionButScreenChangedIsNotRetried(t *testing.T) {
	t.Parallel()
	var count atomic.Int64
	tap := btn("Tap me", "tap_me", 300)
	tap.Class = "Button"
	f := &fakeDriver{plat: "ios", tapSettle: driver.NoChange}
	f.screen = func() []*ui.Node {
		return []*ui.Node{tap, btn(fmt.Sprintf("Tapped %d", count.Load()), "tap_count", 100)}
	}
	f.onTap = func(x, y int) { count.Add(1); f.change(false) }
	mustPass(t, runWith(t, f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}, "- tapOn: {id: tap_me}"))
	if count.Load() != 1 {
		t.Errorf("tapped %d times, want 1", count.Load())
	}
}

// iOS: a veil's spinner is listed with the whole veil's frame. A tap on a button under it
// waits for it to go; a small spinner beside a button does not hold the tap.
func TestIOSVeilSpinnerHoldsTheTap(t *testing.T) {
	t.Parallel()
	under := btn("Under", "under", 600)
	under.Class = "Button"
	var veiled atomic.Bool
	veiled.Store(true)
	f := &fakeDriver{plat: "ios"}
	f.screen = func() []*ui.Node {
		if veiled.Load() {
			spin := &ui.Node{Class: "ActivityIndicator", Text: "1", Bounds: ui.Rect{L: 0, T: 62, R: 400, B: 790}}
			return []*ui.Node{spin, under}
		}
		return []*ui.Node{under}
	}
	var underVeil atomic.Bool
	f.onTap = func(x, y int) { underVeil.Store(veiled.Load()) }
	go func() {
		time.Sleep(400 * time.Millisecond)
		veiled.Store(false)
		f.change(true)
	}()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if underVeil.Load() || len(f.taps) != 1 {
		t.Errorf("taps %v, under the veil %v", f.taps, underVeil.Load())
	}
	// A spinner of its own size beside the button.
	small := &ui.Node{Class: "ActivityIndicator", Bounds: ui.Rect{L: 0, T: 540, R: 40, B: 580}}
	g := &fakeDriver{plat: "ios", screen: static(small, under)}
	start := time.Now()
	mustPass(t, runWith(t, g, quick, "- tapOn: Under"))
	if time.Since(start) > 300*time.Millisecond {
		t.Errorf("a small spinner held the tap for %s", time.Since(start))
	}
}

// A floating bar over a content layer that holds a spinner far away (a feed loading more):
// the layer is drawn under the bar, so it is no cover. Android lists the layer after the bar
// (by place, not as drawn); the drawing order says which is on top. Issue #11.
func TestLoadingCoverDrawnUnderTarget(t *testing.T) {
	const pb = "android.widget.ProgressBar"
	order := func(n *ui.Node, o int) *ui.Node { n.DrawOrder, n.DrawOrderKnown = o, true; return n }
	build := func(barOrder, layerOrder int, known bool) (*ui.Screen, *ui.Node, *ui.Node) {
		root := at("Frame", 0, 0, 0, 400, 800)
		bar := at("LinearLayout", 1, 100, 680, 300, 740)
		action := clickable(2, 120, 690, 280, 730)
		layer := at("FrameLayout", 1, 0, 0, 400, 800)
		rows := at("LinearLayout", 2, 0, 0, 400, 800)
		spin := at(pb, 3, 180, 100, 220, 140) // near the top, far from the bar
		if known {
			order(bar, barOrder)
			order(layer, layerOrder)
		}
		return linked("android", root, bar, action, layer, rows, spin), action, layer
	}
	if s, action, _ := build(1, 0, true); loadingCover(s, action) != nil {
		t.Error("a layer drawn under the bar was taken for a loading overlay over it")
	}
	if s, action, layer := build(0, 1, true); loadingCover(s, action) != layer {
		t.Error("a layer drawn over the bar (a real veil) is a cover")
	}
	if s, action, layer := build(0, 0, false); loadingCover(s, action) != layer {
		t.Error("drawing order unknown: as before, the layer is a cover")
	}
	if s, action, layer := build(1, 1, true); loadingCover(s, action) != layer {
		t.Error("drawn at once (equal orders): as before, the layer is a cover")
	}
}

// The tap itself: no wait for a layer drawn under the floating bar (it took the whole lookup
// time before, Issue #11).
func TestTapOnFloatingBarOverSpinningLayerDoesNotWait(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		root := at("Frame", 0, 0, 0, 400, 800)
		bar := at("LinearLayout", 1, 100, 680, 300, 740)
		bar.DrawOrder, bar.DrawOrderKnown = 1, true
		action := clickable(2, 120, 690, 280, 730)
		action.Text = "Bar action"
		layer := at("FrameLayout", 1, 0, 0, 400, 800)
		layer.DrawOrder, layer.DrawOrderKnown = 0, true
		return []*ui.Node{root, bar, action, layer, at("LinearLayout", 2, 0, 0, 400, 800),
			at("android.widget.ProgressBar", 3, 180, 100, 220, 140)}
	}
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, `- tapOn: "Bar action"`))
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s: waited for a layer drawn under the bar", d)
	}
	if len(f.taps) != 1 || f.taps[0][1] < 690 || f.taps[0][1] > 730 {
		t.Errorf("taps %v, want one on the bar's button", f.taps)
	}
}

// closingList is an account list over a form that, once a row is picked, leaves the tree at
// once but stays drawn, still, and taking touches for hold (bench/flows/scenarios/transitions.yaml;
// a React Native screen closing while its JavaScript thread is busy). A touch on it in that
// time picks the row under it and starts the hold again. Its last row, "Credit card", lies
// over the form's amount field.
type closingList struct {
	f        *fakeDriver
	hold     time.Duration
	open     bool      // listed and drawn
	heldTill time.Time // drawn and taking touches, though out of the tree, until then
	account  string
	focused  bool      // the amount field took a tap
	picked   time.Time // the last pick
	tapped   time.Time // the amount field's tap
}

var (
	clList    = ui.Rect{T: 100, R: 400, B: 800}
	clRows    = []ui.Rect{{T: 100, R: 400, B: 164}, {T: 164, R: 400, B: 228}, {T: 228, R: 400, B: 800}}
	clNames   = []string{"Checking", "Savings", "Credit card"}
	clPick    = ui.Rect{L: 20, T: 140, R: 200, B: 180}
	clAmount  = ui.Rect{L: 20, T: 300, R: 380, B: 340}
	clAccount = ui.Rect{L: 20, T: 110, R: 380, B: 130}
)

// drawn: the list shows (open, or closed but not yet removed). Called with f.mu held or not.
func (c *closingList) drawn() bool { return c.open || time.Now().Before(c.heldTill) }

func (c *closingList) nodes() []*ui.Node {
	ios := c.f.Platform() == "ios"
	kind := func(android, iosType string) string {
		if ios {
			return iosType
		}
		return android
	}
	node := func(class, id, text string, depth int, b ui.Rect, touch bool) *ui.Node {
		return &ui.Node{Class: class, ID: id, Text: text, Depth: depth, Bounds: b, Enabled: true, Clickable: touch, Editable: class == "EditText"}
	}
	d := 0 // the form's and the list's depth: on Android both sit in a frame
	var out []*ui.Node
	if !ios {
		out = append(out, node("FrameLayout", "", "", 0, clList, false))
		d = 1
	}
	list := node(kind("LinearLayout", "Other"), "tr_list", "", d, clList, false)
	list.DrawOrder, list.DrawOrderKnown = 2, !ios
	switch {
	case c.open:
		out = append(out, list)
		for i, r := range clRows {
			out = append(out, node(kind("TextView", "Button"), "tr_account_"+strings.ToLower(strings.Fields(clNames[i])[0]), clNames[i], d+1, r, true))
		}
	case ios && c.drawn():
		list.ID = "" // iOS keeps the closing view, with no id and nothing in it
		out = append(out, list)
	}
	fd := d
	if !ios {
		form := node("LinearLayout", "", "", d, clList, false)
		form.DrawOrder, form.DrawOrderKnown = 1, true
		out = append(out, form)
		fd = d + 1
	}
	out = append(out,
		node(kind("TextView", "StaticText"), "tr_account", "To: "+c.account, fd, clAccount, false),
		node(kind("Button", "Button"), "tr_pick", "Choose account", fd, clPick, true),
		node(kind("EditText", "TextField"), "tr_amount", "Amount", fd, clAmount, true))
	return out
}

func (c *closingList) image() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 400, 800))
	fill := func(r ui.Rect, col color.RGBA) {
		for y := r.T; y < r.B; y++ {
			for x := r.L; x < r.R; x++ {
				img.SetRGBA(x, y, col)
			}
		}
	}
	fill(ui.Rect{R: 400, B: 800}, color.RGBA{255, 255, 255, 255})
	f := c.f
	f.mu.Lock()
	drawn := c.drawn()
	f.mu.Unlock()
	if drawn {
		fill(clList, color.RGBA{235, 235, 235, 255})
		for _, r := range clRows {
			fill(ui.Rect{L: 20, T: r.T + 20, R: 140, B: r.T + 40}, color.RGBA{30, 30, 30, 255}) // its label
		}
		return img
	}
	fill(clAccount, color.RGBA{30, 30, 30, 255})
	fill(clPick, color.RGBA{40, 80, 200, 255})
	fill(clAmount, color.RGBA{20, 20, 20, 255}) // the border
	fill(ui.Rect{L: clAmount.L + 2, T: clAmount.T + 2, R: clAmount.R - 2, B: clAmount.B - 2}, color.RGBA{250, 250, 255, 255})
	fill(ui.Rect{L: 30, T: 312, R: 100, B: 328}, color.RGBA{150, 150, 150, 255}) // the hint
	return img
}

// tap is what a touch at (x, y) does. Called without f.mu held.
func (c *closingList) tap(x, y int) {
	f := c.f
	f.mu.Lock()
	if c.drawn() && clList.Contains(x, y) {
		for i, r := range clRows {
			if r.Contains(x, y) {
				c.account, c.open = clNames[i], false
				c.heldTill, c.picked = time.Now().Add(c.hold), time.Now()
			}
		}
		hold := c.hold
		f.mu.Unlock()
		f.change(true)
		if hold > 0 && hold < time.Minute {
			time.AfterFunc(hold, func() { f.change(true) }) // removed: the screen redraws
		}
		return
	}
	switch {
	case clPick.Contains(x, y):
		c.open = true
	case clAmount.Contains(x, y):
		c.focused, c.tapped = true, time.Now()
	}
	f.mu.Unlock()
	f.change(true)
}

func newClosingList(plat string, hold time.Duration) *closingList {
	c := &closingList{f: &fakeDriver{plat: plat}, hold: hold, account: "none"}
	c.f.screen = func() []*ui.Node { return c.nodes() } // f.mu is held here
	c.f.draw = c.image
	c.f.onTap = c.tap
	return c
}

// A tap on an element a closing screen still covers waits until the screen's pixels go
// (bench/flows/scenarios/transitions.yaml; a React Native screen closing while its
// JavaScript thread is busy): picking an account takes the list out of the tree at once, but
// it stays drawn, still, and takes touches for a while. The amount field under it is listed
// at once; nothing in the tree or the event stream says the list is still there, but the
// pixels where the field is are still the list's, as they were before the pick. The tap waits
// until they change. A list really gone at once costs no wait; one that never goes is waited
// for up to the cap, then tapped anyway, and the trace says so.
func TestTapWaitsForClosingScreenStillDrawn(t *testing.T) {
	t.Parallel()
	flow := "- tapOn: {id: tr_pick}\n- tapOn: {id: tr_account_savings}\n- tapOn: {id: tr_amount}\n"
	for _, plat := range []string{"android", "ios"} {
		t.Run(plat+", held", func(t *testing.T) {
			t.Parallel()
			const hold = 250 * time.Millisecond
			c := newClosingList(plat, hold)
			res, tr := traced(t, c.f, flow)
			mustPass(t, res)
			c.f.mu.Lock()
			defer c.f.mu.Unlock()
			if c.account != "Savings" || !c.focused {
				t.Fatalf("account %q, field tapped %v: want Savings kept, the field tapped once the list went\n%s", c.account, c.focused, tr)
			}
			if gap := c.tapped.Sub(c.picked); gap < hold {
				t.Errorf("the field was tapped %s after the pick, while the list still covered it (%s)", gap, hold)
			}
			wantLines(t, tr, `still drawn where it was`, `it went after \d+ ms`)
		})
		t.Run(plat+", gone at once", func(t *testing.T) {
			t.Parallel()
			c := newClosingList(plat, 0)
			res, tr := traced(t, c.f, flow)
			mustPass(t, res)
			shots := c.f.called("Screenshot")
			c.f.mu.Lock()
			defer c.f.mu.Unlock()
			if c.account != "Savings" || !c.focused {
				t.Fatalf("account %q, field tapped %v\n%s", c.account, c.focused, tr)
			}
			if gap := c.tapped.Sub(c.picked); gap > 150*time.Millisecond {
				t.Errorf("waited %s to tap the field, with the list gone at once", gap)
			}
			if strings.Contains(tr, "still drawn") {
				t.Errorf("waited for a list that went at once:\n%s", tr)
			}
			// One screenshot before the pick, one to see the list gone; none for the others.
			if len(shots) != 2 {
				t.Errorf("screenshots %v, want 2", shots)
			}
		})
		t.Run(plat+", never goes", func(t *testing.T) {
			t.Parallel()
			c := newClosingList(plat, time.Hour)
			res, tr := traced(t, c.f, flow) // quick: the tap's lookup timeout (400 ms) caps it
			mustPass(t, res)
			c.f.mu.Lock()
			defer c.f.mu.Unlock()
			if gap := time.Since(c.picked); gap > 2*time.Second {
				t.Errorf("waited %s, over the cap", gap)
			}
			wantLines(t, tr, `still drawn where it was`, `still drawn after \d+ ms: tapping anyway`)
		})
	}
}

// A tap in a see-through layer over the content takes no screenshot (bench/flows/scenarios/
// navigation.yaml on iOS: a full-screen container holding only the tab buttons, listed apart
// from the tab's content, whose order iOS does not tell): the layer takes no touches over the
// content's buttons, so it covers nothing a tap could miss. A list whose rows lie over two of
// a form's buttons does (bench/flows/scenarios/transitions.yaml).
func TestCoverOverNeedsTouchesOverWhatItCovers(t *testing.T) {
	t.Parallel()
	tab := &ui.Node{Class: "Button", ID: "navtab_feed", Depth: 1, Bounds: ui.Rect{L: 100, T: 740, R: 200, B: 800}, Enabled: true}
	layer := &ui.Node{Class: "Other", Bounds: ui.Rect{R: 400, B: 800}}
	content := []*ui.Node{
		{Class: "Button", ID: "open_post", Bounds: ui.Rect{L: 100, T: 300, R: 300, B: 340}, Enabled: true},
		{Class: "Button", ID: "share", Bounds: ui.Rect{L: 100, T: 400, R: 300, B: 440}, Enabled: true},
	}
	s := linked("ios", append([]*ui.Node{layer, tab}, content...)...)
	if c, _ := coverOver(s, tab); c != nil {
		t.Errorf("a tab in a see-through layer: cover %s, want none", nodeDesc(c))
	}
	rows := &ui.Node{Class: "Button", ID: "row", Depth: 1, Bounds: ui.Rect{T: 280, R: 400, B: 800}, Enabled: true}
	list := &ui.Node{Class: "Other", ID: "list", Bounds: ui.Rect{T: 280, R: 400, B: 800}}
	s = linked("ios", append([]*ui.Node{list, rows}, content...)...)
	if c, under := coverOver(s, rows); c == nil || c.ID != "list" || len(under) != 2 {
		t.Errorf("a list over two buttons: cover %s over %d, want the list over 2", nodeDesc(c), len(under))
	}
}
