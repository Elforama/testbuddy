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
	// the probe's shared lock, held a moment as a slow probe would
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(probeWait / 2); syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	l, err = TryAcquire("emulator-5554", "tb test")
	if err != nil {
		t.Errorf("refused during a probe: %v", err)
	}
	l.Release()
}
