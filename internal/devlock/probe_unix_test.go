//go:build !windows

package devlock

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// Review of issue #398: a probe holding the lock for an instant does not get a tb refused:
// TryAcquire tries again.
func TestTryAcquireRidesOutAProbe(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	l, err := TryAcquire("emulator-5554", "tb test")
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	f, err := os.Open(path("emulator-5554"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if held, err := heldElsewhere(f); err != nil || held {
		t.Fatalf("probe: %v %v", held, err)
	}
	// the probe's shared lock and mark, held a moment as a slow probe would
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(probeMark("emulator-5554"), nil, 0o644)
	released := make(chan struct{})
	go func() {
		time.Sleep(time.Millisecond)
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		os.Remove(probeMark("emulator-5554"))
		close(released)
	}()
	l, err = TryAcquire("emulator-5554", "tb test")
	<-released
	if err != nil {
		t.Errorf("refused during a probe: %v", err)
	}
	l.Release()

	// A lock really held (no probe) is refused at once, not waited for.
	held, _ := TryAcquire("emulator-5554", "tb session")
	defer held.Release()
	start := time.Now()
	if _, err := TryAcquire("emulator-5554", "tb test"); err == nil {
		t.Error("a held lock was taken")
	}
	if d := time.Since(start); d >= probeWait {
		t.Errorf("a held lock took %s to refuse, want at once", d)
	}
}
