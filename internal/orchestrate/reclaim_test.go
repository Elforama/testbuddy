package orchestrate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/engine"
)

// issue #395: an agent looks at a device (tb look holds it in a session of its daemon), then
// runs a flow on it with tb test. The run asks the caller's own daemon for the device
// (Reclaim): an idle session gives it up and the flow runs; a busy one, or another daemon's
// session, is named for what it is.
func TestPoolReclaimsTheCallersOwnSession(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	session := func() *devlock.Lock {
		t.Helper()
		l, err := devlock.TryAcquire("emulator-5750", `tb session "emulator-5750" (tb open/do/look)`)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	run := func(pool *Pool) Result {
		t.Helper()
		j := job(typingFlow(t, "f"))
		j.Needs = []Need{{Device: "emulator-5750"}}
		var res []Result
		within(t, 10*time.Second, func() {
			res = Schedule(context.Background(), pool, []*Job{j}, 0, 0, false, newRecorder(t).hooks())
		})
		pool.Close()
		return res[0]
	}

	// The caller's own idle session: given up, and the flow runs.
	l := session()
	pool := poolOf(newFake("emulator-5750", "sdk gphone64", "android"))
	var asked []string
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		l.Release()
		return Reclaimed{OK: true, Label: "emulator-5750"}
	}
	if r := run(pool); r.Status != engine.Passed {
		t.Errorf("issue #395: the caller's own idle session refused the run: %s %v", r.Status, r.Err)
	}
	if fmt.Sprint(asked) != "[emulator-5750]" {
		t.Errorf("asked %v", asked)
	}

	// Its own session, running a command: the error says so, and what to do.
	l = session()
	pool = poolOf(newFake("emulator-5750", "sdk gphone64", "android"))
	pool.Reclaim = func(context.Context, string) Reclaimed { return Reclaimed{Busy: true, Label: "emulator-5750"} }
	r := run(pool)
	if r.Status != engine.Failed || !strings.Contains(fmt.Sprint(r.Err), "is held by your own tb session emulator-5750, which is running a command") {
		t.Errorf("own busy session: %s %v", r.Status, r.Err)
	}

	// With --wait-for-device, the busy session finishes and gives the device up.
	pool = poolOf(newFake("emulator-5750", "sdk gphone64", "android"))
	pool.WaitForDevice = 5 * time.Second
	var mu sync.Mutex
	n := 0
	pool.Reclaim = func(context.Context, string) Reclaimed {
		mu.Lock()
		defer mu.Unlock()
		if n++; n < 3 {
			return Reclaimed{Busy: true, Label: "emulator-5750"}
		}
		l.Release()
		return Reclaimed{OK: true}
	}
	if r := run(pool); r.Status != engine.Passed {
		t.Errorf("waiting for its own busy session: %s %v", r.Status, r.Err)
	}

	// Another daemon's session: not the caller's to take, asked once, and said so.
	l = session()
	defer l.Release()
	pool = poolOf(newFake("emulator-5750", "sdk gphone64", "android"))
	pool.WaitForDevice = 1500 * time.Millisecond
	calls := 0
	pool.Reclaim = func(context.Context, string) Reclaimed { calls++; return Reclaimed{} }
	r = run(pool)
	if r.Status != engine.Failed || !strings.Contains(fmt.Sprint(r.Err), "a session of another tb daemon") || strings.Contains(fmt.Sprint(r.Err), "if that session is yours") {
		t.Errorf("another daemon's session: %s %v", r.Status, r.Err)
	}
	if calls != 1 {
		t.Errorf("asked another daemon's session %d times, want once", calls)
	}
}

// Review of issue #395: only as many of the caller's sessions are taken back as the run needs,
// and none for a job that still could not start. A daemon that could not be asked (an older
// build, no answer) is not said to be another agent's.
func TestPoolReclaimsOnlyWhatTheRunNeeds(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	hold := func(serial, what string) *devlock.Lock {
		t.Helper()
		l, err := devlock.TryAcquire(serial, what)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	schedule := func(pool *Pool, j *Job) Result {
		t.Helper()
		var res []Result
		within(t, 10*time.Second, func() {
			res = Schedule(context.Background(), pool, []*Job{j}, 0, 0, false, newRecorder(t).hooks())
		})
		pool.Close()
		return res[0]
	}

	// Two idle sessions fit a flow with no device named: one is taken back, not both.
	la, lb := hold("a", `tb session "a" (tb open/do/look)`), hold("b", `tb session "b" (tb open/do/look)`)
	pool := poolOf(newFake("a", "A", "android"), newFake("b", "B", "android"))
	var asked []string
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		map[string]*devlock.Lock{"a": la, "b": lb}[serial].Release()
		return Reclaimed{OK: true}
	}
	if r := schedule(pool, job(typingFlow(t, "f1"))); r.Status != engine.Passed || len(asked) != 1 {
		t.Errorf("two sessions, one needed: %s, asked %v (%v)", r.Status, asked, r.Err)
	}
	la.Release()
	lb.Release()

	// A free device fits: no session is asked.
	la = hold("a", `tb session "a" (tb open/do/look)`)
	pool = poolOf(newFake("a", "A", "android"), newFake("b", "B", "android"))
	asked = nil
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		return Reclaimed{OK: true}
	}
	if r := schedule(pool, job(typingFlow(t, "f2"))); r.Status != engine.Passed || len(asked) != 0 {
		t.Errorf("a free device: %s, asked %v", r.Status, asked)
	}

	// Two roles: alice fits only the caller's session, bob only a device another run holds.
	// The job cannot start, so the session is left alone.
	lb = hold("b", "tb test other/")
	pool = poolOf(newFake("a", "A", "android"), newFake("b", "B", "android"))
	asked = nil
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		return Reclaimed{OK: true}
	}
	j := job(typingFlow(t, "f3"))
	j.Needs = []Need{{Role: "alice", Device: "a"}, {Role: "bob", Device: "b"}}
	if r := schedule(pool, j); r.Status != engine.Failed || len(asked) != 0 {
		t.Errorf("a job that still could not start: %s, asked %v", r.Status, asked)
	}
	lb.Release()

	// The caller's daemon could not be asked: the old message, not "another daemon's".
	pool = poolOf(newFake("a", "A", "android"))
	pool.Reclaim = func(context.Context, string) Reclaimed { return Reclaimed{Unknown: true} }
	j = job(typingFlow(t, "f4"))
	j.Needs = []Need{{Device: "a"}}
	r := schedule(pool, j)
	if e := fmt.Sprint(r.Err); r.Status != engine.Failed || strings.Contains(e, "another tb daemon") || !strings.Contains(e, "if that session is yours, close it: tb close -d a") {
		t.Errorf("a daemon that could not be asked: %s %v", r.Status, r.Err)
	}

	// A session that opens again at once (a tb look alongside) is not asked over and over.
	pool = poolOf(newFake("a", "A", "android"))
	calls := 0
	pool.Reclaim = func(context.Context, string) Reclaimed { calls++; return Reclaimed{OK: true} } // but la stays held
	j = job(typingFlow(t, "f5"))
	j.Needs = []Need{{Device: "a"}}
	if r := schedule(pool, j); r.Status != engine.Failed || calls != maxReclaims {
		t.Errorf("a session that comes back: %s, asked %d times, want %d", r.Status, calls, maxReclaims)
	}
	la.Release()
}

// Re-review of issue #395: a two-device flow, one device free and the other in the caller's
// idle session. The free device serves one role only, so the session is taken back for the
// other; with no Reclaim, the error names the holder instead of "no device could run it".
func TestPoolReclaimsForATwoDeviceFlow(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	needs := []Need{{Role: "alice"}, {Role: "bob"}}
	lb, err := devlock.TryAcquire("b", `tb session "b" (tb open/do/look)`)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Release()

	pool := poolOf(newFake("a", "A", "android"), newFake("b", "B", "android"))
	got, err := pool.allocOrWait(context.Background(), needs)
	if got != nil || err == nil || !strings.Contains(err.Error(), "B (b) is in use by tb session") {
		t.Errorf("no Reclaim: %v, %v", got, err)
	}
	pool.Close()

	pool = poolOf(newFake("a", "A", "android"), newFake("b", "B", "android"))
	defer pool.Close()
	var asked []string
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		lb.Release()
		return Reclaimed{OK: true}
	}
	got, err = pool.allocOrWait(context.Background(), needs)
	if err != nil || len(got) != 2 || fmt.Sprint(asked) != "[b]" {
		t.Errorf("issue #395, two devices: %v, %v, asked %v", got, err, asked)
	}
}
