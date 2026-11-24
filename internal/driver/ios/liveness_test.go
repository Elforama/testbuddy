package ios

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
)

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

func TestConnectWaitsForABusyRunner(t *testing.T) {
	ts := stubTools(t)
	fp := stubProcs(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	r := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), udid, BuildID())
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

func TestConnectReplacesAStuckRunner(t *testing.T) {
	for _, tc := range []struct {
		name string
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
					time.Sleep(4 * time.Second)
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
