package engine

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// webCover says the browser's hit test found by over n's middle (n on a 400x800 page), with
// the layers of by's branch that may make it modal (issue #300).
func webCover(n, by *ui.Node, layers ...ui.HitLayer) {
	v := n.Bounds.Intersect(ui.Rect{R: 400, B: 800})
	n.Covers = []ui.Hit{{Point: 0, X: v.CX(), Y: v.CY(), By: by, Layers: layers}}
}

// webDrawer is a page (400x800) with a drawer open over a list: the list's rows ("Images",
// "Audio") lie under the drawer's fixed backdrop, as the browser's hit test reports; the
// drawer repeats "Images".
func webDrawer() []*ui.Node {
	r := func(depth int, class, text string, l, t, rr, b int) *ui.Node {
		return &ui.Node{Depth: depth, Class: class, Text: text, Bounds: ui.Rect{L: l, T: t, R: rr, B: b}, Enabled: true}
	}
	body := r(0, "body", "", 0, 0, 400, 800)
	images := r(1, "div", "Images", 0, 200, 400, 250)
	audio := r(1, "div", "Audio", 0, 250, 400, 300)
	images.Clickable, audio.Clickable = true, true
	backdrop := r(1, "div", "", 0, 0, 400, 800)
	backdrop.ID = "backdrop"
	drawer := r(1, "nav", "", 0, 0, 200, 800)
	item := r(2, "div", "Images", 0, 200, 200, 250)
	item.Clickable = true
	fixed := ui.HitLayer{Node: backdrop, Kind: "fixed"}
	webCover(images, backdrop, fixed)
	webCover(audio, backdrop, fixed)
	return []*ui.Node{body, images, audio, backdrop, drawer, item}
}

// Issue #300: `tapOn: Images` with a drawer open taps the drawer's item, the one the browser's
// hit test finds clear, not the list's row under its backdrop; nothing is dragged.
func TestWebTapPicksTheDrawerItemOverTheRowUnderIt(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "web", screen: static(webDrawer()...)}
	mustPass(t, runWith(t, f, quick, "- tapOn: Images"))
	if len(f.swipeAt) != 0 {
		t.Errorf("dragged %v: scrolled the page under the drawer", f.swipeAt)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{100, 225} {
		t.Errorf("taps %v, want the drawer's item's middle (100, 225)", f.taps)
	}
}

// Issue #300: the only "Audio" is under the drawer's backdrop, which stays: no drag (it would
// scroll the page under the drawer), no tap (it would land on the backdrop); the step fails,
// naming the cover.
func TestWebTapUnderAModalLayerThatStaysFails(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "web", screen: static(webDrawer()...)}
	mustFail(t, runWith(t, f, quick, "- tapOn: Audio"), `"Audio" is under`)
	if len(f.taps) != 0 || len(f.swipeAt) != 0 {
		t.Errorf("taps %v, drags %v: want none", f.taps, f.swipeAt)
	}
}

// Review of #300: a fixed toast holding the target (no dialog, no aria-modal) ranks as modal
// but goes by itself: the tap waits for it up to the lookup timeout, not 2 s.
func TestWebTapWaitsOutAFixedToastThatHoldsTheTarget(t *testing.T) {
	t.Parallel()
	var gone atomic.Bool
	f := &fakeDriver{plat: "web"}
	f.screen = func() []*ui.Node {
		body := &ui.Node{Class: "body", Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		goBtn := &ui.Node{Depth: 1, Class: "button", Text: "Go", Clickable: true, Bounds: ui.Rect{L: 40, T: 100, R: 240, B: 140}, Enabled: true}
		nodes := []*ui.Node{body, goBtn}
		if !gone.Load() {
			toast := &ui.Node{Depth: 1, Class: "div", ID: "t", Text: "Saved", Bounds: ui.Rect{L: 20, T: 60, R: 320, B: 200}, Enabled: true}
			undo := &ui.Node{Depth: 2, Class: "button", Text: "Undo", Clickable: true, Bounds: ui.Rect{L: 30, T: 160, R: 90, B: 190}, Enabled: true}
			webCover(goBtn, toast, ui.HitLayer{Node: toast, Kind: "fixed"})
			nodes = append(nodes, toast, undo)
		}
		return nodes
	}
	go func() {
		time.Sleep(2500 * time.Millisecond)
		gone.Store(true)
		f.change(true)
	}()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 4 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Go"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{140, 120} {
		t.Errorf("taps %v, want Go's middle once the toast went", f.taps)
	}
}

// Review of #300: a cover in the page's flow (a stretched link over a card title) scrolls with
// the title: the page is not dragged to bring it clear.
func TestWebNoDragForACoverThatScrollsWithThePage(t *testing.T) {
	t.Parallel()
	body := &ui.Node{Class: "body", Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
	card := &ui.Node{Depth: 1, Class: "div", Bounds: ui.Rect{L: 40, T: 100, R: 340, B: 220}, Enabled: true}
	title := &ui.Node{Depth: 2, Class: "h5", Text: "Card title", Bounds: ui.Rect{L: 56, T: 116, R: 324, B: 140}, Enabled: true}
	link := &ui.Node{Depth: 2, Class: "a", Clickable: true, Bounds: ui.Rect{L: 40, T: 100, R: 340, B: 220}, Enabled: true}
	webCover(title, link)
	f := &fakeDriver{plat: "web", screen: static(body, card, title, link)}
	runWith(t, f, quick, "- tapOn: Card title")
	if len(f.swipeAt) != 0 {
		t.Errorf("dragged %v for a cover that scrolls with the page", f.swipeAt)
	}
}

// Issue #300: a sticky header over the middle of a row is a bar: the page (which the tree
// does not list as a scrolling element) is dragged to bring the row clear, and the row is
// tapped where it then is.
func TestWebTapUnderAStickyHeaderDrags(t *testing.T) {
	t.Parallel()
	var moved atomic.Bool
	f := &fakeDriver{plat: "web"}
	f.screen = func() []*ui.Node {
		body := &ui.Node{Class: "body", Bounds: ui.Rect{R: 400, B: 800}, Enabled: true}
		header := &ui.Node{Depth: 1, Class: "header", Text: "Header", Bounds: ui.Rect{R: 400, B: 100}, Enabled: true}
		row := &ui.Node{Depth: 1, Class: "div", Text: "Row", Clickable: true, Bounds: ui.Rect{T: 60, R: 400, B: 110}, Enabled: true}
		if moved.Load() {
			row.Bounds = ui.Rect{T: 160, R: 400, B: 210}
		} else {
			webCover(row, header, ui.HitLayer{Node: header, Kind: "sticky"})
		}
		return []*ui.Node{body, header, row}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) { moved.Store(true); f.change(true) }
	mustPass(t, runWith(t, f, quick, "- tapOn: Row"))
	if len(f.swipeAt) != 1 {
		t.Errorf("%d drags, want 1 to bring the row clear of the header", len(f.swipeAt))
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 185} {
		t.Errorf("taps %v, want the row's middle where the drag left it (200, 185)", f.taps)
	}
}
