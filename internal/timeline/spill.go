package timeline

// A run's screenshots go to one temp file per recorder as they are taken (issue #65), each
// appended at the file's end and remembered by offset and length, so a long run does not
// keep them all in memory. Writing the page copies each one from that file through a
// base64 encoder straight to the page (render.go), so it never holds them all either.
//
// Disk trouble never fails a run or loses a screenshot: when the temp file cannot be made
// or written, that screenshot and every later one stay in memory, as they all did before,
// and the recorder says so once (OnWarning). Close removes the file.

import (
	"fmt"
	"io"
	"os"
)

// spillFile is where screenshots are kept: an *os.File in the temp dir.
type spillFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
	Name() string
}

func newTempSpill() (spillFile, error) { return os.CreateTemp("", "tb-timeline-*.tmp") }

// OnWarning sets where the recorder reports trouble that does not stop it: the temp file
// for screenshots could not be written, so they are kept in memory. It is called at most
// once, without the recorder's lock held.
func (r *Recorder) OnWarning(fn func(msg string)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warn = fn
}

// storeLocked puts a screenshot's bytes in the temp file, made on the first screenshot, or
// in memory when that fails or the recorder is closed. It returns a warning the first time
// the temp file fails.
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
	// At the end the recorder keeps, not the file's: what a failed write left there is
	// never read.
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

// Close removes the recorder's temp file. Call it once the page is written, or when it
// will not be: a page written after Close lacks the screenshots that were in the file
// (WriteFile fails). Screenshots taken after Close are kept in memory. Safe to call more
// than once, and on a nil recorder.
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
	r.files.Lock() // waits for a page being written
	defer r.files.Unlock()
	discard(r.spill)
	r.spill = nil
}

func discard(f spillFile) {
	f.Close()
	os.Remove(f.Name())
}

// memBytes is how many screenshot bytes the recorder holds in memory (tests).
func (r *Recorder) memBytes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.shots {
		n += len(s.Data)
	}
	return n
}

// spillPath is the temp file's path, "" when there is none (tests).
func (r *Recorder) spillPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.spill == nil {
		return ""
	}
	return r.spill.Name()
}
