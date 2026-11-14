package ios

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

// stallTree is a list the scroll drags, without the element it looks for.
func stallTree(seq int) map[string]any {
	node := func(d int, b []int, c, id string, f int) map[string]any {
		return map[string]any{"d": d, "b": b, "c": c, "id": id, "f": f}
	}
	return map[string]any{"seq": seq, "windows": []any{map[string]any{
		"id": 1, "type": 1, "active": true, "focused": true, "pkg": "dev.testbuddy.bench", "b": []int{0, 0, 393, 852},
		"nodes": []any{
			node(0, []int{0, 0, 393, 852}, "Application", "", 4098),
			node(1, []int{0, 100, 393, 852}, "ScrollView", "good_list", 4106),
			node(2, []int{20, 120, 373, 160}, "Button", "good_tiny", 4099),
			node(2, []int{20, 180, 373, 220}, "Button", "good_offer", 4099),
		},
	}}}
}

// stallRunner is a fake runner whose app stops answering accessibility for stall after the
// first swipe, the way a starved CI simulator did (issue #237): the next screen read is given up
// on by the agent ("did not answer accessibility"), and the read asked once more answers. With
// stall 0 it answers every read at once.
func stallRunner(t *testing.T, stall time.Duration) *fakeRunner {
	r := newFakeRunner(t)
	var mu sync.Mutex
	seq, swipes, stalled := 1, 0, false
	r.on("tree", func(map[string]any) map[string]any {
		mu.Lock()
		stallNow := stall > 0 && swipes > 0 && !stalled
		if stallNow {
			stalled = true
		}
		s := seq
		mu.Unlock()
		if stallNow {
			time.Sleep(stall)
			return map[string]any{"ok": false, "transient": true,
				"error": "reading the screen did not finish: the app did not answer accessibility for 30.0 s (taking the snapshot); nothing was read"}
		}
		return stallTree(s)
	})
	r.on("swipe", func(map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		swipes++
		return map[string]any{"result": "changed", "seq": seq}
	})
	r.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "settled", "seq": seq} })
	r.on("quiet", func(map[string]any) map[string]any { return map[string]any{"quiet": true, "seq": seq} })
	return r
}

// shortReads scales the read limits down for a test: the agent's time for a read, and a read
// that counts as slow.
func shortReads(t *testing.T, read, slow time.Duration) {
	oldRead, oldSlow := readTimeout, stallRead
	readTimeout, stallRead = read, slow
	t.Cleanup(func() { readTimeout, stallRead = oldRead, oldSlow })
}

func runScroll(t *testing.T, d *Driver) error {
	t.Helper()
	cmds, err := flow.ParseCommands(".", []byte("- scrollUntilVisible: {element: {id: good_save}, direction: DOWN, timeout: 1500}"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// The stalls here are seconds, not tens of seconds: so is the shortest stall a step names.
	return engine.New(d, engine.Options{StallFloor: time.Second}).RunCommands(ctx, cmds, "dev.testbuddy.bench", nil)
}

// Issue #237: on a CI runner the whole simulator was starved for ~45 s in the middle of a
// scrollUntilVisible: a screen read was given up on (the app did not answer accessibility for
// 30 s), the read asked once more waited 12.7 s for the app's main thread, and by then the step's
// time was up. It failed with "No visible element found: id: good_save (after 3 swipes, 1 of which
// moved the screen)", as if the list had no such element. The failure must name the stall.
func TestScrollFailureNamesASimulatorStall(t *testing.T) {
	t.Setenv("TB_CI", "1")
	shortReads(t, 2*time.Second, time.Second) // the agent has 2 s for a read, as it has 30 s
	err := runScroll(t, newIOSDriver(t, stallRunner(t, 2*time.Second)))
	if err == nil {
		t.Fatal("the scroll passed")
	}
	if !strings.Contains(err.Error(), "the simulator stopped answering for") ||
		!strings.Contains(err.Error(), "an accessibility timeout") ||
		!strings.Contains(err.Error(), "the step's own failure: No visible element found: id: good_save") {
		t.Fatalf("the failure does not name the stall: %v", err)
	}
	t.Logf("failure: %v", err)
}

// Issue #237, the other way round: a device that answers every read promptly and has no such
// element fails with the plain "not found", never with a stall it did not have.
func TestScrollFailureWithoutAStallIsAPlainNotFound(t *testing.T) {
	t.Setenv("TB_CI", "1")
	err := runScroll(t, newIOSDriver(t, stallRunner(t, 0)))
	if err == nil || !strings.HasPrefix(err.Error(), "No visible element found: id: good_save") ||
		strings.Contains(err.Error(), "stopped answering") {
		t.Fatalf("err = %v, want a plain not found", err)
	}
}

// mainHeld is the agent's answer to a read it gave up on while waiting for the app's main thread.
var mainHeld = map[string]any{"ok": false, "transient": true,
	"error": "reading the screen did not finish: the app did not answer accessibility for 30.0 s (waiting for the main thread, held by an earlier call to the app); nothing was read"}

// Issue #237: what counts as a stall. A read slower than stallRead counts as slow (it says how
// long it took), a prompt one does not, a read given up on is the simulator not answering (with
// the main thread held only when the agent says so), and one given up on because the run's
// deadline cut its time short does not count.
func TestScreenReadsKeepStalls(t *testing.T) {
	shortReads(t, time.Second, 300*time.Millisecond)
	t.Setenv("TB_CI", "0")
	r := newFakeRunner(t)
	var mu sync.Mutex
	replies := []func() map[string]any{
		func() map[string]any { return stallTree(1) },
		func() map[string]any { time.Sleep(400 * time.Millisecond); return stallTree(2) },
		func() map[string]any { return mainHeld },
		func() map[string]any { return mainHeld },
	}
	n := 0
	r.on("tree", func(map[string]any) map[string]any {
		mu.Lock()
		f := replies[n]
		n++
		mu.Unlock()
		return f()
	})
	d := newIOSDriver(t, r)

	t0 := time.Now()
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if st := d.StallSince(t0); st.For != 0 {
		t.Fatalf("a prompt read counted as a stall: %+v", st)
	}
	t1 := time.Now()
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	st := d.StallSince(t1)
	if st.For < 400*time.Millisecond || st.Stopped || strings.Join(st.Signs, ", ") != "one screen read took 0.4 s" {
		t.Fatalf("a slow read: %+v", st)
	}
	t2 := time.Now()
	if _, err := d.Screen(ctxT(t)); err == nil {
		t.Fatal("a read given up on (not in CI) passed")
	}
	st = d.StallSince(t2)
	if st.For <= 0 || !st.Stopped || strings.Join(st.Signs, ", ") != "an accessibility timeout, app main thread held" {
		t.Fatalf("a read given up on: %+v", st)
	}
	if st := d.StallSince(time.Now()); st.For != 0 {
		t.Fatalf("stalls before t counted: %+v", st)
	}
	// A read whose time the run's deadline cut short: given up on, but no stall.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	t3 := time.Now()
	if _, err := d.Screen(ctx); err == nil {
		t.Fatal("a read given up on passed")
	}
	if st := d.StallSince(t3); st.For != 0 {
		t.Fatalf("a read cut short by the run's deadline counted: %+v", st)
	}
	if notes := strings.Join(d.Notes(), "\n"); !strings.Contains(notes, "the simulator stopped answering for") ||
		!strings.Contains(notes, "the simulator was slow to answer for") {
		t.Errorf("the trace does not show the stalls: %q", notes)
	}
}

// Issue #237: overlapping stalls count once, and only from t on.
func TestStallSinceMergesOverlaps(t *testing.T) {
	d := &Driver{}
	t0 := time.Now().Add(-10 * time.Second)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	d.stalls = []stall{
		{start: at(0), end: at(4), givenUp: true, retry: 3 * time.Second},
		{start: at(2), end: at(5)},
		{start: at(7), end: at(8)},
	}
	st := d.StallSince(at(1))
	if st.For != 5*time.Second || !st.Stopped {
		t.Fatalf("got %+v, want 5 s stopped", st)
	}
	if got := strings.Join(st.Signs, ", "); got != "an accessibility timeout, the next read took 3.0 s, 2 screen reads took 4.0 s" {
		t.Errorf("signs %q", got)
	}
}

// withMainWait is a tree reply from an agent that waited ms for the app's main thread before
// the read (mainWaitMs, issue #255).
func withMainWait(tree map[string]any, ms int) map[string]any {
	tree["mainWaitMs"] = ms
	return tree
}

// Issue #255: the agent knew how long a slow read waited for the app's main thread and only
// logged it, so a read that did answer, slowly, was named "one screen read took 12.8 s" and
// never "app main thread held". It sends mainWaitMs now, and a slow read that is kept as a stall
// names the held main thread with its time. A reply without the field (an older agent) gives
// the signs as before, and a prompt read with it is no stall.
func TestASlowReadNamesTheHeldMainThread(t *testing.T) {
	shortReads(t, time.Second, 300*time.Millisecond)
	t.Setenv("TB_CI", "0")
	r := newFakeRunner(t)
	var mu sync.Mutex
	replies := []func() map[string]any{
		func() map[string]any { time.Sleep(400 * time.Millisecond); return withMainWait(stallTree(1), 380) },
		func() map[string]any { time.Sleep(400 * time.Millisecond); return stallTree(2) },
		func() map[string]any { return withMainWait(stallTree(3), 150) },
	}
	n := 0
	r.on("tree", func(map[string]any) map[string]any {
		mu.Lock()
		f := replies[n]
		n++
		mu.Unlock()
		return f()
	})
	d := newIOSDriver(t, r)

	t0 := time.Now()
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	st := d.StallSince(t0)
	if st.For < 400*time.Millisecond || st.Stopped || strings.Join(st.Signs, ", ") != "app main thread held for 0.4 s, one screen read took 0.4 s" {
		t.Fatalf("a slow read held on the main thread: %+v", st)
	}
	if notes := strings.Join(d.Notes(), "\n"); !strings.Contains(notes, "app main thread held for 0.4 s") {
		t.Errorf("the trace does not name the held main thread: %q", notes)
	}

	// An older agent sends no mainWaitMs: the signs are as before issue #255.
	t1 := time.Now()
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if st := d.StallSince(t1); strings.Join(st.Signs, ", ") != "one screen read took 0.4 s" {
		t.Fatalf("a slow read without mainWaitMs: %+v", st)
	}

	// A prompt read is no stall, whatever it waited.
	t2 := time.Now()
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if st := d.StallSince(t2); st.For != 0 {
		t.Fatalf("a prompt read counted as a stall: %+v", st)
	}
}

// Issue #255: a scroll that fails while a read waited long for the app's main thread names the
// held main thread in its failure, with the time it was held.
func TestScrollFailureNamesTheHeldMainThread(t *testing.T) {
	t.Setenv("TB_CI", "0")
	shortReads(t, 3*time.Second, time.Second)
	r := newFakeRunner(t)
	var mu sync.Mutex
	swipes, slowed := 0, false
	r.on("tree", func(map[string]any) map[string]any {
		mu.Lock()
		slowNow := swipes > 0 && !slowed
		slowed = slowed || slowNow
		mu.Unlock()
		if slowNow {
			time.Sleep(1500 * time.Millisecond)
			return withMainWait(stallTree(1), 1400)
		}
		return stallTree(1)
	})
	r.on("swipe", func(map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		swipes++
		return map[string]any{"result": "changed", "seq": 1}
	})
	r.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "settled", "seq": 1} })
	r.on("quiet", func(map[string]any) map[string]any { return map[string]any{"quiet": true, "seq": 1} })
	err := runScroll(t, newIOSDriver(t, r))
	if err == nil {
		t.Fatal("the scroll passed")
	}
	if !strings.Contains(err.Error(), "the simulator was slow to answer for") ||
		!strings.Contains(err.Error(), "app main thread held for 1.4 s") ||
		!strings.Contains(err.Error(), "the step's own failure: No visible element found: id: good_save") {
		t.Fatalf("the failure does not name the held main thread: %v", err)
	}
	t.Logf("failure: %v", err)
}
