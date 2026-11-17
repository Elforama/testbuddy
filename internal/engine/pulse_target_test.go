package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// pulseInset is a never-ending pulse, one step per read: the inset goes 0..8..0 and around
// again (a period of 16 reads, about 0.7 s at restGap), so no two reads in a row agree.
func pulseInset(read int64) int {
	p := int(read % 16)
	if p > 8 {
		p = 16 - p
	}
	return p
}

// pulsing is a button that grows and shrinks around a fixed middle (200, 125) forever.
func pulsing(reads *atomic.Int64) func() []*ui.Node {
	return func() []*ui.Node {
		d := pulseInset(reads.Add(1) + 5) // starts mid-swing
		return []*ui.Node{{ID: "pulse", Class: "Button", Text: "Pulse", Enabled: true, Clickable: true,
			Bounds: ui.Rect{L: 100 + d, T: 100 + d/2, R: 300 - d, B: 150 - d/2}}}
	}
}

// Issue #357: a target that pulses in place without end (the bench's "Pulse" button) never
// gives two reads that agree, so the tap waited out restWait (2 s) before acting. Its bounds
// swing back and forth around a centre that stays put: it is tapped on that centre within about
// one pulse period.
func TestTapOnANeverEndingPulseActsWithinAPeriod(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = pulsing(&reads)
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- tapOn: Pulse"))
	if d := time.Since(start); d > 1200*time.Millisecond {
		t.Errorf("took %s: want the pulse tapped within about one period (0.7 s), not after restWait", d)
	}
	if len(f.taps) != 1 || f.taps[0][0] < 198 || f.taps[0][0] > 202 || f.taps[0][1] < 123 || f.taps[0][1] > 127 {
		t.Errorf("taps %v, want one on the pulse's centre (200, 125)", f.taps)
	}
}

// Issue #357, iOS outside CI: a pulse is tapped as a target that may move (only the point must
// be on it), so the probe does not withhold the touch because its frame changed since the read
// (issue #103), which would fail the step.
func TestIOSTapOnANeverEndingPulseIsNotWithheld(t *testing.T) {
	t.Setenv("TB_CI", "0")
	d := &tickerDriver{fakeDriver: &fakeDriver{plat: "ios"}}
	var reads atomic.Int64
	d.screen = pulsing(&reads)
	cmds, err := flow.ParseCommands(".", []byte("- tapOn: {id: pulse}"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := New(d, Options{}).RunCommands(context.Background(), cmds, "app", nil); err != nil {
		t.Fatal(err)
	}
	if len(d.moving) != 1 || !d.moving[0] {
		t.Fatalf("taps sent as moving: %v, want [true]", d.moving)
	}
	if took := time.Since(start); took > 1200*time.Millisecond {
		t.Errorf("took %s: want the pulse tapped within about one period", took)
	}
}

// Issue #357: a target that slides in (its centre moves one way) is still waited for, and
// tapped where it comes to rest.
func TestTapStillWaitsForASlidingTarget(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		l := 400 // at rest from the 25th read
		if n := reads.Add(1); n < 25 {
			l = 100 + int(n)*12
		}
		return []*ui.Node{{Text: "Slide", Bounds: ui.Rect{L: l, T: 100, R: l + 100, B: 150}, Enabled: true, Clickable: true}}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Slide"))
	if len(f.taps) != 1 || f.taps[0][0] != 450 {
		t.Errorf("taps %v, want one on the target at rest (x 450)", f.taps)
	}
}

// Issue #357: a target that pulses while it drifts (its centre moves 2 px a read) is not taken
// for a pulse in place: it is waited for, and tapped where it comes to rest.
func TestTapWaitsForATargetThatPulsesWhileItDrifts(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		x, d := 160, 0 // at rest from the 31st read
		if n <= 30 {
			x, d = 100+int(n)*2, pulseInset(n)
		}
		return []*ui.Node{{Text: "Drift", Bounds: ui.Rect{L: x - 50 + d, T: 100 + d/2, R: x + 50 - d, B: 150 - d/2}, Enabled: true, Clickable: true}}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Drift"))
	if len(f.taps) != 1 || f.taps[0][0] != 160 {
		t.Errorf("taps %v, want one on the target at rest (x 160)", f.taps)
	}
}
