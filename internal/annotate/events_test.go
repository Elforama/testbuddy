package annotate

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func dms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func upAt(l eventLog, t time.Duration) []string {
	for _, s := range l.states {
		if s.Start <= t && t < s.End {
			var out []string
			for _, r := range s.Rows {
				out = append(out, r.text)
			}
			return out
		}
	}
	return nil
}

func TestEventLogOneRowForSparseEvents(t *testing.T) {
	l := layoutEvents([]Event{{dms(1000), "a"}, {dms(5000), "b"}}, 0)
	if l.rows != 1 || len(l.holds) != 0 {
		t.Fatalf("rows %d holds %v", l.rows, l.holds)
	}
	if got := upAt(l, dms(1500)); len(got) != 1 || got[0] != "a" {
		t.Errorf("at 1.5 s %v", got)
	}
	if got := upAt(l, dms(3500)); got != nil {
		t.Errorf("at 3.5 s %v, want nothing (a is up for 2 s)", got)
	}
}

func TestEventLogBurstGetsARowEach(t *testing.T) {
	l := layoutEvents([]Event{{dms(1000), "a"}, {dms(1010), "b"}, {dms(1020), "c"}, {dms(1030), "d"}}, 0)
	if l.rows != 4 || len(l.holds) != 0 {
		t.Fatalf("rows %d holds %v", l.rows, l.holds)
	}
	if got := strings.Join(upAt(l, dms(1500)), ","); got != "a,b,c,d" {
		t.Errorf("at 1.5 s %q", got)
	}
}

func TestEventLogHoldsForABurstOverTheRows(t *testing.T) {
	var evs []Event
	for i := 0; i < 10; i++ {
		evs = append(evs, Event{dms(2000), fmt.Sprintf("e%d", i)})
	}
	l := layoutEvents(evs, 0)
	if l.rows != eventRowsMax {
		t.Fatalf("rows %d, want %d", l.rows, eventRowsMax)
	}
	if len(l.holds) != 1 || l.holds[0].At != dms(2000) || l.holds[0].Dur != eventRead {
		t.Fatalf("holds %+v, want one of %s at 2 s", l.holds, eventRead)
	}
	seen := map[string]time.Duration{}
	for _, s := range l.states {
		if len(s.Rows) > l.rows {
			t.Errorf("state %+v has more rows than the band", s)
		}
		for _, r := range s.Rows {
			seen[r.text] += s.End - s.Start
			if r.at != dms(2000) {
				t.Errorf("%s shows %s, want its real time 2 s", r.text, r.at)
			}
		}
	}
	for _, e := range evs {
		if seen[e.Text] < eventRead {
			t.Errorf("%s up %s, want at least %s", e.Text, seen[e.Text], eventRead)
		}
	}
	paused := false
	for _, s := range l.states {
		if s.Paused {
			paused = true
			if s.Start != dms(2000) || s.End != dms(3000) {
				t.Errorf("paused %s to %s, want 2 s to 3 s", s.Start, s.End)
			}
		}
	}
	if !paused {
		t.Error("no state says the video is paused")
	}
}

func TestEventLogStatesDoNotOverlap(t *testing.T) {
	var evs []Event
	for i := 0; i < 40; i++ {
		evs = append(evs, Event{dms(100 * (i % 7) * i), fmt.Sprintf("e%d", i)})
	}
	l := layoutEvents(evs, 0)
	for i := 1; i < len(l.states); i++ {
		if l.states[i].Start < l.states[i-1].End {
			t.Fatalf("state %d starts %s before %d ends %s", i, l.states[i].Start, i-1, l.states[i-1].End)
		}
	}
	for _, s := range l.states {
		if s.End <= s.Start {
			t.Errorf("empty state %+v", s)
		}
	}
}

func TestHeldNotesMoveByTheHolds(t *testing.T) {
	holds := []hold{{At: dms(2000), Dur: dms(1000)}}
	caps, touches := heldNotes([]Caption{{dms(1000), dms(3000), "spans"}, {dms(3000), dms(4000), "after"}},
		[]Touch{{At: dms(500), Kind: "tap"}, {At: dms(2500), Kind: "tap"}}, holds)
	if caps[0].Start != dms(1000) || caps[0].End != dms(4000) || caps[1].Start != dms(4000) || caps[1].End != dms(5000) {
		t.Errorf("caps %+v", caps)
	}
	if touches[0].At != dms(500) || touches[1].At != dms(3500) {
		t.Errorf("touches %+v", touches)
	}
}

func TestEventLogHoldsWhereFFmpegCan(t *testing.T) {
	burst := func(at time.Duration) []Event {
		var evs []Event
		for i := 0; i < 8; i++ {
			evs = append(evs, Event{at, fmt.Sprintf("e%d", i)})
		}
		return evs
	}
	l := layoutEvents(burst(dms(10)), 5*time.Second)
	if len(l.holds) != 1 || l.holds[0].At != oneFrame {
		t.Fatalf("holds %+v, want one at the first frame", l.holds)
	}
	for _, s := range l.states {
		if s.Paused && s.Start != oneFrame {
			t.Errorf("paused from %s, want %s, where the frame is held", s.Start, oneFrame)
		}
	}
	if l.shown[7] != oneFrame+eventRead {
		t.Errorf("the last event shows at %s, want after the hold", l.shown[7])
	}
	l = layoutEvents(burst(dms(4990)), 5*time.Second)
	if len(l.holds) != 0 {
		t.Errorf("holds %+v in the last frame", l.holds)
	}
	for _, s := range l.states {
		if s.Paused {
			t.Errorf("a paused state %+v with no hold", s)
		}
	}
}

func TestGraphWithEventsAndHolds(t *testing.T) {
	l := layoutEvents([]Event{{dms(1000), "a"}, {dms(1000), "b"}}, 0)
	g := graph(frame{w: 400, h: 800, band: 60, eventBand: 50, caps: []Caption{{0, dms(500), "x"}, {dms(500), dms(900), "y"}},
		touches: []Touch{{At: 0, Kind: "tap", Pts: [][2]float64{{0.5, 0.5}}}}, states: l.states,
		holds: []hold{{At: dms(1000), Dur: dms(700)}}})
	for _, want := range []string{
		"[0:v]setpts=PTS-STARTPTS,fps=30,format=yuv420p[b]",
		"[b]split=2[p0][p1]",
		"[p0]trim=end_frame=30,setpts=PTS-STARTPTS,tpad=stop_mode=clone:stop=21[q0]",
		"[p1]trim=start_frame=30,setpts=PTS-STARTPTS[q1]",
		"[q0][q1]concat=n=2:v=1:a=0",
		"pad=400:860:0:0:color=0x" + bandColor,
		"[2:v]overlay=x=0:y=800",
		"[3:v]overlay=x=0:y=800",
		"pad=400:910:0:0:color=0x" + eventBandColor,
		"[4:v]setpts=PTS-STARTPTS,format=rgba[ev]",
		"[ev]overlay=x=0:y=860:eof_action=pass",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("graph lacks %q:\n%s", want, g)
		}
	}
	if strings.Count(g, "[ev]overlay") != 1 || !strings.HasSuffix(g, "[v]") {
		t.Errorf("graph: %s", g)
	}
	g = graph(frame{w: 400, h: 800, eventBand: 50, states: l.states})
	if !strings.Contains(g, "pad=400:850:0:0") || !strings.Contains(g, "[1:v]setpts") || !strings.Contains(g, "overlay=x=0:y=800") {
		t.Errorf("graph without captions:\n%s", g)
	}
}

func TestWriteLog(t *testing.T) {
	var evs []Event
	for i := 0; i < 300; i++ {
		evs = append(evs, Event{dms(500 + 3000*i), fmt.Sprintf("e%d", i)})
	}
	l := layoutEvents(evs, 0)
	dir := t.TempDir()
	if err := writeLog(context.Background(), dir, l, 200, eventBandHeight(l.rows, 12), 12); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "events.ffconcat"))
	if err != nil {
		t.Fatal(err)
	}
	var at time.Duration
	starts := map[string]time.Duration{}
	name := ""
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		switch {
		case strings.HasPrefix(line, "file "):
			name = strings.Trim(strings.TrimPrefix(line, "file "), "'")
			if _, ok := starts[name]; !ok {
				starts[name] = at
			}
		case strings.HasPrefix(line, "duration "):
			var d float64
			fmt.Sscanf(line, "duration %f", &d)
			at += time.Duration(math.Round(d*1000)) * time.Millisecond
		case line != "ffconcat version 1.0" && line != "option framerate 1000":
			t.Errorf("unexpected line %q", line)
		}
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i, s := range l.states {
		name := fmt.Sprintf("events-%04d.png", i)
		if got := starts[name]; (got - s.Start).Abs() > time.Millisecond {
			t.Errorf("%s starts at %s, want %s", name, got, s.Start)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
	if lines[len(lines)-2] != "file 'events-blank.png'" || lines[len(lines)-1] != "option framerate 1000" {
		t.Errorf("the list does not end by repeating its last file: %q", lines[len(lines)-1])
	}
}

func TestLogImage(t *testing.T) {
	px := eventSize(1080, 2400)
	band := eventBandHeight(3, px)
	s := logState{Rows: []eventRow{{at: dms(1230), text: "screen_view"}, {at: dms(1240), text: strings.Repeat("purchase ", 80)}}, Paused: true}
	img := logImage(s, 1080, band, 3, px)
	if b := img.Bounds(); b.Dx() != 1080 || b.Dy() != band {
		t.Errorf("image %v, want 1080x%d", b, band)
	}
	if band%2 != 0 {
		t.Errorf("band %d is odd", band)
	}
}

func TestEventsJSON(t *testing.T) {
	var rows []struct {
		At, Shown float64
		Text      string
	}
	b := eventsJSON([]Event{{dms(500), "a"}, {dms(2000), "b"}}, []time.Duration{dms(500), dms(3500)})
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].At != 0.5 || rows[0].Shown != 0.5 || rows[1].At != 2 || rows[1].Shown != 3.5 {
		t.Errorf("%s", b)
	}
}

func TestBurnDrawsEvents(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=360x640:rate=15:duration=4",
		"-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	var evs []Event
	for i := 0; i < 8; i++ {
		evs = append(evs, Event{dms(1000), fmt.Sprintf("event_%d {id: %d}", i, i)})
	}
	if err := Burn(context.Background(), path, Drawing{Caps: []Caption{{0, dms(3000), "tapOn: Buy"}}, Events: evs}); err != nil {
		t.Fatal(err)
	}
	w, h, length, err := videoInfo(context.Background(), dir, ffmpeg, path)
	want := 640 + bandHeight(360, 640) + eventBandHeight(eventRowsMax, eventSize(360, 640))
	if err != nil || w != 360 || h != want {
		t.Errorf("size %dx%d %v, want 360x%d", w, h, err, want)
	}
	if length < 4*time.Second+eventRead-200*time.Millisecond {
		t.Errorf("length %s, want about %s (the hold)", length, 4*time.Second+eventRead)
	}
	if _, err := os.Stat(filepath.Join(dir, "recording.events.json")); err != nil {
		t.Error(err)
	}
}

func frameCount(t *testing.T, ffmpeg, path string) int {
	t.Helper()
	out, err := exec.Command(ffmpeg, "-hide_banner", "-i", path, "-map", "0:v", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var n int
	for _, line := range strings.Split(string(out), "\r") {
		if i := strings.LastIndex(line, "frame="); i >= 0 {
			fmt.Sscanf(strings.TrimSpace(line[i+6:]), "%d", &n)
		}
	}
	return n
}

func TestBurnHoldsAsManyFramesAsTheLayout(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x480:rate=30:duration=6",
		"-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	var evs []Event
	for i := 0; i < 20; i++ {
		evs = append(evs, Event{dms(1000 + i), fmt.Sprintf("a%d", i)})
	}
	for i := 0; i < 15; i++ {
		evs = append(evs, Event{dms(3500 + 7*i), fmt.Sprintf("b%d", i)})
	}
	l := layoutEvents(append([]Event(nil), evs...), 6*time.Second)
	if len(l.holds) < 3 {
		t.Fatalf("holds %+v, want several", l.holds)
	}
	var held time.Duration
	for _, h := range l.holds {
		held += h.Dur
	}
	if err := Burn(context.Background(), path, Drawing{Events: evs}); err != nil {
		t.Fatal(err)
	}
	want := 180 + int(math.Round(held.Seconds()*30))
	if got := frameCount(t, ffmpeg, path); got < want-1 || got > want+1 {
		t.Errorf("%d frames, want %d: 6 s and the layout's %s of holds", got, want, held)
	}
	out, _ := exec.Command(ffmpeg, "-hide_banner", "-i", path, "-vf", "crop=320:480:0:0,freezedetect=n=0.001:d=0.2", "-f", "null", "-").CombinedOutput()
	var still [][2]time.Duration
	re := regexp.MustCompile(`freeze_(start|end): ([0-9.]+)`)
	for _, m := range re.FindAllStringSubmatch(string(out), -1) {
		f, _ := strconv.ParseFloat(m[2], 64)
		d := time.Duration(f * float64(time.Second))
		if m[1] == "start" {
			if k := len(still); k > 0 && (d-still[k-1][1]).Abs() < 2*oneFrame {
				continue
			}
			still = append(still, [2]time.Duration{d, d})
		} else if k := len(still); k > 0 {
			still[k-1][1] = d
		}
	}
	var extra time.Duration
	used := make([]bool, len(still))
	for _, h := range l.holds {
		from, to := h.At+extra, h.At+extra+h.Dur
		extra += h.Dur
		found := false
		for i, st := range still {
			if from >= st[0]-oneFrame && to <= st[1]+oneFrame {
				found, used[i] = true, true
			}
		}
		if !found {
			t.Errorf("hold %s to %s is not where the picture stands still %v", from, to, still)
		}
	}
	for i, u := range used {
		if !u {
			t.Errorf("the picture stands still %v with no hold there", still[i])
		}
	}
}

func TestBurnManyHoldsDoNotDrift(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "recording.mp4")
	if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x240:rate=30:duration=12",
		"-pix_fmt", "yuv420p", path).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make a test video: %v %s", err, out)
	}
	var evs []Event
	for i := 0; i < 400; i++ {
		evs = append(evs, Event{dms(300 + 27*i), fmt.Sprintf("e%d", i)})
	}
	l := layoutEvents(append([]Event(nil), evs...), 12*time.Second)
	if len(l.holds) < 10 {
		t.Fatalf("%d holds, want many", len(l.holds))
	}
	var held time.Duration
	for _, h := range l.holds {
		held += h.Dur
	}
	if err := Burn(context.Background(), path, Drawing{Events: evs}); err != nil {
		t.Fatal(err)
	}
	want := 360 + frames(held)
	if got := frameCount(t, ffmpeg, path); got < want-1 || got > want+1 {
		t.Errorf("%d frames, want %d: 12 s and the layout's %d holds, %s", got, want, len(l.holds), held)
	}
}

func TestLogSlideshowTimesInFFmpeg(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	l := layoutEvents([]Event{{dms(517), "a"}, {dms(1234), "b"}, {dms(1251), "c"}, {dms(4007), "d"}}, 0)
	dir := t.TempDir()
	if err := writeLog(context.Background(), dir, l, 64, eventBandHeight(l.rows, 12), 12); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ffprobe, "-hide_banner", "-loglevel", "error", "-f", "concat", "-safe", "0", "-i", filepath.Join(dir, "events.ffconcat"),
		"-show_entries", "frame=pts_time", "-of", "csv=p=0").CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	var pts []time.Duration
	for _, f := range strings.Fields(string(out)) {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(f, ","), 64)
		pts = append(pts, time.Duration(math.Round(v*1000))*time.Millisecond)
	}
	for _, s := range l.states {
		found := false
		for _, p := range pts {
			if (p - s.Start).Abs() <= time.Millisecond {
				found = true
			}
		}
		if !found {
			t.Errorf("no image starts at %s in ffmpeg's timing %v", s.Start, pts)
		}
	}
	if last := pts[len(pts)-1]; last > l.states[len(l.states)-1].End+time.Second {
		t.Errorf("the slideshow runs to %s, past its last state %s", last, l.states[len(l.states)-1].End)
	}
}

func TestEventLogKeepsTheOrder(t *testing.T) {
	var evs []Event
	for _, at := range []int{2152, 2160, 2165, 2170, 2175, 2180, 2185, 2190} {
		evs = append(evs, Event{dms(at), fmt.Sprint(at)})
	}
	l := layoutEvents(evs, 5*time.Second)
	for i := 1; i < len(l.shown); i++ {
		if l.shown[i] < l.shown[i-1] {
			t.Errorf("event %d shows at %s, before event %d at %s", i, l.shown[i], i-1, l.shown[i-1])
		}
	}
	full := false
	for _, s := range l.states {
		if s.Start > l.shown[len(l.shown)-1] {
			break
		}
		if full && len(s.Rows) < l.rows {
			t.Errorf("only %d rows at %s, in the middle of the burst", len(s.Rows), s.Start)
		}
		full = full || len(s.Rows) == l.rows
	}
}

func statesByScan(rows []*eventRow, holds []hold) []logState {
	var paused [][2]time.Duration
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

func TestLogStatesSweep(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	for round := 0; round < 50; round++ {
		var evs []Event
		at := time.Duration(0)
		for i := 0; i < 300; i++ {
			at += time.Duration(rnd.Intn(400)) * time.Millisecond
			evs = append(evs, Event{at, fmt.Sprint(i)})
		}
		l := layoutEvents(evs, 0)
		var rows []*eventRow
		seen := map[eventRow]bool{}
		for _, s := range l.states {
			for _, r := range s.Rows {
				if !seen[r] {
					seen[r] = true
					rr := r
					rows = append(rows, &rr)
				}
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].shown < rows[j].shown })
		if got, want := logStates(rows, l.holds), statesByScan(rows, l.holds); !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: the sweep's states differ from the scan's", round)
		}
	}
	var evs []Event
	for i := 0; i < 60_000; i++ {
		evs = append(evs, Event{time.Duration(i) * 20 * time.Millisecond, "e"})
	}
	start := time.Now()
	layoutEvents(evs, 0)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("60,000 events laid out in %s", d)
	}
}

func TestTouchRingLastsThroughAHold(t *testing.T) {
	_, touches := heldNotes(nil, []Touch{{At: dms(2150), Kind: "tap", Pts: [][2]float64{{0.5, 0.5}}}},
		[]hold{{At: dms(2200), Dur: time.Second}})
	if !strings.Contains(overlayAt(touches[0]), "enable='between(t,2.150,3.750)'") {
		t.Errorf("ring shown %s, want 2.15 s to 3.75 s (0.6 s and the 1 s hold)", overlayAt(touches[0]))
	}
	_, touches = heldNotes(nil, []Touch{{At: dms(500), Kind: "tap", Pts: [][2]float64{{0.5, 0.5}}}},
		[]hold{{At: dms(2200), Dur: time.Second}})
	if !strings.Contains(overlayAt(touches[0]), "enable='between(t,0.500,1.100)'") {
		t.Errorf("ring shown %s, want 0.5 s to 1.1 s", overlayAt(touches[0]))
	}
}

func TestIssue413EarlyEventShowsAtTheFirstFrame(t *testing.T) {
	l := layoutEvents([]Event{{-dms(350), "launch"}, {dms(1000), "tap"}}, 5*time.Second)
	if got := upAt(l, oneFrame); len(got) != 1 || got[0] != "launch" {
		t.Errorf("issue #413: at the first frame %v, want the event sent before the video", got)
	}
	if got := eventTime(-dms(350)); got != "-0.35s" {
		t.Errorf("issue #413: its time reads %q", got)
	}
}

func TestIssue413EarlyBurstGetsRowsNotALongHold(t *testing.T) {
	var evs []Event
	for i := 10; i >= 1; i-- {
		evs = append(evs, Event{-time.Duration(i) * time.Second, fmt.Sprintf("e%d", i)})
	}
	l := layoutEvents(evs, 5*time.Second)
	var held time.Duration
	for _, h := range l.holds {
		held += h.Dur
	}
	if l.rows != eventRowsMax || held > 5*eventRead {
		t.Errorf("issue #413 review: 10 events from before the video get %d rows and hold its first frame %s", l.rows, held)
	}
}
