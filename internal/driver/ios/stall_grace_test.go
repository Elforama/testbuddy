package ios

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

// stallThenFoundRunner is stallRunner with a list that does hold the element: it comes into
// view with the second swipe. The app stops answering accessibility for stall after the first
// swipe (the read is given up on, the read asked once more answers), by which time the scroll's
// own time is up.
func stallThenFoundRunner(t *testing.T, stall time.Duration) *fakeRunner {
	r := stallRunner(t, stall)
	var mu sync.Mutex
	seq, swipes, stalled := 1, 0, false
	r.on("tree", func(map[string]any) map[string]any {
		mu.Lock()
		stallNow := stall > 0 && swipes > 0 && !stalled
		if stallNow {
			stalled = true
		}
		s, found := seq, swipes >= 2
		mu.Unlock()
		if stallNow {
			time.Sleep(stall)
			return map[string]any{"ok": false, "transient": true,
				"error": "reading the screen did not finish: the app did not answer accessibility for 30.0 s (taking the snapshot); nothing was read"}
		}
		tree := stallTree(s)
		if found {
			w := tree["windows"].([]any)[0].(map[string]any)
			w["nodes"] = append(w["nodes"].([]any),
				map[string]any{"d": 2, "b": []int{20, 240, 373, 280}, "c": "Button", "id": "good_save", "f": 4099})
		}
		return tree
	})
	r.on("swipe", func(map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		swipes++
		seq++
		return map[string]any{"result": "changed", "seq": seq}
	})
	return r
}

// runScrollTraced is runScroll with the run's trace kept.
func runScrollTraced(t *testing.T, d *Driver) (string, error) {
	t.Helper()
	cmds, err := flow.ParseCommands(".", []byte("- scrollUntilVisible: {element: {id: good_save}, direction: DOWN, timeout: 1500}"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var buf bytes.Buffer
	// The stalls here are seconds, not tens of seconds: so is the shortest stall a step names
	// or waits out.
	err = engine.New(d, engine.Options{StallFloor: time.Second, Trace: engine.NewTrace(&buf)}).
		RunCommands(ctx, cmds, "dev.testbuddy.bench", nil)
	return buf.String(), err
}

const graceLine = "more, once (CI mode)"

// Issue #243: on a CI runner the whole simulator was starved for ~45 s in the middle of a
// scrollUntilVisible (issue #237): the step's time ran out while nothing could answer, and the
// step failed, naming the stall. In CI mode the step gets the stalled time back, once, up to its
// own timeout: here a 2 s stall eats a 1.5 s scroll, and the element comes with the next swipe.
func TestScrollWaitsOutASimulatorStallInCI(t *testing.T) {
	t.Setenv("TB_CI", "1")
	shortReads(t, 2*time.Second, time.Second) // the agent has 2 s for a read, as it has 30 s
	trace, err := runScrollTraced(t, newIOSDriver(t, stallThenFoundRunner(t, 2*time.Second)))
	if err != nil {
		t.Fatalf("the scroll failed after a stall it could have waited out: %v\n%s", err, trace)
	}
	if !strings.Contains(trace, "the simulator stopped answering for") || !strings.Contains(trace, "giving the step 1.5 s "+graceLine) {
		t.Errorf("the trace does not say the step was given more time:\n%s", trace)
	}
}

// Issue #243: outside CI nothing changes: the same stall fails the step, as since #237.
func TestScrollDoesNotWaitOutAStallOutsideCI(t *testing.T) {
	t.Setenv("TB_CI", "0")
	shortReads(t, 2*time.Second, time.Second)
	trace, err := runScrollTraced(t, newIOSDriver(t, stallThenFoundRunner(t, 2*time.Second)))
	if err == nil {
		t.Fatalf("the scroll passed outside CI:\n%s", trace)
	}
	if strings.Contains(trace, graceLine) || strings.Contains(err.Error(), "CI mode") {
		t.Errorf("a step outside CI was given more time: %v\n%s", err, trace)
	}
}

// Issue #243: the extra time is one stall's, once: an element that is really missing still
// fails, within the step's timeout twice over plus the stall, and the failure says it was given
// more time.
func TestScrollStillFailsForAMissingElementAfterAStall(t *testing.T) {
	t.Setenv("TB_CI", "1")
	shortReads(t, 2*time.Second, time.Second)
	start := time.Now()
	trace, err := runScrollTraced(t, newIOSDriver(t, stallRunner(t, 2*time.Second)))
	took := time.Since(start)
	if err == nil {
		t.Fatalf("a missing element was found:\n%s", trace)
	}
	if !strings.Contains(err.Error(), "the simulator stopped answering for") ||
		!strings.Contains(err.Error(), "even with 1.5 s more (CI mode)") ||
		!strings.Contains(err.Error(), "the step's own failure: No visible element found: id: good_save") {
		t.Errorf("the failure does not name the stall and the extra time: %v", err)
	}
	if n := strings.Count(trace, graceLine); n != 1 {
		t.Errorf("given more time %d times, want once:\n%s", n, trace)
	}
	if took > 2*1500*time.Millisecond+2*time.Second+time.Second {
		t.Errorf("the step took %s: more than its timeout twice and the stall", took)
	}
}

// Issue #243: a healthy CI run never gets the extra time, even when the element is missing and
// its wait runs out: the allowance cannot hide slowness that is not a stall.
func TestScrollWithoutAStallGetsNoExtraTime(t *testing.T) {
	t.Setenv("TB_CI", "1")
	trace, err := runScrollTraced(t, newIOSDriver(t, stallRunner(t, 0)))
	if err == nil || !strings.HasPrefix(err.Error(), "No visible element found: id: good_save") {
		t.Fatalf("err = %v, want a plain not found", err)
	}
	if strings.Contains(trace, graceLine) {
		t.Errorf("a healthy run was given more time:\n%s", trace)
	}
	t.Logf("failure: %v", err)
}
