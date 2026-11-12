package orchestrate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/engine"
)

// blockUntilCancel makes each launch on the fakes report itself on entered, then wait for the
// run's cancel: a flow that is running when the run is stopped.
func blockUntilCancel(ctx context.Context, entered chan<- string, fakes ...*fakeDriver) {
	for _, f := range fakes {
		f.onLaunch = func(string) {
			entered <- f.serial
			<-ctx.Done()
		}
	}
}

// waitEntered waits for n launches to be under way.
func waitEntered(t *testing.T, entered <-chan string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of %d flows started", i, n)
		}
	}
}

// checkStoppedRun checks a run stopped while the flows named running ran: they finished
// failed, as cancelled; every other flow was not started (no Started hook, no launch) and is
// reported skipped, saying the run was stopped, with a Finished that is not a failure (so the
// CLI saves no failure files for it).
func checkStoppedRun(t *testing.T, res []Result, rec *recorder, launches int, total int, running ...string) {
	t.Helper()
	isRunning := map[string]bool{}
	for _, n := range running {
		isRunning[n] = true
	}
	if started := rec.Started(); len(started) != len(running) {
		t.Errorf("issue #189: Started ran for %v; want only the flows running at the cancel %v", started, running)
	}
	if launches != len(running) {
		t.Errorf("issue #189: %d launches on the devices; want %d (only the flows running at the cancel)", launches, len(running))
	}
	if len(res) != total {
		t.Fatalf("%d results, want %d (every flow reported): %+v", len(res), total, res)
	}
	for name, r := range byName(res) {
		if isRunning[name] {
			if r.Status != engine.Failed || r.Err == nil || !strings.Contains(r.Err.Error(), "cancelled: the run was stopped") {
				t.Errorf("%s (running at the cancel) = %s %v; want failed, cancelled", name, r.Status, r.Err)
			}
			continue
		}
		if r.Status != engine.Skipped || r.Err == nil || !strings.Contains(r.Err.Error(), "the run was stopped before this flow ran") {
			t.Errorf("issue #189: %s (not started at the cancel) = %s %v; want skipped: the run was stopped before this flow ran", name, r.Status, r.Err)
		}
	}
	for _, f := range rec.Finished() {
		if !isRunning[f.Job.Flow.Name()] && f.Status == engine.Failed {
			t.Errorf("issue #189: Finished reported %s failed (%v): the CLI would save its failure files on a device going away", f.Job.Flow.Name(), f.Err)
		}
	}
}

// issue #189: once the run is cancelled, the queue does not launch the flows still waiting
// for a device. Two devices, six flows: the first two run when the run is stopped; the four
// others never start, are not reported failed (their Finished hook would save failure files,
// waiting on devices that are going away) and are reported skipped.
func TestScheduleCancelLaunchesNoQueuedFlow(t *testing.T) {
	a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan string, 16)
	blockUntilCancel(ctx, entered, a, b)
	var jobs []*Job
	for i := 0; i < 6; i++ {
		jobs = append(jobs, job(typingFlow(t, fmt.Sprintf("f%d", i))))
	}
	rec := newRecorder(t)
	var res []Result
	done := make(chan struct{})
	go func() {
		defer close(done)
		res = Schedule(ctx, poolOf(a, b), jobs, 0, 0, false, rec.hooks())
	}()
	waitEntered(t, entered, 2)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Schedule did not return after the cancel")
	}
	checkStoppedRun(t, res, rec, len(a.Launched())+len(b.Launched()), 6, "f0", "f1")
}

// issue #189: the same in the ordered sequence (executionOrder with continueOnFailure): the
// ordered flows after the one running at the cancel, and the queue after them, never start.
func TestScheduleCancelLaunchesNoFurtherOrderedFlow(t *testing.T) {
	for _, continueSeq := range []bool{true, false} {
		t.Run(fmt.Sprintf("continueOnFailure=%v", continueSeq), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := newFake("d", "D", "android")
			entered := make(chan string, 16)
			blockUntilCancel(ctx, entered, d)
			var jobs []*Job
			for i := 0; i < 3; i++ {
				j := job(typingFlow(t, fmt.Sprintf("seq%d", i)))
				j.Seq = true
				jobs = append(jobs, j)
			}
			jobs = append(jobs, job(typingFlow(t, "q0")), job(typingFlow(t, "q1")))
			rec := newRecorder(t)
			var res []Result
			done := make(chan struct{})
			go func() {
				defer close(done)
				res = Schedule(ctx, poolOf(d), jobs, 0, 0, continueSeq, rec.hooks())
			}()
			waitEntered(t, entered, 1)
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Schedule did not return after the cancel")
			}
			checkStoppedRun(t, res, rec, len(d.Launched()), 5, "seq0")
		})
	}
}

// issue #189: a retry waiting for a device when the run is cancelled is not launched. The
// flow's result is its last attempt, failed with its own reason (it did run and fail), saying
// it was not retried; Finished, which reported that attempt, is not called again.
func TestScheduleCancelLaunchesNoQueuedRetry(t *testing.T) {
	for _, path := range []string{"queue", "sequence"} {
		t.Run(path, func(t *testing.T) {
			a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
			a.launchFails = 1 // flaky's first attempt fails on a
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan string, 16)
			blockUntilCancel(ctx, entered, b) // slow runs on b until the cancel
			a.onLaunch = nil
			flaky, slow := job(typingFlow(t, "flaky")), job(typingFlow(t, "slow"))
			rec := newRecorder(t)
			jobs := []*Job{flaky, slow}
			if path == "sequence" {
				// one at a time: flaky fails on a, and the run is stopped before its retry
				flaky.Seq = true
				jobs = []*Job{flaky}
			}
			rec.onFinish = func(r Result) {
				if r.Job.Flow.Name() == "flaky" && r.Job.Attempt == 0 {
					if path == "queue" {
						waitEntered(t, entered, 1) // slow holds b: the retry waits for a device
					}
					cancel()
				}
			}
			var res []Result
			within(t, 10*time.Second, func() {
				res = Schedule(ctx, poolOf(a, b), jobs, 2, 0, true, rec.hooks())
			})
			r := byName(res)["flaky"]
			if r.Status != engine.Failed || attemptOf(r) != 0 || r.Err == nil ||
				!strings.Contains(r.Err.Error(), "cannot launch") || !strings.Contains(r.Err.Error(), "not retried: the run was stopped") {
				t.Errorf("issue #189: flaky = %s on attempt %d: %v; want its failed attempt 0, with its own reason, not retried", r.Status, attemptOf(r), r.Err)
			}
			if n := len(a.Launched()) + len(b.Launched()); n != len(jobs) {
				t.Errorf("issue #189: %d launches; want %d: the retry was launched after the cancel", n, len(jobs))
			}
			n := 0
			for _, f := range rec.Finished() {
				if f.Job.Flow.Name() == "flaky" {
					n++
				}
			}
			if n != 1 {
				t.Errorf("issue #189: Finished ran %d times for flaky; want once (its attempt 0)", n)
			}
		})
	}
}

// issue #189: a run cancelled before any flow started launches none of them; they are all
// reported skipped.
func TestScheduleCancelledBeforeAnyFlow(t *testing.T) {
	d := newFake("d", "D", "android")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := newRecorder(t)
	var res []Result
	within(t, 10*time.Second, func() {
		res = Schedule(ctx, poolOf(d), []*Job{job(typingFlow(t, "x")), job(typingFlow(t, "y"))}, 0, 0, false, rec.hooks())
	})
	checkStoppedRun(t, res, rec, len(d.Launched()), 2)
}
