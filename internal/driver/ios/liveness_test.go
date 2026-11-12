package ios

// Issue #48: a runner busy with an op answers the probe at once and says so (busy, busyMs,
// busyLimitMs). connect waits for it to finish the op before using it, replaces it once it is
// busy past the op's limit (stuck), and ends a runner of another build that holds the port.

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

// fakeProcs replaces ending a runner process: each pid's handler runs instead (closing the fake
// runner, as the process ending would); the pids ended are recorded.
type fakeProcs struct {
	mu    sync.Mutex
	ended []int
	on    map[int]func()
}

func stubProcs(t *testing.T) *fakeProcs {
	fp := &fakeProcs{on: map[int]func(){}}
	end := func(pid int) {
		fp.mu.Lock()
		fp.ended = append(fp.ended, pid)
		h := fp.on[pid]
		delete(fp.on, pid)
		fp.mu.Unlock()
		if h != nil {
			h()
		}
	}
	oldT, oldK, oldPoll, oldWait := terminateProc, killProc, idlePoll, staleExitWait
	terminateProc, killProc, idlePoll, staleExitWait = end, end, 10*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { terminateProc, killProc, idlePoll, staleExitWait = oldT, oldK, oldPoll, oldWait })
	return fp
}

func (fp *fakeProcs) endedPids() []int {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return append([]int(nil), fp.ended...)
}

// A runner of this build busy with an earlier connection's op (a launch) is waited for, then
// reused: not stopped, no xcodebuild, and the new connection starts once the op is done.
func TestConnectWaitsForABusyRunner(t *testing.T) {
	ts := stubTools(t)
	fp := stubProcs(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	r := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), udid, BuildID())
	// Busy for the first probe, idle from the second: counted, not timed, so a slow machine
	// whose first probe lands late still sees the runner busy once.
	var probes atomic.Int32
	var idleAt atomic.Int64
	r.on("info", func(map[string]any) map[string]any {
		n := probes.Add(1)
		in := map[string]any{"w": 393, "h": 852, "udid": udid, "build": BuildID(), "pid": 4242, "busy": ""}
		if n == 1 {
			in["busy"], in["busyMs"], in["busyLimitMs"] = "launch", 59_000, 90_000
		} else {
			idleAt.CompareAndSwap(0, time.Now().UnixNano())
		}
		return in
	})
	d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if idleAt.Load() == 0 {
		t.Error("the busy runner was used before its op was done")
	}
	if d.Port() != port || probes.Load() < 2 {
		t.Errorf("port %d, %d info requests", d.Port(), probes.Load())
	}
	if len(ts.calls("xcrun")) != 0 || len(r.requests("exit")) != 0 || len(fp.endedPids()) != 0 {
		t.Errorf("the busy runner was replaced: xcrun %v, exit %v, ended %v", ts.calls("xcrun"), r.requests("exit"), fp.endedPids())
	}
}

// A runner busy past its op's limit is stuck: its process is ended and a new runner started
// on its port. Before #48's limit a hung op left a runner that answered the probe forever.
func TestConnectReplacesAStuckRunner(t *testing.T) {
	for _, tc := range []struct {
		name string
		// busy: the info the stuck runner answers its probe with; then: later infos.
		then map[string]any
	}{
		{"past its limit", map[string]any{"busy": "tree", "busyMs": 95_000, "busyLimitMs": 90_000}},
		{"silent once past it", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := stubTools(t)
			fp := stubProcs(t)
			port := freePorts(t, 1)
			udid := udidFor(t, port)
			old := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), udid, BuildID())
			var calls atomic.Int32
			old.on("info", func(map[string]any) map[string]any {
				in := map[string]any{"udid": udid, "build": BuildID(), "pid": 4242}
				if tc.then != nil {
					for k, v := range tc.then {
						in[k] = v
					}
					return in
				}
				if calls.Add(1) > 1 {
					time.Sleep(4 * time.Second) // in turn behind the hung op: no answer in 3 s
					return noReply
				}
				in["busy"], in["busyMs"], in["busyLimitMs"] = "tree", 89_990, 90_000
				return in
			})
			fp.on[4242] = old.close
			started := startsRunnerAt(t, ts, port, udid, nil)
			d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			fresh := <-started
			if fresh == nil {
				t.Fatal("no new runner")
			}
			if got := fp.endedPids(); len(got) == 0 || got[0] != 4242 {
				t.Errorf("ended %v, want the stuck runner 4242", got)
			}
			if d.Port() != port || len(fresh.requests("info")) != 1 {
				t.Errorf("port %d, fresh runner requests %v", d.Port(), fresh.ops())
			}
		})
	}
}

// A runner of another build that does not exit when told (busy: exit waits behind its op) has
// its process ended, and this build's runner is started on the port.
func TestConnectEndsAStaleRunnerThatDoesNotExit(t *testing.T) {
	ts := stubTools(t)
	fp := stubProcs(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	old := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), udid, "0123456789ab")
	old.on("info", func(map[string]any) map[string]any {
		return map[string]any{"udid": udid, "build": "0123456789ab", "pid": 4343}
	})
	old.on("exit", func(map[string]any) map[string]any { return noReply })
	fp.on[4343] = old.close
	started := startsRunnerAt(t, ts, port, udid, nil)
	d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if <-started == nil {
		t.Fatal("no new runner")
	}
	if got := fp.endedPids(); len(got) == 0 || got[0] != 4343 {
		t.Errorf("ended %v, want the stale runner 4343", got)
	}
	if len(old.requests("exit")) != 1 {
		t.Errorf("the stale runner got %d exits", len(old.requests("exit")))
	}
}

// A runner of another build answering on the port after the start is not driven.
func TestConnectRefusesARunnerOfAnotherBuild(t *testing.T) {
	ts := stubTools(t)
	stubProcs(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	started := startsRunnerAt(t, ts, port, udid, func(r *fakeRunner) {
		r.on("info", func(map[string]any) map[string]any {
			return map[string]any{"udid": udid, "build": "0123456789ab"}
		})
	})
	_, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err == nil || !strings.Contains(err.Error(), "another build") {
		t.Errorf("err = %v", err)
	}
	<-started
}
