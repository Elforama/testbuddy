package engine

import (
	"context"
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

// Issue #204: Android, a tap right after launchApp (the Flutter bench, "Accessibility: buttons
// and switch with values", on CI). Android dropped the touch (the window had no input channel
// yet), but the app was still redrawing for its own start-up, so the tap's reply said "the screen
// changed" and tb took that for the tap's effect: it never tapped again, and the next step failed
// on the unchanged list. No view reported a click, and nothing in the app's tree changed but the
// spinner and the start-up animation away from the tap point: the tap is unconfirmed and is sent
// once more. A tap whose effect shows (its screen opens, its own state, a result elsewhere on the
// screen, a dialog) is not; nor one whose click the view reports after the reply.
func TestTapUnconfirmedByAnUnrelatedRedrawIsRetried(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		effect   string // what the first tap does: "" nothing (dropped), or the effect it shows
		wantTaps int
	}{
		{"dropped, the spinner turns", "", 2},
		{"dropped, the start-up animation moves", "logo", 2},
		{"dropped, nothing in the tree changes", "none", 2},
		{"opens its screen", "open", 1},
		{"its own state changes", "self", 1},
		{"a result shows elsewhere on the screen", "result", 1},
		{"a dialog opens", "dialog", 1},
		{"the view reports its click after the reply", "lateClick", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: "android"}
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
				first := taps == 1
				if !first {
					opened = true // the second tap reaches the row
				} else {
					switch tc.effect {
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
			if tc.effect == "dialog" {
				f.sysAlert = func() []*ui.Node {
					mu.Lock()
					defer mu.Unlock()
					if taps == 0 {
						return nil
					}
					return []*ui.Node{{Class: "Button", ID: "ok", Text: "OK", Clickable: true, Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}}
				}
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

// Issue #204: a tap that took effect late is not tapped again (issue #141's case: an app behind
// on a slow runner). The reply says "the screen changed" (the app's own redraw), no view reports
// a click, and the tap's screen opens only some time after the reply: while the target settles,
// or just after. And a tap that was retried once is never retried twice, even when the second
// tap shows no effect either.
func TestTapThatTookEffectLateIsNotRetried(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		vanish   bool          // the row goes in place first (issue #141), then its screen opens
		opensIn  time.Duration // when the tap's screen opens (0: never)
		wantTaps int
	}{
		{"the row goes in place, then its screen opens", true, 600 * time.Millisecond, 1},
		{"its screen opens while it settles", false, 100 * time.Millisecond, 1},
		{"its screen opens just after it settled", false, 250 * time.Millisecond, 1},
		{"dropped twice: retried once only", false, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: "android"}
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

// receiptDriver: iOS with the in-app probe. A guarded tap's reply carries the probe's receipt:
// Targeted when the touch completed on the target's live frame. targeted says, per tap, whether
// it did; onTap is what each tap does to the screen.
type receiptDriver struct {
	*fakeDriver
	targeted func(n int) bool
	clicked  func(n int) *bool
	tapped   int
	effect   func(n int)
}

func (d *receiptDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	d.mu.Lock()
	d.taps = append(d.taps, [2]int{x, y})
	d.tapOpts = append(d.tapOpts, o)
	d.tapped++
	n := d.tapped
	d.mu.Unlock()
	if d.effect != nil {
		d.effect(n)
	}
	d.change(true)
	res := driver.ActionResult{Guarded: o.Target != nil, Probed: true, Settle: driver.Changed}
	if o.Target != nil {
		res.Targeted = d.targeted(n)
		if d.clicked != nil {
			res.Clicked = d.clicked(n)
		}
	}
	return res, nil
}

// Issue #204 on iOS: the probe's receipts say whether a guarded tap's touch completed on its
// target. A tap whose touch the probe never saw complete there (no receipt, no control action),
// on a screen whose only change is the app's own redraw away from the tap point, is unconfirmed
// and sent once more. A tap whose receipt says it was targeted, or whose control reported its
// action, or whose screen opened, is not.
func TestIOSTapUnconfirmedByItsReceiptIsRetried(t *testing.T) {
	t.Parallel()
	yes := true
	for _, tc := range []struct {
		name     string
		targeted bool
		clicked  bool
		opens    bool
		wantTaps int
	}{
		{"no receipt, the spinner turns", false, false, false, 2},
		{"targeted", true, false, false, 1},
		{"its control reported its action", false, true, false, 1},
		{"no receipt, but its screen opened", false, false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &receiptDriver{fakeDriver: &fakeDriver{plat: "ios"}}
			var step atomic.Int64
			var opened atomic.Bool
			d.screen = func() []*ui.Node {
				if opened.Load() {
					return confirmOpened()
				}
				nodes := confirmList(int(step.Load()))
				nodes[1].Class, nodes[1].State, nodes[1].Value = "ActivityIndicator", "", []string{"0%", "50%", "100%"}[step.Load()%3]
				return nodes
			}
			d.targeted = func(n int) bool { return n > 1 || tc.targeted }
			d.clicked = func(n int) *bool {
				if n == 1 && tc.clicked {
					return &yes
				}
				return nil
			}
			d.effect = func(n int) {
				if n > 1 || tc.opens {
					opened.Store(true)
					return
				}
				step.Add(1)
			}
			mustPass(t, runWith(t, d, quick, "- tapOn: {id: scenario_a11y_good}"))
			d.mu.Lock()
			defer d.mu.Unlock()
			if len(d.taps) != tc.wantTaps {
				t.Fatalf("taps %v, want %d", d.taps, tc.wantTaps)
			}
			if len(d.taps) == 2 && (d.tapOpts[1].Target == nil || d.tapOpts[1].Target.ID != "scenario_a11y_good") {
				t.Errorf("the second tap is not guarded on its target: %+v", d.tapOpts[1].Target)
			}
		})
	}
}
