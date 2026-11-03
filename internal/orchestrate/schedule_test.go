package orchestrate

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
)

func names(n int, prefix string) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, prefix+string(rune('a'+i)))
	}
	return out
}

// Every job runs once and is reported once, with Started before Finished.
func TestScheduleRunsEveryJob(t *testing.T) {
	a, b, c := newFake("a", "A", "android"), newFake("b", "B", "android"), newFake("c", "C", "android")
	pool := poolOf(a, b, c)
	rec := newRecorder(t)
	var jobs []*Job
	for _, n := range names(6, "flow-") {
		jobs = append(jobs, job(typingFlow(t, n)))
	}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), pool, jobs, 0, 0, false, rec.hooks()) })
	if len(res) != 6 {
		t.Fatalf("%d results, want 6", len(res))
	}
	for _, r := range res {
		if r.Status != engine.Passed {
			t.Errorf("%s: %s %v", r.Job.Flow.Name(), r.Status, r.Err)
		}
		if r.Runner == nil || len(r.Steps) != 2 || r.Start.IsZero() {
			t.Errorf("%s: runner=%v steps=%d start=%v", r.Job.Flow.Name(), r.Runner, len(r.Steps), r.Start)
		}
		if len(r.Devices) != 1 || r.Devices[""].Serial == "" {
			t.Errorf("%s: devices %v", r.Job.Flow.Name(), r.Devices)
		}
	}
	if len(rec.Started()) != 6 || len(rec.Finished()) != 6 {
		t.Errorf("started %d finished %d", len(rec.Started()), len(rec.Finished()))
	}
	// each flow typed its name on exactly the device its result names
	for _, r := range res {
		drv := map[string]*fakeDriver{"a": a, "b": b, "c": c}[r.Devices[""].Serial]
		found := false
		for _, s := range drv.Typed() {
			found = found || s == r.Job.Flow.Name()
		}
		if !found {
			t.Errorf("%s did not run on %s", r.Job.Flow.Name(), r.Devices[""].Serial)
		}
	}
	// and the app was launched once per flow, across devices
	total := len(a.Launched()) + len(b.Launched()) + len(c.Launched())
	if total != 6 {
		t.Errorf("%d launches, want 6", total)
	}
}

// No jobs: nothing to report, no hooks called.
func TestScheduleNoJobs(t *testing.T) {
	rec := newRecorder(t)
	if res := Schedule(context.Background(), poolOf(newFake("a", "A", "android")), nil, 0, 0, false, rec.hooks()); len(res) != 0 {
		t.Errorf("results %v", res)
	}
	if len(rec.Started())+len(rec.Finished()) != 0 {
		t.Error("hooks called with no jobs")
	}
}

// maxParallel caps how many jobs run at once, even with more free devices.
func TestScheduleMaxParallel(t *testing.T) {
	g := newGauge()
	var drvs []*fakeDriver
	for _, s := range []string{"a", "b", "c"} {
		d := newFake(s, strings.ToUpper(s), "android")
		d.onType = func(string) { g.enter(); time.Sleep(15 * time.Millisecond); g.exit() }
		drvs = append(drvs, d)
	}
	var jobs []*Job
	for _, n := range names(6, "f") {
		jobs = append(jobs, job(typingFlow(t, n)))
	}
	res := Schedule(context.Background(), poolOf(drvs...), jobs, 0, 1, false, newRecorder(t).hooks())
	if len(res) != 6 {
		t.Fatalf("%d results", len(res))
	}
	if g.Max() != 1 {
		t.Errorf("max concurrency %d, want 1", g.Max())
	}
}

// maxParallel 0 means one job per device, all at the same time.
func TestScheduleParallelPerDevice(t *testing.T) {
	g := newGauge()
	var drvs []*fakeDriver
	for _, s := range []string{"a", "b"} {
		d := newFake(s, strings.ToUpper(s), "android")
		d.onType = func(string) {
			g.enter()
			g.waitFor(2, 5*time.Second) // both devices must be busy at once
			g.exit()
		}
		drvs = append(drvs, d)
	}
	jobs := []*Job{job(typingFlow(t, "one")), job(typingFlow(t, "two"))}
	var res []Result
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), poolOf(drvs...), jobs, 0, 0, false, newRecorder(t).hooks())
	})
	if g.Max() != 2 {
		t.Errorf("max concurrency %d, want 2", g.Max())
	}
	for _, r := range res {
		if r.Status != engine.Passed {
			t.Errorf("%s: %v", r.Job.Flow.Name(), r.Err)
		}
	}
}

// A work queue, not a fixed split: while one device is stuck on a slow flow, the other
// device takes every remaining job.
func TestScheduleWorkQueue(t *testing.T) {
	release := make(chan struct{})
	slow, fast := newFake("slow", "Slow", "android"), newFake("fast", "Fast", "android")
	slow.onType = func(string) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}
	rec := newRecorder(t)
	fastDone := 0
	rec.onFinish = func(r Result) {
		if r.Devices[""].Serial == "fast" {
			fastDone++
			if fastDone == 4 {
				close(release)
			}
		}
	}
	var jobs []*Job
	for _, n := range names(5, "q") {
		jobs = append(jobs, job(typingFlow(t, n)))
	}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(slow, fast), jobs, 0, 0, false, rec.hooks()) })
	count := map[string]int{}
	for _, r := range res {
		count[r.Devices[""].Serial]++
	}
	if count["slow"] != 1 || count["fast"] != 4 {
		t.Errorf("jobs per device %v, want slow=1 fast=4", count)
	}
	// the last result is the slow one: it was released only after the fast device emptied the queue
	if res[len(res)-1].Devices[""].Serial != "slow" {
		t.Errorf("last finished on %s", res[len(res)-1].Devices[""].Serial)
	}
}

// A failed job is retried up to `retries` times; Finished sees every attempt, the results
// hold only the final one.
func TestScheduleRetriesUntilLimit(t *testing.T) {
	bad := newFake("bad", "Bad", "android")
	bad.launchFails = -1
	rec := newRecorder(t)
	f := typingFlow(t, "flaky")
	orig := job(f)
	res := Schedule(context.Background(), poolOf(bad), []*Job{orig}, 2, 0, false, rec.hooks())
	if len(res) != 1 {
		t.Fatalf("%d results, want 1", len(res))
	}
	r := res[0]
	if r.Status != engine.Failed || r.Job.Attempt != 2 || r.Job.Flow != f {
		t.Errorf("final: status=%s attempt=%d", r.Status, r.Job.Attempt)
	}
	if r.Err == nil || !strings.Contains(r.Err.Error(), "Unable to launch app com.app") {
		t.Errorf("err = %v", r.Err)
	}
	var attempts []int
	for _, fr := range rec.Finished() {
		attempts = append(attempts, fr.Job.Attempt)
	}
	if !reflect.DeepEqual(attempts, []int{0, 1, 2}) {
		t.Errorf("Finished attempts %v, want [0 1 2]", attempts)
	}
	if orig.Attempt != 0 {
		t.Errorf("the caller's job was modified: attempt %d", orig.Attempt)
	}
	if len(rec.Started()) != 3 {
		t.Errorf("Started %d times, want 3", len(rec.Started()))
	}
	if n := len(bad.Launched()); n != 3 {
		t.Errorf("%d launches, want 3", n)
	}
}

// A flaky job that passes on retry is reported as passed.
func TestScheduleRetryPasses(t *testing.T) {
	d := newFake("d", "D", "android")
	d.launchFails = 1 // only the first launch fails
	res := Schedule(context.Background(), poolOf(d), []*Job{job(typingFlow(t, "flaky"))}, 1, 0, false, newRecorder(t).hooks())
	if len(res) != 1 || res[0].Status != engine.Passed || res[0].Job.Attempt != 1 {
		t.Fatalf("results %+v", res)
	}
}

// No retries: a failure is final.
func TestScheduleNoRetries(t *testing.T) {
	d := newFake("d", "D", "android")
	d.launchFails = 1
	rec := newRecorder(t)
	res := Schedule(context.Background(), poolOf(d), []*Job{job(typingFlow(t, "once"))}, 0, 0, false, rec.hooks())
	if len(res) != 1 || res[0].Status != engine.Failed || len(rec.Finished()) != 1 {
		t.Fatalf("results %+v finished %d", res, len(rec.Finished()))
	}
}

// A retry goes to another device when one is free while other jobs run.
func TestScheduleRetryMovesToAnotherDevice(t *testing.T) {
	bad, good, slow := newFake("bad", "Bad", "android"), newFake("good", "Good", "android"), newFake("slow", "Slow", "android")
	goodDone, retryDone := make(chan struct{}), make(chan struct{})
	bad.launchFails = -1
	bad.onLaunch = func(string) { <-goodDone } // fail only after good's first job finished
	slow.onType = func(string) {               // keep a job running until the retry is done
		select {
		case <-retryDone:
		case <-time.After(5 * time.Second):
		}
	}
	rec := newRecorder(t)
	var once, onceRetry sync.Once
	rec.onFinish = func(r Result) {
		if r.Devices[""].Serial == "good" && r.Job.Attempt == 0 {
			once.Do(func() { close(goodDone) })
		}
		if r.Job.Attempt == 1 {
			onceRetry.Do(func() { close(retryDone) })
		}
	}
	jobs := []*Job{job(typingFlow(t, "j0")), job(typingFlow(t, "j1")), job(typingFlow(t, "j2"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(bad, good, slow), jobs, 1, 0, false, rec.hooks()) })
	got := byName(res)["j0"]
	if got.Job == nil || got.Job.Attempt != 1 {
		t.Fatalf("j0 = %+v", got)
	}
	if got.Devices[""].Serial != "good" || got.Status != engine.Passed {
		t.Errorf("retry ran on %s (%s), want good", got.Devices[""].Serial, got.Status)
	}
}

// With every device idle, a retry should still prefer a device other than the one that
// failed it ("on another device when one is free").
func TestScheduleRetryAvoidsFailedDeviceWhenIdle(t *testing.T) {
	bad, good := newFake("bad", "Bad", "android"), newFake("good", "Good", "android")
	bad.launchFails = -1
	res := Schedule(context.Background(), poolOf(bad, good), []*Job{job(typingFlow(t, "j"))}, 1, 0, false, newRecorder(t).hooks())
	if len(res) != 1 {
		t.Fatalf("%d results", len(res))
	}
	if res[0].Devices[""].Serial != "good" || res[0].Status != engine.Passed {
		t.Errorf("retry ran on %s (%s), want good", res[0].Devices[""].Serial, res[0].Status)
	}
}

// A cancelled run is not retried.
func TestScheduleNoRetryAfterCancel(t *testing.T) {
	d := newFake("d", "D", "android")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := newRecorder(t)
	res := Schedule(ctx, poolOf(d), []*Job{job(typingFlow(t, "x"))}, 3, 0, false, rec.hooks())
	if len(res) != 1 || res[0].Status != engine.Failed || res[0].Job.Attempt != 0 {
		t.Fatalf("results %+v", res)
	}
	if len(rec.Finished()) != 1 {
		t.Errorf("finished %d times", len(rec.Finished()))
	}
	if len(d.Launched()) != 0 {
		t.Error("a cancelled flow touched the device")
	}
}

// A job no device can ever serve fails at once with the reason; it never starts, and the
// other jobs still run.
func TestScheduleUnsatisfiableJob(t *testing.T) {
	d := newFake("d", "D", "android")
	rec := newRecorder(t)
	jobs := []*Job{job(typingFlow(t, "ios-only"), Need{Platform: "ios"}), job(typingFlow(t, "fine"))}
	res := byName(Schedule(context.Background(), poolOf(d), jobs, 3, 0, false, rec.hooks()))
	r := res["ios-only"]
	if r.Status != engine.Failed || r.Err == nil || !strings.Contains(r.Err.Error(), "needs an iOS simulator") {
		t.Errorf("ios-only = %s %v", r.Status, r.Err)
	}
	if r.Job.Attempt != 0 {
		t.Error("an unsatisfiable job should not be retried")
	}
	if res["fine"].Status != engine.Passed {
		t.Errorf("fine = %s %v", res["fine"].Status, res["fine"].Err)
	}
	if got := rec.Started(); !reflect.DeepEqual(got, []string{"fine"}) {
		t.Errorf("started %v", got)
	}
}

// A job whose device's driver will not open fails with the device name and the reason.
func TestScheduleDriverOpenFails(t *testing.T) {
	info := device.Info{Platform: "fake", Serial: "f1", Name: "Fakephone"}
	pool := NewPool([]device.Info{info})
	res := Schedule(context.Background(), pool, []*Job{job(typingFlow(t, "x"))}, 0, 0, false, newRecorder(t).hooks())
	if len(res) != 1 || res[0].Status != engine.Failed {
		t.Fatalf("results %+v", res)
	}
	if got := res[0].Err.Error(); got != "Fakephone: fake devices are not supported yet" {
		t.Errorf("err = %q", got)
	}
	if res[0].Runner != nil {
		t.Error("no runner should be made without a driver")
	}
}

// More browsers than can be started: the job is satisfiable on paper but never fits, so it
// fails instead of hanging.
func TestScheduleTooManyBrowsers(t *testing.T) {
	var needs []Need
	for _, n := range names(9, "r") {
		needs = append(needs, Need{Role: n, Platform: "web"})
	}
	pool := NewPool([]device.Info{chrome})
	var res []Result
	within(t, 5*time.Second, func() {
		res = Schedule(context.Background(), pool, []*Job{job(typingFlow(t, "crowd"), needs...)}, 0, 0, false, newRecorder(t).hooks())
	})
	if len(res) != 1 || res[0].Status != engine.Failed || !strings.Contains(res[0].Err.Error(), "no device could run it") {
		t.Fatalf("results %+v", res)
	}
}

// Prepare runs once per device, before its first flow, with that device's driver.
func TestSchedulePrepareOncePerDevice(t *testing.T) {
	a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
	rec := newRecorder(t)
	var jobs []*Job
	for _, n := range names(6, "p") {
		jobs = append(jobs, job(typingFlow(t, n)))
	}
	Schedule(context.Background(), poolOf(a, b), jobs, 0, 0, false, rec.hooks())
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !reflect.DeepEqual(rec.prepared, map[string]int{"a": 1, "b": 1}) {
		t.Errorf("prepared %v", rec.prepared)
	}
}

// A nil Prepare hook is allowed.
func TestScheduleNilPrepare(t *testing.T) {
	h := newRecorder(t).hooks()
	h.Prepare = nil
	res := Schedule(context.Background(), poolOf(newFake("a", "A", "android")), []*Job{job(typingFlow(t, "x"))}, 0, 0, false, h)
	if len(res) != 1 || res[0].Status != engine.Passed {
		t.Fatalf("results %+v", res)
	}
}

// The Options hook's engine options reach the flow (here, its env).
func TestScheduleOptionsReachFlow(t *testing.T) {
	d := newFake("d", "D", "android")
	rec := newRecorder(t)
	rec.env = []flow.KV{{Key: "WHO", Value: "from-options"}}
	var gotDevs map[string]device.Info
	h := rec.hooks()
	inner := h.Options
	h.Options = func(j *Job, devs map[string]device.Info) engine.Options {
		gotDevs = devs
		return inner(j, devs)
	}
	f := parseFlow(t, "env", "appId: com.app\n---\n- inputText: ${WHO}\n")
	Schedule(context.Background(), poolOf(d), []*Job{job(f)}, 0, 0, false, h)
	if got := d.Typed(); !reflect.DeepEqual(got, []string{"from-options"}) {
		t.Errorf("typed %v", got)
	}
	if gotDevs[""].Serial != "d" {
		t.Errorf("Options got devices %v", gotDevs)
	}
}

// Multi-device jobs start first: they need the most devices at once.
func TestScheduleMultiDeviceFirst(t *testing.T) {
	a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
	multi := parseFlow(t, "multi", "appId: com.app\ndevices:\n  alice: android\n  bob: android\n---\n- alice: [{inputText: m-alice}]\n- bob: [{inputText: m-bob}]\n")
	jobs := []*Job{
		job(typingFlow(t, "single1")),
		job(multi, NeedsFor(multi, "", nil)...),
		job(typingFlow(t, "single2")),
	}
	rec := newRecorder(t)
	res := Schedule(context.Background(), poolOf(a, b), jobs, 0, 0, false, rec.hooks())
	if got := rec.Started(); len(got) != 3 || got[0] != "multi" {
		t.Fatalf("start order %v, want multi first", got)
	}
	m := byName(res)["multi"]
	if m.Status != engine.Passed {
		t.Fatalf("multi: %v", m.Err)
	}
	if len(m.Devices) != 2 || m.Devices["alice"].Serial == m.Devices["bob"].Serial {
		t.Errorf("multi devices %v", m.Devices)
	}
	if m.Runner != nil || m.Steps != nil {
		t.Error("multi-device results carry no single runner or steps")
	}
	if m.Duration <= 0 {
		t.Error("multi duration not set")
	}
	// each role's commands ran on the device the result names for it
	drv := map[string]*fakeDriver{"a": a, "b": b}
	for role, want := range map[string]string{"alice": "m-alice", "bob": "m-bob"} {
		typed := drv[m.Devices[role].Serial].Typed()
		ok := false
		for _, s := range typed {
			ok = ok || s == want
		}
		if !ok {
			t.Errorf("%s's device typed %v, want %q", role, typed, want)
		}
	}
}

// A multi-device job waits until enough devices are free together.
func TestScheduleMultiWaitsForDevices(t *testing.T) {
	a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
	multi := parseFlow(t, "multi", "appId: com.app\ndevices:\n  alice: android\n  bob: android\n---\n- alice: [{inputText: x}]\n")
	g := newGauge()
	for _, d := range []*fakeDriver{a, b} {
		d.onType = func(string) { g.enter(); time.Sleep(10 * time.Millisecond); g.exit() }
	}
	// singles first in the list; the sort puts the multi job first anyway, and then the
	// singles run after it frees both devices
	jobs := []*Job{job(typingFlow(t, "s1")), job(typingFlow(t, "s2")), job(multi, NeedsFor(multi, "", nil)...)}
	res := Schedule(context.Background(), poolOf(a, b), jobs, 0, 0, false, newRecorder(t).hooks())
	for _, r := range res {
		if r.Status != engine.Passed {
			t.Errorf("%s: %v", r.Job.Flow.Name(), r.Err)
		}
	}
	if len(res) != 3 {
		t.Errorf("%d results", len(res))
	}
}

func seqJob(f *flow.Flow) *Job { j := job(f); j.Seq = true; return j }

// Ordered (executionOrder) flows run first, one at a time, in their order; the rest follow.
func TestScheduleSequenceFirstInOrder(t *testing.T) {
	g := newGauge()
	a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
	for _, d := range []*fakeDriver{a, b} {
		d.onType = func(string) { g.enter(); time.Sleep(10 * time.Millisecond); g.exit() }
	}
	jobs := []*Job{
		job(typingFlow(t, "rest1")),
		seqJob(typingFlow(t, "seq1")),
		seqJob(typingFlow(t, "seq2")),
		job(typingFlow(t, "rest2")),
		seqJob(typingFlow(t, "seq3")),
	}
	rec := newRecorder(t)
	var seqMax int
	rec.onFinish = func(r Result) {
		if r.Job.Seq {
			seqMax = max(seqMax, g.Max())
		}
	}
	res := Schedule(context.Background(), poolOf(a, b), jobs, 0, 0, false, rec.hooks())
	got := rec.Started()
	if !reflect.DeepEqual(got[:3], []string{"seq1", "seq2", "seq3"}) {
		t.Errorf("start order %v", got)
	}
	if seqMax != 1 {
		t.Errorf("ordered flows ran %d at once", seqMax)
	}
	if len(res) != 5 {
		t.Errorf("%d results", len(res))
	}
	for _, r := range res {
		if r.Status != engine.Passed {
			t.Errorf("%s: %v", r.Job.Flow.Name(), r.Err)
		}
	}
}

// An ordered flow that fails stops the run: every flow after it is reported skipped.
func TestScheduleSequenceFailureSkipsRest(t *testing.T) {
	d := newFake("d", "D", "android")
	d.launchFails = 1 // the first flow to launch fails
	rec := newRecorder(t)
	jobs := []*Job{seqJob(typingFlow(t, "seq1")), job(typingFlow(t, "rest1")), job(typingFlow(t, "rest2"))}
	res := byName(Schedule(context.Background(), poolOf(d), jobs, 0, 0, false, rec.hooks()))
	if res["seq1"].Status != engine.Failed {
		t.Errorf("seq1 = %s", res["seq1"].Status)
	}
	for _, n := range []string{"rest1", "rest2"} {
		r := res[n]
		if r.Status != engine.Skipped || r.Err == nil || !strings.Contains(r.Err.Error(), "an ordered flow failed") {
			t.Errorf("%s = %s %v", n, r.Status, r.Err)
		}
	}
	if got := rec.Started(); !reflect.DeepEqual(got, []string{"seq1"}) {
		t.Errorf("started %v", got)
	}
	if len(rec.Finished()) != 3 {
		t.Errorf("finished %d, want 3", len(rec.Finished()))
	}
}

// The ordered flows after a failed one are reported too (skipped), not dropped.
func TestScheduleSequenceFailureReportsLaterOrderedFlows(t *testing.T) {
	d := newFake("d", "D", "android")
	d.launchFails = 1
	jobs := []*Job{seqJob(typingFlow(t, "seq1")), seqJob(typingFlow(t, "seq2")), job(typingFlow(t, "rest"))}
	res := byName(Schedule(context.Background(), poolOf(d), jobs, 0, 0, false, newRecorder(t).hooks()))
	if len(res) != 3 {
		t.Fatalf("%d results, want 3 (every job reported)", len(res))
	}
	if res["seq2"].Status != engine.Skipped {
		t.Errorf("seq2 = %s", res["seq2"].Status)
	}
}

// continueOnFailure: an ordered flow's failure does not stop the others.
func TestScheduleSequenceContinueOnFailure(t *testing.T) {
	d := newFake("d", "D", "android")
	d.launchFails = 1
	jobs := []*Job{seqJob(typingFlow(t, "seq1")), seqJob(typingFlow(t, "seq2")), job(typingFlow(t, "rest"))}
	res := byName(Schedule(context.Background(), poolOf(d), jobs, 0, 0, true, newRecorder(t).hooks()))
	want := map[string]engine.Status{"seq1": engine.Failed, "seq2": engine.Passed, "rest": engine.Passed}
	for n, st := range want {
		if res[n].Status != st {
			t.Errorf("%s = %s, want %s", n, res[n].Status, st)
		}
	}
}

// An ordered flow no device can run is reported failed without starting.
func TestScheduleSequenceUnsatisfiable(t *testing.T) {
	d := newFake("d", "D", "android")
	rec := newRecorder(t)
	jobs := []*Job{seqJob(typingFlow(t, "needs-ios")), seqJob(typingFlow(t, "seq2"))}
	jobs[0].Needs = []Need{{Platform: "ios"}}
	res := byName(Schedule(context.Background(), poolOf(d), jobs, 0, 0, true, rec.hooks()))
	if r := res["needs-ios"]; r.Status != engine.Failed || !strings.Contains(r.Err.Error(), "iOS simulator") {
		t.Errorf("needs-ios = %s %v", r.Status, r.Err)
	}
	if res["seq2"].Status != engine.Passed {
		t.Errorf("seq2 = %s", res["seq2"].Status)
	}
	if got := rec.Started(); !reflect.DeepEqual(got, []string{"seq2"}) {
		t.Errorf("started %v", got)
	}
}

// Without continueOnFailure, an ordered flow that cannot run at all stops the sequence like
// any other failed ordered flow.
func TestScheduleSequenceUnsatisfiableStops(t *testing.T) {
	d := newFake("d", "D", "android")
	jobs := []*Job{seqJob(typingFlow(t, "needs-ios")), job(typingFlow(t, "rest"))}
	jobs[0].Needs = []Need{{Platform: "ios"}}
	res := byName(Schedule(context.Background(), poolOf(d), jobs, 0, 0, false, newRecorder(t).hooks()))
	if res["rest"].Status != engine.Skipped {
		t.Errorf("rest = %s, want skipped", res["rest"].Status)
	}
}

// withAvoid pins a single-device retry to another fitting device; other needs pass through.
func TestWithAvoid(t *testing.T) {
	pool := NewPool([]device.Info{pixel, iphone, galaxy})
	cases := []struct {
		name  string
		needs []Need
		avoid string
		want  []Need
	}{
		{"picks the next fitting device", []Need{{Platform: "android"}}, pixel.Serial, []Need{{Platform: "android", Device: galaxy.Serial}}},
		{"any platform takes the first other device", []Need{{}}, pixel.Serial, []Need{{Device: iphone.Serial}}},
		{"keeps the app id", []Need{{AppID: "com.app"}}, pixel.Serial, []Need{{AppID: "com.app", Device: iphone.Serial}}},
		{"no other fitting device", []Need{{Platform: "ios"}}, iphone.Serial, []Need{{Platform: "ios"}}},
		{"pinned need unchanged", []Need{{Device: pixel.Serial}}, pixel.Serial, []Need{{Device: pixel.Serial}}},
		{"multi-device needs unchanged", []Need{{Role: "a"}, {Role: "b"}}, pixel.Serial, []Need{{Role: "a"}, {Role: "b"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withAvoid(c.needs, c.avoid, pool); !reflect.DeepEqual(got, c.want) {
				t.Errorf("withAvoid = %+v, want %+v", got, c.want)
			}
		})
	}
}

// DeviceLabel is the device name for single-device runs and sorted role=name pairs otherwise.
func TestDeviceLabel(t *testing.T) {
	cases := []struct {
		name string
		devs map[string]device.Info
		want string
	}{
		{"single", map[string]device.Info{"": pixel}, "Pixel 7"},
		{"roles sorted", map[string]device.Info{"bob": pixel, "alice": iphone}, "alice=iPhone 17, bob=Pixel 7"},
		{"one role", map[string]device.Info{"alice": iphone}, "alice=iPhone 17"},
		{"none", map[string]device.Info{}, ""},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeviceLabel(c.devs); got != c.want {
				t.Errorf("DeviceLabel = %q, want %q", got, c.want)
			}
		})
	}
}

// Results come back for every job even when many jobs race for few devices.
func TestScheduleManyJobsFewDevices(t *testing.T) {
	var drvs []*fakeDriver
	for _, s := range []string{"a", "b", "c"} {
		drvs = append(drvs, newFake(s, s, "android"))
	}
	var jobs []*Job
	for i := 0; i < 40; i++ {
		jobs = append(jobs, job(typingFlow(t, fmt.Sprintf("j%02d", i))))
	}
	var res []Result
	within(t, 20*time.Second, func() {
		res = Schedule(context.Background(), poolOf(drvs...), jobs, 0, 2, false, newRecorder(t).hooks())
	})
	if len(res) != 40 {
		t.Fatalf("%d results", len(res))
	}
	var got []string
	for _, r := range res {
		got = append(got, r.Job.Flow.Name())
	}
	sort.Strings(got)
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Errorf("%s reported twice", got[i])
		}
	}
}

// Device locks: a device another process holds (another tb test, an agent's session) is
// passed over for a free one; a job only held devices can run fails naming the holder, or
// waits for it with WaitForDevice. The run's own locks are freed when the pool closes.
func TestPoolLocks(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	other, err := devlock.TryAcquire("a", "tb session alice")
	if err != nil {
		t.Fatal(err)
	}

	// Passed over: the free device runs the job.
	a, b := newFake("a", "A", "android"), newFake("b", "B", "android")
	pool := poolOf(a, b)
	var res []Result
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), pool, []*Job{job(typingFlow(t, "f1"))}, 0, 0, false, newRecorder(t).hooks())
	})
	if res[0].Status != engine.Passed || res[0].Devices[""].Serial != "b" {
		t.Errorf("free device: %s on %v (%v)", res[0].Status, res[0].Devices, res[0].Err)
	}
	// While this run holds b, another process cannot take it.
	if _, err := devlock.TryAcquire("b", "tb test"); err == nil {
		t.Error("the run's device was not locked")
	}
	pool.Close()
	if l, err := devlock.TryAcquire("b", "tb test"); err != nil {
		t.Errorf("after Close: %v", err)
	} else {
		l.Release()
	}

	// Only the held device fits (pinned): fails at once, naming the holder.
	pool = poolOf(newFake("a", "A", "android"))
	j := job(typingFlow(t, "f2"))
	j.Needs = []Need{{Device: "a"}}
	start := time.Now()
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), pool, []*Job{j}, 0, 0, false, newRecorder(t).hooks())
	})
	if res[0].Status != engine.Failed || res[0].Err == nil ||
		!strings.Contains(res[0].Err.Error(), "A (a) is in use by tb session alice (pid ") ||
		!strings.Contains(res[0].Err.Error(), "--wait-for-device") {
		t.Errorf("held device: %s %v", res[0].Status, res[0].Err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("failing took %s, want at once", time.Since(start))
	}
	pool.Close()

	// With a wait: the holder lets go, and the job runs.
	pool = poolOf(newFake("a", "A", "android"))
	pool.WaitForDevice = 5 * time.Second
	go func() {
		time.Sleep(400 * time.Millisecond)
		other.Release()
	}()
	j = job(typingFlow(t, "f3"))
	j.Needs = []Need{{Device: "a"}}
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), pool, []*Job{j}, 0, 0, false, newRecorder(t).hooks())
	})
	if res[0].Status != engine.Passed {
		t.Errorf("after waiting: %s %v", res[0].Status, res[0].Err)
	}
	pool.Close()

	// The ordered sequence waits and reports the same way.
	held, _ := devlock.TryAcquire("a", "tb test other/")
	defer held.Release()
	pool = poolOf(newFake("a", "A", "android"))
	j = job(typingFlow(t, "f4"))
	j.Needs = []Need{{Device: "a"}}
	j.Seq = true
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), pool, []*Job{j}, 0, 0, false, newRecorder(t).hooks())
	})
	if res[0].Status != engine.Failed || !strings.Contains(fmt.Sprint(res[0].Err), "in use by tb test other/") {
		t.Errorf("sequence: %s %v", res[0].Status, res[0].Err)
	}
	pool.Close()
}
