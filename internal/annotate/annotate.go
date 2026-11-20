// Package annotate draws on a finished screen recording what a person watching it needs to
// follow the run (`tb test --record --slow`): a caption at the bottom naming the step that is
// running, and a ring where each tap, double tap and long press landed (a ring that moves
// along a swipe).
//
// The device records the screen as it is; tb's own taps and swipes are not drawn there (iOS
// and Chrome have no setting for that, and Android's "show taps" draws a dot too small and
// brief to see in a video). So tb draws them afterwards with ffmpeg, from what the runner
// noted while it ran, the same on every platform. Burning them in, rather than a subtitle
// track beside the video, is what keeps them in a video attached to a pull request.
package annotate

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Caption is a step's text, shown from Start to End (times from the video's start).
type Caption struct {
	Start, End time.Duration
	Text       string
}

// Touch is a gesture: when it began (from the video's start), its kind (tap, doubleTap,
// longPress, swipe) and its points as fractions of the screen's width and height.
type Touch struct {
	At   time.Duration
	Kind string
	Pts  [][2]float64
}

// FFmpegPath finds ffmpeg: TB_FFMPEG, else the PATH. A variable so tests need no ffmpeg.
var FFmpegPath = func() (string, error) {
	if p := os.Getenv("TB_FFMPEG"); p != "" {
		return p, nil
	}
	return exec.LookPath("ffmpeg")
}

// runFFmpeg runs ffmpeg in dir and returns what it printed. A variable so tests can check
// the arguments without ffmpeg.
var runFFmpeg = func(ctx context.Context, dir, ffmpeg string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

const (
	tapShown   = 600 * time.Millisecond  // how long a tap's ring stays
	pressShown = 1200 * time.Millisecond // a long press's
	swipeMove  = 500 * time.Millisecond  // how long a swipe's ring takes to travel
	swipeShown = 800 * time.Millisecond  // and stays in all
)

// Burn draws caps and touches onto the video at path, in place. The captions are also
// written beside it (the video's name with .srt). On an error the video is left as it was.
func Burn(ctx context.Context, path string, caps []Caption, touches []Touch) error {
	if len(caps) == 0 && len(touches) == 0 {
		return nil
	}
	ffmpeg, err := FFmpegPath()
	if err != nil || ffmpeg == "" {
		return errors.New("captions and touches need ffmpeg on PATH (or TB_FFMPEG)")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(abs), ".annotate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	w, h, err := videoSize(ctx, tmp, ffmpeg, abs)
	if err != nil {
		return err
	}
	if len(caps) > 0 {
		srt := SRT(caps)
		if err := os.WriteFile(filepath.Join(tmp, "captions.srt"), []byte(srt), 0o644); err != nil {
			return err
		}
		os.WriteFile(strings.TrimSuffix(abs, filepath.Ext(abs))+".srt", []byte(srt), 0o644)
	}
	ring := ringSize(w, h)
	if len(touches) > 0 {
		if err := writeRing(filepath.Join(tmp, "ring.png"), ring); err != nil {
			return err
		}
	}
	out := filepath.Join(tmp, "out.mp4")
	args := func(codec ...string) []string {
		a := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", abs}
		if len(touches) > 0 {
			a = append(a, "-loop", "1", "-i", "ring.png")
		}
		a = append(a, "-filter_complex", Graph(len(caps) > 0, touches, h), "-map", "[v]", "-map", "0:a?")
		a = append(a, codec...)
		return append(a, "-pix_fmt", "yuv420p", "-movflags", "+faststart", out)
	}
	if o, err := runFFmpeg(ctx, tmp, ffmpeg, args("-c:v", "libx264", "-preset", "veryfast", "-crf", "23")); err != nil {
		// No libx264 in this ffmpeg build: MPEG-4, which most players still play.
		if o2, err2 := runFFmpeg(ctx, tmp, ffmpeg, args("-c:v", "mpeg4", "-q:v", "4")); err2 != nil {
			return fmt.Errorf("ffmpeg could not draw on the video: %v: %s / %v: %s",
				err, strings.TrimSpace(string(o)), err2, strings.TrimSpace(string(o2)))
		}
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return errors.New("ffmpeg wrote no video")
	}
	return os.Rename(out, abs)
}

var sizeRE = regexp.MustCompile(`Video: .*?, (\d{2,5})x(\d{2,5})`)

// videoSize reads the video's width and height from ffmpeg's description of it (ffprobe
// may not be there when TB_FFMPEG names ffmpeg alone).
func videoSize(ctx context.Context, dir, ffmpeg, path string) (int, int, error) {
	out, _ := runFFmpeg(ctx, dir, ffmpeg, []string{"-hide_banner", "-i", path}) // exits 1: no output named
	m := sizeRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, fmt.Errorf("ffmpeg did not give the video's size: %s", strings.TrimSpace(string(out)))
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	return w, h, nil
}

// SRT writes caps as SubRip subtitles.
func SRT(caps []Caption) string {
	var b strings.Builder
	n := 0
	for _, c := range caps {
		if c.End <= c.Start || strings.TrimSpace(c.Text) == "" {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, srtTime(c.Start), srtTime(c.End), oneLine(c.Text))
	}
	return b.String()
}

func srtTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	ms := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// oneLine keeps a caption to one paragraph: a blank line would end the subtitle, and libass
// reads { and \ as style codes.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.NewReplacer(`\`, `\\`, "{", `\{`, "}", `\}`).Replace(s)
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return s
}

// Graph is the ffmpeg filter graph: the captions (captions.srt, in the working directory)
// and a ring (input 1) over each touch. Its output is [v].
func Graph(captions bool, touches []Touch, height int) string {
	var g []string
	cur := "0:v"
	if captions {
		// libass sizes text against a 288-line script: 11 is about 4% of the height.
		// BorderStyle 3 puts the text on an opaque box, readable on any screen.
		size := 11
		if height > 0 && height < 600 {
			size = 14
		}
		g = append(g, fmt.Sprintf("[%s]subtitles=captions.srt:force_style='FontName=Sans,FontSize=%d,BorderStyle=3,Outline=6,Shadow=0,"+
			"PrimaryColour=&H00FFFFFF,OutlineColour=&H50000000,BackColour=&H50000000,Alignment=2,MarginV=14,MarginL=10,MarginR=10'[c]", cur, size))
		cur = "c"
	}
	if len(touches) > 0 {
		var outs []string
		for i := range touches {
			outs = append(outs, fmt.Sprintf("[r%d]", i))
		}
		g = append(g, "[1:v]split="+strconv.Itoa(len(touches))+strings.Join(outs, ""))
		for i, t := range touches {
			next := fmt.Sprintf("t%d", i)
			g = append(g, fmt.Sprintf("[%s][r%d]overlay=%s:shortest=0:eof_action=pass[%s]", cur, i, overlayAt(t), next))
			cur = next
		}
	}
	if cur == "0:v" {
		g = append(g, "[0:v]null[v]")
	} else {
		g[len(g)-1] = strings.TrimSuffix(g[len(g)-1], "["+cur+"]") + "[v]"
	}
	return strings.Join(g, ";")
}

// overlayAt places a touch's ring and says when it shows: on the point for a tap, moving
// from the first point to the last for a swipe.
func overlayAt(t Touch) string {
	a := secs(t.At)
	p0, p1 := t.Pts[0], t.Pts[len(t.Pts)-1]
	shown := tapShown
	switch t.Kind {
	case "longPress":
		shown = pressShown
	case "swipe":
		shown = swipeShown
	}
	at := func(v0, v1 float64, size string) string {
		if t.Kind != "swipe" || v0 == v1 {
			return fmt.Sprintf("'%s*%s-%s/2'", num(v0), size, strings.ToLower(size))
		}
		k := fmt.Sprintf("min(max((t-%s)/%s,0),1)", a, secs(swipeMove))
		return fmt.Sprintf("'%s*(%s+(%s)*%s)-%s/2'", size, num(v0), num(v1-v0), k, strings.ToLower(size))
	}
	return fmt.Sprintf("x=%s:y=%s:enable='between(t,%s,%s)'", at(p0[0], p1[0], "W"), at(p0[1], p1[1], "H"), a, secs(t.At+shown))
}

func secs(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }
func num(f float64) string        { return strconv.FormatFloat(f, 'f', 4, 64) }

// ringSize is the ring's diameter for a w×h video: about a fingertip on a phone.
func ringSize(w, h int) int {
	d := min(w, h) / 9
	return max(d, 24)
}

// writeRing draws the touch marker: a translucent disc with a white rim and a dark edge, so
// it shows on light and dark screens alike.
func writeRing(path string, d int) error {
	img := image.NewNRGBA(image.Rect(0, 0, d, d))
	c := float64(d) / 2
	rim := math.Max(2, float64(d)/14)
	for y := 0; y < d; y++ {
		for x := 0; x < d; x++ {
			r := math.Hypot(float64(x)+0.5-c, float64(y)+0.5-c)
			switch {
			case r > c:
			case r > c-1.5:
				img.Set(x, y, color.NRGBA{0, 0, 0, 140})
			case r > c-1.5-rim:
				img.Set(x, y, color.NRGBA{255, 255, 255, 235})
			default:
				img.Set(x, y, color.NRGBA{255, 64, 64, 110})
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
