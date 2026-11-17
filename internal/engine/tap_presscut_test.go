package engine

import (
	"regexp"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #337: on iOS CI a double tap's first press reached the app while its main thread was
// stalled: delivering the down took 124 ms, the lift came 5 ms later, and a press-duration
// handler (a 75 ms long press) saw a 5 ms press. The probe says so (ActionResult.PressCut). A
// long press, which needs its whole press, fails naming the stall; a tap or double tap only
// notes it (a plain button acts on a 5 ms press), so an assertion that fails after it points
// to it. Neither is sent again.
func TestPressCutShort(t *testing.T) {
	cut := &driver.PressCut{SeenMs: 5, PressMs: 129, StallMs: 133}
	for _, tc := range []struct {
		name  string
		flow  string
		kind  string
		cut   *driver.PressCut
		fail  string // the failure's text ("" = passes)
		lines []string
	}{
		{name: "a long press fails", flow: "- longPressOn: {id: box}", kind: "long", cut: cut,
			fail: `long press at \(\d+, \d+\): the press was cut short: the app saw 5 ms of a 129 ms press, after a 133 ms stall in its down`},
		{name: "a double tap notes it", flow: "- doubleTapOn: {id: box}", kind: "double", cut: cut,
			lines: []string{`double tap at \(\d+, \d+\): the app saw only 5 ms of a 129 ms press, after a 133 ms stall in its down: a control that acts on how long it is pressed .* may have missed it`}},
		{name: "a tap notes it", flow: "- tapOn: {id: box}", kind: "tap", cut: cut,
			lines: []string{`tap at \(\d+, \d+\): the app saw only 5 ms of a 129 ms press`}},
		// No probe, or a press seen whole: nothing changes.
		{name: "a long press seen whole passes", flow: "- longPressOn: {id: box}", kind: "long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			box := &ui.Node{ID: "box", Class: "Button", Text: "React", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 20, T: 100, R: 380, B: 160}}
			f.screen = func() []*ui.Node { return []*ui.Node{box} }
			f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
				res.PressCut = tc.cut
				return res
			}
			err, trace := runUndelivered(t, f, tc.flow, true)
			if tc.fail == "" && err != nil {
				t.Fatalf("err=%v; trace:\n%s", err, trace)
			}
			if tc.fail != "" && (err == nil || !regexp.MustCompile(tc.fail).MatchString(err.Error())) {
				t.Fatalf("err=%v, want %q; trace:\n%s", err, tc.fail, trace)
			}
			f.mu.Lock()
			taps, kinds := len(f.taps), append([]string(nil), f.tapKind...)
			f.mu.Unlock()
			if taps != 1 || kinds[0] != tc.kind {
				t.Fatalf("touches %v, want one %q (never sent again); trace:\n%s", kinds, tc.kind, trace)
			}
			for _, l := range tc.lines {
				if !regexp.MustCompile(l).MatchString(trace) {
					t.Errorf("trace lacks %q:\n%s", l, trace)
				}
			}
			if tc.cut == nil && strings.Contains(trace, "of a") {
				t.Errorf("a press seen whole was noted:\n%s", trace)
			}
		})
	}
}
