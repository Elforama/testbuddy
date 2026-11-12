package engine

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// startList is a list app's screen just after launch (issue #204: the Flutter bench app's
// scenario list): a title, a spinner and the rows. step turns the spinner: the app's own redraw,
// away from the tap point.
func startList(step int, extra ...*ui.Node) []*ui.Node {
	nodes := []*ui.Node{
		{Class: "TextView", ID: "title", Text: "Scenarios", Bounds: ui.Rect{R: 400, B: 50}},
		{Class: "ProgressBar", ID: "spinner", State: []string{"0%", "50%", "100%"}[step%3], Bounds: ui.Rect{L: 350, T: 50, R: 400, B: 100}},
		{Class: "ScrollView", ID: "scenarios", Scrollable: true, Bounds: ui.Rect{T: 100, R: 400, B: 800}},
	}
	for i, id := range []string{"scenario_a11y_good", "scenario_lists"} {
		top := 100 + i*60
		nodes = append(nodes, &ui.Node{Depth: 1, Class: "Button", ID: id, Text: id, Clickable: true, Enabled: true,
			Bounds: ui.Rect{T: top, R: 400, B: top + 60}})
	}
	return append(nodes, extra...)
}

// Issue #204 (second review): an app that never reports a click (Compose, React Native, a bare
// gesture detector), with a redraw of its own on screen as it is tapped and a window that just
// changed, and the tap's effect coming late: a toggle that flips after a slow update, a counter,
// a submit whose result shows after a network call. However late the effect (0.5 to 3 s), the
// tap is sent once, in CI mode or not: nothing tells tb a tap was lost but the platform's own
// report that its touch was not injected (TestTapWhoseDownWasRefusedIsSentAgainOnce).
func TestTapWithALateEffectIsNeverSentTwice(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		for _, probe := range []string{"slow toggle", "counter", "submit"} {
			for _, delay := range []time.Duration{500 * time.Millisecond, 1200 * time.Millisecond, 2 * time.Second, 3 * time.Second} {
				t.Run(fmt.Sprintf("TB_CI=%s/%s/%s", mode, probe, delay), func(t *testing.T) {
					t.Setenv("TB_CI", mode)
					f := &fakeDriver{plat: "android"}
					var step, effects atomic.Int64
					f.screen = func() []*ui.Node {
						nodes := startList(int(step.Load()))
						switch n := effects.Load(); {
						case probe == "slow toggle":
							nodes[3].Checkable, nodes[3].Checked = true, n%2 == 1
						case probe == "counter":
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
							time.Sleep(delay)
							effects.Add(1)
							f.change(true)
						}()
					}
					mustPass(t, runWith(t, f, quick, "- tapOn: {id: scenario_a11y_good}"))
					f.mu.Lock()
					defer f.mu.Unlock()
					if len(f.taps) != 1 {
						t.Fatalf("taps %v: want one (a second would act twice)", f.taps)
					}
				})
			}
		}
	}
}

// Issue #204: Android dropped the touch of a tap made just after launch (the window, just
// added, had no input channel yet) while the app redrew for its own start-up, so the reply said
// "the screen changed" and the tap passed with nothing done. The agent now holds a touch until
// its window can take input, and reports a touch the platform did not inject. A refused DOWN
// (nothing else sent: no app saw a tap) is sent again, once, in CI mode or not; refused twice, the
// step fails. A release (UP) not delivered is not sent again: its DOWN may have reached the app
// (the agent cancels the gesture), so the step fails, saying the tap may or may not have taken
// effect. A tap injected is never sent again on this account.
func TestTapWhoseDownWasRefusedIsSentAgainOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		missed   []string // per tap: what the platform reports not injected ("" injected)
		wantTaps int
		fail     string // what the step's failure says ("": it passes)
	}{
		{"injected", []string{""}, 1, ""},
		{"its DOWN refused, then injected", []string{"down", ""}, 2, ""},
		{"its DOWN refused twice", []string{"down", "down", ""}, 2, "refused the touch twice"},
		{"its release not delivered", []string{"up", ""}, 1, "the touch's release was not delivered; the tap may or may not have taken effect"},
		{"its DOWN refused, then its release not delivered", []string{"down", "up", ""}, 2, "the touch's release was not delivered"},
	} {
		for _, mode := range []string{"1", "0"} {
			t.Run(tc.name+"/TB_CI="+mode, func(t *testing.T) {
				t.Setenv("TB_CI", mode)
				f := &fakeDriver{plat: "android"}
				var step atomic.Int64
				var opened atomic.Bool
				f.screen = func() []*ui.Node {
					if opened.Load() {
						return []*ui.Node{{Class: "TextView", ID: "scenario_title", Text: "Accessibility", Bounds: ui.Rect{R: 400, B: 50}}}
					}
					return startList(int(step.Load()))
				}
				f.notInjected = func(n int) string {
					if n <= len(tc.missed) {
						return tc.missed[n-1]
					}
					return ""
				}
				yes := true
				f.clicked = func(_, _ int) *bool { return &yes }
				f.onTap = func(_, _ int) {
					f.mu.Lock()
					landed := f.notInjected(len(f.taps)) == ""
					f.mu.Unlock()
					step.Add(1) // the start-up redraw goes on either way
					if landed {
						opened.Store(true)
					}
					f.change(true)
				}
				res := runWith(t, f, quick, "- tapOn: {id: scenario_a11y_good}")
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.taps) != tc.wantTaps {
					t.Fatalf("taps %v, want %d", f.taps, tc.wantTaps)
				}
				if tc.fail != "" {
					if res.Err == nil || !strings.Contains(res.Err.Error(), tc.fail) {
						t.Fatalf("err = %v: want the step to fail saying %q", res.Err, tc.fail)
					}
					return
				}
				mustPass(t, res)
			})
		}
	}
}
