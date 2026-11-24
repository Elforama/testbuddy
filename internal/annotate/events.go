package annotate

// The event log: events sent to tb while the flow ran (`tb mark`, the event endpoint; package
// mark), shown in a band of their own below the step's caption, one row per event with its
// time, the newest highlighted, like a console.
//
// Events come in bursts: an app's tracking sends several in the same few milliseconds. Each
// must still be readable, so the log keeps an event up for eventShown, and the band has a row
// for each event that is up at once, as many as the run's busiest moment needs, up to
// eventRowsMax. tb draws after the run, so it knows that moment before it sizes the band.
//
// A burst of more events than that pushes the oldest out. One pushed out before it was up
// eventRead would go by unread, so the video holds there instead: the frame stays still, with
// "paused for events" in the band, until the oldest has been up that long. The video then runs
// longer than the run did; the rows keep the events' real times.

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Event is something sent to tb while the flow ran, at At from the video's start.
type Event struct {
	At   time.Duration
	Text string
}

const (
	eventShown   = 2 * time.Second // how long an event stays in the log when nothing pushes it out
	eventRead    = time.Second     // the least an event stays up before a newer one pushes it out
	eventRowsMax = 6               // the most rows the band has
)

// hold keeps the video's frame at At (in the recording's time) still for Dur.
type hold struct {
	At, Dur time.Duration
}

// eventRow is one event in the log, up from shown to gone (in the drawn video's time, holds
// included); at is its real time, which its row shows.
type eventRow struct {
	at          time.Duration
	text        string
	shown, gone time.Duration
}

// logState is what the band shows from Start to End (the drawn video's time): rows, oldest
// first, and whether the video is held there.
type logState struct {
	Start, End time.Duration
	Rows       []eventRow
	Paused     bool
}

// eventLog lays the events out: the band's rows, the holds bursts need, what the band shows
// when, and when each event shows in the drawn video (shown, in the order of events once
// sorted).
type eventLog struct {
	rows   int
	holds  []hold
	states []logState
	shown  []time.Duration
}

// oneFrame is a frame of the drawn video.
const oneFrame = time.Second / outFPS

// wholeFrames is d rounded up to whole frames: the time of the first frame at or after d.
func wholeFrames(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	k := (int64(d)*outFPS + int64(time.Second) - 1) / int64(time.Second)
	return time.Duration(k * int64(time.Second) / outFPS)
}

// frames is how many frames d is, to the nearest.
func frames(d time.Duration) int {
	return int((int64(d)*outFPS + int64(time.Second)/2) / int64(time.Second))
}

// layoutEvents lays out events (see the top of the file), sorting them by At, for a video
// length long (0: unknown). ffmpeg holds a frame after the first and before the last only, so a
// hold is at oneFrame at the earliest, and a burst in the last frame is not held: its oldest
// rows leave sooner.
func layoutEvents(events []Event, length time.Duration) eventLog {
	if len(events) == 0 {
		return eventLog{}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At < events[j].At })
	n := min(busiest(events), eventRowsMax)
	var (
		log   eventLog
		up    []*eventRow // the rows up now, oldest first
		all   []*eventRow
		extra time.Duration // the holds so far
	)
	log.rows = n
	for _, e := range events {
		// ffmpeg holds no frame before the second, so the layout times nothing before it either
		// (the row still shows e.At)
		base := max(e.At, oneFrame)
		t := base + extra
		// the rows whose time is over leave
		keep := up[:0]
		for _, r := range up {
			if r.gone > t {
				keep = append(keep, r)
			}
		}
		up = keep
		if len(up) == n {
			oldest := up[0]
			// ffmpeg holds a frame for whole frames: the hold starts on the next frame and lasts
			// a whole number of them (holds under a frame apart, or of a frame and a bit, would
			// drift from the picture)
			at := wholeFrames(base)
			canHold := length <= 0 || at < length-oneFrame
			if need := oldest.shown + eventRead; need > t && canHold {
				if dur := wholeFrames(need - (at + extra)); dur > 0 {
					if k := len(log.holds); k > 0 && log.holds[k-1].At == at {
						log.holds[k-1].Dur += dur
					} else {
						log.holds = append(log.holds, hold{At: at, Dur: dur})
					}
					extra += dur
					t = at + extra
				} else {
					t = need // within a frame of the next one: no hold
				}
			}
			if k := len(log.shown); k > 0 {
				t = max(t, log.shown[k-1]) // see below
			}
			oldest.gone = t
			up = up[1:]
		}
		// never before the one before it, which may have waited for a hold this one did not need
		if k := len(log.shown); k > 0 {
			t = max(t, log.shown[k-1])
		}
		r := &eventRow{at: e.At, text: e.Text, shown: t, gone: t + eventShown}
		up = append(up, r)
		all = append(all, r)
		log.shown = append(log.shown, t)
	}
	log.states = logStates(all, log.holds)
	return log
}

// busiest is the most events up at once, each for eventShown from its time.
func busiest(events []Event) int {
	most, from := 0, 0
	for i, e := range events {
		for events[from].At+eventShown <= e.At {
			from++
		}
		most = max(most, i-from+1)
	}
	return most
}

// logStates cuts the drawn video into stretches over which the band shows the same thing.
func logStates(rows []*eventRow, holds []hold) []logState {
	var paused [][2]time.Duration // the holds, in the drawn video's time
	var extra time.Duration
	for _, h := range holds {
		at := h.At + extra
		paused = append(paused, [2]time.Duration{at, at + h.Dur})
		extra += h.Dur
	}
	var cuts []time.Duration
	for _, r := range rows {
		cuts = append(cuts, r.shown, r.gone)
	}
	for _, p := range paused {
		cuts = append(cuts, p[0], p[1])
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	var states []logState
	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		if b <= a {
			continue
		}
		s := logState{Start: a, End: b}
		for _, r := range rows {
			if r.shown <= a && a < r.gone {
				s.Rows = append(s.Rows, *r)
			}
		}
		for _, p := range paused {
			if p[0] <= a && a < p[1] {
				s.Paused = true
			}
		}
		if len(s.Rows) == 0 && !s.Paused {
			continue
		}
		if k := len(states); k > 0 && states[k-1].End == a && sameState(states[k-1], s) {
			states[k-1].End = b
			continue
		}
		states = append(states, s)
	}
	return states
}

func sameState(a, b logState) bool {
	if a.Paused != b.Paused || len(a.Rows) != len(b.Rows) {
		return false
	}
	for i := range a.Rows {
		if a.Rows[i] != b.Rows[i] {
			return false
		}
	}
	return true
}

// later is t in the drawn video: moved by the holds before it.
func later(t time.Duration, holds []hold) time.Duration {
	out := t
	for _, h := range holds {
		if h.At < t {
			out += h.Dur
		}
	}
	return out
}

// eventSize is the event text's size in pixels for a w×h video: a little under the caption's.
func eventSize(w, h int) int {
	return max(captionSize(w, h)*3/4, 12)
}

func eventRowHeight(px int) int { return px * 14 / 10 }

// eventBandHeight is the band's height for rows rows of px-pixel text, even.
func eventBandHeight(rows, px int) int {
	if rows == 0 {
		return 0
	}
	b := rows*eventRowHeight(px) + px
	return b + b%2
}

// eventBandColor is the event band's background (ffmpeg's 0xRRGGBB): a shade off the caption
// band's, so the two read as two bands.
const eventBandColor = "0e0e10"

var (
	eventBandRGB = color.RGBA{0x0e, 0x0e, 0x10, 0xff}
	newestRGB    = color.RGBA{0x2c, 0x2c, 0x30, 0xff} // the newest row's background
	timeRGB      = color.RGBA{0x8e, 0x8e, 0x93, 0xff}
	olderRGB     = color.RGBA{0xc7, 0xc7, 0xcc, 0xff}
	pausedRGB    = color.RGBA{0xff, 0xb3, 0x40, 0xff}
)

var monoFace = func() func(px int) font.Face {
	f, err := opentype.Parse(gomono.TTF)
	return func(px int) font.Face {
		if err != nil {
			return nil
		}
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil
		}
		return face
	}
}()

const pausedLabel = "paused for events"

// eventTime is how a row shows an event's time: seconds from the video's start.
func eventTime(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 2, 64) + "s"
}

// logImage is the band for state s: w×band, rows rows of px-pixel text, oldest at the top, the
// newest on a lighter background.
func logImage(s logState, w, band, rows, px int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, band))
	draw.Draw(img, img.Bounds(), &image.Uniform{eventBandRGB}, image.Point{}, draw.Src)
	face := monoFace(px)
	if face == nil {
		return img
	}
	defer face.Close()
	rowH := eventRowHeight(px)
	top := (band - rows*rowH) / 2
	margin := px
	// the time column fits the longest time shown
	timeW := fixed.I(0)
	for _, r := range s.Rows {
		timeW = max(timeW, font.MeasureString(face, eventTime(r.at)))
	}
	gap := fixed.I(px)
	pausedW := fixed.I(0)
	if s.Paused {
		pausedW = font.MeasureString(face, pausedLabel) + gap
	}
	for i, r := range s.Rows {
		y := top + i*rowH
		newest := i == len(s.Rows)-1
		if newest {
			draw.Draw(img, image.Rect(0, y, w, y+rowH), &image.Uniform{newestRGB}, image.Point{}, draw.Src)
		}
		base := fixed.I(y + (rowH+px*7/10)/2)
		ts := eventTime(r.at)
		d := &font.Drawer{Dst: img, Src: &image.Uniform{timeRGB}, Face: face}
		d.Dot = fixed.Point26_6{X: fixed.I(margin) + timeW - d.MeasureString(ts), Y: base}
		d.DrawString(ts)
		textCol := image.Image(&image.Uniform{olderRGB})
		if newest {
			textCol = image.White
		}
		x := fixed.I(margin) + timeW + gap
		room := fixed.I(w-margin) - x
		if i == 0 {
			room -= pausedW
		}
		d = &font.Drawer{Dst: img, Src: textCol, Face: face, Dot: fixed.Point26_6{X: x, Y: base}}
		if room > 0 {
			d.DrawString(fitText(face, oneLine(r.text), room))
		}
	}
	if s.Paused {
		d := &font.Drawer{Dst: img, Src: &image.Uniform{pausedRGB}, Face: face}
		d.Dot = fixed.Point26_6{X: fixed.I(w-margin) - d.MeasureString(pausedLabel), Y: fixed.I(top + (rowH+px*7/10)/2)}
		d.DrawString(pausedLabel)
	}
	return img
}

// writeLog writes the event log as a slideshow ffmpeg reads as one input: an image for each
// state and a blank band between them, and events.ffconcat, the concat demuxer's list saying
// how long each shows, from the video's start. Each file's frame rate is set to 1000, so the
// demuxer times them to the millisecond (by default, to a 25th of a second), and each duration
// is the difference of rounded times from the start, so the roundings do not add up.
func writeLog(ctx context.Context, dir string, log eventLog, w, band, px int) error {
	blank := image.NewRGBA(image.Rect(0, 0, w, band))
	draw.Draw(blank, blank.Bounds(), &image.Uniform{eventBandRGB}, image.Point{}, draw.Src)
	if err := writePNG(filepath.Join(dir, "events-blank.png"), blank); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	ms := func(d time.Duration) int64 { return (int64(d) + int64(time.Millisecond)/2) / int64(time.Millisecond) }
	var shownTo int64 // in milliseconds, as written
	show := func(name string, until time.Duration) {
		end := ms(until)
		fmt.Fprintf(&b, "file '%s'\noption framerate 1000\nduration %.3f\n", name, float64(end-shownTo)/1000)
		shownTo = end
	}
	var at time.Duration
	for i, s := range log.states {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.Start > at {
			show("events-blank.png", s.Start)
		}
		name := fmt.Sprintf("events-%04d.png", i)
		if err := writePNG(filepath.Join(dir, name), logImage(s, w, band, log.rows, px)); err != nil {
			return err
		}
		show(name, s.End)
		at = s.End
	}
	// the demuxer keeps the last file's duration only when it is listed again
	show("events-blank.png", at+oneFrame)
	b.WriteString("file 'events-blank.png'\noption framerate 1000\n")
	return os.WriteFile(filepath.Join(dir, "events.ffconcat"), []byte(b.String()), 0o644)
}
