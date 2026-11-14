package orchestrate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
)

// startsOn records, per flow, the devices each of its attempts started on, and the run's
// notices.
type startsOn struct {
	mu      sync.Mutex
	on      map[string][]string
	notices []string
}

func watchStarts(rec *recorder, h *Hooks, then func(name, serial string)) *startsOn {
	s := &startsOn{on: map[string][]string{}}
	rec.onStart = func(j *Job, devs map[string]device.Info) {
		s.mu.Lock()
		s.on[j.Flow.Name()] = append(s.on[j.Flow.Name()], devs[""].Serial)
		s.mu.Unlock()
		if then != nil {
			then(j.Flow.Name(), devs[""].Serial)
		}
	}
	h.Notice = func(j *Job, d device.Info, msg string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.notices = append(s.notices, j.Flow.Name()+" "+d.Serial+": "+msg)
	}
	return s
}

func (s *startsOn) of(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.on[name]...)
}

func (s *startsOn) Notices() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.notices...)
}

// count is how many results name the flow (each flow is reported once, with its final outcome).
func count(rs []Result, name string) int {
	n := 0
	for _, r := range rs {
		if r.Job.Flow.Name() == name {
			n++
		}
	}
	return n
}

// Issue #268: a flow whose device turned out lost before it ran any step (the reopen failed)
// failed, though another device could run it. It must go back to the queue and run on the
// other device, without using one of its retries (none here), and its log must say it moved.
func TestScheduleLostDeviceMovesTheFlow(t *testing.T) {
	o := &reopener{err: errors.New("device offline")}
	o.install(t)
	dying, good := newFake("emu", "Pixel", "android"), newFake("good", "Good", "android")
	dying.dieOnLaunch = true
	release := make(chan struct{})
	var once sync.Once
	// Good holds b until c has started on the dead device, so c meets the lost connection.
	good.onType = func(string) {
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
	}
	rec := newRecorder(t)
	hooks, trace := tracedHooks(rec)
	starts := watchStarts(rec, &hooks, func(name, serial string) {
		if name == "c" && serial == "emu" {
			once.Do(func() { close(release) })
		}
	})
	jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b")), job(typingFlow(t, "c"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(dying, good), jobs, 0, 0, false, hooks) })
	got := byName(res)
	if on := starts.of("c"); len(on) == 0 || on[0] != "emu" {
		t.Fatalf("c started on %v, want it first on the dead device (the test's setup)", on)
	}
	if r := got["c"]; r.Status != engine.Passed || r.Devices[""].Serial != "good" || r.Job.Attempt != 0 {
		t.Errorf("issue #268: c = %s on %s (attempt %d) %v, want it moved to Good and passed, no retry used; results: %v",
			r.Status, r.Devices[""].Serial, r.Job.Attempt, r.Err, summarize(res))
	}
	if len(res) != 3 || count(res, "c") != 1 {
		t.Errorf("results %v, want each flow once", summarize(res))
	}
	finished := 0
	for _, r := range rec.Finished() {
		if r.Job.Flow.Name() == "c" {
			finished++
		}
	}
	if finished != 1 {
		t.Errorf("c was reported finished %d times, want once (its final outcome)", finished)
	}
	if tr := trace("c"); !strings.Contains(tr, "Pixel (emu): the connection to the device was lost") || !strings.Contains(tr, "moved to another device") {
		t.Errorf("c's log does not say it moved off the lost device:\n%s", tr)
	}
	moved := false
	for _, n := range starts.Notices() {
		moved = moved || (strings.HasPrefix(n, "c emu: ") && strings.Contains(n, "opening it again failed") && strings.Contains(n, "moved to another device"))
	}
	if !moved {
		t.Errorf("notices %q, want one saying c moved off emu", starts.Notices())
	}
}

// Issue #268: with no other device that fits, the flow still fails, with the same "lost"
// message as before.
func TestScheduleLostOnlyDeviceFails(t *testing.T) {
	o := &reopener{err: errors.New("device offline")}
	o.install(t)
	dying := newFake("emu", "Pixel", "android")
	dying.dieOnLaunch = true
	rec := newRecorder(t)
	hooks := rec.hooks()
	starts := watchStarts(rec, &hooks, nil)
	var res []Result
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), poolOf(dying), []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b"))}, 1, 0, false, hooks)
	})
	got := byName(res)
	r := got["b"]
	if r.Status != engine.Failed || r.Err == nil {
		t.Fatalf("b = %s %v, want it to fail: no device is left; results: %v", r.Status, r.Err, summarize(res))
	}
	for _, want := range []string{"Pixel", "lost", "browser connection closed: EOF", "opening it again failed", "device offline"} {
		if !strings.Contains(r.Err.Error(), want) {
			t.Errorf("message %q does not name %q", r.Err, want)
		}
	}
	if len(res) != 2 || count(res, "b") != 1 {
		t.Errorf("results %v, want each flow once", summarize(res))
	}
	for _, n := range starts.Notices() {
		if strings.Contains(n, "moved to another device") {
			t.Errorf("notice %q: no other device fits, nothing was moved", n)
		}
	}
}

// Issue #268: moving a flow off lost devices is bounded: each device is tried at most once
// per flow, and when every device that fits is lost the flow fails, without a retry.
func TestScheduleLostDeviceMovesAreBounded(t *testing.T) {
	o := &reopener{err: errors.New("device offline")}
	o.install(t)
	var drvs []*fakeDriver
	for _, s := range []string{"emu1", "emu2", "emu3"} {
		d := newFake(s, "Pixel "+s, "android")
		d.dieOnLaunch = true
		drvs = append(drvs, d)
	}
	rec := newRecorder(t)
	hooks := rec.hooks()
	starts := watchStarts(rec, &hooks, nil)
	// x1..x3 each kill a device; y comes after them, and meets every dead device
	jobs := []*Job{job(typingFlow(t, "x1")), job(typingFlow(t, "x2")), job(typingFlow(t, "x3")), job(typingFlow(t, "y"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(drvs...), jobs, 0, 0, false, hooks) })
	on := starts.of("y")
	seen := map[string]bool{}
	for _, s := range on {
		if seen[s] {
			t.Errorf("y started on %s twice (%v): each device is tried once per flow", s, on)
		}
		seen[s] = true
	}
	if len(on) != 3 {
		t.Errorf("issue #268: y started on %v, want each of the 3 devices once before it fails", on)
	}
	r := byName(res)["y"]
	if r.Status != engine.Failed || r.Err == nil || !strings.Contains(r.Err.Error(), "lost") || r.Job.Attempt != 0 {
		t.Errorf("y = %s (attempt %d) %v, want it to fail naming the lost device", r.Status, r.Job.Attempt, r.Err)
	}
	if len(res) != 4 || count(res, "y") != 1 {
		t.Errorf("results %v, want each flow once", summarize(res))
	}
}

// Issue #268: an ordered flow whose device turned out lost moves to another device too, and
// the sequence goes on.
func TestScheduleLostDeviceMovesAnOrderedFlow(t *testing.T) {
	o := &reopener{err: errors.New("device offline")}
	o.install(t)
	dying, good := newFake("emu", "Pixel", "android"), newFake("good", "Good", "android")
	dying.dieOnLaunch = true
	rec := newRecorder(t)
	hooks := rec.hooks()
	starts := watchStarts(rec, &hooks, nil)
	jobs := []*Job{seqJob(typingFlow(t, "a")), seqJob(typingFlow(t, "b")), seqJob(typingFlow(t, "c"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(dying, good), jobs, 0, 0, true, hooks) })
	if on := starts.of("b"); len(on) == 0 || on[0] != "emu" {
		t.Fatalf("b started on %v, want it first on the dead device (the test's setup)", on)
	}
	got := byName(res)
	if r := got["b"]; r.Status != engine.Passed || r.Devices[""].Serial != "good" || r.Job.Attempt != 0 {
		t.Errorf("issue #268: b = %s on %s %v, want it moved to Good and passed; results: %v",
			r.Status, r.Devices[""].Serial, r.Err, summarize(res))
	}
	if r := got["c"]; r.Status != engine.Passed {
		t.Errorf("c = %s %v, want it to pass", r.Status, r.Err)
	}
	if len(res) != 3 {
		t.Errorf("results %v, want each flow once", summarize(res))
	}
}

// Issue #268: a lost browser frees its slot under the cap of 8: the flow it held starts a
// ninth browser (the eighth live one) instead of failing.
func TestScheduleLostBrowserMovesTheFlowToANewBrowser(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	hold := func(string) {
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
	}
	var drvs []*fakeDriver
	for i := 1; i <= maxBrowsers; i++ {
		d := device.WebDevice(i)
		f := newFake(d.Serial, d.Name, "web")
		if i == 1 {
			f.dieOnLaunch = true
		} else {
			f.onType = hold // the other browsers stay busy
		}
		drvs = append(drvs, f)
	}
	t.Cleanup(connect.Override(device.Web, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		if i.Serial == drvs[0].serial {
			return nil, errors.New("Chrome did not start")
		}
		return newFake(i.Serial, i.Name, "web"), nil
	}))
	rec := newRecorder(t)
	hooks := rec.hooks()
	starts := watchStarts(rec, &hooks, func(name, serial string) {
		if name == "z" && serial != drvs[0].serial {
			once.Do(func() { close(release) })
		}
	})
	web := Need{Platform: string(device.Web)}
	var jobs []*Job
	for _, n := range []string{"f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "z"} {
		jobs = append(jobs, job(typingFlow(t, n), web))
	}
	var res []Result
	within(t, 15*time.Second, func() { res = Schedule(context.Background(), poolOf(drvs...), jobs, 0, 0, false, hooks) })
	if on := starts.of("z"); len(on) == 0 || on[0] != drvs[0].serial {
		t.Fatalf("z started on %v, want it first on the dead browser (the test's setup)", on)
	}
	if r := byName(res)["z"]; r.Status != engine.Passed || r.Devices[""].Serial != device.WebDevice(maxBrowsers+1).Serial {
		t.Errorf("issue #268: z = %s on %s %v, want it to pass on a new browser; results: %v",
			r.Status, r.Devices[""].Serial, r.Err, summarize(res))
	}
	if len(res) != len(jobs) {
		t.Errorf("results %v, want each flow once", summarize(res))
	}
}

// Issue #268: a cancelled run moves nothing: the flow that met the lost device as the run was
// stopped is reported once, not sent to another device.
func TestScheduleLostDeviceNotMovedOnceCancelled(t *testing.T) {
	o := &reopener{err: errors.New("device offline")}
	o.install(t)
	dying, good := newFake("emu", "Pixel", "android"), newFake("good", "Good", "android")
	dying.dieOnLaunch = true
	release := make(chan struct{})
	var once sync.Once
	good.onType = func(string) {
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := newRecorder(t)
	hooks := rec.hooks()
	starts := watchStarts(rec, &hooks, func(name, serial string) {
		if name == "c" && serial == "emu" {
			cancel() // stopped as c is about to open the dead device
			once.Do(func() { close(release) })
		}
	})
	jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b")), job(typingFlow(t, "c"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(ctx, poolOf(dying, good), jobs, 0, 0, false, hooks) })
	if on := starts.of("c"); len(on) != 1 || on[0] != "emu" {
		t.Errorf("c started on %v, want once, on emu: a stopped run moves nothing", on)
	}
	if r := byName(res)["c"]; r.Status == engine.Passed || count(res, "c") != 1 {
		t.Errorf("results %v, want c once, not passed", summarize(res))
	}
	for _, n := range starts.Notices() {
		if strings.Contains(n, "moved to another device") {
			t.Errorf("notice %q: the run was stopped, nothing moves", n)
		}
	}
}
