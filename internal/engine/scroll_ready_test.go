package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// lateRows is a screen right after a launch (issue #291): its first read shows first (no
// node: the app's window not on screen yet; or a list container with no rows yet), and the
// list's rows come after (100 ms after the first read) with a "changed" event and no
// "settled". Row i is 50 px tall at y 100+50i: "Row 3" is near the top, in view.
func (f *fakeDriver) lateRows(first func() []*ui.Node, after time.Duration) {
	var drawn atomic.Bool
	var reads atomic.Int64
	rows := func() []*ui.Node {
		nodes := []*ui.Node{{Class: "RecyclerView", ID: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		for i := range 14 {
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Row %d", i), Depth: 1, Enabled: true, Clickable: true,
				Bounds: ui.Rect{T: 100 + 50*i, R: 400, B: 150 + 50*i}})
		}
		return nodes
	}
	f.screen = func() []*ui.Node {
		if drawn.Load() {
			return rows()
		}
		if reads.Add(1) == 1 {
			// Called under f.mu: change after the read, from another goroutine.
			time.AfterFunc(after, func() { drawn.Store(true); f.change(false) })
		}
		return first()
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {} // a drag on the bare window, or the empty list, moves nothing
}

// issue #291: scrollUntilVisible right after launchApp decided "not visible" on a read of the
// screen before the list had drawn its rows, swiped, and scrolled past a target near the top.
// The first swipe must wait for a read that shows the list's content: here none is needed.
func TestScrollUntilVisibleWaitsForTheListToDraw(t *testing.T) {
	for name, first := range map[string]func() []*ui.Node{
		"empty list container": func() []*ui.Node {
			return []*ui.Node{{Class: "RecyclerView", ID: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		},
		"no window content yet": func() []*ui.Node { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeDriver{}
			f.lateRows(first, 100*time.Millisecond)
			start := time.Now()
			mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: \"Row 3\"}"))
			if len(f.swipeAt) != 0 {
				t.Errorf("swiped %d times before the list drew its rows: %v", len(f.swipeAt), f.swipeAt)
			}
			if took := time.Since(start); took > 1500*time.Millisecond {
				t.Errorf("took %s: the rows came 100 ms after the first read", took)
			}
		})
	}
}

// issue #291, the normal case: a list already drawn, the target below the fold. The first drag
// goes out on the first read, with no wait and no second read before it.
func TestScrollUntilVisibleDrawnListDragsAtOnce(t *testing.T) {
	f := &fakeDriver{}
	f.scroller(40, false, 0)
	var firstDrag time.Duration
	var readsBefore int64
	start := time.Now()
	move := f.onSwipe
	f.onSwipe = func(x1, y1, x2, y2 int) {
		if firstDrag == 0 {
			firstDrag, readsBefore = time.Since(start), f.reads.Load()
		}
		move(x1, y1, x2, y2)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: \"Item 30\"}"))
	if readsBefore != 1 || firstDrag > 100*time.Millisecond {
		t.Errorf("first drag after %d reads and %s; want 1 read, at once", readsBefore, firstDrag)
	}
	if n := f.waits.Load(); n != 0 {
		t.Errorf("%d WaitSettled calls before the target showed", n)
	}
}

// issue #291: a list that stays empty and silent is judged as it is once the device has said
// nothing for the silence net's gap: the scroll goes on (and fails here) within its timeout.
func TestScrollUntilVisibleEmptyListStillScrolls(t *testing.T) {
	defer func(d time.Duration) { silentAfter = d }(silentAfter)
	silentAfter = 150 * time.Millisecond
	f := &fakeDriver{screen: static(&ui.Node{Class: "RecyclerView", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}),
		onSwipe: func(x1, y1, x2, y2 int) {}}
	start := time.Now()
	mustFail(t, runWith(t, f, quick, "- scrollUntilVisible: {element: \"Row 3\", timeout: 1500}"), "No visible element found")
	if len(f.swipeAt) == 0 {
		t.Error("never swiped: an empty list that stays so must still be scrolled")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s for a 1.5 s timeout", took)
	}
}

// issue #291: a drawn list on a screen that never stops changing (a ticker) is re-read once
// for the change after the read, then scrolled: the wait for news before the first swipe is
// bounded (RestlessAfter), not repeated.
func TestScrollUntilVisibleTickerScreenDragsSoon(t *testing.T) {
	f := &fakeDriver{}
	f.scroller(40, false, 0)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(30 * time.Millisecond):
				f.change(false)
			}
		}
	}()
	var firstDrag atomic.Int64
	start := time.Now()
	move := f.onSwipe
	f.onSwipe = func(x1, y1, x2, y2 int) {
		firstDrag.CompareAndSwap(0, int64(time.Since(start)))
		move(x1, y1, x2, y2)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: \"Item 30\"}"))
	if d := time.Duration(firstDrag.Load()); d == 0 || d > 700*time.Millisecond {
		t.Errorf("first drag after %s; want within one restless read", d)
	}
}
