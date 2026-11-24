package web

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

var ffmpegPath = func() (string, error) {
	if p := os.Getenv("TB_FFMPEG"); p != "" {
		return p, nil
	}
	return exec.LookPath("ffmpeg")
}

var runFFmpeg = func(ctx context.Context, ffmpeg string, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput()
}

type recording struct {
	path   string
	dir    string
	ffmpeg string

	mu      sync.Mutex
	frames  []recFrame
	writes  sync.WaitGroup
	stopped bool
}

type recFrame struct {
	file string
	at   time.Time
}

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

func (rec *recording) stop() {
	rec.mu.Lock()
	rec.stopped = true
	rec.mu.Unlock()
	rec.writes.Wait()
}

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
	d.rec, d.lastRec = rec, rec
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

func (d *Driver) screencastParams() map[string]any {
	w, h := d.Size()
	return map[string]any{"format": "jpeg", "quality": 70, "maxWidth": w, "maxHeight": h, "everyNthFrame": 1}
}

func (d *Driver) onScreencastFrame(params json.RawMessage, session string) {
	if session != "" {
		return
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

func (d *Driver) RecordingStarted() time.Time {
	d.recMu.Lock()
	rec := d.lastRec
	d.recMu.Unlock()
	if rec == nil {
		return time.Time{}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.frames) == 0 {
		return time.Time{}
	}
	return rec.frames[0].at
}

func (d *Driver) RecordingClockExact() bool { return true }

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
	_ = stopErr
	return rec.encode(ctx, end)
}

const videoFPS = 25

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
		out2, err2 := runFFmpeg(ctx, rec.ffmpeg, args("-c:v", "mpeg4", "-q:v", "4"))
		if err2 != nil {
			return fmt.Errorf("recording: ffmpeg could not encode the video: %v: %s / %v: %s",
				err, strings.TrimSpace(string(out)), err2, strings.TrimSpace(string(out2)))
		}
	}
	return nil
}

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
