package orchestrate

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/driver/android"
	"github.com/elforama/testbuddy/internal/driver/ios"
	"github.com/elforama/testbuddy/internal/engine"
)

// Issue #101: a device opened again after its connection was lost runs a new agent, and may
// be a device that restarted: the once-per-device setup (Prepare: animations off) must run
// again on it, on the new driver, before its next flow. It ran once per serial, so the flows
// after a reopen ran without it.
func TestScheduleReopenedDeviceIsPreparedAgain(t *testing.T) {
	o := &reopener{}
	o.install(t)
	dying := newFake("emu", "Pixel", "android")
	dying.dieOnLaunch = true
	hooks := newRecorder(t).hooks()
	var mu sync.Mutex
	var prepared []driver.Driver
	hooks.Prepare = func(ctx context.Context, d device.Info, drv driver.Driver) {
		mu.Lock()
		defer mu.Unlock()
		prepared = append(prepared, drv)
	}
	jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b")), job(typingFlow(t, "c"))}
	var res []Result
	within(t, 10*time.Second, func() { res = Schedule(context.Background(), poolOf(dying), jobs, 0, 0, false, hooks) })
	if got := byName(res); got["b"].Status != engine.Passed || got["c"].Status != engine.Passed {
		t.Fatalf("results %v, want b and c to pass on the device opened again", summarize(res))
	}
	opened := o.Opened()
	if len(opened) != 1 {
		t.Fatalf("opened again %d times, want 1", len(opened))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(prepared) != 2 || prepared[0] != driver.Driver(dying) || prepared[1] != driver.Driver(opened[0]) {
		t.Errorf("issue #101: Prepare ran %d times, want twice: on the first driver, then on the one opened again",
			len(prepared))
	}
}

// Issue #101: a lost browser no longer serves the run, so it must not count toward the cap of
// 8 browsers: a run that lost one can still start an eighth live one (under a serial of its
// own, not the lost one's).
func TestAddWebDoesNotCountLostBrowsers(t *testing.T) {
	var devs []device.Info
	for i := 1; i <= 8; i++ {
		devs = append(devs, device.WebDevice(i))
	}
	p := NewPool(devs)
	p.lost["web-3"] = errors.New("Chrome exited, and opening it again failed")
	for _, d := range devs {
		if d.Serial != "web-3" {
			p.busy[d.Serial] = true
		}
	}
	got := p.tryAlloc([]Need{{Platform: string(device.Web)}})
	if got == nil {
		t.Fatal("issue #101: 7 browsers are live and one was lost, but no eighth live browser was started")
	}
	for _, d := range devs {
		if got[""].Serial == d.Serial {
			t.Errorf("the new browser reuses the serial %q of a browser already in the pool", d.Serial)
		}
	}
	// the cap still holds: 8 live browsers, no ninth
	if again := p.tryAlloc([]Need{{Platform: string(device.Web)}}); again != nil {
		t.Errorf("a ninth live browser was started: %v", again)
	}
}

// Issue #101: a reopen is bounded, but must leave the device as long as opening it takes:
// a simulator whose runner starts cold (xcodebuild) gets ios.RunnerStartTimeout from the iOS
// driver, which the 3-minute reopen bound cut short; in CI mode, opening an Android emulator
// may restart it (android.RestartBoot) before connecting again. Outside CI mode an Android
// reopen keeps its 3 minutes.
func TestReopenLeavesRoomForTheDriversOwnStart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plat    device.Platform
		ci      string
		atLeast time.Duration
		atMost  time.Duration // 0: no upper check
	}{
		{"ios", device.IOS, "0", ios.RunnerStartTimeout + 30*time.Second, 0},
		{"android in CI mode", device.Android, "1", 3*time.Minute + android.RestartBoot, 0},
		{"android", device.Android, "0", 0, 3*time.Minute + time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", tc.ci)
			var mu sync.Mutex
			var left time.Duration
			t.Cleanup(connect.Override(tc.plat, func(ctx context.Context, i device.Info) (driver.Driver, error) {
				mu.Lock()
				defer mu.Unlock()
				if dl, ok := ctx.Deadline(); ok {
					left = time.Until(dl)
				}
				return newFake(i.Serial, i.Name, string(i.Platform)), nil
			}))
			dying := newFake("dev", "Device", string(tc.plat))
			dying.dieOnLaunch = true
			jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b"))}
			within(t, 10*time.Second, func() {
				Schedule(context.Background(), poolOf(dying), jobs, 0, 0, false, newRecorder(t).hooks())
			})
			mu.Lock()
			defer mu.Unlock()
			if left == 0 {
				t.Fatal("the device was not opened again with a deadline")
			}
			// (left is read a moment after the deadline was set: 5 s of slack for that)
			if left+5*time.Second < tc.atLeast {
				t.Errorf("issue #101: the reopen had %s, want at least %s", left.Round(time.Second), tc.atLeast)
			}
			if tc.atMost > 0 && left > tc.atMost {
				t.Errorf("the reopen had %s, want at most %s outside CI mode", left.Round(time.Second), tc.atMost)
			}
		})
	}
}

// Issue #101: Failure reads a failed attempt's devices while the attempt still holds them,
// through the driver opened again when the attempt lost its connection, not the dead one; the
// next flow on that device starts only after it.
func TestScheduleFailureReadsTheReopenedDevice(t *testing.T) {
	o := &reopener{}
	o.install(t)
	dying := newFake("emu", "Pixel", "android")
	dying.dieOnLaunch = true
	rec := newRecorder(t)
	hooks := rec.hooks()
	var mu sync.Mutex
	var got []driver.Driver
	var startedBefore []string
	hooks.Failure = func(r Result, drivers map[string]driver.Driver) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, drivers[""])
		startedBefore = rec.Started()
	}
	jobs := []*Job{job(typingFlow(t, "a")), job(typingFlow(t, "b"))}
	within(t, 10*time.Second, func() { Schedule(context.Background(), poolOf(dying), jobs, 0, 0, false, hooks) })
	opened := o.Opened()
	mu.Lock()
	defer mu.Unlock()
	if len(opened) != 1 || len(got) != 1 || got[0] != driver.Driver(opened[0]) {
		t.Fatalf("issue #101: Failure got %v, want the one driver opened again (%d opened)", got, len(opened))
	}
	if len(startedBefore) != 1 {
		t.Errorf("flows started before the failure was read: %v, want only the failed one", startedBefore)
	}
}
