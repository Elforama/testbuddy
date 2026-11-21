package android

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A screen recording's own frame times, read with the device's clock against tb's, give when
// its first frame showed by tb's clock (--annotate; CI: the touches alone lined up too rarely,
// 9-11 of 24, to time the drawings).
func TestVideoStartedFromFrameTimes(t *testing.T) {
	ahead := 2*time.Second + 250*time.Millisecond // the device's clock ahead of tb's
	devNow := time.Now().Add(ahead)
	a := shAgent(t, map[string]shAnswer{"date +%s%N": {fmt.Sprintf("%d\n", devNow.UnixNano()), 0}})
	d := newDriver(t, a)

	// a video whose first frame showed 1.4 s ago, by the device's clock
	elapsed := uint64(600 * time.Second)
	first := devNow.Add(-1400 * time.Millisecond)
	b := []byte("#VV1NSC0PET1ME2#")
	b = binary.LittleEndian.AppendUint32(b, 2)
	b = binary.LittleEndian.AppendUint64(b, uint64(first.UnixNano()-int64(elapsed)))
	b = binary.LittleEndian.AppendUint32(b, 1)
	b = binary.LittleEndian.AppendUint64(b, elapsed)
	path := filepath.Join(t.TempDir(), "recording.mp4")
	os.WriteFile(path, append([]byte("....mdat"), b...), 0o644)

	got, ok := d.VideoStarted(ctxT(t), path)
	want := first.Add(-ahead)
	if !ok || got.Sub(want).Abs() > 100*time.Millisecond {
		t.Fatalf("VideoStarted = %v, %v; want about %v", got, ok, want)
	}
	if n := len(a.requests("sh")); n != clockSamples {
		t.Errorf("%d clock reads, want %d", n, clockSamples)
	}
	d.VideoStarted(ctxT(t), path)
	if n := len(a.requests("sh")); n != clockSamples {
		t.Errorf("the clock was measured again: %d reads", n)
	}
}

// No frame times (an older Android), or a device whose date has no %N: no answer.
func TestVideoStartedUnknown(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"date +%s%N": {"1763760000N\n", 0}})
	d := newDriver(t, a)
	path := filepath.Join(t.TempDir(), "recording.mp4")
	os.WriteFile(path, []byte("....mdat...."), 0o644)
	if _, ok := d.VideoStarted(ctxT(t), path); ok {
		t.Error("a video with no frame times was timed")
	}
	b := []byte("#VV1NSC0PET1ME2#")
	b = binary.LittleEndian.AppendUint32(b, 2)
	b = binary.LittleEndian.AppendUint64(b, uint64(time.Now().UnixNano()))
	b = binary.LittleEndian.AppendUint32(b, 1)
	b = binary.LittleEndian.AppendUint64(b, 1)
	os.WriteFile(path, b, 0o644)
	if _, ok := d.VideoStarted(ctxT(t), path); ok {
		t.Error("timed with a device clock that could not be read")
	}
}
