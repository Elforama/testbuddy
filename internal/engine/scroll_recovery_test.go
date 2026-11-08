package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// An element that keeps moving after a drag (a bounce, a snap) counts once the UI settles,
// after a second of reads that disagree.
func TestScrollUntilVisibleElementKeepsMoving(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var dragged atomic.Bool
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		list := &ui.Node{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		if !dragged.Load() {
			return []*ui.Node{list}
		}
		row := btn("Target", "", 300+int(reads.Add(1)%2)*10) // never the same twice
		row.Depth = 1
		return []*ui.Node{list, row}
	}
	stop := make(chan struct{})
	defer close(stop)
	f.onSwipe = func(x1, y1, x2, y2 int) {
		dragged.Store(true)
		go func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(40 * time.Millisecond):
					f.change(false)
				}
			}
		}()
	}
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Target, timeout: 5000}"))
	if d := time.Since(start); d < 900*time.Millisecond || d > 2500*time.Millisecond {
		t.Errorf("took %s, want about a second of disagreeing reads", d)
	}
	if len(f.swipeAt) != 1 {
		t.Errorf("%d drags", len(f.swipeAt))
	}
	settled := false
	for _, d := range f.waitDur {
		if d == 2*time.Second {
			settled = true
		}
	}
	if !settled {
		t.Errorf("settle waits %v: want the 2 s settle", f.waitDur)
	}
}

// A list that redraws a beat after each drag (the screen changes at once, elsewhere): a read
// right after a drag shows nothing new, but the settled screen does. Such a drag is not
// fruitless, so the scroll keeps dragging (no flinging, which could carry the element past
// the screen) and finds the element.
func TestScrollUntilVisibleLaggingList(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, _ = f.scroller(60, false, 0)
	moveList := f.onSwipe
	list := f.screen
	var pending [][4]int
	var wobble atomic.Int64 // something on screen that changes at once with each drag
	f.screen = func() []*ui.Node {
		// A ticker in the list, cycling through a few texts it soon has shown already.
		tick := &ui.Node{Text: fmt.Sprintf("Live %d", wobble.Load()%3), Depth: 1, Bounds: ui.Rect{L: 300, T: 0, R: 400, B: 20}}
		return append(list(), tick)
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		f.mu.Lock()
		pending = append(pending, [4]int{x1, y1, x2, y2})
		f.mu.Unlock()
		wobble.Add(1)
		f.change(false)
	}
	f.onWait = func() { // the list catches up once the screen settles
		f.mu.Lock()
		p := pending
		pending = nil
		f.mu.Unlock()
		for _, s := range p {
			moveList(s[0], s[1], s[2], s[3])
		}
	}
	done := make(chan FlowResult, 1)
	go func() { done <- runWith(t, f, quick, "- scrollUntilVisible: {element: Item 45, timeout: 3000}") }()
	select {
	case res := <-done:
		mustPass(t, res)
	case <-time.After(10 * time.Second):
		t.Fatal("the scroll did not end: flinging at a list that lags (the drags were taken for fruitless)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, o := range f.swipes {
		if !o.Drag {
			t.Fatalf("swipe %d of %d flung: the lagging drags were taken for fruitless", i+1, len(f.swipes))
		}
	}
}

// Android: a row reported fully visible but flush with its list's edge may be cut off there
// (Android cuts bounds to what shows). With 100% asked, it is brought in once more.
func TestScrollUntilVisibleFlushRowBroughtIn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	off, _ := f.scroller(40, false, 0)
	list := f.screen
	f.screen = func() []*ui.Node {
		nodes := list()
		for _, n := range nodes {
			if n.Text == "Item 18" && n.Bounds.B > 800 {
				n.Bounds.B = 800 // cut to the screen, as Android reports it
			}
		}
		return nodes
	}
	off.Store(250) // Item 18 (rows are 50 px from y=100) at 750-800: flush with the list's bottom
	res := runWith(t, f, quick, "- scrollUntilVisible: {element: Item 18, visibilityPercentage: 100, timeout: 5000}")
	mustPass(t, res)
	if len(f.swipeAt) != 1 {
		t.Errorf("%d drags, want 1 to bring the flush row in", len(f.swipeAt))
	}
}

// A snapping pager can briefly remove a row after reporting it flush with the
// viewport. A pending edge correction must not dereference that missing row.
func TestScrollUntilVisibleFlushRowDisappearsWhileSnapping(t *testing.T) {
	var phase atomic.Int32
	f := &fakeDriver{}
	f.onSwipe = func(_, _, _, _ int) { phase.CompareAndSwap(0, 1) }
	f.screen = func() []*ui.Node {
		list := &ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		switch phase.Load() {
		case 0:
			return []*ui.Node{list}
		case 1:
			phase.Store(2)
			f.seq++ // a subsequent render is already queued
			return []*ui.Node{list, {Depth: 1, Text: "Discover", Bounds: ui.Rect{L: 300, T: 100, R: 400, B: 200}}}
		case 2:
			phase.Store(3)
			f.seq++
			return []*ui.Node{list}
		default:
			return []*ui.Node{list, {Depth: 1, Text: "Discover", Bounds: ui.Rect{L: 150, T: 100, R: 250, B: 200}}}
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Discover, direction: LEFT, visibilityPercentage: 100, timeout: 2000}"))
}

// After two drags that brought nothing new (a list that did not move for a moment), a fling
// that brings new rows into view returns to drags: flings could carry the element past.
func TestScrollUntilVisibleBackToDragsAfterFling(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, _ = f.scroller(80, false, 0)
	moveList := f.onSwipe
	list := f.screen
	var swipes, tick atomic.Int64
	f.screen = func() []*ui.Node {
		t := &ui.Node{Text: fmt.Sprintf("Live %d", tick.Load()%2), Depth: 1, Bounds: ui.Rect{L: 300, R: 400, B: 20}}
		return append(list(), t)
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		tick.Add(1)
		if swipes.Add(1) <= 3 {
			f.change(false) // the first drags move nothing (the screen still changes)
			return
		}
		moveList(x1, y1, x2, y2)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 60, timeout: 10000}"))
	f.mu.Lock()
	defer f.mu.Unlock()
	// Drag 1 shows the ticker's other text (new); drags 2 and 3 show nothing new.
	if len(f.swipes) < 5 || !f.swipes[2].Drag || f.swipes[3].Drag || !f.swipes[4].Drag {
		var kinds []bool
		for _, o := range f.swipes {
			kinds = append(kinds, o.Drag)
		}
		t.Errorf("drag per swipe %v: want three drags, a fling, then drags again", kinds)
	}
}

func TestIOSScrollWaitsForFreshBoundsAfterFling(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios"}
	var flung bool
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		if flung {
			// Flutter can keep coasting without a new accessibility generation.
			// The first frames even have the same bounds when rounded to points.
			x := 100.2
			if reads.Add(1) > 1 {
				x = 100.1
			}
			nodes = append(nodes, &ui.Node{Depth: 1, Text: "Card", Bounds: ui.Rect{L: 100, T: 100, R: 200, B: 200},
				PreciseBounds: &[4]float64{x, 100, x + 100, 200}})
		}
		return nodes
	}
	f.onSwipe = func(_, _, _, _ int) {
		if !f.swipes[len(f.swipes)-1].Drag {
			flung = true
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Card, direction: RIGHT, timeout: 8000}"))
	if got := reads.Load(); got < 3 {
		t.Fatalf("returned while the card was still coasting after only %d fresh reads", got)
	}
}

// Two panes: start in the list that shows the target or related rows, and
// retain the other-pane search when both lists have similar labels.
func TestScrollUntilVisibleTwoPanes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		platform                      string
		listed, similarRight, batched bool
	}{
		{"android", true, false, false}, {"android", false, false, false},
		{"android", false, true, false}, {"android", false, false, true},
		{"ios", false, false, true},
	} {
		f := &fakeDriver{plat: tc.platform}
		var off, tick atomic.Int64
		drags := 0
		f.screen = func() []*ui.Node {
			root := &ui.Node{Class: "root", Bounds: ui.Rect{R: 400, B: 800}}
			left := &ui.Node{Class: "list", Depth: 1, Scrollable: true, Bounds: ui.Rect{R: 160, B: 800}}
			if tc.platform == "ios" {
				left.Class = "CollectionView"
			} else if tc.batched {
				left.Coll = &ui.Coll{Rows: 80, Cols: 1}
			}
			nodes := []*ui.Node{root, left}
			for i := 1; i <= 80; i++ {
				top := i*60 - int(off.Load())
				if !tc.listed && (top < -60 || top > 800) {
					continue // a recycling list lists only what shows
				}
				nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Mail %d", i), Depth: 2, Bounds: ui.Rect{T: top, R: 160, B: top + 60}, Enabled: true, Clickable: true})
			}
			right := &ui.Node{Class: "body", Depth: 1, Scrollable: true, Bounds: ui.Rect{L: 160, R: 400, B: 800}}
			label := fmt.Sprintf("Live %d", tick.Load()%2)
			if tc.similarRight {
				label = fmt.Sprintf("Mail 6%d", 1+tick.Load()%2)
			}
			live := &ui.Node{Text: label, Depth: 2, Bounds: ui.Rect{L: 170, T: 10, R: 390, B: 40}}
			return append(nodes, right, live)
		}
		f.onSwipe = func(x1, y1, x2, y2 int) {
			tick.Add(1)
			if x1 < 160 { // only the left list moves
				off.Store(min(off.Load()+int64(y1-y2), 80*60-700))
				if tc.batched {
					drags++
					switch drags {
					case 1:
						off.Store(25) // only the beginning of the first drag is visible
					case 2:
						off.Store(2 * int64(y1-y2)) // the rest arrives with the second drag
					}
				}
				f.change(true)
			}
		}
		target, timeout := "Mail 60", 10000
		if tc.batched {
			target, timeout = "Mail 16", 1800
		}
		mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: %s, timeout: %d}", target, timeout)))
		f.mu.Lock()
		last := f.swipeAt[len(f.swipeAt)-1]
		if last[0] >= 160 {
			t.Errorf("%+v: the last drag was at x=%d, in the right pane", tc, last[0])
		}
		if rightFirst := f.swipeAt[0][0] >= 160; rightFirst != tc.similarRight {
			t.Errorf("%+v: unexpected starting pane: %v", tc, f.swipeAt[0])
		}
		f.mu.Unlock()
	}
}

// A sideways scroll whose list is mostly below the screen's edge first drags the page to bring
// it in, then drags across it (not along the few pixels that show).
func TestScrollSidewaysBringsListIn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var pageOff, shelfOff atomic.Int64
	f.screen = func() []*ui.Node {
		page := &ui.Node{Class: "page", Depth: 1, Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		top := 760 - int(pageOff.Load()) // the shelf starts 40 px above the bottom edge
		shelf := &ui.Node{Class: "shelf", Depth: 2, Scrollable: true, Bounds: ui.Rect{T: top, R: 400, B: top + 120}}
		nodes := []*ui.Node{{Class: "root", Bounds: ui.Rect{R: 400, B: 800}}, page, shelf}
		for i := 1; i <= 15; i++ {
			l := (i-1)*140 - int(shelfOff.Load())
			if l+140 <= 0 || l >= 400 {
				continue
			}
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Card 6-%d", i), Depth: 3, Bounds: ui.Rect{L: l, T: top + 10, R: l + 140, B: top + 110}, Enabled: true, Clickable: true})
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		top := 760 - int(pageOff.Load())
		if y1 >= top && y1 < top+120 && y1 == y2 { // across the shelf
			shelfOff.Store(min(shelfOff.Load()+int64(x1-x2), 15*140-400))
		} else if x1 == x2 { // the page
			pageOff.Store(max(0, min(pageOff.Load()+int64(y1-y2), 400)))
		}
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Card 6-12, direction: RIGHT, timeout: 5000}"))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.swipeAt) < 2 || f.swipeAt[0][0] != f.swipeAt[0][2] {
		t.Errorf("swipes %v: want the page brought up first, then sideways drags", f.swipeAt)
	}
}

// Rows taller than the room a drag leaves (bench/flows/scenarios/lists.yaml's grid on web: a
// 420 px cell where 580 px of the list shows): a row is whole on screen only within a window
// shorter than one drag, so full drags step past it. With 100% asked, once the row shows in
// part, the drag goes just far enough to bring it in whole.
func TestScrollUntilVisibleTallRows(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	const size = 760 // in an 800 px list: whole only while its top is within 0-40
	var off atomic.Int64
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		for i := 0; i < 12; i++ {
			p := 100 + i*size - int(off.Load())
			if p+size <= 0 || p >= 800 {
				continue
			}
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Item %d", i), Depth: 1, Enabled: true, Clickable: true,
				Bounds: ui.Rect{T: p, R: 400, B: p + size}})
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		if o := max(0, min(off.Load()+int64(y1-y2), int64(100+12*size-800))); o != off.Load() {
			off.Store(o)
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 5, timeout: 5000}"))
	if top := 100 + 5*size - int(off.Load()); top < 0 || top+size > 800 {
		t.Errorf("Item 5 at %d-%d: not whole in the 0-800 list", top, top+size)
	}
}

// scrollUntilVisibleIn, on the screen bench/flows/scenarios/lists.yaml met on an iPhone 17e:
// shelf 1 in view, shelf 6's heading on the bottom line and its cards below, not listed. The
// page scrolls until the shelf's first card is in, then that shelf, never shelf 1, is
// dragged sideways until the card shows. Every shelf's cards have the same texts, so only the
// list item (not a name shared with the element) tells which shelf to drag.
func TestScrollUntilVisibleIn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var pageOff atomic.Int64
	var shelfOff [2]atomic.Int64 // shelf 1, shelf 6
	tops := [2]int{300, 790}     // shelf 6's cards start below the 800 px screen
	names := [2]int{1, 6}
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{{Class: "root", Bounds: ui.Rect{R: 400, B: 800}},
			{Class: "page", Depth: 1, Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		for s := range 2 {
			top := tops[s] - int(pageOff.Load())
			if top+120 <= 0 || top >= 800 {
				continue
			}
			nodes = append(nodes, &ui.Node{Class: "shelf", Depth: 2, Scrollable: true, Bounds: ui.Rect{T: top, R: 400, B: top + 120}})
			for i := 1; i <= 15; i++ {
				l := (i-1)*140 - int(shelfOff[s].Load())
				if l+140 <= 0 || l >= 400 {
					continue
				}
				nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Card %d", i), ID: fmt.Sprintf("card_%d_%d", names[s], i), Depth: 3, Enabled: true, Clickable: true,
					Bounds: ui.Rect{L: l, T: top + 10, R: l + 140, B: top + 110}})
			}
		}
		return nodes
	}
	move := func(v *atomic.Int64, by, end int64) {
		if o := max(0, min(v.Load()+by, end)); o != v.Load() {
			v.Store(o)
			f.change(true)
		}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		if x1 == x2 { // the page, which ends 120 px down: both shelves stay in view
			move(&pageOff, int64(y1-y2), 120)
		}
		for s := range 2 {
			if top := tops[s] - int(pageOff.Load()); y1 == y2 && y1 >= top && y1 < top+120 {
				move(&shelfOff[s], int64(x1-x2), 15*140-400)
			}
		}
	}
	res := runWith(t, f, quick, "- scrollUntilVisibleIn: {list: {id: card_6_1}, element: {id: card_6_12}, direction: RIGHT, timeout: 5000}")
	mustPass(t, res)
	if shelfOff[0].Load() != 0 {
		t.Errorf("shelf 1 was dragged to %d: want only shelf 6 dragged", shelfOff[0].Load())
	}
	if want := "Scroll to id: card_6_12 in the list of id: card_6_1"; res.Steps[0].Desc != want {
		t.Errorf("step %q, want %q", res.Steps[0].Desc, want)
	}
}

func TestScrollUntilVisibleInFollowsMovingShelf(t *testing.T) {
	f := &fakeDriver{plat: "android"}
	top, offset := 560, 0
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{
			{Class: "page", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}},
			{Class: "shelf", ID: "shelf_6", Depth: 1, Scrollable: true, Bounds: ui.Rect{T: top, R: 400, B: top + 100}},
		}
		for i := range 12 {
			x := i*140 - offset
			if x+140 <= 0 || x >= 400 {
				continue
			}
			nodes = append(nodes, &ui.Node{Depth: 2, ID: fmt.Sprintf("card_%d", i+1), Text: fmt.Sprintf("Card %d", i+1),
				Enabled: true, Bounds: ui.Rect{L: x, T: top + 10, R: x + 140, B: top + 90}})
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		if y1 != y2 {
			return
		}
		if top == 560 {
			top = 390 // the parent finishes scrolling just as the first sideways drag starts
		} else if y1 >= top && y1 < top+100 {
			offset = min(offset+x1-x2, 12*140-400)
		} else {
			return
		}
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisibleIn: {list: {id: card_1}, element: {id: card_12}, direction: RIGHT, timeout: 2500}"))
}

// Under load a recycling list may expose the end of one drag together with
// the next, jumping over a page that neither accessibility read saw.
func TestScrollCoversSkippedCollectionRows(t *testing.T) {
	for _, tc := range []struct {
		dir, target string
		initial     int
	}{
		{"DOWN", "Mail 21", 0}, {"UP", "Mail 60", 3200},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			f := &fakeDriver{}
			offset := tc.initial
			first := true
			reversed := false
			f.screen = func() []*ui.Node {
				screen := ui.Rect{R: 400, B: 800}
				nodes := []*ui.Node{{Class: "Root", Bounds: screen}, {Depth: 1, Class: "RecyclerView", ID: "mail", Scrollable: true, Bounds: screen, Coll: &ui.Coll{Rows: 80, Cols: 1}}}
				for i := 0; i < 80; i++ {
					b := ui.Rect{T: i*50 - offset, R: 400, B: (i+1)*50 - offset}.Intersect(screen)
					if !b.Empty() {
						nodes = append(nodes, &ui.Node{Depth: 2, Text: fmt.Sprintf("Mail %d", i+1), Item: &ui.Coll{Rows: i, Cols: 0}, Enabled: true, Bounds: b})
					}
				}
				return nodes
			}
			f.onSwipe = func(_, y1, _, y2 int) {
				if first {
					first = false
					offset = 32 * 50
				} else {
					reversed = reversed || (tc.dir == "DOWN" && y2 > y1) || (tc.dir == "UP" && y2 < y1)
					offset = max(0, min(3200, offset+y1-y2))
				}
				f.change(true)
			}
			mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: '%s', direction: %s, timeout: 1500}", tc.target, tc.dir)))
			if !reversed {
				t.Fatal("the skipped rows were never searched")
			}
		})
	}
}

// A read after a seemingly fruitless drag can catch the recycler's delayed
// layout. It must check for a skipped interval before advancing again.
func TestScrollChecksCollectionGapAfterSettling(t *testing.T) {
	f := &fakeDriver{plat: "android"}
	offset, _ := f.scroller(80, false, 10)
	screen := f.screen
	f.screen = func() []*ui.Node {
		nodes := screen()
		nodes[0].Coll = &ui.Coll{Rows: 80, Cols: 1}
		for _, row := range nodes[1:] {
			var index int
			fmt.Sscanf(row.Text, "Item %d", &index)
			row.Item = &ui.Coll{Rows: index}
		}
		return nodes
	}
	dragged, pending := false, false
	f.onSwipe = func(_, y1, _, y2 int) {
		if !dragged {
			dragged, pending = true, true
			offset.Store(11) // same rows, slightly moved
		} else {
			offset.Store(max(0, min(3300, offset.Load()+int64(y1-y2))))
		}
		f.change(true)
	}
	f.onWait = func() {
		if pending {
			pending = false
			offset.Store(1600) // the delayed layout jumps past Item 20
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 20, timeout: 1800}"))
}
