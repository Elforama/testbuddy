package annotate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"time"
)

var winscopeMagic = []byte("#VV1NSC0PET1ME2#")

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
