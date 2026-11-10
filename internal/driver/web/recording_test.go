package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// encoderCall is one run of the (fake) encoder: its args, and the image sequence it was given
// (one entry per 1/25 s tick), read while the files still exist.
type encoderCall struct {
	args  []string
	ticks []string // each tick's frame contents, in order
	dir   string   // the temp folder the frames are in
}

// shown collapses the ticks to the frames shown, in order, each with how many ticks it lasted.
func (c encoderCall) shown() (frames []string, ticks []int) {
	for _, f := range c.ticks {
		if n := len(frames); n > 0 && frames[n-1] == f {
			ticks[n-1]++
			continue
		}
		frames, ticks = append(frames, f), append(ticks, 1)
	}
	return frames, ticks
}

// fakeEncoder replaces ffmpeg: it records each call and, unless fail says otherwise, writes
// the output file.
type fakeEncoder struct {
	mu    sync.Mutex
	calls []encoderCall
	fail  func(args []string) error
}

func useFakeEncoder(t *testing.T) *fakeEncoder {
	t.Helper()
	e := &fakeEncoder{}
	oldPath, oldRun := ffmpegPath, runFFmpeg
	ffmpegPath = func() (string, error) { return "/fake/ffmpeg", nil }
	runFFmpeg = func(ctx context.Context, ffmpeg string, args []string) ([]byte, error) {
		c := encoderCall{args: args}
		for i, a := range args {
			if a == "-i" && i+1 < len(args) {
				pattern := args[i+1]
				c.dir = filepath.Dir(pattern)
				for k := 0; ; k++ {
					b, err := os.ReadFile(fmt.Sprintf(pattern, k))
					if err != nil {
						break
					}
					c.ticks = append(c.ticks, string(b))
				}
			}
		}
		e.mu.Lock()
		e.calls = append(e.calls, c)
		fail := e.fail
		e.mu.Unlock()
		if fail != nil {
			if err := fail(args); err != nil {
				return []byte("encoder said no"), err
			}
		}
		return nil, os.WriteFile(args[len(args)-1], []byte("mp4"), 0o644)
	}
	t.Cleanup(func() { ffmpegPath, runFFmpeg = oldPath, oldRun })
	return e
}

func (e *fakeEncoder) all() []encoderCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]encoderCall(nil), e.calls...)
}

// recordingPage is a fake page that answers the recording commands.
func recordingPage(t *testing.T) (*fakeCDP, *Driver) {
	page := newFakeCDP(t)
	page.on("Page.captureScreenshot", func(cdpMsg) any {
		return map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("first"))}
	})
	return page, newWebDriver(t, page, nil)
}

func screencastFrame(page *fakeCDP, id int, data string) {
	page.event("Page.screencastFrame", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(data)),
		"metadata": map[string]any{"timestamp": float64(time.Now().UnixNano()) / 1e9}, "sessionId": id}, "")
}

// A recording starts with a screenshot (the video begins at once, even on an idle page),
// then the screencast's frames, each acknowledged; stopping encodes them, each frame shown
// until the next arrived and the last until the stop, so the video lasts as long as the
// recording did.
func TestRecordingFramesAndTiming(t *testing.T) {
	enc := useFakeEncoder(t)
	page, d := recordingPage(t)
	out := filepath.Join(t.TempDir(), "out", "recording.mp4")
	start := time.Now()
	if err := d.StartRecording(ctxT(t), out); err != nil {
		t.Fatal(err)
	}
	sc := page.calls("Page.startScreencast")
	if len(sc) != 1 {
		t.Fatalf("startScreencast sent %d times", len(sc))
	}
	p := sc[0].params()
	if p["format"] != "jpeg" || p["maxWidth"] != float64(1280) || p["maxHeight"] != float64(800) || p["everyNthFrame"] != float64(1) {
		t.Errorf("startScreencast params = %v", p)
	}
	time.Sleep(150 * time.Millisecond)
	screencastFrame(page, 7, "one")
	eventually(t, "the first frame's ack", func() bool { return len(page.calls("Page.screencastFrameAck")) == 1 })
	time.Sleep(150 * time.Millisecond)
	screencastFrame(page, 8, "two")
	eventually(t, "the second frame's ack", func() bool { return len(page.calls("Page.screencastFrameAck")) == 2 })
	for i, a := range page.calls("Page.screencastFrameAck") {
		if got := a.params()["sessionId"]; got != float64(7+i) {
			t.Errorf("ack %d: sessionId %v", i, got)
		}
	}
	time.Sleep(150 * time.Millisecond)
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start).Seconds()
	if len(page.calls("Page.stopScreencast")) != 1 {
		t.Error("stopScreencast not sent")
	}
	calls := enc.all()
	if len(calls) != 1 {
		t.Fatalf("encoder ran %d times", len(calls))
	}
	c := calls[0]
	frames, ticks := c.shown()
	if got := strings.Join(frames, ","); got != "first,one,two" {
		t.Fatalf("frames shown = %s", got)
	}
	// Each frame shows for its ~150 ms or more: 25 ticks a second.
	for i, n := range ticks {
		if n < 3 || n > 15 {
			t.Errorf("%s shows for %d ticks, want about 4", frames[i], n)
		}
	}
	// The video lasts as long as the recording: ticks / 25.
	if got := float64(len(c.ticks)) / videoFPS; got < elapsed-0.25 || got > elapsed+0.05 {
		t.Errorf("the video lasts %.2f s, the recording %.2f s", got, elapsed)
	}
	args := strings.Join(c.args, " ")
	for _, want := range []string{"-framerate 25", "tick-%06d.jpg", "-c:v libx264", "-pix_fmt yuv420p", "-movflags +faststart",
		"scale=trunc(iw/2)*2:trunc(ih/2)*2", "-y"} {
		if !strings.Contains(args, want) {
			t.Errorf("encoder args lack %q: %s", want, args)
		}
	}
	if c.args[len(c.args)-1] != out {
		t.Errorf("output = %s, want %s", c.args[len(c.args)-1], out)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no video: %v", err)
	}
	if _, err := os.Stat(c.dir); !os.IsNotExist(err) {
		t.Errorf("the frames' folder is still there: %v", err)
	}
}

// Without libx264 the video is encoded with mpeg4.
func TestRecordingFallsBackToMPEG4(t *testing.T) {
	enc := useFakeEncoder(t)
	enc.fail = func(args []string) error {
		if strings.Contains(strings.Join(args, " "), "libx264") {
			return errors.New("Unknown encoder 'libx264'")
		}
		return nil
	}
	_, d := recordingPage(t)
	out := filepath.Join(t.TempDir(), "r.mp4")
	if err := d.StartRecording(ctxT(t), out); err != nil {
		t.Fatal(err)
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	calls := enc.all()
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[1].args, " "), "-c:v mpeg4") {
		t.Fatalf("encoder calls = %d, last %v", len(calls), calls[len(calls)-1].args)
	}
}

// Without ffmpeg, recording fails at once and says why; nothing starts.
func TestRecordingNeedsFFmpeg(t *testing.T) {
	useFakeEncoder(t)
	ffmpegPath = func() (string, error) { return "", errors.New("not found") }
	page, d := recordingPage(t)
	err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "r.mp4"))
	if err == nil || !strings.Contains(err.Error(), "web recording needs ffmpeg on PATH (or TB_FFMPEG)") {
		t.Fatalf("err = %v", err)
	}
	if len(page.calls("Page.startScreencast")) != 0 {
		t.Error("the screencast started anyway")
	}
}

// One recording at a time; stopping with none is fine; a failed start leaves none behind.
func TestRecordingStartStopRules(t *testing.T) {
	useFakeEncoder(t)
	page, d := recordingPage(t)
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Errorf("stop without a recording: %v", err)
	}
	dir := t.TempDir()
	if err := d.StartRecording(ctxT(t), filepath.Join(dir, "a.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := d.StartRecording(ctxT(t), filepath.Join(dir, "b.mp4")); err == nil || !strings.Contains(err.Error(), "already recording") {
		t.Errorf("second start: %v", err)
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Fatal(err)
	}

	page.on("Page.startScreencast", func(cdpMsg) any { return errReply{cdpError{-32000, "no screencast"}} })
	if err := d.StartRecording(ctxT(t), filepath.Join(dir, "c.mp4")); err == nil {
		t.Fatal("a failed screencast start reported success")
	}
	d.recMu.Lock()
	left := d.rec
	d.recMu.Unlock()
	if left != nil {
		t.Error("a failed start left a recording behind")
	}
}

// Closing the driver mid-recording discards it: no encoding, no frames left on disk, and
// frames arriving afterwards are ignored.
func TestCloseDiscardsARecording(t *testing.T) {
	enc := useFakeEncoder(t)
	page, d := recordingPage(t)
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "r.mp4")); err != nil {
		t.Fatal(err)
	}
	screencastFrame(page, 1, "one")
	eventually(t, "the frame's ack", func() bool { return len(page.calls("Page.screencastFrameAck")) == 1 })
	d.recMu.Lock()
	dir := d.rec.dir
	d.recMu.Unlock()
	d.Close()
	if len(enc.all()) != 0 {
		t.Error("a discarded recording was encoded")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("frames left in %s: %v", dir, err)
	}
}

// Frames from another session (a frame's target) are not the page's screencast.
func TestOnlyThePagesFramesAreRecorded(t *testing.T) {
	enc := useFakeEncoder(t)
	page, d := recordingPage(t)
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "r.mp4")); err != nil {
		t.Fatal(err)
	}
	page.event("Page.screencastFrame", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("x")), "sessionId": 3}, "frame-session")
	screencastFrame(page, 4, "page")
	eventually(t, "the page frame's ack", func() bool { return len(page.calls("Page.screencastFrameAck")) >= 1 })
	time.Sleep(100 * time.Millisecond) // "page" shows for a few ticks
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if frames, _ := enc.all()[0].shown(); strings.Join(frames, ",") != "first,page" {
		t.Errorf("frames = %s", strings.Join(frames, ","))
	}
}
