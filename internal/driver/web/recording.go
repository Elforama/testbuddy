package web

// Screen recording (startRecording / stopRecording, tb test --record): Chrome's screencast
// sends a JPEG frame each time the page repaints, so a still page sends none. Each frame is
// written to a temporary folder as it arrives, timed on arrival, and acknowledged (Chrome
// sends the next only then). Stopping encodes them at a constant frame rate, each frame
// shown until the next one arrived and the last until the stop: the video lasts as long as
// the recording did, still screens included. A screenshot taken at the start makes the video
// begin at once even on an idle page.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ffmpegPath finds ffmpeg: TB_FFMPEG, else the PATH. A variable so tests need no ffmpeg.
var ffmpegPath = func() (string, error) {
	if p := os.Getenv("TB_FFMPEG"); p != "" {
		return p, nil
	}
	return exec.LookPath("ffmpeg")
}

// runFFmpeg runs ffmpeg with args and returns what it printed. A variable so tests can check
// the encoding without ffmpeg.
var runFFmpeg = func(ctx context.Context, ffmpeg string, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
}

// recording is one screen recording in progress.
type recording struct {
	path   string // the video to write
	dir    string // the frames, until encoded
	ffmpeg string

	mu      sync.Mutex
	frames  []recFrame
	writes  sync.WaitGroup // frames still being written
	stopped bool           // frames arriving now are dropped
}

// recFrame is one frame file and when it arrived.
type recFrame struct {
	file string
	at   time.Time
}

// add files a frame under the next name, timed now, and writes it in the background (the
// read loop must not wait on the disk). It returns false once the recording has stopped.
func (rec *recording) add(data []byte) bool {
	rec.mu.Lock()
	if rec.stopped {
		rec.mu.Unlock()
		return false
	}
	f := recFrame{file: fmt.Sprintf("frame-%06d.jpg", len(rec.frames)), at: time.Now()}
	rec.frames = append(rec.frames, f)
	rec.writes.Add(1)
	rec.mu.Unlock()
	go func() {
		defer rec.writes.Done()
		os.WriteFile(filepath.Join(rec.dir, f.file), data, 0o644)
	}()
	return true
}

// stop ends taking frames and waits for those being written.
func (rec *recording) stop() {
	rec.mu.Lock()
	rec.stopped = true
	rec.mu.Unlock()
	rec.writes.Wait()
}

// StartRecording starts recording the page into path (an .mp4 written by StopRecording). It
// returns once the screencast runs, so what follows is recorded.
func (d *Driver) StartRecording(ctx context.Context, path string) error {
	ffmpeg, err := ffmpegPath()
	if err != nil || ffmpeg == "" {
		return errors.New("web recording needs ffmpeg on PATH (or TB_FFMPEG)")
	}
	d.recMu.Lock()
	if d.rec != nil {
		d.recMu.Unlock()
		return errors.New("already recording")
	}
	dir, err := os.MkdirTemp("", "tb-recording-")
	if err != nil {
		d.recMu.Unlock()
		return err
	}
	rec := &recording{path: path, dir: dir, ffmpeg: ffmpeg}
	d.rec = rec
	d.recMu.Unlock()
	fail := func(err error) error {
		d.recMu.Lock()
		if d.rec == rec {
			d.rec = nil
		}
		d.recMu.Unlock()
		rec.stop()
		os.RemoveAll(dir)
		return err
	}

	// The first frame: the page as it is now, so the video starts here even if nothing repaints.
	// Bounded like any screenshot: one the browser never answers fails the start (issue #163).
	// A frame that does not decode is only skipped.
	img, err := d.capture(ctx, map[string]any{"format": "jpeg", "quality": 70})
	var bad base64.CorruptInputError
	if err != nil && !errors.As(err, &bad) {
		return fail(fmt.Errorf("recording: %w", err))
	}
	if err == nil && len(img) > 0 {
		rec.add(img)
	}
	if err := d.page.call(ctx, "Page.startScreencast", d.screencastParams(), nil); err != nil {
		return fail(fmt.Errorf("recording: %w", err))
	}
	return nil
}

// screencastParams starts a recording's screencast, in the driven tab or one switched to.
func (d *Driver) screencastParams() map[string]any {
	w, h := d.Size()
	return map[string]any{"format": "jpeg", "quality": 70, "maxWidth": w, "maxHeight": h, "everyNthFrame": 1}
}

// onScreencastFrame keeps a frame of the page's screencast and acknowledges it. It runs on
// the connection's read loop, so the acknowledgement is sent from another goroutine.
func (d *Driver) onScreencastFrame(params json.RawMessage, session string) {
	if session != "" {
		return // a frame target's own screencast: the page's shows the frames already
	}
	var e struct {
		Data      string `json:"data"`
		SessionID int    `json:"sessionId"`
	}
	if json.Unmarshal(params, &e) != nil {
		return
	}
	d.recMu.Lock()
	rec := d.rec
	d.recMu.Unlock()
	if rec != nil {
		if img, err := base64.StdEncoding.DecodeString(e.Data); err == nil && len(img) > 0 {
			rec.add(img)
		}
	}
	go d.answer("", "Page.screencastFrameAck", map[string]any{"sessionId": e.SessionID})
}

// StopRecording stops the screencast and encodes the frames to the recording's path. With no
// recording it does nothing.
func (d *Driver) StopRecording(ctx context.Context) error {
	d.recMu.Lock()
	rec := d.rec
	d.rec = nil
	d.recMu.Unlock()
	if rec == nil {
		return nil
	}
	end := time.Now()
	stopErr := d.page.call(ctx, "Page.stopScreencast", nil, nil)
	rec.stop()
	defer os.RemoveAll(rec.dir)
	if len(rec.frames) == 0 {
		return errors.New("recording: no frames")
	}
	// A screencast that would not stop (a page gone) still leaves its frames to encode.
	_ = stopErr
	return rec.encode(ctx, end)
}

// videoFPS is the recording's frame rate: each 1/videoFPS s shows the frame on screen then.
const videoFPS = 25

// encode writes the video at a constant videoFPS: tick k shows the last frame that arrived
// by k/videoFPS s after the first, up to end. The ticks are links to the frame files, named
// in order, so ffmpeg reads a plain image sequence and the video lasts exactly as long as
// the recording (ticks / videoFPS), whatever its ffmpeg version.
func (rec *recording) encode(ctx context.Context, end time.Time) error {
	first := rec.frames[0].at
	ticks := max(1, int(end.Sub(first).Seconds()*videoFPS+0.5))
	cur := 0
	for k := 0; k < ticks; k++ {
		t := first.Add(time.Duration(k) * time.Second / videoFPS)
		for cur+1 < len(rec.frames) && !rec.frames[cur+1].at.After(t) {
			cur++
		}
		src := filepath.Join(rec.dir, rec.frames[cur].file)
		dst := filepath.Join(rec.dir, fmt.Sprintf("tick-%06d.jpg", k))
		if err := os.Link(src, dst); err != nil {
			b, rerr := os.ReadFile(src)
			if rerr != nil {
				return fmt.Errorf("recording: %w", rerr)
			}
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return fmt.Errorf("recording: %w", err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(rec.path), 0o755); err != nil {
		return err
	}
	args := func(codec ...string) []string {
		a := []string{"-hide_banner", "-loglevel", "error", "-y", "-framerate", fmt.Sprint(videoFPS),
			"-i", filepath.Join(rec.dir, "tick-%06d.jpg"), "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2"}
		a = append(a, codec...)
		return append(a, "-pix_fmt", "yuv420p", "-movflags", "+faststart", rec.path)
	}
	out, err := runFFmpeg(ctx, rec.ffmpeg, args("-c:v", "libx264", "-preset", "veryfast"))
	if err != nil {
		// No libx264 in this ffmpeg build: MPEG-4, which most players still play.
		out2, err2 := runFFmpeg(ctx, rec.ffmpeg, args("-c:v", "mpeg4", "-q:v", "4"))
		if err2 != nil {
			return fmt.Errorf("recording: ffmpeg could not encode the video: %v: %s / %v: %s",
				err, strings.TrimSpace(string(out)), err2, strings.TrimSpace(string(out2)))
		}
	}
	return nil
}

// discardRecording drops a recording in progress (the browser is closing): its frames are
// deleted and nothing is encoded.
func (d *Driver) discardRecording() {
	d.recMu.Lock()
	rec := d.rec
	d.rec = nil
	d.recMu.Unlock()
	if rec != nil {
		rec.stop()
		os.RemoveAll(rec.dir)
	}
}
