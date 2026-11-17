package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// proverDriver is a device that can prove a target at rest in one check (driver.RestProver,
// issue #369). prove says what the device finds for the read in hand (nil: it never proves).
type proverDriver struct {
	*fakeDriver
	mu    sync.Mutex
	prove func(s *ui.Screen, n *ui.Node) bool
	asked []*ui.Screen    // the reads it was asked about
	spans []time.Duration // the span each was asked for
}

func (d *proverDriver) AtRest(ctx context.Context, s *ui.Screen, n *ui.Node, span time.Duration) bool {
	d.mu.Lock()
	d.asked = append(d.asked, s)
	d.spans = append(d.spans, span)
	prove := d.prove
	d.mu.Unlock()
	return prove != nil && prove(s, n)
}

func (d *proverDriver) askedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.asked)
}

// readsAtTap runs a tap on a still "Go" button and says how many reads had been made when the
// touch went out.
func readsAtTap(t *testing.T, d *proverDriver, reads *atomic.Int64) int64 {
	t.Helper()
	w := &tapWatch{start: time.Now(), reads: reads}
	d.onTap = w.onTap
	mustPass(t, runWith(t, d, quick, "- tapOn: Go"))
	if w.read == 0 {
		t.Fatal("no tap went out")
	}
	return w.read
}

func stillGo(reads *atomic.Int64) func() []*ui.Node {
	return func() []*ui.Node {
		reads.Add(1)
		return []*ui.Node{{Text: "Go", Class: "Button", Bounds: ui.Rect{L: 100, T: 100, R: 300, B: 150}, Enabled: true, Clickable: true}}
	}
}

// Issue #369: before a tap, the target was read again 40 ms later (two or three times), to
// see it was not moving, even when nothing on screen moved: about 20 s of a bench round. A
// device that proves it at rest in one check is not read again: the tap goes out on the read
// that found the target.
func TestTapOnATargetTheDeviceProvesAtRestIsNotReadAgain(t *testing.T) {
	t.Parallel()
	var unprovenReads atomic.Int64
	unproven := &proverDriver{fakeDriver: &fakeDriver{}}
	unproven.screen = stillGo(&unprovenReads)
	before := readsAtTap(t, unproven, &unprovenReads)

	var reads atomic.Int64
	d := &proverDriver{fakeDriver: &fakeDriver{}, prove: func(*ui.Screen, *ui.Node) bool { return true }}
	d.screen = stillGo(&reads)
	got := readsAtTap(t, d, &reads)
	if d.askedCount() == 0 {
		t.Fatal("the device was never asked whether the target is at rest")
	}
	if got >= before {
		t.Errorf("issue #369: %d reads before the tap with the device's proof of rest, %d without: want fewer (no re-read)", got, before)
	}
	if len(d.taps) != 1 || d.taps[0] != [2]int{200, 125} {
		t.Errorf("taps %v, want one on (200, 125)", d.taps)
	}
}

// Issue #369: a device that finds the target moving (no proof) leaves the reads as they were:
// a target sliding in is waited for, and tapped where it comes to rest.
func TestTapWaitsForASlidingTargetTheDeviceFindsMoving(t *testing.T) {
	t.Parallel()
	var reads atomic.Int64
	d := &proverDriver{fakeDriver: &fakeDriver{}}
	d.screen = func() []*ui.Node {
		l := 400 // at rest from the 25th read
		if n := reads.Add(1); n < 25 {
			l = 100 + int(n)*12
		}
		return []*ui.Node{{Text: "Slide", Bounds: ui.Rect{L: l, T: 100, R: l + 100, B: 150}, Enabled: true, Clickable: true}}
	}
	// The device proves it at rest only once it is (a read that shows it where it ends up).
	d.prove = func(_ *ui.Screen, n *ui.Node) bool { return n.Bounds.L == 400 }
	w := &tapWatch{start: time.Now(), reads: &reads}
	d.onTap = w.onTap
	mustPass(t, runWith(t, d, quick, "- tapOn: Slide"))
	if w.actedEarly(25) {
		t.Errorf("tapped at read %d, after %s: before it came to rest (read 25)", w.read, w.after)
	}
	if len(d.taps) != 1 || d.taps[0] != [2]int{450, 125} {
		t.Errorf("taps %v, want one where it came to rest (450, 125)", d.taps)
	}
}

// Issue #369: a change reported while the device checked (an event after the read) voids the
// proof: the target is read again, as before.
func TestAChangeDuringTheProofOfRestVoidsIt(t *testing.T) {
	t.Parallel()
	var unprovenReads atomic.Int64
	unproven := &proverDriver{fakeDriver: &fakeDriver{}}
	unproven.screen = stillGo(&unprovenReads)
	before := readsAtTap(t, unproven, &unprovenReads)

	var reads atomic.Int64
	d := &proverDriver{fakeDriver: &fakeDriver{}}
	d.screen = stillGo(&reads)
	d.prove = func(*ui.Screen, *ui.Node) bool {
		d.fakeDriver.mu.Lock()
		d.fakeDriver.seq++ // something changed as the device looked
		d.fakeDriver.mu.Unlock()
		return true
	}
	if got := readsAtTap(t, d, &reads); got < before {
		t.Errorf("issue #369: %d reads before the tap with a change during the proof, %d without a proof: want it read again as before", got, before)
	}
}

// freshProverDriver: an Android-like device whose plain reads come from a node cache that misses
// a view moved by translation (freshReader), and whose proof of rest is about a read past it.
type freshProverDriver struct {
	*proverDriver
	freshMu sync.Mutex
	fresh   map[*ui.Screen]bool // the reads made past the cache
	moved   atomic.Bool         // the view was translated, with no event: only a fresh read shows it
}

func (d *freshProverDriver) Screen(ctx context.Context) (*ui.Screen, error) {
	return d.proverDriver.fakeDriver.Screen(ctx)
}

func (d *freshProverDriver) ScreenFresh(ctx context.Context) (*ui.Screen, error) {
	s, err := d.proverDriver.fakeDriver.Screen(ctx)
	if err != nil {
		return nil, err
	}
	if d.moved.Load() {
		for _, n := range s.Nodes {
			n.Bounds.L, n.Bounds.R = n.Bounds.L+100, n.Bounds.R+100
		}
	}
	d.freshMu.Lock()
	d.fresh[s] = true
	d.freshMu.Unlock()
	return s, nil
}

func newFreshProver(reads *atomic.Int64) *freshProverDriver {
	d := &freshProverDriver{proverDriver: &proverDriver{fakeDriver: &fakeDriver{}}, fresh: map[*ui.Screen]bool{}}
	d.screen = func() []*ui.Node {
		reads.Add(1)
		return []*ui.Node{{Text: "Go", Class: "Button", Bounds: ui.Rect{L: 100, T: 100, R: 300, B: 150}, Enabled: true, Clickable: true}}
	}
	d.prove = func(s *ui.Screen, _ *ui.Node) bool {
		d.freshMu.Lock()
		defer d.freshMu.Unlock()
		return d.fresh[s] // the frames it reports are those of a read past the cache
	}
	return d
}

// Issue #369, Android: the device's proof is about a read past its node cache; the read that
// found the target may come from the cache. It is read once past it, at once, and the proof
// asked about that read.
func TestTheProofOfRestIsAskedAboutAReadPastTheCache(t *testing.T) {
	t.Parallel()
	var reads atomic.Int64
	d := newFreshProver(&reads)
	w := &tapWatch{start: time.Now(), reads: &reads}
	d.onTap = w.onTap
	mustPass(t, runWith(t, d, quick, "- tapOn: Go"))
	d.proverDriver.mu.Lock()
	asked := append([]*ui.Screen(nil), d.asked...)
	d.proverDriver.mu.Unlock()
	if len(asked) == 0 {
		t.Fatal("issue #369: the device was never asked whether the target is at rest")
	}
	for _, s := range asked {
		if !d.fresh[s] {
			t.Errorf("issue #369: the proof of rest was asked about a cached read (Read %d)", s.Read)
		}
	}
	if w.read > 2 {
		t.Errorf("issue #369: %d reads before the tap: want the one that found it and one past the cache", w.read)
	}
}

// Issue #369, Android: a view moved by translation (no event) since the cached read that found
// it is not taken at rest where that read had it: the read past the cache shows it elsewhere,
// and it is tapped there.
func TestATargetMovedUnseenByTheCacheIsTappedWhereItIs(t *testing.T) {
	t.Parallel()
	var reads atomic.Int64
	d := newFreshProver(&reads)
	d.moved.Store(true)
	mustPass(t, runWith(t, d, quick, "- tapOn: Go"))
	if len(d.taps) != 1 || d.taps[0] != [2]int{300, 125} {
		t.Errorf("taps %v, want one where it is now (300, 125)", d.taps)
	}
}

// Issue #369 review: the proof covers the span two reads would have compared: 40 ms, and 80 ms
// right after an action (a spring turns: two reads 40 ms apart on either side of a turn can
// agree, so the old loop wanted three). A device asked for less could prove a spring at its
// turn.
func TestTheProofOfRestCoversTheReadsSpan(t *testing.T) {
	t.Parallel()
	var reads atomic.Int64
	d := &proverDriver{fakeDriver: &fakeDriver{}, prove: func(*ui.Screen, *ui.Node) bool { return true }}
	d.screen = stillGo(&reads)
	mustPass(t, runWith(t, d, quick, "- tapOn: Go\n- tapOn: Go"))
	d.mu.Lock()
	spans := append([]time.Duration(nil), d.spans...)
	d.mu.Unlock()
	if len(spans) < 2 {
		t.Fatalf("issue #369: proof asked %d times for two taps, want 2", len(spans))
	}
	if spans[0] < restGap {
		t.Errorf("issue #369: the first tap's proof spans %v, want at least %v (two reads' gap)", spans[0], restGap)
	}
	if last := spans[len(spans)-1]; last < restSpanAfterAction {
		t.Errorf("issue #369: the proof right after an action spans %v, want at least %v (three reads, a spring's turn)", last, restSpanAfterAction)
	}
}

// honestProver proves a target at rest the way a device would: it looks at the target again
// (as the device's own frames would show it) and proves only when it has not moved.
func honestProver(d *proverDriver) func(s *ui.Screen, n *ui.Node) bool {
	return func(s *ui.Screen, n *ui.Node) bool {
		d.fakeDriver.mu.Lock()
		now := d.fakeDriver.screen()
		d.fakeDriver.mu.Unlock()
		for _, m := range now {
			if m.Text == n.Text && m.ID == n.ID {
				return m.Bounds == n.Bounds
			}
		}
		return false
	}
}

// Issue #357 under the proof (issue #369 review): a target that pulses in place is never proven
// at rest, and is still tapped on its centre within about a period.
func TestAPulseUnderTheProofIsStillTappedOnItsCentre(t *testing.T) {
	t.Parallel()
	var reads atomic.Int64
	d := &proverDriver{fakeDriver: &fakeDriver{}}
	d.screen = pulsing(&reads)
	d.prove = honestProver(d)
	w := &tapWatch{start: time.Now(), reads: &reads}
	d.onTap = w.onTap
	mustPass(t, runWith(t, d, quick, "- tapOn: Pulse"))
	if d.askedCount() == 0 {
		t.Fatal("issue #369: the device was never asked whether the pulse is at rest")
	}
	if w.read == 0 || w.read > 28 {
		t.Errorf("tapped after %d reads: want within about one period (16 reads) of finding it", w.read)
	}
	if len(d.taps) != 1 || d.taps[0] != [2]int{200, 125} {
		t.Errorf("taps %v, want one on the pulse's centre (200, 125)", d.taps)
	}
}

// Issue #321 under the proof (issue #369 review): the tap names the node of the very read the
// device proved at rest, so the agent asks its app for that node just before the touch.
func TestTheTapAimsAtTheNodeOfTheProvenRead(t *testing.T) {
	t.Parallel()
	var reads atomic.Int64
	d := &proverDriver{fakeDriver: &fakeDriver{}, prove: func(*ui.Screen, *ui.Node) bool { return true }}
	d.screen = stillGo(&reads)
	mustPass(t, runWith(t, d, quick, "- tapOn: Go"))
	d.mu.Lock()
	asked := append([]*ui.Screen(nil), d.asked...)
	d.mu.Unlock()
	if len(asked) == 0 || len(d.tapOpts) != 1 {
		t.Fatalf("issue #369: proof asked %d times, %d taps", len(asked), len(d.tapOpts))
	}
	aim := d.tapOpts[0].Aim
	if aim == nil || aim.Read != asked[len(asked)-1].Read || aim.Ref != 1 {
		t.Errorf("issue #321: the tap aims at %+v, want node 1 of the proven read %d", aim, asked[len(asked)-1].Read)
	}
}
