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

// The wait above needs a turn (a new size): screens that are not a restore are judged at once
// (issue #284 review). Each check here took 3 s before: a new window, a dialog, a new page
// counted as a new screen, and a determinate progress bar as loading.

// absenceQuick runs flow on f and fails the test if it does not pass within 300 ms (from the
// last step's start: the steps before it may tap, and a tap waits).
func absenceQuick(t *testing.T, f *fakeDriver, flow string) {
	t.Helper()
	res := runWith(t, f, Options{LookupTimeout: 3 * time.Second}, flow)
	mustPass(t, res)
	if last := res.Steps[len(res.Steps)-1]; last.Duration > 300*time.Millisecond {
		t.Errorf("%s took %s, want under 300 ms", last.Desc, last.Duration)
	}
}

// issue #284 review: a screen with a determinate progress bar (a value: Android's state
// description, iOS's value) and a confirm dialog that comes and goes, layered over it.
func TestAbsenceAfterADialogBesideADeterminateBarIsQuick(t *testing.T) {
	for name, tc := range map[string]struct {
		plat string
		bar  *ui.Node
	}{
		"android view": {"android", &ui.Node{Class: "android.widget.ProgressBar", ID: "upload", State: "40%", Bounds: ui.Rect{T: 100, R: 400, B: 120}}},
		"compose":      {"android", &ui.Node{Class: "android.widget.ProgressBar", State: "40 percent", Bounds: ui.Rect{T: 100, R: 400, B: 120}}},
		"ios":          {"ios", &ui.Node{Class: "ProgressIndicator", Value: "40%", Bounds: ui.Rect{T: 100, R: 400, B: 120}}},
	} {
		t.Run(name, func(t *testing.T) {
			var shown atomic.Bool
			f := &fakeDriver{plat: tc.plat}
			f.screen = static(btn("Delete", "delete", 300), tc.bar)
			f.appAbove = func() *ui.Window {
				if !shown.Load() {
					return nil
				}
				return &ui.Window{ID: 7, Type: "app", Package: "app", Active: true, Bounds: ui.Rect{L: 50, T: 300, R: 350, B: 500},
					Nodes: []*ui.Node{btn("Discard?", "", 320), btn("OK", "ok", 400)}}
			}
			f.onTap = func(x, y int) { shown.Store(!shown.Load()); f.change(true) }
			absenceQuick(t, f, `
- tapOn: "Delete"
- tapOn: "OK"
- assertNotVisible: "Discard?"
`)
		})
	}
}

// issue #284 review: a new Activity (a new window, same size) that opens with a spinner.
func TestAbsenceAfterANewActivityWithASpinnerIsQuick(t *testing.T) {
	var opened atomic.Bool
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		if opened.Load() {
			return []*ui.Node{btn("Details", "title", 0),
				{Class: "android.widget.ProgressBar", ID: "loading", Bounds: ui.Rect{L: 180, T: 380, R: 220, B: 420}}}
		}
		return []*ui.Node{btn("Open", "open", 300)}
	}
	f.onTap = func(x, y int) { opened.Store(true); f.winID.Store(9); f.change(true) }
	absenceQuick(t, f, `
- tapOn: "Open"
- assertNotVisible: "Open"
`)
}

// issue #284 review: a web navigation (a new URL and title) to a page whose list is empty.
func TestAbsenceAfterAWebNavigationIsQuick(t *testing.T) {
	var page atomic.Int32
	f := &fakeDriver{plat: "web"}
	f.pkg = func() string { return fmt.Sprintf("http://127.0.0.1/page%d", page.Load()) }
	f.screen = func() []*ui.Node {
		if page.Load() == 1 {
			return []*ui.Node{btn("Inbox", "title", 0), {Class: "div", ID: "mails", Scrollable: true, Bounds: ui.Rect{T: 60, R: 400, B: 800}}}
		}
		return []*ui.Node{btn("Go to inbox", "go", 300)}
	}
	f.onTap = func(x, y int) { page.Store(1); f.change(true) }
	absenceQuick(t, f, `
- tapOn: "Go to inbox"
- assertNotVisible: "Go to inbox"
`)
}

// issue #284 (bench): after the turn, a read of the loading frame, then one with no app
// window (Android replacing the window, its root not readable): still loading, not judged.
func TestAbsenceAfterRestoreWaitsThroughAReadWithNoAppWindow(t *testing.T) {
	f := &fakeDriver{}
	reloadingFeed(f, reloadFrames["empty list"], 600*time.Millisecond, true)
	turn := f.onOrient
	f.onOrient = func(o string) {
		turn(o)
		time.AfterFunc(200*time.Millisecond, func() { f.noApp.Store(true); f.change(true) })
		time.AfterFunc(400*time.Millisecond, func() { f.noApp.Store(false); f.change(true) })
	}
	mustFail(t, runWith(t, f, Options{LookupTimeout: 1500 * time.Millisecond}, `
- assertNotVisible: {id: composer}
- setOrientation: LANDSCAPE_LEFT
- assertVisible: {id: feed}
- assertNotVisible: {id: composer}
`), "is not visible")
}

// issue #284 (bench): the turn is read before the window is replaced (the old Activity's
// content at the new size), then the new window comes, empty, then the reload: the check
// judges on the reload, not on the old content nor the empty frame.
func TestAbsenceAfterRestoreWaitsForTheReplacedWindow(t *testing.T) {
	var phase atomic.Int32
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		switch phase.Load() {
		case 1:
			return reloadFrames["empty list"]()
		case 2:
			return feedRows(0)
		}
		return feedRows(7)
	}
	var turned atomic.Bool
	f.onOrient = func(string) {
		f.rotated.Store(true)
		f.change(true)
		turned.Store(true)
		time.AfterFunc(450*time.Millisecond, func() { phase.Store(2); f.change(true) })
	}
	// The device is still changing when the check settles: the window is replaced meanwhile.
	var waits atomic.Int32
	f.onWait = func() {
		// The first wait after the turn is setOrientation's own: the window is replaced later.
		if turned.Load() && waits.Add(1) > 1 && phase.Load() == 0 {
			f.winID.Store(5)
			phase.Store(1)
			f.change(true)
		}
	}
	mustFail(t, runWith(t, f, Options{LookupTimeout: 1500 * time.Millisecond}, `
- assertNotVisible: {id: composer}
- setOrientation: LANDSCAPE_LEFT
- assertVisible: "Post 7"
- assertNotVisible: {id: composer}
`), "is not visible")
}

// issue #284 (bench, Android): right after the turn the old window still shows its content at
// the new size, settled, while its replacement is listed with no root yet (Screen.Unread). The
// check must not judge on the old content: it waits for the new window's reloaded content.
func TestAbsenceAfterRestoreWaitsForAWindowWithNoRootYet(t *testing.T) {
	var phase atomic.Int32
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		if phase.Load() == 1 {
			return feedRows(0)
		}
		return feedRows(7)
	}
	f.onOrient = func(string) {
		f.rotated.Store(true)
		f.unread.Store(1)
		f.change(true)
		time.AfterFunc(400*time.Millisecond, func() { f.unread.Store(0); f.winID.Store(5); phase.Store(1); f.change(true) })
	}
	mustFail(t, runWith(t, f, Options{LookupTimeout: 1500 * time.Millisecond}, turnAndCheck), "is not visible")
}

// issue #284 review: on iOS a turn swaps the size too; a spinner (ActivityIndicator, or a
// ProgressIndicator with no value) or an empty table (always scrollable there) is loading.
func TestAbsenceAfterRestoreWaitsForTheReloadOnIOS(t *testing.T) {
	for name, loading := range map[string]func() []*ui.Node{
		"activity indicator": func() []*ui.Node {
			return []*ui.Node{{Class: "ActivityIndicator", Bounds: ui.Rect{L: 380, T: 180, R: 420, B: 220}}}
		},
		"indeterminate progress": func() []*ui.Node {
			return []*ui.Node{{Class: "ProgressIndicator", Bounds: ui.Rect{T: 60, R: 800, B: 64}}}
		},
		"empty table": func() []*ui.Node {
			return []*ui.Node{{Class: "Table", Scrollable: true, Bounds: ui.Rect{R: 800, B: 400}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			reloadingFeed(f, loading, 300*time.Millisecond, true)
			mustFail(t, runWith(t, f, Options{LookupTimeout: 1500 * time.Millisecond}, turnAndCheck), "is not visible")
		})
	}
}
