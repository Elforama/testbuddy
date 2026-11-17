package engine

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// These tests count reads, not wall time: a slow runner reads less often, and the rule they
// test (pulseWatch) goes by reads. The one wall-time bound is restWait, which only lets a
// slow runner act later, never sooner.

// pulseInset is a never-ending pulse, one step per read: the inset goes 0..8..0 and around
// again (a period of 16 reads), so no two reads in a row agree.
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

// tapWatch records how many reads had been made, and how long the run had taken, when the
// first tap went out.
type tapWatch struct {
	start time.Time
	reads *atomic.Int64
	read  int64
	after time.Duration
}

func (w *tapWatch) onTap(x, y int) {
	if w.read == 0 {
		w.read, w.after = w.reads.Load(), time.Since(w.start)
	}
}

// actedEarly: the tap went out before read rest (when the target came to rest) and before
// restWait ran out, so something took the moving target for a pulse.
func (w *tapWatch) actedEarly(rest int64) bool {
	return w.read < rest && w.after < restWait
}

// Issue #357: a target that pulses in place without end (the bench's "Pulse" button) never
// gives two reads that agree, so the tap waited out restWait (2 s, about 45 reads) before
// acting. Its size swings back and forth around a centre that stays put: it is tapped on that
// centre within about one pulse period (16 reads).
func TestTapOnANeverEndingPulseActsWithinAPeriod(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = pulsing(&reads)
	w := &tapWatch{start: time.Now(), reads: &reads}
	f.onTap = w.onTap
	mustPass(t, runWith(t, f, quick, "- tapOn: Pulse"))
	if w.read == 0 || w.read > 26 {
		t.Errorf("tapped after %d reads: want within about one period (16 reads) of finding it", w.read)
	}
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 125} {
		t.Errorf("taps %v, want one on the pulse's centre (200, 125)", f.taps)
	}
}

// guardDriver: the iOS probe's guard (TBTouchGuard.h, tbWithholdReason, issue #103): a touch
// is withheld as "moved" when the target's centre at the touch is more than 0.5 pt from the
// centre the agent read, unless the host sent it as a target that may move.
type guardDriver struct {
	*fakeDriver
	moving   []bool
	withheld int
}

func (d *guardDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target == nil {
		return d.fakeDriver.Tap(ctx, x, y, o)
	}
	d.moving = append(d.moving, o.Target.Moving)
	live := d.screen()[0].Bounds // the frame as the touch goes down
	read := o.Target.Bounds
	if !o.Target.Moving && (abs((live.L+live.R)-(read.L+read.R)) > 1 || abs((live.T+live.B)-(read.T+read.B)) > 1) {
		d.withheld++
		return driver.ActionResult{Withheld: "moved", Guarded: true, Probed: true}, nil
	}
	d.taps = append(d.taps, [2]int{x, y})
	return driver.ActionResult{Guarded: true, Probed: true, Targeted: true, Settle: driver.Changed}, nil
}

// Issue #357, iOS outside CI: a pulse's centre stays put, so the probe's own guard lets the
// touch through; the tap is not sent as a target that may move (that would give up the
// guard's "moved" withhold for the whole touch).
func TestIOSTapOnANeverEndingPulsePassesTheGuard(t *testing.T) {
	t.Setenv("TB_CI", "0")
	d := &guardDriver{fakeDriver: &fakeDriver{plat: "ios"}}
	var reads atomic.Int64
	d.screen = pulsing(&reads)
	cmds, err := flow.ParseCommands(".", []byte("- tapOn: {id: pulse}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(d, Options{}).RunCommands(context.Background(), cmds, "app", nil); err != nil {
		t.Fatal(err)
	}
	if len(d.moving) != 1 || d.moving[0] || d.withheld != 0 {
		t.Fatalf("taps sent as moving: %v, withheld %d: want one tap, not sent as moving, not withheld", d.moving, d.withheld)
	}
	if n := reads.Load(); n > 28 {
		t.Errorf("tapped after %d reads: want within about one period (16 reads) of finding it", n)
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
	w := &tapWatch{start: time.Now(), reads: &reads}
	f.onTap = w.onTap
	mustPass(t, runWith(t, f, quick, "- tapOn: Slide"))
	if w.actedEarly(25) {
		t.Errorf("tapped at read %d, after %s: before it came to rest (read 25)", w.read, w.after)
	}
}

// Issue #357: a target that pulses while it drifts (its centre moves 2 px a read) is not taken
// for a pulse in place: it is waited for.
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
	w := &tapWatch{start: time.Now(), reads: &reads}
	f.onTap = w.onTap
	mustPass(t, runWith(t, f, quick, "- tapOn: Drift"))
	if w.actedEarly(31) {
		t.Errorf("tapped at read %d, after %s: before it came to rest (read 31)", w.read, w.after)
	}
}

// scaling is a card around (200.25, 125.25) at scale s of its full 201 x 51, each edge
// rounded on its own, as a platform rounds a frame: its doubled centre reads 400 or 401 as it
// grows, though it never moves.
func scaling(s float64) ui.Rect {
	hw, hh := 100.5*s, 25.5*s
	return ui.Rect{L: int(math.Round(200.25 - hw)), T: int(math.Round(125.25 - hh)), R: int(math.Round(200.25 + hw)), B: int(math.Round(125.25 + hh))}
}

// Issue #357 review: a card that scales once from 60% to 100%, each edge rounded, has a centre
// that wobbles by a pixel from rounding (doubled: 401, 400, 401...), and a size that only grows,
// about 3 px a read, so no two reads agree. It is not a pulse: it is waited for until it rests.
func TestTapWaitsForAScaleInWhoseCentreWobblesFromRounding(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		s := 1.0 // at rest from the 25th read
		if n < 25 {
			s = 0.6 + 0.4*float64(n)/25
		}
		return []*ui.Node{{Text: "Card", Bounds: scaling(s), Enabled: true, Clickable: true}}
	}
	w := &tapWatch{start: time.Now(), reads: &reads}
	f.onTap = w.onTap
	mustPass(t, runWith(t, f, quick, "- tapOn: Card"))
	if w.actedEarly(25) {
		t.Errorf("tapped at read %d, after %s: taken for a pulse before it came to rest (read 25)", w.read, w.after)
	}
}

// Issue #357 review: a card that scales in from its centre (from 20%, linear, exact halves)
// grows and never shrinks: it is waited for until it rests.
func TestTapWaitsForAScaleInFromTheCentre(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		d := 0 // at rest from the 21st read
		if n <= 20 {
			d = int(21-n) * 4
		}
		return []*ui.Node{{Text: "Card", Bounds: ui.Rect{L: 100 + d, T: 100 + d/4, R: 300 - d, B: 150 - d/4}, Enabled: true, Clickable: true}}
	}
	w := &tapWatch{start: time.Now(), reads: &reads}
	f.onTap = w.onTap
	mustPass(t, runWith(t, f, quick, "- tapOn: Card"))
	if w.actedEarly(21) {
		t.Errorf("tapped at read %d, after %s: before it came to rest (read 21)", w.read, w.after)
	}
}

// Issue #357 review, a documented limit: a card that springs in from its centre and rings
// (overshoots, undershoots, overshoots) turns its size back twice, as a pulse does, so it is
// tapped mid-ring. Its centre is where it rests, within the pulse slack, so the tap lands on
// the card's resting centre.
func TestTapOnASpringThatRingsInPlaceLandsOnItsRestingCentre(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	ring := []int{-40, -20, 0, 10, 14, 10, 0, -8, -10, -8, 0, 5, 6, 4, 2, 1} // inset at each read, then 0
	f.screen = func() []*ui.Node {
		n := int(reads.Add(1))
		d := 0
		if n <= len(ring) {
			d = -ring[n-1]
		}
		return []*ui.Node{{Text: "Card", Bounds: ui.Rect{L: 100 + d, T: 100 + d/4, R: 300 - d, B: 150 - d/4}, Enabled: true, Clickable: true}}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Card"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 125} {
		t.Errorf("taps %v, want one on the card's resting centre (200, 125)", f.taps)
	}
}
