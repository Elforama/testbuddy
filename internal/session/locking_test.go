package session

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
)

func blockTyping(f *fakeDriver) (started, unblock chan struct{}) {
	started, unblock = make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.mu.Lock()
	f.onType = func(string) {
		once.Do(func() { close(started) })
		<-unblock
	}
	f.mu.Unlock()
	return started, unblock
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

func TestCloseWaitsForRunningCommand(t *testing.T) {
	for _, dev := range []string{"alice", "all"} {
		t.Run(dev, func(t *testing.T) {
			t.Setenv("TB_HOME", t.TempDir())
			s := New()
			f := newFake("emulator-5554", "Pixel 7", device.Android)
			f.show(loginScreen()...)
			d := addDev(s, f, "alice")
			started, unblock := blockTyping(f)
			doDone := make(chan daemon.Response, 1)
			go func() {
				r, _ := call(t, s.do, map[string]any{"device": "alice", "commands": "inputText: hello"}, "")
				doDone <- r
			}()
			waitFor(t, started, "the do's step")

			closeDone := make(chan daemon.Response, 1)
			go func() {
				r, _ := call(t, s.close, map[string]any{"device": dev}, "")
				closeDone <- r
			}()
			select {
			case r := <-closeDone:
				var closed int
				f.get(func() { closed = f.closed })
				close(unblock)
				<-doDone
				t.Fatalf("issue #153: close %s returned (%q) while a do was mid-step; the driver was closed %d times under it", dev, r.Text, closed)
			case <-time.After(300 * time.Millisecond):
			}
			f.get(func() {
				if f.closed != 0 {
					t.Errorf("issue #153: the driver was closed under a running do")
				}
			})

			close(unblock)
			if r := <-doDone; !r.OK {
				t.Errorf("do = %+v", r)
			}
			select {
			case r := <-closeDone:
				if !r.OK {
					t.Errorf("close = %+v", r)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("close did not end after the do did")
			}
			f.get(func() {
				if f.closed != 1 || f.released != 1 {
					t.Errorf("closed %d, released %d: want the agent released once", f.closed, f.released)
				}
			})
			if err := s.lockOpen(d); err == nil || !strings.Contains(err.Error(), "was closed") {
				t.Errorf("a command after the close: %v", err)
				if err == nil {
					d.mu.Unlock()
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if len(s.devs) != 0 || len(s.names) != 0 {
				t.Errorf("devs %v names %v after close", s.devs, s.names)
			}
		})
	}
}

func TestDroppedConnectionWaitsForRunningCommand(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	s := New()
	s.openDrv = func(context.Context, device.Info) (driver.Driver, error) { return f, nil }
	if _, err := s.connect(context.Background(), f.info, "alice"); err != nil {
		t.Fatal(err)
	}
	free := func() bool {
		l, err := devlock.TryAcquire("emulator-5554", "tb test")
		if err != nil {
			return false
		}
		l.Release()
		return true
	}
	if free() {
		t.Fatal("the session does not hold its device")
	}
	started, unblock := blockTyping(f)
	doDone := make(chan daemon.Response, 1)
	go func() {
		r, _ := call(t, s.do, map[string]any{"device": "alice", "commands": "inputText: hello"}, "")
		doDone <- r
	}()
	waitFor(t, started, "the do's step")

	f.push("disconnected")
	forgotten := make(chan struct{})
	go func() {
		defer close(forgotten)
		for {
			s.mu.Lock()
			_, open := s.devs["emulator-5554"]
			s.mu.Unlock()
			if !open {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	waitFor(t, forgotten, "forgetting the dropped device")
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if free() {
			t.Errorf("issue #153: the dropped session freed its device while a do was still running on it")
			break
		}
	}
	if _, err := s.connect(context.Background(), f.info, "alice"); err == nil || !strings.Contains(err.Error(), "is still closing") {
		t.Errorf("reopening a device whose dropped session still closes: %v", err)
	}

	close(unblock)
	<-doDone
	var closed int
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.get(func() { closed = f.closed })
		if (closed == 1 && free()) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !free() {
		t.Error("the dropped session did not free its device after the do ended")
	}
	if closed != 1 {
		t.Errorf("issue #124: the dropped session's driver was closed %d times, want 1", closed)
	}
}

func TestSessionNameNoRace(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	addDev(s, f, "")
	shotsDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-shotsDone:
				return
			default:
			}
			if _, err := s.connect(context.Background(), f.info, "alice"); err != nil {
				t.Errorf("connect: %v", err)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	for i := 0; i < 20; i++ {
		if r, _ := call(t, s.shot, map[string]any{"device": "emulator-5554"}, ""); !r.OK {
			t.Errorf("shot: %+v", r)
			break
		}
	}
	close(shotsDone)
	wg.Wait()
}

func TestCloseCancelledWhileWaiting(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	d := addDev(s, f, "alice")
	started, unblock := blockTyping(f)
	doDone := make(chan daemon.Response, 1)
	go func() {
		r, _ := call(t, s.do, map[string]any{"device": "alice", "commands": "inputText: hello"}, "")
		doDone <- r
	}()
	waitFor(t, started, "the do's step")

	ctx, cancel := context.WithCancel(context.Background())
	closeDone := make(chan daemon.Response, 1)
	go func() {
		r, _ := callCtx(t, ctx, s.close, map[string]any{"device": "alice"}, "")
		closeDone <- r
	}()
	cancel()
	close(unblock)
	if r := <-doDone; !r.OK {
		t.Errorf("do = %+v", r)
	}
	select {
	case r := <-closeDone:
		if r.OK || !strings.Contains(r.Text, "tb close was cancelled: alice (Pixel 7) is left open") {
			t.Errorf("cancelled close = %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled close did not end")
	}
	f.get(func() {
		if f.closed != 0 {
			t.Errorf("a cancelled close closed the driver %d times", f.closed)
		}
	})
	if err := s.lockOpen(d); err != nil {
		t.Errorf("the session after a cancelled close: %v", err)
	} else {
		d.mu.Unlock()
	}
	if got, err := s.resolve(context.Background(), devArgs{Device: "alice"}); err != nil || got != d {
		t.Errorf("resolve alice after a cancelled close = %v, %v", got, err)
	}
}

func TestCloseAllSideBySide(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	s := New()
	busy := newFake("emulator-5554", "Pixel 7", device.Android)
	busy.show(loginScreen()...)
	idle := newFake("UDID-1", "iPhone 17", device.IOS)
	addDev(s, busy, "alice")
	addDev(s, idle, "bob")
	started, unblock := blockTyping(busy)
	doDone := make(chan daemon.Response, 1)
	go func() {
		r, _ := call(t, s.do, map[string]any{"device": "alice", "commands": "inputText: hello"}, "")
		doDone <- r
	}()
	waitFor(t, started, "the do's step")

	closeDone := make(chan daemon.Response, 1)
	go func() {
		r, _ := call(t, s.close, map[string]any{"device": "all"}, "")
		closeDone <- r
	}()
	idleClosed := make(chan struct{})
	go func() {
		defer close(idleClosed)
		for {
			var n int
			idle.get(func() { n = idle.released })
			if n == 1 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	select {
	case <-idleClosed:
	case <-time.After(5 * time.Second):
		close(unblock)
		t.Fatal("close all did not close the idle device while the other one was busy")
	}
	busy.get(func() {
		if busy.closed != 0 {
			t.Error("issue #153: the busy device was closed under its running do")
		}
	})
	close(unblock)
	<-doDone
	select {
	case r := <-closeDone:
		if !r.OK || r.Text != "closed all devices\n" {
			t.Errorf("close all = %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close all did not end after the do did")
	}
	busy.get(func() {
		if busy.released != 1 {
			t.Errorf("the busy device's agent was released %d times, want 1", busy.released)
		}
	})
}

func TestCloseStopsTheAgentAfterADrop(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	for _, taken := range []bool{false, true} {
		s := New()
		f := newFake("emulator-5554", "Pixel 7", device.Android)
		d := addDev(s, f, "alice")
		d.mu.Lock()
		d.shut(false)
		d.mu.Unlock()
		var other *devlock.Lock
		if taken {
			var err error
			if other, err = devlock.TryAcquire("emulator-5554", "tb test flows/"); err != nil {
				t.Fatal(err)
			}
		}
		label, err := s.closeDev(context.Background(), d)
		other.Release()
		var released int
		f.get(func() { released = f.released })
		if !taken && (err != nil || released != 1) {
			t.Errorf("close after a drop: %v; agent released %d times, want 1", err, released)
		}
		if taken && (err == nil || !strings.Contains(err.Error(), "tb's agent was left on it: the device is in use by tb test flows/") || released != 0) {
			t.Errorf("close after a drop, device taken by another tb: %v; agent released %d times, want 0", err, released)
		}
		if label != "alice (Pixel 7)" {
			t.Errorf("label %q", label)
		}
	}
}
