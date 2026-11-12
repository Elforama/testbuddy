package devlock

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain: run as a helper that holds a lock until killed, when asked to.
func TestMain(m *testing.M) {
	if serial := os.Getenv("DEVLOCK_HOLD"); serial != "" {
		if _, err := TryAcquire(serial, "tb test helper"); err != nil {
			os.Stdout.WriteString("ERR " + err.Error() + "\n")
			os.Exit(1)
		}
		os.Stdout.WriteString("HELD\n")
		time.Sleep(time.Hour)
	}
	os.Exit(m.Run())
}

// A second lock on a held device fails with who holds it; releasing frees it.
func TestTryAcquireSameProcess(t *testing.T) {
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	a, err := TryAcquire("emulator-5554", "tb test flows/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = TryAcquire("emulator-5554", "tb session alice")
	b, busy := IsBusy(err)
	if !busy || b.Serial != "emulator-5554" || b.Owner.What != "tb test flows/" || b.Owner.PID != os.Getpid() {
		t.Fatalf("second lock: %v", err)
	}
	if !strings.Contains(err.Error(), "device emulator-5554 is in use by tb test flows/ (pid ") {
		t.Errorf("message %q", err)
	}
	// Another device is free.
	c, err := TryAcquire("6C34A72F-2B11-4374-B939-B152D38B32E0", "tb test")
	if err != nil {
		t.Fatal(err)
	}
	c.Release()
	a.Release()
	a.Release() // twice is fine
	d, err := TryAcquire("emulator-5554", "tb session alice")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	d.Release()
	var none *Lock
	none.Release() // a nil lock is fine
}

// A lock held by another process is refused, with that process's pid; when the process dies
// (killed, no clean-up) the device is free again.
func TestLockAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TB_LOCK_DIR", dir)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "DEVLOCK_HOLD=emulator-5556", "TB_LOCK_DIR="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line, _ := bufio.NewReader(out).ReadString('\n')
	if strings.TrimSpace(line) != "HELD" {
		t.Fatalf("helper: %q", line)
	}
	_, err = TryAcquire("emulator-5556", "tb test")
	b, busy := IsBusy(err)
	if !busy || b.Owner.PID != cmd.Process.Pid || b.Owner.What != "tb test helper" {
		t.Fatalf("held by the helper: %v (helper pid %d)", err, cmd.Process.Pid)
	}
	// Waiting: the helper is killed meanwhile.
	go func() {
		time.Sleep(300 * time.Millisecond)
		cmd.Process.Kill()
	}()
	start := time.Now()
	l, err := Acquire(context.Background(), "emulator-5556", "tb test", 5*time.Second)
	if err != nil {
		t.Fatalf("after the holder died: %v", err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 3*time.Second {
		t.Errorf("waited %s", d)
	}
	l.Release()
	cmd.Wait()
}

// Acquire with no wait, or a wait that runs out, returns Busy.
func TestAcquireTimesOut(t *testing.T) {
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	a, err := TryAcquire("x", "first")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	start := time.Now()
	if _, err := Acquire(context.Background(), "x", "second", 700*time.Millisecond); err == nil {
		t.Fatal("acquired a held lock")
	} else if _, busy := IsBusy(err); !busy {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(start); d < 650*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %s, want about 700 ms", d)
	}
	if _, err := Acquire(context.Background(), "x", "second", 0); err == nil {
		t.Error("no wait: acquired a held lock")
	}
}

// issue #115: a wait for a held device stops when its context is cancelled (Ctrl-C while tb
// waits for a device another run holds), at once, with the context's error and no lock held.
// It used to sleep through the whole wait.
func TestAcquireCancelled(t *testing.T) {
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	a, err := TryAcquire("x", "first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type got struct {
		l   *Lock
		err error
	}
	done := make(chan got, 1)
	go func() {
		l, err := Acquire(ctx, "x", "second", 30*time.Second)
		done <- got{l, err}
	}()
	time.Sleep(200 * time.Millisecond) // waiting
	cancel()
	var g got
	select {
	case g = <-done:
	case <-time.After(5 * time.Second):
		a.Release() // let the waiter go
		t.Fatal("issue #115: Acquire did not return within 5s of its context being cancelled")
	}
	if !errors.Is(g.err, context.Canceled) || g.l != nil {
		t.Errorf("issue #115: Acquire after cancel = %v, %v; want no lock and context.Canceled", g.l, g.err)
		g.l.Release()
	}
	// No lock left held: the device is free once the first holder lets go.
	a.Release()
	l, err := TryAcquire("x", "third")
	if err != nil {
		t.Fatalf("after the cancelled wait: %v", err)
	}
	l.Release()
	// Cancelled before it starts: no lock taken, even of a free device.
	if l, err := Acquire(ctx, "x", "fourth", time.Second); !errors.Is(err, context.Canceled) || l != nil {
		t.Errorf("issue #115: Acquire on a cancelled context = %v, %v; want context.Canceled", l, err)
		l.Release()
	}
}

// Serials with odd characters make safe file names; the folder follows TB_LOCK_DIR.
func TestPathAndDir(t *testing.T) {
	t.Setenv("TB_LOCK_DIR", "/tmp/locks-x")
	if got := path("192.168.1.5:5555"); got != filepath.FromSlash("/tmp/locks-x/192.168.1.5_5555.lock") {
		t.Errorf("path %q", got)
	}
	t.Setenv("TB_LOCK_DIR", "")
	if !strings.HasSuffix(Dir(), filepath.Join(".testbuddy", "locks")) {
		t.Errorf("default dir %q", Dir())
	}
	o := Owner{PID: 42, What: "tb session bob", Dir: "/w", Since: time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)}
	if o.String() != "tb session bob (pid 42, since 03:04:05, in /w)" {
		t.Errorf("owner %q", o)
	}
	if (Owner{PID: 1}).String() != "another tb (pid 1)" {
		t.Error("unnamed owner")
	}
}
