package annotate

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// winscopeTrack is the frame-time data screenrecord writes (version 2).
func winscopeTrack(offset int64, frames ...uint64) []byte {
	b := append([]byte(nil), winscopeMagic...)
	b = binary.LittleEndian.AppendUint32(b, 2)
	b = binary.LittleEndian.AppendUint64(b, uint64(offset))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(frames)))
	for _, f := range frames {
		b = binary.LittleEndian.AppendUint64(b, f)
	}
	return b
}

// An Android recording's own frame times give its start by the device's clock (CI: the
// touches alone lined up too rarely, 9-11 of 24, to time the drawings).
func TestFirstFrame(t *testing.T) {
	wall := time.Date(2026, 11, 21, 21, 40, 3, 250_000_000, time.UTC)
	elapsed := uint64(812 * time.Second)
	offset := wall.UnixNano() - int64(elapsed)
	video := append([]byte("....ftyp....mdat"), make([]byte, 4096)...) // the frames
	video = append(video, winscopeTrack(offset, elapsed, elapsed+uint64(33*time.Millisecond))...)
	path := filepath.Join(t.TempDir(), "recording.mp4")
	os.WriteFile(path, append(video, "....moov...."...), 0o644)
	got, err := FirstFrame(path)
	if err != nil || !got.Equal(wall) {
		t.Errorf("FirstFrame = %v, %v; want %v", got, err, wall)
	}
}

func TestFirstFrameNone(t *testing.T) {
	for name, b := range map[string][]byte{
		"no track":      []byte("....ftyp....mdat....moov"),
		"legacy only":   append([]byte("#VV1NSC0PET1ME!#"), make([]byte, 20)...),
		"cut short":     winscopeTrack(0)[:20],
		"no frames":     winscopeTrack(1_700_000_000_000_000_000),
		"not wall time": winscopeTrack(0, 5_000_000_000),
	} {
		if got, err := firstFrame(b); err == nil {
			t.Errorf("%s: %v, want an error", name, got)
		}
	}
}
