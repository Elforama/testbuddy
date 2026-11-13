package ios

// Issue #215: a port that accepts a connection but never says hello (a process that is not a
// tb runner, or a runner of ours so stuck its connection thread is dead) is occupied, not free:
// connect must not start a runner there, which could not bind it.

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
)

// silentListener accepts connections on port and never writes to them; the func it returns
// stops it (as ending the process holding the port would).
func silentListener(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ln.Close()
			mu.Lock()
			for _, c := range conns {
				c.Close()
			}
			mu.Unlock()
		})
	}
	t.Cleanup(stop)
	return stop
}

// Issue #215: a runner started on the next port while the preferred one was held is reused by
// the next Open once the preferred port is free again (its holder, a stuck runner of ours, was
// ended): not replaced by a new runner on the preferred port, which would end it.
func TestConnectReusesTheRunnerPastAFreedPort(t *testing.T) {
	ts := stubTools(t)
	stubProcs(t)
	port := freePorts(t, 2)
	udid := udidFor(t, port)
	stopSilent := silentListener(t, port)
	started := startsRunnerAt(t, ts, port+1, udid, nil)
	d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	if <-started == nil {
		t.Fatal("no new runner")
	}
	d.Close()
	stopSilent()
	d2, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	if d2.Port() != port+1 {
		t.Errorf("port %d, want %d (the running runner's)", d2.Port(), port+1)
	}
	if xc := ts.calls("xcrun"); len(xc) != 1 {
		t.Errorf("xcodebuild ran %d times, want once: xcrun %q", len(xc), xc)
	}
}

// Issue #215: the holder of a silent port is named from lsof -Fpc output.
func TestLsofHolder(t *testing.T) {
	for out, want := range map[string]string{
		"p123\ncpython3\nf4\n":            "pid 123 (python3)",
		"p123\ncpython3\nf4\np456\ncnc\n": "pid 123 (python3)",
		"p123\n":                          "pid 123",
		"":                                "another process",
	} {
		if got := lsofHolder(out); got != want {
			t.Errorf("lsofHolder(%q) = %q, want %q", out, got, want)
		}
	}
}

// Issue #215: the preferred port is held by something that never says hello: the runner is
// started on the next port of the range, and what holds the port is left alone.
func TestConnectSkipsAPortThatNeverSaysHello(t *testing.T) {
	ts := stubTools(t)
	stubProcs(t)
	port := freePorts(t, 2)
	udid := udidFor(t, port)
	silentListener(t, port)
	started := startsRunnerAt(t, ts, port+1, udid, nil)
	d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if <-started == nil {
		t.Fatal("no new runner")
	}
	if d.Port() != port+1 {
		t.Errorf("port %d, want %d (the port after the silent one)", d.Port(), port+1)
	}
}

// Issue #215: every port of the simulator's range is held by something that never says hello:
// Open fails at once and says the ports are held, without starting a runner that cannot bind.
func TestConnectFailsWhenEveryPortIsHeld(t *testing.T) {
	ts := stubTools(t)
	stubProcs(t)
	old := portTries
	portTries = 3 // each silent port costs the probe's 2 s hello wait
	t.Cleanup(func() { portTries = old })
	port := freePorts(t, portTries)
	udid := udidFor(t, port)
	for p := port; p < port+portTries; p++ {
		silentListener(t, p)
	}
	_, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("port %d", port)) || !strings.Contains(err.Error(), "held") {
		t.Errorf("err = %v, want one saying port %d is held", err, port)
	}
	if xc := ts.calls("xcrun"); len(xc) != 0 {
		t.Errorf("a runner was started: xcrun %q", xc)
	}
}
