package annotate

import (
	"context"
	"encoding/json"
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

type Caption struct {
	Start, End time.Duration
	Text       string
}

type Touch struct {
	At    time.Duration
	Kind  string
	Pts   [][2]float64
	Shown time.Duration
}

var FFmpegPath = func() (string, error) {
	if p := os.Getenv("TB_FFMPEG"); p != "" {
		return p, nil
	}
	return exec.LookPath("ffmpeg")
}

var runFFmpeg = func(ctx context.Context, dir, ffmpeg string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

const (
	tapShown   = 600 * time.Millisecond
	pressShown = 1200 * time.Millisecond
	swipeMove  = 500 * time.Millisecond
	swipeShown = 800 * time.Millisecond
)

type Drawing struct {
	Caps    []Caption
	Touches []Touch
	Events  []Event
}

func (d Drawing) empty() bool { return len(d.Caps) == 0 && len(d.Touches) == 0 && len(d.Events) == 0 }

func Burn(ctx context.Context, path string, d Drawing) error {
	if d.empty() {
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

	w, h, length, err := videoInfo(ctx, tmp, ffmpeg, abs)
	if err != nil {
		return err
	}
	events := append([]Event(nil), d.Events...)
	log := layoutEvents(events, length)
	caps, touches := heldNotes(d.Caps, d.Touches, log.holds)
	inputs := []string{"-i", abs}
	if len(touches) > 0 {
		if err := writeRing(filepath.Join(tmp, "ring.png"), ringSize(w, h)); err != nil {
			return err
		}
		inputs = append(inputs, "-loop", "1", "-i", "ring.png")
	}
	band := 0
	if len(caps) > 0 {
		band = bandHeight(w, h)
		for i, c := range caps {
			name := fmt.Sprintf("caption-%03d.png", i)
			if err := writePNG(filepath.Join(tmp, name), captionImage(c.Text, w, band, captionSize(w, h))); err != nil {
				return err
			}
			inputs = append(inputs, "-i", name)
		}
	}
	eband := 0
	if log.rows > 0 && len(log.states) > 0 {
		px := eventSize(w, h)
		eband = eventBandHeight(log.rows, px)
		if err := writeLog(ctx, tmp, log, w, eband, px); err != nil {
			return err
		}
		inputs = append(inputs, "-f", "concat", "-safe", "0", "-i", "events.ffconcat")
	}
	g := graph(frame{w: w, h: h, band: band, eventBand: eband, caps: caps, touches: touches, states: log.states, holds: log.holds, tail: log.tail})
	out := filepath.Join(tmp, "out.mp4")
	args := func(codec ...string) []string {
		a := append([]string{"-hide_banner", "-loglevel", "error", "-y"}, inputs...)
		a = append(a, "-filter_complex", g, "-map", "[v]")
		if len(log.holds) == 0 {
			a = append(a, "-map", "0:a?")
		}
		a = append(a, codec...)
		return append(a, "-pix_fmt", "yuv420p", "-movflags", "+faststart", out)
	}
	if o, err := runFFmpeg(ctx, tmp, ffmpeg, args("-c:v", "libx264", "-preset", "veryfast", "-crf", "23")); err != nil {
		if o2, err2 := runFFmpeg(ctx, tmp, ffmpeg, args("-c:v", "mpeg4", "-q:v", "4")); err2 != nil {
			return fmt.Errorf("ffmpeg could not draw on the video: %v: %s / %v: %s",
				err, strings.TrimSpace(string(o)), err2, strings.TrimSpace(string(o2)))
		}
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		return errors.New("ffmpeg wrote no video")
	}
	if err := os.Rename(out, abs); err != nil {
		return err
	}
	base := strings.TrimSuffix(abs, filepath.Ext(abs))
	if len(caps) > 0 {
		os.WriteFile(base+".srt", []byte(SRT(caps)), 0o644)
	}
	if len(events) > 0 {
		os.WriteFile(base+".events.json", eventsJSON(events, log.shown), 0o644)
	}
	return nil
}

func heldNotes(caps []Caption, touches []Touch, holds []hold) ([]Caption, []Touch) {
	if len(holds) == 0 {
		return caps, touches
	}
	var c2 []Caption
	for _, c := range caps {
		c.Start, c.End = later(c.Start, holds), later(c.End, holds)
		c2 = append(c2, c)
	}
	var t2 []Touch
	for _, t := range touches {
		end := later(t.At+touchShown(t), holds)
		t.At = later(t.At, holds)
		t.Shown = end - t.At
		t2 = append(t2, t)
	}
	return c2, t2
}

func eventsJSON(events []Event, shown []time.Duration) []byte {
	type row struct {
		At    float64 `json:"at"`
		Shown float64 `json:"shown"`
		Text  string  `json:"text"`
	}
	rows := []row{}
	for i, e := range events {
		s := e.At
		if i < len(shown) {
			s = shown[i]
		}
		rows = append(rows, row{At: round3(e.At), Shown: round3(s), Text: e.Text})
	}
	b, _ := json.MarshalIndent(rows, "", "  ")
	return append(b, '\n')
}

func round3(d time.Duration) float64 { return math.Round(d.Seconds()*1000) / 1000 }

var sizeRE = regexp.MustCompile(`Video: .*?, (\d{2,5})x(\d{2,5})`)

var durationRE = regexp.MustCompile(`Duration: (\d+):(\d\d):(\d\d(?:\.\d+)?)`)

func videoInfo(ctx context.Context, dir, ffmpeg, path string) (int, int, time.Duration, error) {
	out, _ := runFFmpeg(ctx, dir, ffmpeg, []string{"-hide_banner", "-i", path})
	m := sizeRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, 0, fmt.Errorf("ffmpeg did not give the video's size: %s", strings.TrimSpace(string(out)))
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	var length time.Duration
	if d := durationRE.FindStringSubmatch(string(out)); d != nil {
		hh, _ := strconv.Atoi(d[1])
		mm, _ := strconv.Atoi(d[2])
		ss, _ := strconv.ParseFloat(d[3], 64)
		length = time.Duration((float64(hh*3600+mm*60) + ss) * float64(time.Second))
	}
	return w, h, length, nil
}

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

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return s
}

const outFPS = 30

func Graph(w, h, band int, caps []Caption, touches []Touch) string {
	return graph(frame{w: w, h: h, band: band, caps: caps, touches: touches})
}

type frame struct {
	w, h, band, eventBand int
	caps                  []Caption
	touches               []Touch
	states                []logState
	holds                 []hold
	tail                  time.Duration
}

func graph(f frame) string {
	base := ""
	if len(f.holds) > 0 {
		base = "setpts=PTS-STARTPTS,"
	}
	g := []string{fmt.Sprintf("[0:v]%sfps=%d,format=yuv420p[b]", base, outFPS)}
	cur := "b"
	next := 0
	step := func(filter string) {
		out := fmt.Sprintf("s%d", next)
		next++
		g = append(g, fmt.Sprintf("%s[%s]", filter, out))
		cur = out
	}
	if len(f.holds) > 0 {
		var parts, segs []string
		for i := 0; i <= len(f.holds); i++ {
			parts = append(parts, fmt.Sprintf("[p%d]", i))
			segs = append(segs, fmt.Sprintf("[q%d]", i))
		}
		g = append(g, fmt.Sprintf("[%s]split=%d%s", cur, len(parts), strings.Join(parts, "")))
		from := 0
		for i, hd := range f.holds {
			at := frames(hd.At)
			cut := fmt.Sprintf("trim=end_frame=%d", at)
			if i > 0 {
				cut = fmt.Sprintf("trim=start_frame=%d:end_frame=%d", from, at)
			}
			g = append(g, fmt.Sprintf("%s%s,setpts=PTS-STARTPTS,tpad=stop_mode=clone:stop=%d%s", parts[i], cut, frames(hd.Dur), segs[i]))
			from = at
		}
		g = append(g, fmt.Sprintf("%strim=start_frame=%d,setpts=PTS-STARTPTS%s", parts[len(f.holds)], from, segs[len(f.holds)]))
		step(fmt.Sprintf("%sconcat=n=%d:v=1:a=0", strings.Join(segs, ""), len(segs)))
	}
	if n := frames(f.tail); n > 0 {
		step(fmt.Sprintf("[%s]tpad=stop_mode=clone:stop=%d", cur, n))
	}
	input := 1
	if len(f.touches) > 0 {
		var outs []string
		for i := range f.touches {
			outs = append(outs, fmt.Sprintf("[r%d]", i))
		}
		g = append(g, "[1:v]split="+strconv.Itoa(len(f.touches))+strings.Join(outs, ""))
		for i, t := range f.touches {
			step(fmt.Sprintf("[%s][r%d]overlay=%s:shortest=0:eof_action=pass", cur, i, overlayAt(t)))
		}
		input = 2
	}
	band := 0
	if f.band > 0 && len(f.caps) > 0 {
		band = f.band
		step(fmt.Sprintf("[%s]pad=%d:%d:0:0:color=0x%s", cur, f.w, f.h+band, bandColor))
		for i, c := range f.caps {
			step(fmt.Sprintf("[%s][%d:v]overlay=x=0:y=%d:enable='between(t,%s,%s)'", cur, input+i, f.h, secs(c.Start), secs(c.End)))
		}
		input += len(f.caps)
	}
	if f.eventBand > 0 && len(f.states) > 0 {
		step(fmt.Sprintf("[%s]pad=%d:%d:0:0:color=0x%s", cur, f.w, f.h+band+f.eventBand, eventBandColor))
		g = append(g, fmt.Sprintf("[%d:v]setpts=PTS-STARTPTS,format=rgba[ev]", input))
		step(fmt.Sprintf("[%s][ev]overlay=x=0:y=%d:eof_action=pass", cur, f.h+band))
	}
	g[len(g)-1] = strings.TrimSuffix(g[len(g)-1], "["+cur+"]") + "[v]"
	return strings.Join(g, ";")
}

func overlayAt(t Touch) string {
	a := secs(t.At)
	p0, p1 := t.Pts[0], t.Pts[len(t.Pts)-1]
	shown := touchShown(t)
	at := func(v0, v1 float64, size string) string {
		if t.Kind != "swipe" || v0 == v1 {
			return fmt.Sprintf("'%s*%s-%s/2'", num(v0), size, strings.ToLower(size))
		}
		k := fmt.Sprintf("min(max((t-%s)/%s,0),1)", a, secs(swipeMove))
		return fmt.Sprintf("'%s*(%s+(%s)*%s)-%s/2'", size, num(v0), num(v1-v0), k, strings.ToLower(size))
	}
	return fmt.Sprintf("x=%s:y=%s:enable='between(t,%s,%s)'", at(p0[0], p1[0], "W"), at(p0[1], p1[1], "H"), a, secs(t.At+shown))
}

func touchShown(t Touch) time.Duration {
	if t.Shown > 0 {
		return t.Shown
	}
	switch t.Kind {
	case "longPress":
		return pressShown
	case "swipe":
		return swipeShown
	}
	return tapShown
}

func secs(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }
func num(f float64) string        { return strconv.FormatFloat(f, 'f', 4, 64) }

func ringSize(w, h int) int {
	d := min(w, h) / 9
	return max(d, 24)
}

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
	return writePNG(path, img)
}

func writePNG(path string, img image.Image) error {
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
