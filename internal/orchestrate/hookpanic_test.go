package orchestrate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
)

func busyDevices(p *Pool) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for s := range p.busy {
		out = append(out, s)
	}
	return out
}

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
					var hc *HookCrash
					if !errors.As(r.Err, &hc) || hc.Hook != hook {
						t.Errorf("boom's error %v does not hold a HookCrash from %s", r.Err, hook)
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

func TestScheduleFinishedPanicOnARetriedAttempt(t *testing.T) {
	for _, path := range []string{"queue", "sequence"} {
		t.Run(path, func(t *testing.T) {
			d := newFake("d", "D", "android")
			d.launchFails = 1
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

func TestScheduleCancelKeepsAFlowsOwnReason(t *testing.T) {
	d := newFake("d", "D", "android")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.launchFails = -1
	d.onLaunch = func(string) { cancel() }
	var res []Result
	within(t, 10e9, func() {
		res = Schedule(ctx, poolOf(d), []*Job{job(typingFlow(t, "own")), job(typingFlow(t, "later"))}, 2, 1, false, newRecorder(t).hooks())
	})
	got := byName(res)
	if r := got["own"]; r.Status != engine.Failed || attemptOf(r) != 0 || r.Err == nil ||
		!strings.Contains(r.Err.Error(), "cannot launch") || strings.Contains(r.Err.Error(), "cancelled") {
		t.Errorf("own = %s on attempt %d: %v; want failed once with its own reason", r.Status, attemptOf(r), r.Err)
	}
	if r := got["later"]; r.Status != engine.Skipped || r.Err == nil || !strings.Contains(r.Err.Error(), "the run was stopped before this flow ran") {
		t.Errorf("later = %s: %v; want skipped, saying the run was stopped", r.Status, r.Err)
	}
}
