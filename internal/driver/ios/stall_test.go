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

func runScroll(t *testing.T, d *Driver) error {
	t.Helper()
	cmds, err := flow.ParseCommands(".", []byte("- scrollUntilVisible: {element: {id: good_save}, direction: DOWN, timeout: 1500}"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return engine.New(d, engine.Options{}).RunCommands(ctx, cmds, "dev.testbuddy.bench", nil)
}

// Issue #237: on a CI runner the whole simulator was starved for ~45 s in the middle of a
// scrollUntilVisible: a screen read was given up on (the app did not answer accessibility for
// 30 s), the read asked once more waited 12.7 s for the app's main thread, and by then the step's
// time was up. It failed with "No visible element found: id: good_save (after 3 swipes, 1 of which
// moved the screen)", as if the list had no such element. The failure must name the stall.
func TestScrollFailureNamesASimulatorStall(t *testing.T) {
	t.Setenv("TB_CI", "1")
	err := runScroll(t, newIOSDriver(t, stallRunner(t, 2*time.Second)))
	if err == nil {
		t.Fatal("the scroll passed")
	}
	if !strings.Contains(err.Error(), "the simulator stopped answering for") ||
		!strings.Contains(err.Error(), "accessibility timeouts") {
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

// Issue #237: what counts as a stall. A read slower than stallRead counts (it says how long it
// took), a prompt one does not, and a read given up on while waiting for the main thread names it.
func TestScreenReadsKeepStalls(t *testing.T) {
	old := stallRead
	stallRead = 300 * time.Millisecond
	t.Cleanup(func() { stallRead = old })
	t.Setenv("TB_CI", "0")
	r := newFakeRunner(t)
	var mu sync.Mutex
	replies := []func() map[string]any{
		func() map[string]any { return stallTree(1) },
		func() map[string]any { time.Sleep(400 * time.Millisecond); return stallTree(2) },
		func() map[string]any {
			return map[string]any{"ok": false, "transient": true,
				"error": "reading the screen did not finish: the app did not answer accessibility for 30.0 s (waiting for the main thread); nothing was read"}
		},
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
	if st.For < 400*time.Millisecond || len(st.Signs) != 1 || !strings.HasPrefix(st.Signs[0], "a screen read took") {
		t.Fatalf("a slow read: %+v", st)
	}
	t2 := time.Now()
	if _, err := d.Screen(ctxT(t)); err == nil {
		t.Fatal("a read given up on (not in CI) passed")
	}
	st = d.StallSince(t2)
	if st.For <= 0 || strings.Join(st.Signs, ", ") != "accessibility timeouts, app main thread held" {
		t.Fatalf("a read given up on: %+v", st)
	}
	if st := d.StallSince(time.Now()); st.For != 0 {
		t.Fatalf("stalls before t counted: %+v", st)
	}
	if notes := strings.Join(d.Notes(), "\n"); !strings.Contains(notes, "the simulator stopped answering for") {
		t.Errorf("the trace does not show the stall: %q", notes)
	}
}
