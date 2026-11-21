package annotate

// An Android screen recording says when each of its frames showed. screenrecord adds to the
// MP4 a data track for Android's Winscope (frameworks/av, cmds/screenrecord/screenrecord.cpp,
// writeWinscopeMetadata), version 2 laid out, little endian:
//
//	"#VV1NSC0PET1ME2#"  16 bytes
//	version             uint32 (2)
//	realtime - elapsed  int64, ns: the device's wall clock less its elapsed-realtime clock
//	frames              uint32
//	per frame           uint64, ns: when it showed, on the elapsed-realtime clock
//
// So the video's first frame showed at the device's wall-clock time first + offset: the exact
// start of the video by the device's clock, which tb's own guess (when screenrecord said it
// started) misses by however long the first frame took.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"time"
)

var winscopeMagic = []byte("#VV1NSC0PET1ME2#")

// FirstFrame reads, from the screen recording at path, when its first frame showed by the
// device's wall clock. An error when the file holds no frame times (older Android versions
// write none, or only the legacy kind, which has no wall-clock offset).
func FirstFrame(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	return firstFrame(b)
}

func firstFrame(b []byte) (time.Time, error) {
	i := bytes.LastIndex(b, winscopeMagic)
	if i < 0 {
		return time.Time{}, errors.New("no frame times in the video")
	}
	p := b[i+len(winscopeMagic):]
	const head = 4 + 8 + 4
	if len(p) < head+8 {
		return time.Time{}, errors.New("frame times cut short")
	}
	le := binary.LittleEndian
	if v := le.Uint32(p); v != 2 {
		return time.Time{}, errors.New("frame times of an unknown version")
	}
	offset := int64(le.Uint64(p[4:]))
	n := le.Uint32(p[12:])
	if n == 0 {
		return time.Time{}, errors.New("no frames")
	}
	first := int64(le.Uint64(p[head:]))
	t := time.Unix(0, first+offset)
	if t.Year() < 2000 {
		return time.Time{}, errors.New("frame times off the calendar")
	}
	return t, nil
}
