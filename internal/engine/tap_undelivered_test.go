package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #334: on iOS CI a tap was reported done and its effect never came. UIKit drops the
// touches an app gets while it presents a screen (a full-screen cover still sliding in): the
// system reported the touch completed, the app never did, and it went on (finished the
// presentation) without it. The screen was still changing from the presentation, so the
// settle said "the screen changed", and nothing sent the lost tap again (issue #280's replay
// needs "nothing changed"). Now a touch the agent reports not delivered (NotDelivered) is sent
// again once the screen settles, whatever the settle said; a touch with no such proof (an app
// that posted nothing: a stalled one may still take it) is not.
func TestTapNotDeliveredIsSentAgain(t *testing.T) {
	lost := driver.ActionResult{Guarded: true, Probed: true, Touch: "none", NotDelivered: true, Settle: driver.Changed}
	stalled := driver.ActionResult{Guarded: true, Probed: true, Touch: "none", Settle: driver.Changed}
	for _, tc := range []struct {
		name    string
		flow    string
		kind    string
		replies []driver.ActionResult // per touch (the last one again once they run out); nil entries: delivered
		taps    int                   // touches sent
		fail    bool
		lines   []string
	}{
		{name: "a tap on a named button", flow: "- tapOn: {id: cover_sheet}\n- assertVisible: {id: sheet_title}",
			kind: "tap", replies: []driver.ActionResult{lost, {}}, taps: 2,
			lines: []string{`the app never got the touch .*: tapping again once the screen settles`}},
		{name: "a double tap", flow: "- doubleTapOn: Open sheet\n- assertVisible: {id: sheet_title}",
			kind: "double", replies: []driver.ActionResult{lost, {}}, taps: 2,
			lines: []string{`the app never got the touch .*: tapping again once the screen settles`}},
		// A point may be on the keyboard or a system window, whose touches the app never gets:
		// not sent again.
		{name: "a tap at a point is not sent again", flow: "- tapOn: {point: \"50%,15%\"}",
			kind: "tap", replies: []driver.ActionResult{lost, {}}, taps: 1},
		// No proof the app ran on without the touch: it may still come (a stalled app), and a
		// second tap would act twice.
		{name: "no proof: not sent again", flow: "- tapOn: {id: cover_sheet}",
			kind: "tap", replies: []driver.ActionResult{stalled}, taps: 1},
		// Lost every time: sent again at most twice, then left to the next step.
		{name: "lost every time", flow: "- tapOn: {id: cover_sheet}",
			kind: "tap", replies: []driver.ActionResult{lost}, taps: 3,
			lines: []string{`the app never got the touch 3 times: leaving it`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var shown atomic.Bool
			d := &fakeDriver{plat: "ios"}
			button := &ui.Node{ID: "cover_sheet", Class: "Button", Text: "Open sheet", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 110, T: 100, R: 290, B: 140}}
			d.screen = func() []*ui.Node {
				nodes := []*ui.Node{button}
				if shown.Load() {
					nodes = append(nodes, &ui.Node{ID: "sheet_title", Class: "StaticText", Text: "Sheet", Bounds: ui.Rect{L: 20, T: 300, R: 200, B: 340}})
				}
				return nodes
			}
			d.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
				r := tc.replies[min(n, len(tc.replies))-1]
				if !r.NotDelivered && r.Touch == "" {
					shown.Store(true) // this touch reached the app, and it acted
					return res
				}
				return r
			}
			cmds, err := flow.ParseCommands(".", []byte(tc.flow))
			if err != nil {
				t.Fatal(err)
			}
			var buf syncBuf
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err = New(d, Options{Trace: NewTrace(&buf)}).RunCommands(ctx, cmds, "app", nil)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v, want failure %t; trace:\n%s", err, tc.fail, buf.String())
			}
			d.mu.Lock()
			taps, kinds := len(d.taps), append([]string(nil), d.tapKind...)
			d.mu.Unlock()
			if taps != tc.taps {
				t.Fatalf("%d touches sent, want %d; trace:\n%s", taps, tc.taps, buf.String())
			}
			for _, k := range kinds {
				if k != tc.kind {
					t.Fatalf("touch kinds %v, want all %q", kinds, tc.kind)
				}
			}
			wantLines(t, buf.String(), tc.lines...)
		})
	}
}
