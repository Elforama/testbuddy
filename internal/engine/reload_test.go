package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #284: a screen recreated by a configuration change (a turn, a fold, split screen) or
// restored after its process died can show a frame with no content (an empty list, a
// spinner), then reload its content a moment later: at the top of the list, its state lost,
// or as it was. A wait for absence judged on that frame passed: the composer at the top of
// the feed was not there yet. It must wait for the reloaded content, and judge on that.

// feed is a list scrolled down to "Post 7": the composer at its top is off screen.
func feedRows(from int) []*ui.Node {
	nodes := []*ui.Node{{Class: "androidx.recyclerview.widget.RecyclerView", ID: "feed", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
	top := 0
	if from == 0 {
		nodes = append(nodes, &ui.Node{Text: "What's new?", ID: "composer", Depth: 1, Enabled: true, Bounds: ui.Rect{R: 400, B: 100}})
		top = 100
	}
	for i := from; i < from+6; i++ {
		nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Post %d", i), ID: fmt.Sprintf("post_%d", i), Depth: 1, Enabled: true,
			Bounds: ui.Rect{T: top + 100*(i-from), R: 400, B: top + 100*(i-from) + 100}})
	}
	return nodes
}

// reloadingFeed: the feed above; a turn shows loading (an empty list, a spinner) and, after
// reload (from the turn), the list again, from its top (lost) or from Post 7 (kept). A
// reload of 0 never comes: the screen stays as loading showed it.
func reloadingFeed(f *fakeDriver, loading func() []*ui.Node, reload time.Duration, lost bool) {
	var phase atomic.Int32 // 0 before the turn, 1 loading, 2 reloaded
	f.screen = func() []*ui.Node {
		switch phase.Load() {
		case 1:
			return loading()
		case 2:
			if lost {
				return feedRows(0)
			}
			return feedRows(7)
		}
		return feedRows(7)
	}
	f.onOrient = func(string) {
		f.rotated.Store(true)
		phase.Store(1)
		f.change(true)
		if reload > 0 {
			time.AfterFunc(reload, func() { phase.Store(2); f.change(true) })
		}
	}
}

var reloadFrames = map[string]func() []*ui.Node{
	"empty list": func() []*ui.Node {
		return []*ui.Node{{Class: "androidx.recyclerview.widget.RecyclerView", ID: "feed", Bounds: ui.Rect{R: 800, B: 400}}}
	},
	"spinner": func() []*ui.Node {
		return []*ui.Node{{Class: "android.widget.ProgressBar", ID: "loading", Bounds: ui.Rect{L: 350, T: 150, R: 450, B: 250}}}
	},
	"no content": func() []*ui.Node { return nil },
}

const turnAndCheck = `
- assertNotVisible: {id: composer}
- setOrientation: LANDSCAPE_LEFT
- assertNotVisible: {id: composer}
`

// issue #284: the reload lost the feed's place (the composer is back on screen): the wait for
// the composer's absence must not pass on the loading frame, and fails.
func TestAbsenceAfterRestoreWaitsForTheReload(t *testing.T) {
	for name, loading := range reloadFrames {
		t.Run(name, func(t *testing.T) {
			f := &fakeDriver{}
			reloadingFeed(f, loading, 300*time.Millisecond, true)
			mustFail(t, runWith(t, f, Options{LookupTimeout: 1500 * time.Millisecond}, turnAndCheck),
				"is not visible")
		})
	}
}

// issue #284: the reload kept the feed's place: the wait passes once the content is back,
// not before, and not much after.
func TestAbsenceAfterRestorePassesOnTheKeptState(t *testing.T) {
	for name, loading := range reloadFrames {
		t.Run(name, func(t *testing.T) {
			f := &fakeDriver{}
			reloadingFeed(f, loading, 300*time.Millisecond, false)
			start := time.Now()
			mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second}, turnAndCheck))
			if took := time.Since(start); took < 300*time.Millisecond || took > 1500*time.Millisecond {
				t.Errorf("took %s: the content came back 300 ms after the turn", took)
			}
		})
	}
}

// issue #284: a recreated screen that stays empty (a list with nothing in it) is judged as it
// is once reloadWait has passed: the wait is bounded, and the check passes.
func TestAbsenceAfterRestoreOnAScreenThatStaysEmpty(t *testing.T) {
	defer func(d time.Duration) { reloadWait = d }(reloadWait)
	reloadWait = 400 * time.Millisecond
	f := &fakeDriver{}
	reloadingFeed(f, reloadFrames["empty list"], 0, true)
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second}, turnAndCheck))
	if took := time.Since(start); took < 400*time.Millisecond || took > 1500*time.Millisecond {
		t.Errorf("took %s, want about reloadWait (400 ms)", took)
	}
}

// issue #284: the wait needs evidence of a new screen. A list emptied in place (its last
// item deleted, the screen's context unchanged) is judged at once, as before.
func TestAbsenceOnAListEmptiedInPlaceIsJudgedAtOnce(t *testing.T) {
	var emptied atomic.Bool
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		if emptied.Load() {
			return []*ui.Node{{Class: "androidx.recyclerview.widget.RecyclerView", ID: "feed", Bounds: ui.Rect{R: 400, B: 800}}}
		}
		return feedRows(7)[:2]
	}
	f.onTap = func(x, y int) { emptied.Store(true); f.change(true) }
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second}, `
- assertVisible: "Post 7"
- tapOn: "Post 7"
- assertNotVisible: "Post 7"
`))
	if took := time.Since(start); took > 1200*time.Millisecond {
		t.Errorf("took %s: a list emptied in place must not wait for a reload", took)
	}
}
