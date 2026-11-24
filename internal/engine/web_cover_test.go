package engine

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

func webCover(n, by *ui.Node, layers ...ui.HitLayer) {
	v := n.Bounds.Intersect(ui.Rect{R: 400, B: 800})
	n.Covers = []ui.Hit{{Point: 0, X: v.CX(), Y: v.CY(), By: by, Layers: layers}}
}

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

func TestWebTapUnderAModalLayerThatStaysFails(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "web", screen: static(webDrawer()...)}
	mustFail(t, runWith(t, f, quick, "- tapOn: Audio"), `"Audio" is under`)
	if len(f.taps) != 0 || len(f.swipeAt) != 0 {
		t.Errorf("taps %v, drags %v: want none", f.taps, f.swipeAt)
	}
}

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
