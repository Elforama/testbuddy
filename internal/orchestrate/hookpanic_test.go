package orchestrate

import (
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
)

// busyDevices lists the devices the pool still hands to a job.
func busyDevices(p *Pool) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for s := range p.busy {
		out = append(out, s)
	}
	return out
}

// issue #117: a panic in the Started or Finished hook (the CLI's console, report and
// timeline writing run there) is contained like a panic in the flow itself: it does not
// escape Schedule, the flow it was for is reported failed with the crash, its devices are
// released, and the rest of the run goes on. In the queue and in the ordered sequence, for a
// single-device and a multi-device flow.
func TestScheduleHookPanicIsContained(t *testing.T) {
	for _, hook := range []string{"Started", "Finished"} {
		for _, path := range []string{"queue", "sequence"} {
			for _, kind := range []string{"single", "multi"} {
				t.Run(hook+"/"+path+"/"+kind, func(t *testing.T) {
					a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
					pool := poolOf(a, b)
					boom := job(typingFlow(t, "boom"))
					if kind == "multi" {
						f := parseFlow(t, "boom", "appId: com.app\ndevices:\n  alice: android\n  bob: android\n---\n- alice: [{inputText: x}]\n- bob: [{inputText: y}]\n")
						boom = job(f, NeedsFor(f, "", nil)...)
					}
					after := job(typingFlow(t, "after"))
					boom.Seq, after.Seq = path == "sequence", path == "sequence"
					rec := newRecorder(t)
					h := rec.hooks()
					if hook == "Started" {
						started := h.Started
						h.Started = func(j *Job, devs map[string]device.Info) {
							if j.Flow.Name() == "boom" {
								panic("hook crash in " + j.Flow.Name())
							}
							started(j, devs)
						}
					} else {
						finished := h.Finished
						h.Finished = func(r Result) {
							if r.Job.Flow.Name() == "boom" {
								panic("hook crash in " + r.Job.Flow.Name())
							}
							finished(r)
						}
					}
					res := scheduleNoPanic(t, pool, []*Job{boom, after}, 0, true, h)
					got := byName(res)
					if len(res) != 2 {
						t.Fatalf("%d results, want 2 (both flows reported): %+v", len(res), res)
					}
					r := got["boom"]
					if r.Status != engine.Failed || r.Err == nil || !strings.Contains(r.Err.Error(), "hook crash in boom") ||
						!strings.Contains(r.Err.Error(), "tb crashed") {
						t.Errorf("boom = %s %v; want failed with the hook's crash", r.Status, r.Err)
					}
					if got["after"].Status != engine.Passed {
						t.Errorf("after = %s %v; want passed (the run goes on)", got["after"].Status, got["after"].Err)
					}
					if busy := busyDevices(pool); len(busy) != 0 {
						t.Errorf("devices still held after the run: %v", busy)
					}
				})
			}
		}
	}
}

// issue #117: a crash in Finished for an attempt that is to be retried is not lost: that
// attempt becomes the flow's result, failed with the crash, and is not retried.
func TestScheduleFinishedPanicOnARetriedAttempt(t *testing.T) {
	for _, path := range []string{"queue", "sequence"} {
		t.Run(path, func(t *testing.T) {
			d := newFake("d", "D", "android")
			d.launchFails = 1 // the first attempt fails, a retry would pass
			j := job(typingFlow(t, "flaky"))
			j.Seq = path == "sequence"
			h := newRecorder(t).hooks()
			h.Finished = func(r Result) {
				if r.Job.Attempt == 0 {
					panic("hook crash on attempt 0")
				}
			}
			pool := poolOf(d)
			res := scheduleNoPanic(t, pool, []*Job{j}, 2, true, h)
			if len(res) != 1 || res[0].Status != engine.Failed || res[0].Err == nil ||
				!strings.Contains(res[0].Err.Error(), "hook crash on attempt 0") ||
				!strings.Contains(res[0].Err.Error(), "cannot launch") {
				t.Fatalf("results %+v; want one, failed with its own error and the crash", res)
			}
			if busy := busyDevices(pool); len(busy) != 0 {
				t.Errorf("devices still held after the run: %v", busy)
			}
		})
	}
}
