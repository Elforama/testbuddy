package engine

import (
	"regexp"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func TestPressCutShort(t *testing.T) {
	cut := &driver.PressCut{SeenMs: 5, PressMs: 129, StallMs: 133}
	note := `the app saw only 5 ms of the 129 ms press, after a 133 ms stall in its down; a control that needs a longer press may have missed it`
	for _, tc := range []struct {
		name  string
		flow  string
		kind  string
		cut   *driver.PressCut
		lines []string
	}{
		{name: "a long press notes it", flow: "- longPressOn: {id: box}", kind: "long", cut: cut,
			lines: []string{`long press at \(\d+, \d+\): ` + note}},
		{name: "a double tap notes it", flow: "- doubleTapOn: {id: box}", kind: "double", cut: cut,
			lines: []string{`double tap at \(\d+, \d+\): ` + note}},
		{name: "a tap notes it", flow: "- tapOn: {id: box}", kind: "tap", cut: cut,
			lines: []string{`tap at \(\d+, \d+\): ` + note}},
		{name: "a long press seen whole", flow: "- longPressOn: {id: box}", kind: "long"},
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
			if err != nil {
				t.Fatalf("err=%v, want the step to pass; trace:\n%s", err, trace)
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
			if tc.cut == nil && strings.Contains(trace, "of the") {
				t.Errorf("a press seen whole was noted:\n%s", trace)
			}
		})
	}
}
