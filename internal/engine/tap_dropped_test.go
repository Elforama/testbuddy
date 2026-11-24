package engine

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

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
					f.clicked = func(_, _ int) *bool { return &no }
					f.onTap = func(_, _ int) {
						step.Add(1)
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

func TestTapWhoseDownWasRefusedIsSentAgainOnce(t *testing.T) {
	const refused = "the platform refused the touch: no app saw it"
	for _, tc := range []struct {
		name          string
		missed        []string
		wantTaps      int
		fail          string
		wantTapsNotCI int
		failNotCI     string
	}{
		{"injected", []string{""}, 1, "", 1, ""},
		{"its DOWN refused, then injected", []string{"down", ""}, 2, "", 1, refused},
		{"its DOWN refused twice", []string{"down", "down", ""}, 2, "refused the touch twice", 1, refused},
		{"its release not delivered", []string{"up", ""}, 1, "the touch's release was not delivered; the tap may or may not have taken effect",
			1, "the touch's release was not delivered; the tap may or may not have taken effect"},
		{"its DOWN refused, then its release not delivered", []string{"down", "up", ""}, 2, "the touch's release was not delivered", 1, refused},
	} {
		for _, mode := range []string{"1", "0"} {
			t.Run(tc.name+"/TB_CI="+mode, func(t *testing.T) {
				t.Setenv("TB_CI", mode)
				if mode == "0" {
					tc.wantTaps, tc.fail = tc.wantTapsNotCI, tc.failNotCI
				}
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
					step.Add(1)
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
