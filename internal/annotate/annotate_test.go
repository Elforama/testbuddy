package annotate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

func TestSRT(t *testing.T) {
	got := SRT([]Caption{
		{Start: 0, End: 1500 * time.Millisecond, Text: "tapOn: \"Save\""},
		{Start: 2 * time.Second, End: 2 * time.Second, Text: "empty: skipped"},
		{Start: 61 * time.Second, End: 62*time.Second + 5*time.Millisecond, Text: "inputText:\n{x}"},
	})
	want := "1\n00:00:00,000 --> 00:00:01,500\ntapOn: \"Save\"\n\n2\n00:01:01,000 --> 00:01:02,005\ninputText: {x}\n\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

// The graph steadies the frame rate, draws the rings on the recording, then adds the band
// below it and shows each caption's image there while its step runs.
func TestGraph(t *testing.T) {
	caps := []Caption{{Start: 0, End: time.Second, Text: "a"}, {Start: time.Second, End: 2 * time.Second, Text: "b"}}
	g := Graph(1080, 2400, 182, caps, []Touch{
		{At: time.Second, Kind: "tap", Pts: [][2]float64{{0.5, 0.25}}},
		{At: 2 * time.Second, Kind: "swipe", Pts: [][2]float64{{0.5, 0.8}, {0.5, 0.2}}},
	})
	for _, want := range []string{
		"[0:v]fps=30,format=yuv420p[b]",
		"[1:v]split=2[r0][r1]",
		"[b][r0]overlay=x='0.5000*W-w/2':y='0.2500*H-h/2':enable='between(t,1.000,1.600)'",
		"y='H*(0.8000+(-0.6000)*min(max((t-2.000)/0.500,0),1))-h/2'",
		"pad=1080:2582:0:0:color=0x1c1c1e",
		"[2:v]overlay=x=0:y=2400:enable='between(t,0.000,1.000)'",
		"[3:v]overlay=x=0:y=2400:enable='between(t,1.000,2.000)'[v]",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("graph lacks %q:\n%s", want, g)
		}
	}
	// the rings go on before the band: their H is the recording's height, not the band's too
	if strings.Index(g, "pad=") < strings.Index(g, "[r1]overlay") {
		t.Errorf("the band comes before the rings:\n%s", g)
	}
	if g := Graph(400, 800, 0, nil, nil); g != "[0:v]fps=30,format=yuv420p[v]" {
		t.Errorf("empty graph %q", g)
	}
	// rings only: no band
	if g := Graph(400, 800, 0, nil, []Touch{{At: 0, Kind: "tap", Pts: [][2]float64{{0.5, 0.5}}}}); strings.Contains(g, "pad=") || !strings.HasSuffix(g, "[v]") {
		t.Errorf("rings-only graph %q", g)
	}
}

// A caption wraps to two lines at most; a longer one ends in "…" on the second.
func TestCaptionWrap(t *testing.T) {
	face := fontFace(40)
	if face == nil {
		t.Fatal("no font")
	}
	defer face.Close()
	max := fixed.I(600)
	if got := wrap(face, "Tap on id: login", max); len(got) != 1 || got[0] != "Tap on id: login" {
		t.Errorf("short: %q", got)
	}
	long := "Assert that \"Enter a username and password\" is visible on the login screen after the first tap on the button"
	got := wrap(face, long, max)
	if len(got) != 2 || !strings.HasSuffix(got[1], "…") {
		t.Errorf("long: %q", got)
	}
	for _, l := range got {
		if font.MeasureString(face, l) > max {
			t.Errorf("line %q wider than %d", l, max.Round())
		}
	}
	if got := wrap(face, strings.Repeat("x", 200), max); len(got) != 1 || !strings.HasSuffix(got[0], "…") {
		t.Errorf("one long word: %q", got)
	}
}

// The caption image is the band: its size, the band's color at the edge, white text inside.
func TestCaptionImage(t *testing.T) {
	img := captionImage("Tap on id: login", 1080, bandHeight(1080, 2400), captionSize(1080, 2400))
	b := img.Bounds()
	if b.Dx() != 1080 || b.Dy() != bandHeight(1080, 2400) || b.Dy()%2 != 0 {
		t.Fatalf("size %v", b)
	}
	if r, g, bl, _ := img.At(2, 2).RGBA(); r>>8 != 0x1c || g>>8 != 0x1c || bl>>8 != 0x1e {
		t.Errorf("corner %v, want the band's color", img.At(2, 2))
	}
	white := 0
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if r, _, _, _ := img.At(x, y).RGBA(); r>>8 > 0xf0 {
				white++
			}
		}
	}
	if white < 500 {
		t.Errorf("%d white pixels: no text drawn", white)
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
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if after.ModTime().Equal(before.ModTime()) && after.Size() == before.Size() {
		t.Error("the video was not rewritten")
	}
	w, h, err := videoSize(context.Background(), dir, ffmpeg, path)
	if err != nil || w != 360 || h != 640+bandHeight(360, 640) {
		t.Errorf("size %dx%d %v, want 360x%d (the band below)", w, h, err, 640+bandHeight(360, 640))
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

// A video recorded only when the screen changed (Android: a few frames a second) comes out at
// a steady 30 frames a second, so a caption or ring changes when it should, not at the next
// change on screen.
func TestBurnSteadiesASparseVideo(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.mp4")
	// 3 s at 25 fps, keeping one frame a second, with their times: 3 frames in all
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x640:rate=25:duration=3",
		"-vf", "select='not(mod(n,25))'", "-fps_mode", "vfr", "-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a sparse video: %v %s", err, out)
	}
	if err := Burn(context.Background(), path, []Caption{{0, time.Second, "a"}, {time.Second, 3 * time.Second, "b"}}, nil); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffmpeg, "-hide_banner", "-i", path, "-map", "0:v", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	m := regexp.MustCompile(`frame=\s*(\d+)`).FindAllStringSubmatch(string(out), -1)
	if len(m) == 0 {
		t.Fatalf("no frame count in %s", out)
	}
	if n, _ := strconv.Atoi(m[len(m)-1][1]); n < 60 {
		t.Errorf("%d frames, want about 30 a second", n)
	}
}
