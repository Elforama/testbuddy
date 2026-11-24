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
