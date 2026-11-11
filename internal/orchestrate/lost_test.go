package orchestrate

import (
	"bytes"
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

// reopener stands in for connect.Open on Android devices: each open makes a fresh fake for
// the device (or fails with err), and the test can see how many were made.
type reopener struct {
	mu     sync.Mutex
	err    error
	opened []*fakeDriver
}

func (o *reopener) install(t *testing.T) {
	t.Helper()
	t.Cleanup(connect.Override(device.Android, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.err != nil {
			return nil, o.err
		}
		f := newFake(i.Serial, i.Name, string(i.Platform))
		o.opened = append(o.opened, f)
		return f, nil
	}))
}

func (o *reopener) Opened() []*fakeDriver {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*fakeDriver(nil), o.opened...)
}

// lockedBuffer is a trace's writer the test reads once the run is over.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// tracedHooks are rec's hooks with one trace per flow, by flow name.
func tracedHooks(rec *recorder) (Hooks, func(name string) string) {
	h := rec.hooks()
	var mu sync.Mutex
	traces := map[string]*lockedBuffer{}
	opts := h.Options
	h.Options = func(j *Job, devs map[string]device.Info) engine.Options {
		o := opts(j, devs)
		b := &lockedBuffer{}
		mu.Lock()
		traces[j.Flow.Name()] = b
		mu.Unlock()
		o.Trace = engine.NewTrace(b)
		return o
	}
	return h, func(name string) string {
		mu.Lock()
		defer mu.Unlock()
		if b := traces[name]; b != nil {
			return b.String()
		}
		return ""
	}
}

// Issue #54: a device whose connection died for good during one flow stayed in the pool, so
// every later flow on it failed at once with the same stale error. The pool must close the
// dead driver, open the device again for the next flow, and say so in that flow's trace.
func TestScheduleReopensADeadDriver(t *testing.T) {
	o := &reopener{}
	o.install(t)
	dying := newFake("emu", "Pixel", "android")
	dying.dieOnLaunch = true
	rec := newRecorder(t)
	hooks, trace := tracedHooks(rec)
	jobs := []*Job{job(typingFlow(t, "a_first")), job(typingFlow(t, "b_second")), job(typingFlow(t, "c_third"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(dying), jobs, 0, 0, false, hooks) })
	got := byName(res)
	if r := got["a_first"]; r.Status != engine.Failed || r.Err == nil || !strings.Contains(r.Err.Error(), "lost the connection") {
		t.Errorf("a_first = %s %v, want it to fail on the lost connection", r.Status, r.Err)
	}
	for _, name := range []string{"b_second", "c_third"} {
		if r := got[name]; r.Status != engine.Passed {
			t.Errorf("%s = %s %v, want it to pass on the device opened again", name, r.Status, r.Err)
		}
	}
	if n := dying.Closed(); n != 1 {
		t.Errorf("the dead driver was closed %d times, want 1", n)
	}
	if n := len(o.Opened()); n != 1 {
		t.Errorf("the device was opened again %d times, want 1 (once, then kept for the run)", n)
	}
	if tr := trace("b_second"); !strings.Contains(tr, "the connection to Pixel (emu) was lost") || !strings.Contains(tr, "opened it again") {
		t.Errorf("b_second's trace does not say the device was lost and opened again:\n%s", tr)
	}
}

// The run's output is told when a device was lost and opened again (Hooks.Notice), once.
func TestScheduleNoticesAReopenedDevice(t *testing.T) {
	o := &reopener{}
	o.install(t)
	dying := newFake("emu", "Pixel", "android")
	dying.dieOnLaunch = true
	hooks := newRecorder(t).hooks()
	var mu sync.Mutex
	var notices []string
	hooks.Notice = func(j *Job, d device.Info, msg string) {
		mu.Lock()
		defer mu.Unlock()
		notices = append(notices, j.Flow.Name()+" "+d.Serial+": "+msg)
	}
	jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b")), job(typingFlow(t, "c"))}
	within(t, 10*time.Second, func() { Schedule(context.Background(), poolOf(dying), jobs, 0, 0, false, hooks) })
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 || !strings.HasPrefix(notices[0], "b emu: the connection to Pixel (emu) was lost (browser connection closed: EOF)") {
		t.Errorf("notices %q, want one, for b", notices)
	}
}

// Issue #54: a retry on the only device that fits must not reuse the driver whose connection
// died in the attempt before.
func TestScheduleRetryReopensADeadDriver(t *testing.T) {
	o := &reopener{}
	o.install(t)
	dying := newFake("emu", "Pixel", "android")
	dying.dieOnLaunch = true
	var res []Result
	within(t, 10*time.Second, func() {
		res = Schedule(context.Background(), poolOf(dying), []*Job{job(typingFlow(t, "flaky"))}, 1, 0, false, newRecorder(t).hooks())
	})
	if len(res) != 1 || res[0].Status != engine.Passed || res[0].Job.Attempt != 1 {
		t.Fatalf("results %+v, want the retry to pass on the device opened again", res)
	}
	if len(o.Opened()) != 1 {
		t.Errorf("opened again %d times, want 1", len(o.Opened()))
	}
}

// Issue #54: when the device cannot be opened again, the flow that needed it fails naming the
// device and both reasons, and the flows left go to the devices still working.
func TestScheduleDeviceLostForGood(t *testing.T) {
	o := &reopener{err: errors.New("device offline")}
	o.install(t)
	dying, good := newFake("emu", "Pixel", "android"), newFake("good", "Good", "android")
	dying.dieOnLaunch = true
	release := make(chan struct{})
	// Good takes the second flow and holds it until the dead device's next flow is over, so
	// that flow meets the lost connection instead of going to Good.
	good.onType = func(string) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
	}
	rec := newRecorder(t)
	var mu sync.Mutex
	var lostErr string
	onEmu := 0
	rec.onFinish = func(r Result) {
		if r.Devices[""].Serial != "emu" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if onEmu++; onEmu == 2 {
			if r.Err != nil {
				lostErr = r.Err.Error()
			}
			close(release)
		}
	}
	jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b")), job(typingFlow(t, "c")), job(typingFlow(t, "d")), job(typingFlow(t, "e"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(dying, good), jobs, 0, 0, false, rec.hooks()) })
	mu.Lock()
	msg := lostErr
	mu.Unlock()
	for _, want := range []string{"Pixel", "lost", "browser connection closed: EOF", "opening it again failed", "device offline"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %q", msg, want)
		}
	}
	failed, onLost := 0, 0
	for _, r := range res {
		if r.Status == engine.Failed {
			failed++
		}
		if r.Devices[""].Serial == "emu" {
			onLost++
		}
	}
	// a fails as the connection dies, one more flow meets it; the other three run on Good.
	if failed != 2 || onLost != 2 {
		t.Errorf("%d failed, %d ran on the lost device, want 2 and 2; results: %v", failed, onLost, summarize(res))
	}
	if n := dying.Closed(); n != 1 {
		t.Errorf("the dead driver was closed %d times, want 1", n)
	}
}

func summarize(rs []Result) []string {
	var out []string
	for _, r := range rs {
		s := r.Job.Flow.Name() + " " + string(r.Status) + " on " + r.Devices[""].Serial
		if r.Err != nil {
			s += ": " + r.Err.Error()
		}
		out = append(out, s)
	}
	return out
}
