package session

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
)

// issue #58: SIGTERM (or Ctrl-C) stops the daemon the orderly way, closing every open
// session's driver before it exits.
func TestDaemonSignalClosesDrivers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGTERM to send on Windows")
	}
	s := New()
	web := newFake("chrome", "Chrome", device.Web)
	addDev(s, web, "")
	srv, served := serveService(t, s, time.Hour)
	defer srv.StopOnSignals(syscall.SIGTERM)()
	dialDaemon(t).Close() // listening
	p, _ := os.FindProcess(os.Getpid())
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitServed(t, served, "SIGTERM")
	checkClosed(t, s, "SIGTERM", web)
}

// closeWatch is a driver whose Close records whether a command still held the device.
type closeWatch struct {
	*fakeDriver
	dev      *Dev
	wasInUse atomic.Bool
}

func (c *closeWatch) Close() error {
	if c.dev.mu.TryLock() {
		c.dev.mu.Unlock()
	} else {
		c.wasInUse.Store(true)
	}
	return c.fakeDriver.Close()
}

// issue #58: a command still running when the daemon stops is cancelled, and has ended,
// before its device's driver is closed; its client is told why.
func TestDaemonStopCancelsCommandsBeforeClosing(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "")
	w := &closeWatch{fakeDriver: f, dev: d}
	d.Drv = w
	_, served := serveService(t, s, time.Hour)
	request := func(c net.Conn, cmd string, args any) {
		t.Helper()
		raw, _ := json.Marshal(args)
		b, _ := json.Marshal(daemon.Request{Cmd: cmd, Args: raw})
		if _, err := c.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	first := dialDaemon(t)
	defer first.Close()
	request(first, "do", map[string]any{"commands": "extendedWaitUntil:\n  visible: Never there\n  timeout: 20000\n"})
	deadline := time.Now().Add(5 * time.Second)
	for d.mu.TryLock() { // the wait has started once it holds the device
		d.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the do did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	second := dialDaemon(t)
	defer second.Close()
	request(second, "stop", nil)
	got := make(chan daemon.Response, 1)
	go func() {
		line, err := bufio.NewReader(first).ReadBytes('\n')
		var resp daemon.Response
		if err == nil {
			json.Unmarshal(line, &resp)
		}
		got <- resp
	}()
	select {
	case resp := <-got:
		if resp.OK || !strings.Contains(resp.Text, "the tb daemon stopped") {
			t.Errorf("the running do answered %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the running do was not cancelled when the daemon stopped")
	}
	waitServed(t, served, "tb daemon stop")
	checkClosed(t, s, "tb daemon stop", f)
	if w.wasInUse.Load() {
		t.Error("the driver was closed while a command still held the device")
	}
}

// stuckDriver's Close never returns (until the test ends); Kill is counted.
type stuckDriver struct {
	*fakeDriver
	unblock chan struct{}
	kills   atomic.Int32
}

func (s *stuckDriver) Close() error { <-s.unblock; return nil }
func (s *stuckDriver) Kill()        { s.kills.Add(1) }

// issue #58: a driver that does not close in time cannot keep the daemon from exiting:
// CloseAll returns when its time is up, and kills what it can (web: Chrome's process).
func TestCloseAllKillsAStuckDriver(t *testing.T) {
	s := New()
	f := newFake("chrome", "Chrome", device.Web)
	d := addDev(s, f, "")
	stuck := &stuckDriver{fakeDriver: f, unblock: make(chan struct{})}
	defer close(stuck.unblock)
	d.Drv = stuck
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	returned := make(chan struct{})
	go func() { s.CloseAll(ctx); close(returned) }()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll waits on a driver that does not close")
	}
	if n := stuck.kills.Load(); n != 1 {
		t.Errorf("killed %d times, want 1", n)
	}
}

// serveService runs s on a daemon server with the given idle limit; served receives Serve's
// result.
func serveService(t *testing.T, s *Service, idle time.Duration) (*daemon.Server, chan error) {
	t.Helper()
	t.Setenv("TB_HOME", t.TempDir())
	srv := daemon.NewServer(idle)
	s.Register(srv)
	served, done := make(chan error, 1), make(chan struct{})
	go func() { served <- srv.Serve(); close(done) }()
	t.Cleanup(func() {
		srv.Stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
	})
	return srv, served
}

func dialDaemon(t *testing.T) net.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("unix", daemon.SocketPath())
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not listen: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // server start-up only
	}
}

func waitServed(t *testing.T, served chan error, why string) {
	t.Helper()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("the daemon did not exit after %s", why)
	}
}

// checkClosed checks that every open session's driver was closed once, and that phone
// agents were kept for the next run (Close, not Release: the same as a `tb test` run's end).
func checkClosed(t *testing.T, s *Service, why string, fakes ...*fakeDriver) {
	t.Helper()
	for _, f := range fakes {
		var closed, released int
		f.get(func() { closed, released = f.closed, f.released })
		if closed != 1 {
			t.Errorf("issue #58: after %s, %s's driver was closed %d times, want 1 (a web session's driver left open leaves Chrome and its profile behind)", why, f.info.Serial, closed)
		}
		if released != 0 {
			t.Errorf("after %s, %s's agent was released %d times, want 0 (kept for the next run)", why, f.info.Serial, released)
		}
	}
	s.mu.Lock()
	n := len(s.devs)
	s.mu.Unlock()
	if n != 0 {
		t.Errorf("after %s, %d sessions are still open", why, n)
	}
}

// issue #58: `tb daemon stop` closes every open session's driver before the daemon exits
// (a web session's Chrome and its temporary profile were left behind), and the stop
// command's answer still reaches the client.
func TestDaemonStopClosesDrivers(t *testing.T) {
	s := New()
	web := newFake("chrome", "Chrome", device.Web)
	phone := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, web, "alice")
	addDev(s, phone, "bob")
	_, served := serveService(t, s, time.Hour)

	c := dialDaemon(t)
	defer c.Close()
	b, _ := json.Marshal(daemon.Request{Cmd: "stop"})
	if _, err := c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		t.Fatalf("the stop command's answer did not arrive: %v", err)
	}
	var resp daemon.Response
	json.Unmarshal(line, &resp)
	if !resp.OK || resp.Text != "tb daemon stopped" {
		t.Errorf("stop = %+v", resp)
	}
	waitServed(t, served, "tb daemon stop")
	checkClosed(t, s, "tb daemon stop", web, phone)
}

// issue #58: the daemon's idle exit closes every open session's driver too.
func TestDaemonIdleExitClosesDrivers(t *testing.T) {
	s := New()
	web := newFake("chrome", "Chrome", device.Web)
	phone := newFake("emulator-5554", "Pixel 7", device.Android)
	addDev(s, web, "")
	addDev(s, phone, "bob")
	_, served := serveService(t, s, 50*time.Millisecond)
	waitServed(t, served, "its idle limit")
	checkClosed(t, s, "the idle exit", web, phone)
}
