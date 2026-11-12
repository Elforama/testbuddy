package android

// A cancelled run sends no more agent requests and stops waiting for the one in flight
// (issue #202).

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/wire"
)

// issue #202: a call on an already cancelled context sends nothing. callOn used to bound the
// reply wait by ctx's deadline only, so a cancelled run (Ctrl-C, a failed role, an MCP cancel)
// still tapped.
func TestCallCancelledSendsNothing(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tap", func(map[string]any) map[string]any { return nil })
	d := newDriver(t, a)
	ctx, cancel := context.WithCancel(ctxT(t))
	cancel()
	_, err := d.call(ctx, "tap", map[string]any{"x": 1, "y": 2}, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("issue #202: call on a cancelled context = %v, want context.Canceled", err)
	}
	if n := len(a.requests("tap")); n != 0 {
		t.Errorf("issue #202: %d taps sent on a cancelled context, want none", n)
	}
}

// slowOp makes the fake agent hold its answer to the first op request until release is
// closed; started is closed once that request has arrived. The agent serves a connection's
// requests in order, as the real one does for a long `sh`, so nothing else is answered
// meanwhile.
func slowOp(t *testing.T, a *fakeAgent, op string, reply map[string]any) (started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	first := true
	a.on(op, func(map[string]any) map[string]any {
		if first {
			first = false
			close(started)
			<-release
		}
		return reply
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	return started, release
}

type callRes struct {
	out string
	err error
}

// cancelInFlight sends a slow sh, cancels its context once the agent has it, and returns how
// the call ended and how long after the cancel. It gives up after 3 s and lets the agent
// answer, so a call that ignores the cancel fails the test instead of hanging it.
func cancelInFlight(t *testing.T, d *Driver, started, release chan struct{}) (callRes, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan callRes, 1)
	go func() {
		m, err := d.call(ctx, "sh", map[string]any{"cmd": "pm install big.apk"}, 30*time.Second)
		done <- callRes{outOf(m), err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent never got the sh request")
	}
	cancel()
	at := time.Now()
	select {
	case r := <-done:
		return r, time.Since(at)
	case <-time.After(3 * time.Second):
		close(release)
		r := recvRes(t, done)
		t.Fatalf("issue #202: a call blocked on a slow agent did not return within 3s of its context being cancelled (it ended with %+v once the agent answered)", r)
		return r, 0
	}
}

// recvRes receives a call's result, failing the test if it does not come within 10 s.
func recvRes(t *testing.T, ch <-chan callRes) callRes {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not return")
		return callRes{}
	}
}

func outOf(m *wire.Msg) string {
	if m == nil {
		return ""
	}
	var r struct {
		Out string `json:"out"`
	}
	m.Decode(&r)
	return r.Out
}

// issue #202: a call waiting on a slow agent returns promptly when its context is cancelled,
// with the cancellation. It used to wait out the reply timeout (65 s for sh).
func TestCallCancelledWhileWaiting(t *testing.T) {
	a := newFakeAgent(t)
	started, release := slowOp(t, a, "sh", map[string]any{"code": 0, "out": "late"})
	d := newDriver(t, a)
	r, took := cancelInFlight(t, d, started, release)
	if !errors.Is(r.err, context.Canceled) {
		t.Errorf("issue #202: cancelled call = %+v, want context.Canceled", r)
	}
	if took > time.Second {
		t.Errorf("issue #202: the cancelled call took %s to return", took)
	}
}

// issue #202: after a call is cancelled, the late reply to it is dropped and the next call on
// the same connection gets its own reply, not that one. The connection is kept.
func TestCallAfterCancelGetsItsOwnReply(t *testing.T) {
	a := newFakeAgent(t)
	started, release := slowOp(t, a, "sh", map[string]any{"code": 0, "out": "late"})
	a.on("info", func(map[string]any) map[string]any {
		return map[string]any{"w": 1080, "h": 2400, "sdk": 34, "out": "info"}
	})
	d := newDriver(t, a)
	c0, conns := d.conn(), a.connCount()
	if r, _ := cancelInFlight(t, d, started, release); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("issue #202: cancelled call = %+v, want context.Canceled", r)
	}
	// The next call goes out while the agent still owes the cancelled one its answer: the
	// agent answers the sh first, then this.
	next := make(chan callRes, 1)
	go func() {
		m, err := d.call(ctxT(t), "info", nil, 5*time.Second)
		next <- callRes{outOf(m), err}
	}()
	eventually(t, "the next call to reach the agent", func() bool { return a.arrived("info") == 1 })
	close(release)
	r := recvRes(t, next)
	if r.err != nil || r.out != "info" {
		t.Errorf("issue #202: the call after a cancelled one = %+v, want its own reply (out %q)", r, "info")
	}
	if d.conn() != c0 || a.connCount() != conns {
		t.Errorf("issue #202: the connection was replaced after the cancel (%d connections, want %d)", a.connCount(), conns)
	}
}

// issue #202: StopRecording on a cancelled run still stops screenrecord and removes its files
// on the device: a call on a cancelled context sends nothing now, so the cleanup runs on a
// context of its own. The video is not kept (the run was cancelled).
func TestStopRecordingCancelledStillStops(t *testing.T) {
	a := recordingAgent(t, true, "/sdcard/tb-recording-1.mp4\n")
	s := stubADB(t, []stubRule{{match: "screenrecord --verbose", out: "Content area is 1080x2400 at offset x=0 y=0"}})
	d := stubbedDriver(t, s, a)
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "run.mp4")); err != nil {
		t.Fatal(err)
	}
	a.reset()
	ctx, cancel := context.WithCancel(ctxT(t))
	cancel()
	if err := d.StopRecording(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("issue #202: StopRecording on a cancelled run = %v, want context.Canceled", err)
	}
	want := []string{"touch /sdcard/tb-recording.stop; pkill -INT screenrecord",
		"rm -f /sdcard/tb-recording-*.mp4 /sdcard/tb-recording.stop"}
	if got := a.shCmds(); !reflect.DeepEqual(got, want) {
		t.Errorf("issue #202: agent commands on a cancelled stop:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if d.recordCmd != nil {
		t.Error("issue #202: still recording after a cancelled stop")
	}
}

// issue #202 (review): a cancelled run's ask whether screenrecord can record without its cap
// gets no answer, and is not remembered as "no": the next run asks again.
func TestUnlimitedRecordingNotCachedOnCancel(t *testing.T) {
	a := recordingAgent(t, true, "")
	d := newDriver(t, a)
	ctx, cancel := context.WithCancel(ctxT(t))
	cancel()
	if d.unlimitedRecording(ctx) {
		t.Error("unlimited with no answer")
	}
	if !d.unlimitedRecording(ctxT(t)) {
		t.Error("issue #202: a cancelled ask was remembered: the device says screenrecord can record without its cap")
	}
}

// issue #202 (second review): the same when the run is cancelled while the agent answers the
// ask: the answer is given up on, the ask says no, and the next one asks again.
func TestUnlimitedRecordingNotCachedOnCancelMidCall(t *testing.T) {
	a := recordingAgent(t, true, "")
	answer := a.handlers["sh"]
	asked := make(chan struct{})
	release := make(chan struct{})
	first := true
	a.on("sh", func(req map[string]any) map[string]any {
		if first && strings.HasPrefix(fmt.Sprint(req["cmd"]), "screenrecord --help") {
			first = false
			close(asked)
			<-release
		}
		return answer(req)
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	d := newDriver(t, a)
	ctx, cancel := context.WithCancel(ctxT(t))
	go func() {
		select {
		case <-asked:
			cancel()
		case <-time.After(5 * time.Second):
		}
	}()
	if d.unlimitedRecording(ctx) {
		t.Error("unlimited with no answer")
	}
	close(release)
	if !d.unlimitedRecording(ctxT(t)) {
		t.Error("issue #202: an ask cancelled mid-call was remembered: the device says screenrecord can record without its cap")
	}
}

// issue #202 (second review): Release after a run was cancelled during a long agent request
// ends within releaseWait. Its location-off and exit requests waited behind the request the
// run gave up on (the agent runs one at a time): a cancelled 30 s sh made Release take 32 s.
// The agent is still stopped (with adb).
func TestReleaseAfterAbandonedRequest(t *testing.T) {
	a := newFakeAgent(t)
	started, release := slowOp(t, a, "sh", map[string]any{"code": 0, "out": ""})
	a.on("location", func(map[string]any) map[string]any { return nil })
	s := stubADB(t, nil)
	d := stubbedDriver(t, s, a)
	d.locAt.Store(&locPoint{1, 2}) // a location is on: Release turns it off
	cancelInFlight(t, d, started, release)
	start := time.Now()
	d.Release(ctxT(t))
	if took := time.Since(start); took > releaseWait {
		t.Errorf("issue #202: Release after a cancelled request took %s, want under releaseWait (%s)", took, releaseWait)
	} else if took > 4*time.Second {
		t.Errorf("issue #202: Release after a cancelled request took %s, want about the exit's 2 s at most", took)
	}
	found := false
	for _, call := range s.calls() {
		found = found || strings.Contains(strings.Join(call, " "), "pidof tb-agent")
	}
	if !found {
		t.Errorf("issue #202: no stop command: adb calls %v", s.calls())
	}
}

// issue #202 (second review): Keep after a cancelled request asks the agent to stay without
// waiting for the request's bound: it times out on its own 2 s.
func TestKeepAfterAbandonedRequest(t *testing.T) {
	a := newFakeAgent(t)
	started, release := slowOp(t, a, "sh", map[string]any{"code": 0, "out": ""})
	a.on("keep", func(map[string]any) map[string]any { return nil })
	d := newDriver(t, a)
	cancelInFlight(t, d, started, release)
	start := time.Now()
	d.Keep(ctxT(t))
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("issue #202: Keep after a cancelled request took %s, want its own 2 s at most", took)
	}
	eventually(t, "keep to reach the agent", func() bool { return a.arrived("keep") == 1 })
}

// issue #202: Release on a cancelled context still asks the agent to exit and stops it with
// adb: a run stopped with Ctrl-C frees its devices.
func TestReleaseCancelledStillStops(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	s := stubADB(t, nil)
	d := s.driver()
	c, h, err := dialHello(a.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c, a.port(), h)
	ctx, cancel := context.WithCancel(ctxT(t))
	cancel()
	if err := d.Release(ctx); err != nil {
		t.Errorf("issue #202: Release on a cancelled context = %v", err)
	}
	if len(a.requests("exit")) != 1 {
		t.Errorf("issue #202: exit sent %d times, want once", len(a.requests("exit")))
	}
	found := false
	for _, call := range s.calls() {
		if strings.Contains(strings.Join(call, " "), "pidof tb-agent") {
			found = true
		}
	}
	if !found {
		t.Errorf("issue #202: no stop command on a cancelled release: adb calls %v", s.calls())
	}
}
