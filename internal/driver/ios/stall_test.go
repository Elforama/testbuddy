package ios

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

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
	return engine.New(d, engine.Options{StallFloor: time.Second}).RunCommands(ctx, cmds, "dev.testbuddy.bench", nil)
}

func TestScrollFailureNamesASimulatorStall(t *testing.T) {
	t.Setenv("TB_CI", "1")
	shortReads(t, 2*time.Second, time.Second)
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

func TestScrollFailureWithoutAStallIsAPlainNotFound(t *testing.T) {
	t.Setenv("TB_CI", "1")
	err := runScroll(t, newIOSDriver(t, stallRunner(t, 0)))
	if err == nil || !strings.HasPrefix(err.Error(), "No visible element found: id: good_save") ||
		strings.Contains(err.Error(), "stopped answering") {
		t.Fatalf("err = %v, want a plain not found", err)
	}
}

var mainHeld = map[string]any{"ok": false, "transient": true,
	"error": "reading the screen did not finish: the app did not answer accessibility for 30.0 s (waiting for the main thread, held by an earlier call to the app); nothing was read"}

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

	d.stalls = []stall{{start: at(0), end: at(4), mainWait: 3 * time.Second}}
	if got := strings.Join(d.StallSince(at(2)).Signs, ", "); got != "app main thread held for 1.0 s, one screen read took 2.0 s" {
		t.Errorf("a clipped stall's signs %q, want the held time from t on", got)
	}
}

func heldFor(t *testing.T, signs []string) float64 {
	t.Helper()
	for _, s := range signs {
		var v float64
		if _, err := fmt.Sscanf(s, "app main thread held for %f s", &v); err == nil {
			return v
		}
	}
	return -1
}

func TestHeldTimeCountsAReadGivenUpOn(t *testing.T) {
	t.Setenv("TB_CI", "1")
	shortReads(t, time.Second, 300*time.Millisecond)
	r := newFakeRunner(t)
	var mu sync.Mutex
	replies := []func() map[string]any{
		func() map[string]any { time.Sleep(600 * time.Millisecond); return mainHeld },
		func() map[string]any { return withMainWait(stallTree(1), 300) },
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
	if h := heldFor(t, st.Signs); h < 0.85 || h > 1.5 {
		t.Fatalf("held for %.1f s, want the given-up read's 0.6 s and the next one's 0.3 s: %+v", h, st)
	}
}

func TestHeldTimeSumsStalls(t *testing.T) {
	t.Setenv("TB_CI", "0")
	shortReads(t, time.Second, 300*time.Millisecond)
	r := newFakeRunner(t)
	var mu sync.Mutex
	replies := []func() map[string]any{
		func() map[string]any { time.Sleep(400 * time.Millisecond); return withMainWait(stallTree(1), 350) },
		func() map[string]any { time.Sleep(400 * time.Millisecond); return withMainWait(stallTree(2), 380) },
		func() map[string]any { time.Sleep(600 * time.Millisecond); return mainHeld },
		func() map[string]any { time.Sleep(400 * time.Millisecond); return withMainWait(stallTree(3), 380) },
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
	for range 2 {
		if _, err := d.Screen(ctxT(t)); err != nil {
			t.Fatal(err)
		}
	}
	if h := heldFor(t, d.StallSince(t0).Signs); h != 0.7 {
		t.Fatalf("two slow reads: held for %.1f s, want 0.35 + 0.38 s", h)
	}
	t1 := time.Now()
	if _, err := d.Screen(ctxT(t)); err == nil {
		t.Fatal("a read given up on (not in CI) passed")
	}
	if got := strings.Join(d.StallSince(t1).Signs, ", "); got != "an accessibility timeout, app main thread held" {
		t.Fatalf("a read given up on alone: %q", got)
	}
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if h := heldFor(t, d.StallSince(t1).Signs); h < 0.9 || h > 1.5 {
		t.Fatalf("a read given up on, then a slow one: held for %.1f s, want 0.6 + 0.38 s", h)
	}
}

func withMainWait(tree map[string]any, ms int) map[string]any {
	tree["mainWaitMs"] = ms
	return tree
}

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

	t1 := time.Now()
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if st := d.StallSince(t1); strings.Join(st.Signs, ", ") != "one screen read took 0.4 s" {
		t.Fatalf("a slow read without mainWaitMs: %+v", st)
	}

	t2 := time.Now()
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if st := d.StallSince(t2); st.For != 0 {
		t.Fatalf("a prompt read counted as a stall: %+v", st)
	}
}

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
