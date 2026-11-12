package engine

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// confirmList is a start-up screen of a list app (issue #204: the Flutter bench app's scenario
// list, just launched): a title, a progress spinner and a logo above the list, and the list's
// rows. step moves the start-up animation on (the spinner turns, the logo slides): the app's own
// redraws, which change nothing under the tap point.
func confirmList(step int, extra ...*ui.Node) []*ui.Node {
	nodes := []*ui.Node{
		{Class: "TextView", ID: "title", Text: "Scenarios", Bounds: ui.Rect{R: 400, B: 50}},
		{Class: "ProgressBar", ID: "spinner", State: []string{"0%", "50%", "100%"}[step%3], Bounds: ui.Rect{L: 350, T: 50, R: 400, B: 100}},
		{Class: "ImageView", ID: "logo", Desc: "logo", Bounds: ui.Rect{L: 10 * step, T: 50, R: 10*step + 100, B: 100}},
		{Class: "ScrollView", ID: "scenarios", Scrollable: true, Bounds: ui.Rect{T: 100, R: 400, B: 800}},
	}
	for i, id := range []string{"scenario_a11y_good", "scenario_lists", "scenario_controls"} {
		top := 100 + i*60
		nodes = append(nodes, &ui.Node{Depth: 1, Class: "Button", ID: id, Text: id, Clickable: true, Enabled: true,
			Bounds: ui.Rect{T: top, R: 400, B: top + 60}})
	}
	return append(nodes, extra...)
}

// confirmOpened is the screen the tap on scenario_a11y_good opens.
func confirmOpened() []*ui.Node {
	return []*ui.Node{
		{Class: "Button", ID: "back", Text: "Back", Clickable: true, Enabled: true, Bounds: ui.Rect{R: 100, B: 50}},
		{Class: "TextView", ID: "scenario_title", Text: "Accessibility", Bounds: ui.Rect{L: 100, R: 400, B: 50}},
	}
}

// windowAgo is what a tap's reply reports of the last window change before its touch.
func windowAgo(d time.Duration) *time.Duration { return &d }

// dropDriver is the start-up list of confirmList on Android, whose first tap's touch is dropped
// (issue #204): no view takes it, and only the app's own redraw (the spinner) changes the screen.
// effect is what the first tap does instead when it is not dropped; later taps reach the row and
// open its screen.
func dropDriver(winAge *time.Duration, effect string) *fakeDriver {
	f := &fakeDriver{plat: "android", winAge: winAge}
	var mu sync.Mutex
	step, taps, opened := 0, 0, false
	var self, result bool
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		if opened {
			return confirmOpened()
		}
		var extra []*ui.Node
		if result {
			extra = append(extra, &ui.Node{Class: "TextView", ID: "status", Text: "Saved", Bounds: ui.Rect{T: 700, R: 400, B: 750}})
		}
		nodes := confirmList(step, extra...)
		if self {
			nodes[4].Text = "scenario_a11y_good (opening)"
		}
		return nodes
	}
	no, yes := false, true
	f.clicked = func(_, _ int) *bool {
		mu.Lock()
		defer mu.Unlock()
		taps++
		if taps == 1 {
			return &no // Android dropped the touch: no view took it
		}
		return &yes
	}
	f.onTap = func(_, _ int) {
		mu.Lock()
		if taps > 1 {
			opened = true // a later tap reaches the row
		} else {
			switch effect {
			case "":
				step++ // the spinner turns
			case "logo":
				step += 3 // the logo slides; the spinner shows the same
			case "open":
				opened = true
			case "self":
				self = true
			case "result":
				result = true
			case "lateClick":
				f.clicks.Add(1)
			}
		}
		mu.Unlock()
		f.change(true) // either way the reply says "the screen changed"
	}
	if effect == "dialog" {
		f.sysAlert = func() []*ui.Node {
			mu.Lock()
			defer mu.Unlock()
			if taps == 0 {
				return nil
			}
			return []*ui.Node{{Class: "Button", ID: "ok", Text: "OK", Clickable: true, Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}}
		}
	}
	return f
}

// Issue #204: Android, a tap right after launchApp (the Flutter bench, "Accessibility: buttons
// and switch with values", on CI). Android dropped the touch (the window, just added, had no input
// channel yet), but the app was still redrawing for its own start-up, so the tap's reply said "the
// screen changed" and tb took that for the tap's effect: it never tapped again, and the next step
// failed on the unchanged list. In CI mode, a tap whose touch came just after a window changed,
// that no view reported a click for, and whose only change on screen is the app's own redraw away
// from the tap point (the spinner, the start-up animation), is sent once more. A tap whose effect
// shows (its screen opens, its own state, a result elsewhere, a dialog) is not; nor one whose
// click the view reports after the reply.
func TestTapUnconfirmedByAnUnrelatedRedrawIsRetried(t *testing.T) {
	t.Setenv("TB_CI", "1")
	for _, tc := range []struct {
		name     string
		effect   string // what the first tap does: "" nothing (dropped), or the effect it shows
		wantTaps int
	}{
		{"dropped, the spinner turns", "", 2},
		{"dropped, the start-up animation moves", "logo", 2},
		{"opens its screen", "open", 1},
		{"its own state changes", "self", 1},
		{"a result shows elsewhere on the screen", "result", 1},
		{"a dialog opens", "dialog", 1},
		{"the view reports its click after the reply", "lateClick", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := dropDriver(windowAgo(150*time.Millisecond), tc.effect)
			mustPass(t, runWith(t, f, quick, "- tapOn: {id: scenario_a11y_good}"))
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.taps) != tc.wantTaps {
				t.Fatalf("taps %v, want %d", f.taps, tc.wantTaps)
			}
		})
	}
}

// Issue #204 (review): no click and a change elsewhere are no evidence that a touch was dropped:
// Compose, React Native and bare gesture detectors never report a click. The re-tap needs a
// window that changed just before the touch, a reply that claimed a change, and CI mode (a CI
// allowance, as for issues #103 and #131).
func TestTapRetryNeedsEvidenceOfADroppedTouch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ci       string
		winAge   *time.Duration
		noChange bool
		wantTaps int
	}{
		{"CI, a window just added", "1", windowAgo(150 * time.Millisecond), false, 2},
		{"not CI, a window just added", "0", windowAgo(150 * time.Millisecond), false, 1},
		{"CI, no window change seen", "1", nil, false, 1},
		{"CI, the last window change long before", "1", windowAgo(5 * time.Second), false, 1},
		{"CI, the reply says nothing changed", "1", windowAgo(150 * time.Millisecond), true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", tc.ci)
			f := dropDriver(tc.winAge, "")
			if tc.noChange {
				f.tapSettle = driver.NoChange
			}
			mustPass(t, runWith(t, f, quick, "- tapOn: {id: scenario_a11y_good}"))
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.taps) != tc.wantTaps {
				t.Fatalf("taps %v, want %d", f.taps, tc.wantTaps)
			}
		})
	}
}

// Issue #204 (review): an app that never reports a click (Compose, React Native, a bare gesture
// detector), with a redraw of its own on screen as it is tapped, and the tap's effect coming
// late: a toggle that flips after a slow update, a counter, a submit that shows its result after
// a 1 s network call. Each is tapped once, in CI mode too: nothing says the touch was dropped.
// With a window that changed just before the touch, an effect within the grace still stops a
// re-tap.
func TestTapOnAnAppWithoutClicksWithALateEffectIsNotRetried(t *testing.T) {
	t.Setenv("TB_CI", "1")
	for _, tc := range []struct {
		name   string
		winAge *time.Duration
		delay  time.Duration
	}{
		{"slow toggle", windowAgo(10 * time.Second), 1500 * time.Millisecond},
		{"counter", windowAgo(10 * time.Second), 1500 * time.Millisecond},
		{"submit with a 1 s network call", nil, time.Second},
		{"submit, a window just changed, a 600 ms call", windowAgo(150 * time.Millisecond), 600 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "android", winAge: tc.winAge}
			var step, effects atomic.Int64
			f.screen = func() []*ui.Node {
				nodes := confirmList(int(step.Load()))
				switch n := effects.Load(); {
				case tc.name == "slow toggle":
					nodes[4].Checkable, nodes[4].Checked = true, n%2 == 1
				case tc.name == "counter":
					nodes = append(nodes, &ui.Node{Class: "TextView", ID: "count", Text: fmt.Sprintf("Count: %d", n), Bounds: ui.Rect{T: 700, R: 400, B: 750}})
				case n > 0:
					nodes = append(nodes, &ui.Node{Class: "TextView", ID: "orders", Text: fmt.Sprintf("Orders placed: %d", n), Bounds: ui.Rect{T: 700, R: 400, B: 750}})
				}
				return nodes
			}
			no := false
			f.clicked = func(_, _ int) *bool { return &no } // never a click event
			f.onTap = func(_, _ int) {
				step.Add(1) // the app's own redraw
				f.change(true)
				go func() {
					time.Sleep(tc.delay)
					effects.Add(1)
					f.change(true)
				}()
			}
			mustPass(t, runWith(t, f, quick, "- tapOn: {id: scenario_a11y_good}"))
			time.Sleep(tc.delay + 100*time.Millisecond) // any second tap's effect lands too
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.taps) != 1 || effects.Load() != 1 {
				t.Fatalf("taps %v, effects %d: want one tap acting once", f.taps, effects.Load())
			}
		})
	}
}

// Issue #204: a tap that took effect late is not tapped again (issue #141's case: an app behind
// on a slow runner), even with a window that changed just before the touch. The reply says "the
// screen changed" (the app's own redraw), no view reports a click, and the tap's screen opens
// only some time after the reply: while the target settles, or within the grace after. And a
// step taps at most twice, even when the second tap shows no effect either.
func TestTapThatTookEffectLateIsNotRetried(t *testing.T) {
	t.Setenv("TB_CI", "1")
	for _, tc := range []struct {
		name     string
		vanish   bool          // the row goes in place first (issue #141), then its screen opens
		opensIn  time.Duration // when the tap's screen opens (0: never)
		wantTaps int
	}{
		{"the row goes in place, then its screen opens", true, 600 * time.Millisecond, 1},
		{"its screen opens while it settles", false, 100 * time.Millisecond, 1},
		{"its screen opens within the grace", false, 800 * time.Millisecond, 1},
		{"dropped twice: retried once only", false, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "android", winAge: windowAgo(150 * time.Millisecond)}
			var tappedAt atomic.Int64
			var step atomic.Int64
			f.screen = func() []*ui.Node {
				at := tappedAt.Load()
				switch {
				case at != 0 && tc.opensIn > 0 && time.Since(time.Unix(0, at)) >= tc.opensIn:
					return confirmOpened()
				case at != 0 && tc.vanish:
					nodes := confirmList(int(step.Load()))
					return append(nodes[:4], nodes[5:]...) // the row is no longer listed, nothing else moved
				}
				return confirmList(int(step.Load()))
			}
			no := false
			f.clicked = func(_, _ int) *bool { return &no } // a Compose or Flutter view: no click event
			f.onTap = func(_, _ int) {
				tappedAt.CompareAndSwap(0, time.Now().UnixNano())
				step.Add(1) // the app's own redraw
				f.change(true)
				if tc.opensIn > 0 {
					go func() {
						time.Sleep(tc.opensIn)
						f.change(true)
					}()
				}
			}
			o := Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}
			mustPass(t, runWith(t, f, o, "- tapOn: {id: scenario_a11y_good}"))
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.taps) != tc.wantTaps {
				t.Fatalf("taps %v, want %d", f.taps, tc.wantTaps)
			}
		})
	}
}

// Issue #204 (review): a step taps at most twice in all. retryTapIfNoChange already tapped again
// (the first reply said nothing changed); the second tap, suspect of a dropped touch, is not
// followed by a third.
func TestTapNeverTapsThreeTimes(t *testing.T) {
	t.Setenv("TB_CI", "1")
	f := &fakeDriver{plat: "android", winAge: windowAgo(150 * time.Millisecond), tapSettle: driver.NoChange}
	var step atomic.Int64
	f.screen = func() []*ui.Node { return confirmList(int(step.Load())) }
	no := false
	f.clicked = func(_, _ int) *bool { return &no }
	f.onTap = func(_, _ int) {
		f.mu.Lock()
		f.tapSettle = driver.Changed // from the second tap on, the app's own redraw shows
		f.mu.Unlock()
		step.Add(1)
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: scenario_a11y_good, retryTapIfNoChange: true}"))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.taps) != 2 {
		t.Fatalf("taps %v, want 2", f.taps)
	}
}
