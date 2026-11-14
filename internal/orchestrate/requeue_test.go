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
	a := job(typingFlow(t, "a"))
	a.Attempt = 1 // a has had its retry, so b is the next flow on emu, and meets the lost connection
	var res []Result
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), poolOf(dying), []*Job{a, job(typingFlow(t, "b"))}, 1, 0, false, hooks)
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
	// b's attempt is final: with no device left, it is not retried (and reported again)
	finished := 0
	for _, fr := range rec.Finished() {
		if fr.Job.Flow.Name() == "b" {
			finished++
		}
	}
	if finished != 1 || r.Job.Attempt != 0 {
		t.Errorf("b was reported finished %d times (final attempt %d), want once, not retried", finished, r.Job.Attempt)
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
	// y never ran anywhere: its error (which the CLI writes to its log, as no log was opened)
	// keeps every device it moved off, not only the last one
	for _, s := range on {
		if r.Err == nil || !strings.Contains(r.Err.Error(), s) {
			t.Errorf("issue #268: y's error %q does not name %s, a device it moved off", r.Err, s)
		}
	}
	if r.Err == nil || strings.Count(r.Err.Error(), "moved to another device") != 2 {
		t.Errorf("issue #268: y's error %q, want its 2 moves noted", r.Err)
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
// stopped is reported once, not sent to another device. Stopped either as the flow started
// there (before driverForJob: the failed reopen is then the run's ending, and the device is
// not marked lost), or just after driverForJob marked the device lost.
func TestScheduleLostDeviceNotMovedOnceCancelled(t *testing.T) {
	for _, when := range []string{"as the flow starts", "once the device is marked lost"} {
		t.Run(when, func(t *testing.T) {
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
			stop := func() {
				cancel()
				once.Do(func() { close(release) })
			}
			pool := poolOf(dying, good)
			rec := newRecorder(t)
			hooks := rec.hooks()
			starts := watchStarts(rec, &hooks, func(name, serial string) {
				if name == "c" && serial == "emu" && when == "as the flow starts" {
					stop() // before c's driverForJob
				}
			})
			if when != "as the flow starts" {
				pool.onLost = func(string) { stop() }
			}
			jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b")), job(typingFlow(t, "c"))}
			var res []Result
			within(t, 10*time.Second, func() { res = Schedule(ctx, pool, jobs, 0, 0, false, hooks) })
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
		})
	}
}

// Issue #268: a retry that meets a lost device moves like a first attempt: no further retry
// is used, and the attempt before it (reported already) stays the flow's last attempt, which
// a cancel then reports (lastOf follows the move, issue #189).
func TestScheduleLostDeviceMovesARetry(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "runs elsewhere"
		if cancelled {
			name = "cancelled after the move"
		}
		t.Run(name, func(t *testing.T) {
			o := &reopener{err: errors.New("device offline")}
			o.install(t)
			dying, flaky := newFake("emu", "Pixel", "android"), newFake("flaky", "Flaky", "android")
			dying.dieOnLaunch = true
			flaky.launchFails = 1 // f's first attempt fails here; its retry avoids it, so goes to emu
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pool := NewPool([]device.Info{dying.info(), flaky.info()})
			pool.drivers["emu"], pool.drivers["flaky"] = dying, flaky
			rec := newRecorder(t)
			hooks := rec.hooks()
			var starts *startsOn
			starts = watchStarts(rec, &hooks, nil)
			notice := hooks.Notice
			hooks.Notice = func(j *Job, d device.Info, msg string) {
				notice(j, d, msg)
				if cancelled {
					cancel() // the moved retry is queued, not launched
				}
			}
			// a takes emu first and kills its connection
			aj := job(typingFlow(t, "a"), Need{Device: "emu"})
			aj.Attempt = 1 // a has had its retry: it fails once, on emu, and is not run again
			jobs := []*Job{aj, job(typingFlow(t, "f"))}
			var res []Result
			within(t, 10*time.Second, func() { res = Schedule(ctx, pool, jobs, 1, 0, false, hooks) })
			on := starts.of("f")
			if len(on) < 2 || on[0] != "flaky" || on[1] != "emu" {
				t.Fatalf("f started on %v, want flaky, then its retry on emu (the test's setup); results: %v", on, summarize(res))
			}
			r := byName(res)["f"]
			if count(res, "f") != 1 {
				t.Errorf("results %v, want f once", summarize(res))
			}
			if !cancelled {
				if r.Status != engine.Passed || r.Job.Attempt != 1 || r.Devices[""].Serial != "flaky" || len(on) != 3 {
					t.Errorf("f = %s (attempt %d) on %s, started on %v; want its retry moved off emu and passed on flaky",
						r.Status, r.Job.Attempt, r.Devices[""].Serial, on)
				}
				return
			}
			// the retry never ran: f's result is its first attempt, which did run and fail
			if r.Status != engine.Failed || r.Job.Attempt != 0 || r.Err == nil ||
				!strings.Contains(r.Err.Error(), "Flaky cannot launch") || !strings.Contains(r.Err.Error(), errNotRetried.Error()) {
				t.Errorf("f = %s (attempt %d) %v, want its first attempt's failure, not retried", r.Status, r.Job.Attempt, r.Err)
			}
			finished := 0
			for _, fr := range rec.Finished() {
				if fr.Job.Flow.Name() == "f" {
					finished++
				}
			}
			if finished != 1 {
				t.Errorf("f was reported finished %d times, want once (its first attempt)", finished)
			}
		})
	}
}

// Issue #268: a multi-device flow one of whose roles meets a lost device moves as a whole: the
// devices of its other roles are given back, and it runs on devices still working.
func TestScheduleLostDeviceMovesAMultiDeviceFlow(t *testing.T) {
	o := &reopener{err: errors.New("device offline")}
	o.install(t)
	dying, g1, g2 := newFake("emu", "Pixel", "android"), newFake("g1", "Good 1", "android"), newFake("g2", "Good 2", "android")
	dying.dieOnLaunch = true
	rec := newRecorder(t)
	hooks := rec.hooks()
	var mu sync.Mutex
	var starts [][]string // m's attempts: the serials of alice and bob
	rec.onStart = func(j *Job, devs map[string]device.Info) {
		if j.Flow.Name() == "m" {
			mu.Lock()
			starts = append(starts, []string{devs["alice"].Serial, devs["bob"].Serial})
			mu.Unlock()
		}
	}
	m := parseFlow(t, "m", twoRoles+"- alice: [{inputText: hi}]\n- bob: [{inputText: there}]\n")
	mj := job(m, NeedsFor(m, "", nil)...)
	// a, an ordered flow, runs first, on emu, and kills its connection; m then takes emu for
	// alice (the first device that fits) and g1 for bob
	aj := seqJob(typingFlow(t, "a"))
	aj.Needs = []Need{{Device: "emu"}}
	var res []Result
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), poolOf(dying, g1, g2), []*Job{aj, mj}, 0, 0, true, hooks)
	})
	mu.Lock()
	got := append([][]string(nil), starts...)
	mu.Unlock()
	if len(got) == 0 || (got[0][0] != "emu" && got[0][1] != "emu") {
		t.Fatalf("m started on %v, want its first attempt to hold emu (the test's setup)", got)
	}
	r := byName(res)["m"]
	if r.Status != engine.Passed || r.Job.Attempt != 0 || len(got) != 2 {
		t.Fatalf("issue #268: m = %s (attempt %d) %v, started on %v; want it moved once and passed", r.Status, r.Job.Attempt, r.Err, got)
	}
	used := map[string]bool{r.Devices["alice"].Serial: true, r.Devices["bob"].Serial: true}
	if !used["g1"] || !used["g2"] {
		t.Errorf("m ran on %v, want g1 and g2: the role that kept a working device gave it back", DeviceLabel(r.Devices))
	}
	if g1.Typed() == nil && g2.Typed() == nil {
		t.Errorf("nothing was typed on g1 or g2")
	}
	if count(res, "m") != 1 {
		t.Errorf("results %v, want m once", summarize(res))
	}
}
