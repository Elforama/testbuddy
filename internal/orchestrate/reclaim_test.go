package orchestrate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/engine"
)

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

	l = session()
	pool = poolOf(newFake("emulator-5750", "sdk gphone64", "android"))
	pool.Reclaim = func(context.Context, string) Reclaimed { return Reclaimed{Busy: true, Label: "emulator-5750"} }
	r := run(pool)
	if r.Status != engine.Failed || !strings.Contains(fmt.Sprint(r.Err), "is held by your own tb session emulator-5750, which is running a command") {
		t.Errorf("own busy session: %s %v", r.Status, r.Err)
	}

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

	pool = poolOf(newFake("a", "A", "android"))
	pool.Reclaim = func(context.Context, string) Reclaimed { return Reclaimed{Unknown: true} }
	j = job(typingFlow(t, "f4"))
	j.Needs = []Need{{Device: "a"}}
	r := schedule(pool, j)
	if e := fmt.Sprint(r.Err); r.Status != engine.Failed || strings.Contains(e, "another tb daemon") || !strings.Contains(e, "if that session is yours, close it: tb close -d a") {
		t.Errorf("a daemon that could not be asked: %s %v", r.Status, r.Err)
	}

	pool = poolOf(newFake("a", "A", "android"))
	calls := 0
	pool.Reclaim = func(context.Context, string) Reclaimed { calls++; return Reclaimed{OK: true} }
	j = job(typingFlow(t, "f5"))
	j.Needs = []Need{{Device: "a"}}
	if r := schedule(pool, j); r.Status != engine.Failed || calls != maxReclaims {
		t.Errorf("a session that comes back: %s, asked %d times, want %d", r.Status, calls, maxReclaims)
	}
	la.Release()
}

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

func TestPoolReclaimsInTryAllocsOrder(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	hold := func(serial string) *devlock.Lock {
		t.Helper()
		l, err := devlock.TryAcquire(serial, `tb session "x" (tb open/do/look)`)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	li := hold("I")
	pool := NewPool([]device.Info{{Serial: "A", Name: "A", Platform: device.Android}, {Serial: "I", Name: "I", Platform: device.IOS}})
	var asked []string
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		li.Release()
		return Reclaimed{OK: true}
	}
	got, err := pool.allocOrWait(context.Background(), []Need{{Role: "alice"}, {Role: "bob", Platform: "android"}})
	if err != nil || got["bob"].Serial != "A" || got["alice"].Serial != "I" || fmt.Sprint(asked) != "[I]" {
		t.Errorf("mixed platforms: %v, %v, asked %v", got, err, asked)
	}
	pool.Close()

	la := hold("A")
	pool = NewPool([]device.Info{{Serial: "A", Name: "A", Platform: device.Android}, chrome})
	asked = nil
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		la.Release()
		return Reclaimed{OK: true}
	}
	got, err = pool.allocOrWait(context.Background(), []Need{{Role: "phone", Device: "A"}, {Role: "w1", Platform: "web"}, {Role: "w2", Platform: "web"}})
	if err != nil || len(got) != 3 || fmt.Sprint(asked) != "[A]" {
		t.Errorf("phone and two browsers: %v, %v, asked %v", got, err, asked)
	}
	pool.Close()
}

func TestPoolPreferBusyStillReclaimsAnother(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	la, err := devlock.TryAcquire("a", `tb session "a" (tb open/do/look)`)
	if err != nil {
		t.Fatal(err)
	}
	defer la.Release()
	lb, err := devlock.TryAcquire("b", `tb session "b" (tb open/do/look)`)
	if err != nil {
		t.Fatal(err)
	}
	pool := poolOf(newFake("a", "A", "android"), newFake("b", "B", "android"))
	defer pool.Close()
	pool.Prefer = "a"
	var asked []string
	pool.Reclaim = func(_ context.Context, serial string) Reclaimed {
		asked = append(asked, serial)
		if serial == "a" {
			return Reclaimed{Busy: true, Label: "a"}
		}
		lb.Release()
		return Reclaimed{OK: true}
	}
	got, err := pool.allocOrWait(context.Background(), []Need{{}})
	if err != nil || got[""].Serial != "b" || asked[len(asked)-1] != "b" || asked[0] != "a" {
		t.Errorf("Prefer busy, another idle own session: %v, %v, asked %v", got, err, asked)
	}
}
