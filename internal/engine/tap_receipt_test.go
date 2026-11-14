package engine

import (
	"context"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// receiptDriver answers each guarded tap on its button with the next of its receipts (the
// last one again once they run out), as the iOS agent reports a tap's delivery.
type receiptDriver struct {
	*fakeDriver
	receipts []driver.ActionResult
	taps     int
}

func (d *receiptDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target == nil {
		return d.fakeDriver.Tap(ctx, x, y, o)
	}
	res := d.receipts[min(d.taps, len(d.receipts)-1)]
	d.taps++
	return res, nil
}

// Issue #134: a tap's trace line said only what the screen did ("the screen changed (sent in
// 208 ms, waited 0 ms)"), not the iOS agent's delivery receipt. A touch that reached the app
// and ran no action (#131) could not be told from one that did without the agent's log. Each
// touch's line now says whether it was targeted (it went down on the target and lifted),
// clicked (the target reported its action), cancelled, or withheld.
func TestTapTraceSaysHowTheTouchWasDelivered(t *testing.T) {
	clicked := true
	targeted := driver.ActionResult{Guarded: true, Probed: true, Targeted: true, Settle: driver.Changed}
	cancelled := driver.ActionResult{Guarded: true, Probed: true, Cancelled: true, Settle: driver.Changed}
	for _, tc := range []struct {
		name     string
		ci       bool
		receipts []driver.ActionResult
		fail     bool
		lines    []string // in order
	}{
		{name: "targeted", receipts: []driver.ActionResult{targeted},
			lines: []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch targeted\)$`}},
		{name: "targeted and clicked",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Targeted: true, Clicked: &clicked, Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch targeted, clicked\)$`}},
		// The agent's touch field: the touch lifted where the probe did not find the target, or
		// the probe sent no receipt in time.
		{name: "lifted, not on the target",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Touch: "ended", Settle: driver.NoChange}},
			lines:    []string{`tap at \(\d+, \d+\): nothing changed \(sent in \d+ ms, waited \d+ ms; touch lifted, not seen on the target\)$`}},
		{name: "no receipt",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Touch: "none", Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; no touch receipt\)$`}},
		// A control that reported its action on a touch that then ended cancelled did act.
		{name: "clicked, then cancelled",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Touch: "cancelled", Clicked: &clicked, Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch cancelled, clicked\)$`}},
		{name: "an app without the probe",
			receipts: []driver.ActionResult{{Guarded: true, Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; no probe\)$`}},
		{name: "cancelled, then targeted, in CI", ci: true, receipts: []driver.ActionResult{cancelled, targeted},
			lines: []string{
				`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch cancelled\)$`,
				`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch targeted\)$`,
			}},
		{name: "cancelled, not in CI", receipts: []driver.ActionResult{cancelled}, fail: true,
			lines: []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch cancelled\)$`}},
		{name: "withheld, then targeted",
			receipts: []driver.ActionResult{{Guarded: true, Withheld: "preflight", Settle: driver.NoChange}, targeted},
			lines: []string{
				`tap at \(\d+, \d+\): nothing changed \(sent in \d+ ms, waited \d+ ms; touch withheld at preflight\)$`,
				`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch targeted\)$`,
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", map[bool]string{true: "1", false: "0"}[tc.ci])
			d := &receiptDriver{fakeDriver: &fakeDriver{plat: "ios"}, receipts: tc.receipts}
			d.screen = static(&ui.Node{ID: "save", Class: "Button", Text: "Save", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 20, T: 100, R: 120, B: 140}})
			cmds, err := flow.ParseCommands(".", []byte("- tapOn: {id: save}"))
			if err != nil {
				t.Fatal(err)
			}
			var buf syncBuf
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = New(d, Options{Trace: NewTrace(&buf)}).RunCommands(ctx, cmds, "app", nil)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v, want failure %t; trace:\n%s", err, tc.fail, buf.String())
			}
			wantLines(t, buf.String(), tc.lines...)
		})
	}
}
