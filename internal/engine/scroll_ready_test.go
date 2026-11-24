package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

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
			time.AfterFunc(after, func() { drawn.Store(true); f.change(false) })
		}
		return first()
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {}
}

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
