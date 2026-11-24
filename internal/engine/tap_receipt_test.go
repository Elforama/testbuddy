package engine

import (
	"context"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

type receiptDriver struct {
	*fakeDriver
	receipts []driver.ActionResult
	taps     int
}

func (d *receiptDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target == nil && d.plat != "android" {
		return d.fakeDriver.Tap(ctx, x, y, o)
	}
	res := d.receipts[min(d.taps, len(d.receipts)-1)]
	d.taps++
	return res, nil
}

func TestTapTraceSaysHowTheTouchWasDelivered(t *testing.T) {
	clicked := true
	targeted := driver.ActionResult{Guarded: true, Probed: true, Targeted: true, Settle: driver.Changed}
	cancelled := driver.ActionResult{Guarded: true, Probed: true, Cancelled: true, Settle: driver.Changed}
	for _, tc := range []struct {
		name     string
		plat     string
		ci       bool
		receipts []driver.ActionResult
		fail     bool
		lines    []string
	}{
		{name: "targeted", receipts: []driver.ActionResult{targeted},
			lines: []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch targeted\)$`}},
		{name: "targeted and clicked",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Targeted: true, Clicked: &clicked, Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch targeted, clicked\)$`}},
		{name: "lifted, not on the target",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Touch: "ended", Settle: driver.NoChange}},
			lines:    []string{`tap at \(\d+, \d+\): nothing changed \(sent in \d+ ms, waited \d+ ms; touch lifted, not seen on the target\)$`}},
		{name: "no receipt",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Touch: "none", Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; no touch receipt\)$`}},
		{name: "clicked, then cancelled",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Touch: "cancelled", Clicked: &clicked, Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms; touch cancelled, clicked\)$`}},
		{name: "an older agent, not targeted",
			receipts: []driver.ActionResult{{Guarded: true, Probed: true, Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms\)$`}},
		{name: "android", plat: "android",
			receipts: []driver.ActionResult{{Clicked: new(bool), Settle: driver.Changed}},
			lines:    []string{`tap at \(\d+, \d+\): the screen changed \(sent in \d+ ms, waited \d+ ms\)$`}},
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
			plat := tc.plat
			if plat == "" {
				plat = "ios"
			}
			d := &receiptDriver{fakeDriver: &fakeDriver{plat: plat}, receipts: tc.receipts}
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
