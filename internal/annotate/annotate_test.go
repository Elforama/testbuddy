package annotate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSRT(t *testing.T) {
	got := SRT([]Caption{
		{Start: 0, End: 1500 * time.Millisecond, Text: "tapOn: \"Save\""},
		{Start: 2 * time.Second, End: 2 * time.Second, Text: "empty: skipped"},
		{Start: 61 * time.Second, End: 62*time.Second + 5*time.Millisecond, Text: "inputText:\n{x}"},
	})
	want := "1\n00:00:00,000 --> 00:00:01,500\ntapOn: \"Save\"\n\n2\n00:01:01,000 --> 00:01:02,005\ninputText: \\{x\\}\n\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestGraph(t *testing.T) {
	g := Graph(true, []Touch{
		{At: time.Second, Kind: "tap", Pts: [][2]float64{{0.5, 0.25}}},
		{At: 2 * time.Second, Kind: "swipe", Pts: [][2]float64{{0.5, 0.8}, {0.5, 0.2}}},
	}, 1200)
	for _, want := range []string{
		"[0:v]subtitles=captions.srt:",
		"[1:v]split=2[r0][r1]",
		"[c][r0]overlay=x='0.5000*W-w/2':y='0.2500*H-h/2':enable='between(t,1.000,1.600)'",
		"y='H*(0.8000+(-0.6000)*min(max((t-2.000)/0.500,0),1))-h/2'",
		"[v]",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("graph lacks %q:\n%s", want, g)
		}
	}
	if strings.Contains(g, "[t1]") {
		t.Errorf("the last overlay should end in [v]:\n%s", g)
	}
	if g := Graph(false, nil, 0); g != "[0:v]null[v]" {
		t.Errorf("empty graph %q", g)
	}
}

// With a real ffmpeg: the video keeps its size and length, and the captions are kept beside it.
func TestBurnWithFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=360x640:rate=15:duration=2",
		"-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	before, _ := os.Stat(path)
	err = Burn(context.Background(), path,
		[]Caption{{0, time.Second, "tapOn: \"Save\""}, {time.Second, 2 * time.Second, "assertVisible: \"Saved\""}},
		[]Touch{{300 * time.Millisecond, "tap", [][2]float64{{0.5, 0.5}}}, {time.Second, "swipe", [][2]float64{{0.5, 0.8}, {0.5, 0.2}}}})
	if err != nil {
		if strings.Contains(err.Error(), "subtitles") || strings.Contains(err.Error(), "No such filter") {
			t.Skipf("this ffmpeg cannot draw subtitles: %v", err)
		}
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if after.ModTime().Equal(before.ModTime()) && after.Size() == before.Size() {
		t.Error("the video was not rewritten")
	}
	w, h, err := videoSize(context.Background(), dir, ffmpeg, path)
	if err != nil || w != 360 || h != 640 {
		t.Errorf("size %dx%d %v", w, h, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "recording.srt")); err != nil || !strings.Contains(string(b), "assertVisible") {
		t.Errorf("recording.srt: %q %v", b, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".annotate-*")); len(left) > 0 {
		t.Errorf("left behind %v", left)
	}
}

// Without ffmpeg Burn says so and leaves the video alone.
func TestBurnWithoutFFmpeg(t *testing.T) {
	old := FFmpegPath
	defer func() { FFmpegPath = old }()
	FFmpegPath = func() (string, error) { return "", exec.ErrNotFound }
	path := filepath.Join(t.TempDir(), "recording.mp4")
	os.WriteFile(path, []byte("video"), 0o644)
	if err := Burn(context.Background(), path, []Caption{{0, time.Second, "x"}}, nil); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("err %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "video" {
		t.Error("video changed")
	}
}
