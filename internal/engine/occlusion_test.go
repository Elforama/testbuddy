package engine

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// filesDrawer is the Files app (DocumentsUI) with its roots drawer open, as tb read it in
// issue #282 (1080x2400): the directory scrolls up and down, a row of filter chips ("Images",
// "Audio") in it scrolls sideways, and the drawer, later in the tree, lists the roots: its
// "Images" row is drawn over the chips. open false lists the directory alone.
func filesDrawer(open bool) []*ui.Node {
	r := func(depth int, class, text string, l, t, rr, b int) *ui.Node {
		return &ui.Node{Depth: depth, Class: class, Text: text, Bounds: ui.Rect{L: l, T: t, R: rr, B: b}, Enabled: true}
	}
	top := r(0, "android.widget.FrameLayout", "", 0, 0, 1080, 2400)
	layout := r(1, "androidx.drawerlayout.widget.DrawerLayout", "", 0, 136, 1080, 2400)
	dir := r(2, "android.widget.ScrollView", "", 0, 136, 1080, 2400)
	dir.Scrollable = true
	chips := r(3, "android.widget.HorizontalScrollView", "", 0, 388, 1080, 514)
	chips.Scrollable = true
	chip := r(4, "android.widget.Button", "Images", 64, 388, 319, 514)
	audio := r(4, "android.widget.Button", "Audio", 341, 388, 571, 514)
	chip.Clickable, audio.Clickable = true, true
	nodes := []*ui.Node{top, layout, dir, chips, chip, audio}
	if !open {
		return nodes
	}
	drawer := r(2, "android.widget.LinearLayout", "", 0, 136, 735, 2400)
	recent := r(3, "android.widget.LinearLayout", "", 0, 283, 735, 430)
	row := r(3, "android.widget.LinearLayout", "", 0, 430, 735, 577)
	recent.Clickable, row.Clickable = true, true
	return append(nodes, drawer,
		recent, r(4, "android.widget.TextView", "Recent", 168, 331, 714, 382),
		row, r(4, "android.widget.TextView", "Images", 168, 478, 714, 529))
}

// Issue #282: `tapOn: Images` with the drawer open picked the chip under the drawer, dragged
// the directory to bring it clear of the drawer's row (scrolling content under the drawer),
// then tapped the chip's middle, which only happened to land on the drawer's row. The
// drawer's "Images" is the one a person reaches: it is tapped, and nothing is dragged.
func TestTapPicksTheDrawerItemOverTheChipUnderIt(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{size: [2]int{1080, 2400}, screen: static(filesDrawer(true)...)}
	mustPass(t, runWith(t, f, quick, "- tapOn: Images"))
	if len(f.swipeAt) != 0 {
		t.Errorf("dragged %v: scrolled the content under the drawer", f.swipeAt)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{441, 503} {
		t.Errorf("taps %v, want the drawer's Images label's middle (441, 503)", f.taps)
	}
}

// Issue #282, a cover that is a modal layer: the only "Audio" is the chip under the drawer.
// No drag can bring it clear (it would scroll the content under the drawer): the tap waits
// for the drawer to go, then taps the chip.
func TestTapUnderAModalLayerWaitsInsteadOfDragging(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{size: [2]int{1080, 2400}}
	var closed atomic.Bool
	f.screen = func() []*ui.Node { return filesDrawer(!closed.Load()) }
	go func() {
		time.Sleep(300 * time.Millisecond)
		closed.Store(true)
		f.change(true)
	}()
	var tappedOpen atomic.Bool
	f.onTap = func(x, y int) { tappedOpen.Store(!closed.Load()) }
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Audio"))
	if len(f.swipeAt) != 0 {
		t.Errorf("dragged %v under the drawer", f.swipeAt)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{456, 451} || tappedOpen.Load() {
		t.Errorf("taps %v (drawer still open: %v), want the chip's middle once the drawer went", f.taps, tappedOpen.Load())
	}
}

// Issue #282's guard: a bar is still dragged clear of. A row whose middle is under a toolbar
// laid over the top of its list (part of the row shows) is brought clear by a drag of its
// list, as before.
func TestTapUnderABarStillDrags(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var moved atomic.Bool
	f.screen = func() []*ui.Node {
		top := &ui.Node{Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		list := &ui.Node{Depth: 1, Class: "RecyclerView", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		row := &ui.Node{Depth: 2, Text: "Row", Clickable: true, Bounds: ui.Rect{T: 20, R: 400, B: 80}, Enabled: true}
		if moved.Load() {
			row.Bounds = ui.Rect{T: 120, R: 400, B: 180}
		}
		bar := &ui.Node{Depth: 1, Class: "Toolbar", Clickable: true, Bounds: ui.Rect{R: 400, B: 60}, Enabled: true}
		return []*ui.Node{top, list, row, bar}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) { moved.Store(true); f.change(true) }
	mustPass(t, runWith(t, f, quick, "- tapOn: Row"))
	if len(f.swipeAt) != 1 {
		t.Errorf("%d drags, want 1 to bring the row clear of the bar", len(f.swipeAt))
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 150} {
		t.Errorf("taps %v, want the row's middle where the drag left it", f.taps)
	}
}

// Issue #282's second part: a drawer slides in with few or no events, so the screen can count
// as settled before the drawer is listed; the first read then has only the chip. When a later
// read picks another match (the drawer's row, now over the chip), that is a new target, not
// the chip moving: it is read until it agrees with itself and tapped, without the wait for a
// target "cut off at an edge" that comparing the two elements' sizes would cause.
func TestTapWaitsForADrawerThatSlidesInUnannounced(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{size: [2]int{1080, 2400}}
	var reads atomic.Int64
	f.screen = func() []*ui.Node { return filesDrawer(reads.Add(1) > 1) }
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- tapOn: Images"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{441, 503} || len(f.swipeAt) != 0 {
		t.Errorf("taps %v, drags %v: want one tap on the drawer's Images", f.taps, f.swipeAt)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s: the new target was taken for one cut off while moving", d)
	}
}

// Review of #282: a target still under a modal layer once the wait is over is not tapped "in
// its middle anyway" (that lands on the layer, on whatever row of the drawer is there): the
// step fails, naming what covers it. An optional step is skipped, with no tap.
func TestTapUnderAModalLayerThatStaysFails(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{size: [2]int{1080, 2400}, screen: func() []*ui.Node { return filesDrawer(true) }}
	o := Options{LookupTimeout: 500 * time.Millisecond, OptionalTimeout: 300 * time.Millisecond}
	mustFail(t, runWith(t, f, o, "- tapOn: Audio"), `"Audio" is under`)
	if len(f.taps) != 0 || len(f.swipeAt) != 0 {
		t.Errorf("taps %v, drags %v: want none", f.taps, f.swipeAt)
	}
	g := &fakeDriver{size: [2]int{1080, 2400}, screen: func() []*ui.Node { return filesDrawer(true) }}
	res := runWith(t, g, o, "- tapOn: {text: Audio, optional: true}")
	if res.Err != nil || len(g.taps) != 0 {
		t.Errorf("optional: %v, taps %v: want it skipped, no tap", res.Err, g.taps)
	}
}

// Review of #282: a BottomSheetDialog's window spans the screen, and its touch_outside lies
// over what is under the sheet. A flow that taps there means to dismiss the sheet: the tap
// goes at once, not after a wait for a "modal layer" to go.
func TestTapThroughASheetsTouchOutside(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Under", "under", 300))}
	f.appAbove = func() *ui.Window {
		root := &ui.Node{Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		outside := &ui.Node{Depth: 1, ID: "com.google.android.material:id/touch_outside", Clickable: true, Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		sheet := &ui.Node{Depth: 1, Bounds: ui.Rect{T: 500, R: 400, B: 800}, Enabled: true}
		action := &ui.Node{Depth: 2, Text: "Sheet action", Clickable: true, Bounds: ui.Rect{T: 600, R: 400, B: 700}, Enabled: true}
		return &ui.Window{Type: "app", Package: "app", Focused: true, Bounds: ui.Rect{R: 400, B: 800}, Nodes: []*ui.Node{root, outside, sheet, action}}
	}
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 325} {
		t.Errorf("taps %v, want Under's middle (on the sheet's outside)", f.taps)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s: waited for the sheet's outside as if it were a modal layer", d)
	}
}

// Review of #282: a window without focus over the app (picture-in-picture) takes only the
// touches on what it draws: it is a cover the row's list is dragged clear of, not a modal layer.
func TestTapDragsClearOfAPictureInPictureWindow(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var moved atomic.Bool
	f.screen = func() []*ui.Node {
		list := &ui.Node{Class: "RecyclerView", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		row := &ui.Node{Depth: 1, Text: "Row", Clickable: true, Bounds: ui.Rect{T: 650, R: 400, B: 750}, Enabled: true}
		if moved.Load() {
			row.Bounds = ui.Rect{T: 400, R: 400, B: 500}
		}
		return []*ui.Node{list, row}
	}
	f.appAbove = func() *ui.Window {
		video := &ui.Node{Class: "SurfaceView", Clickable: true, Bounds: ui.Rect{L: 150, T: 600, R: 400, B: 800}, Enabled: true}
		return &ui.Window{Type: "app", Package: "video", Bounds: video.Bounds, Nodes: []*ui.Node{video}}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) { moved.Store(true); f.change(true) }
	mustPass(t, runWith(t, f, quick, "- tapOn: Row"))
	if len(f.swipeAt) != 1 || len(f.taps) != 1 || f.taps[0] != [2]int{200, 450} {
		t.Errorf("drags %v, taps %v: want one drag, then a tap at the row's middle (200, 450)", f.swipeAt, f.taps)
	}
}

// Review of #282: sameElement tells a read of the same element (moved, or saying something
// else) from another match, even an id-less twin in a container just like its own.
func TestSameElement(t *testing.T) {
	read := func(moved bool) *ui.Screen {
		dy := 0
		if moved {
			dy = 40
		}
		return linked("android",
			at("Frame", 0, 0, 0, 400, 800),
			at("Card", 1, 0, 0, 400, 300), &ui.Node{Depth: 2, Class: "Button", Text: "OK", Bounds: ui.Rect{T: 100 + dy, R: 200, B: 150 + dy}, Enabled: true},
			at("Card", 1, 0, 400, 400, 700), &ui.Node{Depth: 2, Class: "Button", Text: "OK", Bounds: ui.Rect{T: 500, R: 200, B: 550}, Enabled: true})
	}
	a, b := read(false), read(true)
	if !sameElement(a.Nodes[2], b.Nodes[2]) {
		t.Error("the same button, moved: not the same")
	}
	if sameElement(a.Nodes[2], b.Nodes[4]) {
		t.Error("its twin in the other card: the same")
	}
}

// Re-review of #282: inside a list, a row's place among the list's children changes when a
// banner comes in above it (or the list recycles rows): it is still the same row, so atRest
// keeps where it was first found and how large it was (cut-off and away detection).
func TestSameElementInAList(t *testing.T) {
	read := func(banner bool) (*ui.Screen, *ui.Node) {
		list := at("RecyclerView", 0, 0, 0, 400, 800)
		list.Scrollable = true
		nodes := []*ui.Node{list}
		if banner {
			nodes = append(nodes, at("Banner", 1, 0, 0, 400, 100))
		}
		row := at("Row", 1, 0, 300, 400, 350)
		label := &ui.Node{Depth: 2, Class: "Text", Text: "Item", Bounds: row.Bounds, Enabled: true}
		nodes = append(nodes, row, label)
		return linked("android", nodes...), label
	}
	_, before := read(false)
	_, after := read(true)
	if !sameElement(before, after) {
		t.Error("the row's label, after a banner came in above the row: not the same")
	}
}

// Third review of #282: a veil is waited for as long as a loading overlay (the lookup
// timeout), not the 2 s a drawer gets to close: a loading dialog that stays 2.5 s is waited
// out, and the tap lands once it went.
func TestTapWaitsOutALongVeil(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Under", "under", 300))}
	var veiled atomic.Bool
	veiled.Store(true)
	f.appAbove = func() *ui.Window {
		if !veiled.Load() {
			return nil
		}
		veil := &ui.Node{Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		return &ui.Window{Type: "app", Package: "app", Focused: true, Bounds: ui.Rect{R: 400, B: 800}, Nodes: []*ui.Node{veil}}
	}
	go func() {
		time.Sleep(2500 * time.Millisecond)
		veiled.Store(false)
		f.change(true)
	}()
	var tappedVeiled atomic.Bool
	f.onTap = func(x, y int) { tappedVeiled.Store(veiled.Load()) }
	mustPass(t, runWith(t, f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if len(f.taps) != 1 || tappedVeiled.Load() {
		t.Errorf("taps %v (under the veil: %v), want one, once it went", f.taps, tappedVeiled.Load())
	}
}

// Re-review of #282: a focused window holding only an unnamed node as large as itself that
// takes no clicks is a veil (a loading dialog, or one still mounting), not a scrim a tap
// dismisses: the tap waits for it to go, then taps once.
func TestTapWaitsForAVeilWindow(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Under", "under", 300))}
	var veiled atomic.Bool
	veiled.Store(true)
	f.appAbove = func() *ui.Window {
		if !veiled.Load() {
			return nil
		}
		veil := &ui.Node{Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		return &ui.Window{Type: "app", Package: "app", Focused: true, Bounds: ui.Rect{R: 400, B: 800}, Nodes: []*ui.Node{veil}}
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		veiled.Store(false)
		f.change(true)
	}()
	var tappedVeiled atomic.Bool
	f.onTap = func(x, y int) { tappedVeiled.Store(veiled.Load()) }
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if len(f.taps) != 1 || tappedVeiled.Load() {
		t.Errorf("taps %v (under the veil: %v), want one, once it went", f.taps, tappedVeiled.Load())
	}
}
