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
