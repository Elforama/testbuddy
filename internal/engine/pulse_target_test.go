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

func pulseInset(read int64) int {
	p := int(read % 16)
	if p > 8 {
		p = 16 - p
	}
	return p
}

func pulsing(reads *atomic.Int64) func() []*ui.Node {
	return func() []*ui.Node {
		d := pulseInset(reads.Add(1) + 5)
		return []*ui.Node{{ID: "pulse", Class: "Button", Text: "Pulse", Enabled: true, Clickable: true,
			Bounds: ui.Rect{L: 100 + d, T: 100 + d/2, R: 300 - d, B: 150 - d/2}}}
	}
}

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

func (w *tapWatch) actedEarly(rest int64) bool {
	return w.read < rest && w.after < restWait
}

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
	live := d.screen()[0].Bounds
	read := o.Target.Bounds
	if !o.Target.Moving && (abs((live.L+live.R)-(read.L+read.R)) > 1 || abs((live.T+live.B)-(read.T+read.B)) > 1) {
		d.withheld++
		return driver.ActionResult{Withheld: "moved", Guarded: true, Probed: true}, nil
	}
	d.taps = append(d.taps, [2]int{x, y})
	return driver.ActionResult{Guarded: true, Probed: true, Targeted: true, Settle: driver.Changed}, nil
}

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

func TestTapStillWaitsForASlidingTarget(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		l := 400
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

func TestTapWaitsForATargetThatPulsesWhileItDrifts(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		x, d := 160, 0
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

func scaling(s float64) ui.Rect {
	hw, hh := 100.5*s, 25.5*s
	return ui.Rect{L: int(math.Round(200.25 - hw)), T: int(math.Round(125.25 - hh)), R: int(math.Round(200.25 + hw)), B: int(math.Round(125.25 + hh))}
}

func TestTapWaitsForAScaleInWhoseCentreWobblesFromRounding(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		s := 1.0
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

func TestTapWaitsForAScaleInFromTheCentre(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		d := 0
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

func TestTapOnASpringThatRingsInPlaceLandsOnItsRestingCentre(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	ring := []int{-40, -20, 0, 10, 14, 10, 0, -8, -10, -8, 0, 5, 6, 4, 2, 1}
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
