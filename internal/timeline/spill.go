package timeline

import (
	"fmt"
	"io"
	"os"
)

type spillFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
	Name() string
}

func newTempSpill() (spillFile, error) { return os.CreateTemp("", "tb-timeline-*.tmp") }

func (r *Recorder) OnWarning(fn func(msg string)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warn = fn
}

func (r *Recorder) storeLocked(s *Shot, data []byte) (warning string) {
	if r.closed || r.diskErr != nil {
		s.Data = data
		return ""
	}
	if r.spill == nil {
		mk := r.newSpill
		if mk == nil {
			mk = newTempSpill
		}
		f, err := mk()
		if err != nil {
			return r.diskFailedLocked(s, data, err)
		}
		r.files.Lock()
		r.spill = f
		r.files.Unlock()
	}
	if _, err := r.spill.WriteAt(data, r.spillEnd); err != nil {
		return r.diskFailedLocked(s, data, err)
	}
	s.onDisk, s.off, s.n = true, r.spillEnd, int64(len(data))
	r.spillEnd += s.n
	return ""
}

func (r *Recorder) diskFailedLocked(s *Shot, data []byte, err error) string {
	r.diskErr = err
	s.Data = data
	return fmt.Sprintf("timeline: cannot keep screenshots in a temp file (%v); keeping them in memory", err)
}

func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.spill == nil {
		return
	}
	r.files.Lock()
	defer r.files.Unlock()
	discard(r.spill)
	r.spill = nil
}

func discard(f spillFile) {
	f.Close()
	os.Remove(f.Name())
}

func (r *Recorder) memBytes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.shots {
		n += len(s.Data)
	}
	return n
}

func (r *Recorder) spillPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.spill == nil {
		return ""
	}
	return r.spill.Name()
}
