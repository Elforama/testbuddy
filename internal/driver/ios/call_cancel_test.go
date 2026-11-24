package ios

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/wire"
)

func TestCallCancelledSendsNothing(t *testing.T) {
	r := newFakeRunner(t)
	r.on("tap", func(map[string]any) map[string]any { return nil })
	d := newIOSDriver(t, r)
	ctx, cancel := context.WithCancel(ctxT(t))
	cancel()
	_, err := d.call(ctx, "tap", map[string]any{"x": 1, "y": 2}, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("issue #202: call on a cancelled context = %v, want context.Canceled", err)
	}
	if n := len(r.requests("tap")); n != 0 {
		t.Errorf("issue #202: %d taps sent on a cancelled context, want none", n)
	}
}

func slowLaunch(t *testing.T, r *fakeRunner) (started, release chan struct{}) {
	started, release = make(chan struct{}), make(chan struct{})
	first := true
	r.on("launch", func(map[string]any) map[string]any {
		if first {
			first = false
			close(started)
			<-release
		}
		return map[string]any{"out": "late"}
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

func recvRes(t *testing.T, ch <-chan callRes) callRes {
	t.Helper()
	select {
	case res := <-ch:
		return res
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

func cancelLaunch(t *testing.T, d *Driver, started, release chan struct{}) (callRes, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan callRes, 1)
	go func() {
		m, err := d.call(ctx, "launch", map[string]any{"bundle": "com.example.app"}, 60*time.Second)
		done <- callRes{outOf(m), err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never got the launch request")
	}
	cancel()
	at := time.Now()
	select {
	case res := <-done:
		return res, time.Since(at)
	case <-time.After(3 * time.Second):
		close(release)
		res := recvRes(t, done)
		t.Fatalf("issue #202: a call blocked on a slow runner did not return within 3s of its context being cancelled (it ended with %+v once the runner answered)", res)
		return res, 0
	}
}

func TestCallCancelledWhileWaiting(t *testing.T) {
	r := newFakeRunner(t)
	started, release := slowLaunch(t, r)
	d := newIOSDriver(t, r)
	res, took := cancelLaunch(t, d, started, release)
	if !errors.Is(res.err, context.Canceled) {
		t.Errorf("issue #202: cancelled call = %+v, want context.Canceled", res)
	}
	if took > time.Second {
		t.Errorf("issue #202: the cancelled call took %s to return", took)
	}
}

func TestKeepAndStopAfterAbandonedRequest(t *testing.T) {
	r := newFakeRunner(t)
	started, release := slowLaunch(t, r)
	r.on("keep", func(map[string]any) map[string]any { return nil })
	r.on("exit", func(map[string]any) map[string]any { return nil })
	d := newIOSDriver(t, r)
	cancelLaunch(t, d, started, release)
	start := time.Now()
	d.Keep(ctxT(t))
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("issue #202: Keep after a cancelled launch took %s, want its own 2 s at most", took)
	}
	start = time.Now()
	d.StopRunner()
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("issue #202: StopRunner after a cancelled launch took %s, want its own 3 s at most", took)
	}
	if r.arrived("keep") != 1 || r.arrived("exit") != 1 {
		t.Errorf("issue #202: keep arrived %d times, exit %d, want once each", r.arrived("keep"), r.arrived("exit"))
	}
}

func TestCallAfterCancelGetsItsOwnReply(t *testing.T) {
	r := newFakeRunner(t)
	started, release := slowLaunch(t, r)
	r.on("info", func(map[string]any) map[string]any {
		return map[string]any{"w": 393, "h": 852, "udid": testUDID, "build": BuildID(), "agent": "test", "out": "info"}
	})
	d := newIOSDriver(t, r)
	c0 := d.conn()
	if res, _ := cancelLaunch(t, d, started, release); !errors.Is(res.err, context.Canceled) {
		t.Fatalf("issue #202: cancelled call = %+v, want context.Canceled", res)
	}
	next := make(chan callRes, 1)
	go func() {
		m, err := d.call(ctxT(t), "info", nil, 5*time.Second)
		next <- callRes{outOf(m), err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for r.arrived("info") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the next call never reached the runner")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	res := recvRes(t, next)
	if res.err != nil || res.out != "info" {
		t.Errorf("issue #202: the call after a cancelled one = %+v, want its own reply (out %q)", res, "info")
	}
	if d.conn() != c0 {
		t.Error("issue #202: the connection was replaced after the cancel, want it kept")
	}
}
