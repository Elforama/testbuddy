package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/annotate"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// --annotate holds the screen after each step that acts on the app, and only those: a check, a
// script or a runFlow is not held (its steps are), and without --annotate nothing is.
func TestAnnotateHoldsAfterStepsThatAct(t *testing.T) {
	yaml := "- tapOn: Go\n- assertVisible: Go\n- evalScript: ${1}\n- runFlow:\n    commands:\n      - tapOn: Go\n"
	for _, slow := range []bool{false, true} {
		f := &fakeDriver{settled: true}
		f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
		fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("appId: app\n---\n"+yaml))
		if err != nil {
			t.Fatal(err)
		}
		r := New(f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second, Annotate: slow})
		var holds []time.Duration
		r.annotateWait = func(_ context.Context, d time.Duration) { holds = append(holds, d) }
		if res := r.RunFlow(context.Background(), fl); res.Status != Passed {
			t.Fatal(res.Err)
		}
		want := 0
		if slow {
			want = 2 // the two taps
		}
		if len(holds) != want {
			t.Errorf("slow=%v: %d holds %v, want %d", slow, len(holds), holds, want)
		}
		for _, d := range holds {
			if d <= 0 || d > annotateStep {
				t.Errorf("hold %s outside (0, %s]", d, annotateStep)
			}
		}
	}
}

// The hold tops a quick step up to half a second; a step that took that long or longer is
// not lengthened.
func TestAnnotateHoldLength(t *testing.T) {
	for _, c := range []struct{ took, want time.Duration }{
		{0, 500 * time.Millisecond},
		{120 * time.Millisecond, 380 * time.Millisecond},
		{500 * time.Millisecond, 0},
		{3 * time.Second, 0},
	} {
		if got := annotateHold(c.took); got != c.want {
			t.Errorf("annotateHold(%s) = %s, want %s", c.took, got, c.want)
		}
	}
}

// A failed step is not held: the flow ends there.
func TestAnnotateDoesNotHoldAFailedStep(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("appId: app\n---\n- tapOn: Missing\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, Options{LookupTimeout: 200 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond, Annotate: true})
	held := false
	r.annotateWait = func(context.Context, time.Duration) { held = true }
	if res := r.RunFlow(context.Background(), fl); res.Status != Failed {
		t.Fatalf("status %s", res.Status)
	}
	if held {
		t.Error("a failed step was held")
	}
}

// --annotate draws on the recording a caption for each step and a ring where each touch went:
// the burn gets the flow's steps (a runFlow's own steps, not the runFlow) and its tap, at
// its place on the screen, in times within the video.
func TestAnnotateCaptionsAndTouchesTheRecording(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	dir := t.TempDir()
	fl, err := flow.ParseBytes(filepath.Join(dir, "f.yaml"), []byte("appId: app\n---\n- tapOn: Go\n- runFlow:\n    commands:\n      - assertVisible: Go\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second, ArtifactsDir: dir, Record: true, Annotate: true})
	r.annotateWait = func(context.Context, time.Duration) {}
	var burned []string
	var caps []annotate.Caption
	var touches []annotate.Touch
	r.annotateBurn = func(_ context.Context, path string, c []annotate.Caption, ts []annotate.Touch) error {
		burned, caps, touches = append(burned, path), c, ts
		return nil
	}
	res := r.RunFlow(context.Background(), fl)
	if res.Status != Passed {
		t.Fatal(res.Err)
	}
	if len(burned) != 1 || burned[0] != filepath.Join(dir, "recording.mp4") {
		t.Fatalf("burned %v", burned)
	}
	var texts []string
	for _, c := range caps {
		texts = append(texts, c.Text)
		if c.Start < 0 || c.End <= c.Start || c.End > res.Videos[0].End.Sub(res.Videos[0].Start) {
			t.Errorf("caption %+v outside the video", c)
		}
	}
	if strings.Join(texts, "|") != `Tap on "Go"|Assert that "Go" is visible` {
		t.Errorf("captions %q", texts)
	}
	if len(touches) != 1 || touches[0].Kind != "tap" || touches[0].Pts[0] != [2]float64{0.5, 125.0 / 800} {
		t.Errorf("touches %+v", touches)
	}
}

// Without --annotate the recording is left as the device made it.
func TestRecordWithoutAnnotateDrawsNothing(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	dir := t.TempDir()
	fl, _ := flow.ParseBytes(filepath.Join(dir, "f.yaml"), []byte("appId: app\n---\n- tapOn: Go\n"))
	r := New(f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second, ArtifactsDir: dir, Record: true})
	r.annotateBurn = func(context.Context, string, []annotate.Caption, []annotate.Touch) error {
		t.Error("burned without --annotate")
		return nil
	}
	if res := r.RunFlow(context.Background(), fl); res.Status != Passed {
		t.Fatal(res.Err)
	}
}

// Captions close short gaps between steps, so the text does not flicker off, and the last
// one stays half a second after its step.
func TestVideoNotesCaptions(t *testing.T) {
	t0 := time.Now()
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	caps, _ := videoNotes([]stepNote{
		{at(100), at(600), "a"},
		{at(700), at(900), "b"},   // 100 ms after a: a runs up to it
		{at(5000), at(5100), "c"}, // long after b: b ends at its own end
	}, nil, t0, at(10000))
	want := []annotate.Caption{
		{Start: 100 * time.Millisecond, End: 700 * time.Millisecond, Text: "a"},
		{Start: 700 * time.Millisecond, End: 900 * time.Millisecond, Text: "b"},
		{Start: 5000 * time.Millisecond, End: 5600 * time.Millisecond, Text: "c"},
	}
	if len(caps) != len(want) {
		t.Fatalf("caps %+v", caps)
	}
	for i := range want {
		if caps[i] != want[i] {
			t.Errorf("cap %d = %+v, want %+v", i, caps[i], want[i])
		}
	}
}

// The video began later than tb's clock says: the touches line up with the video's changes
// only 800 ms later, so the captions and touches move by that much before they are drawn.
// A video in sync is left alone.
func TestAnnotateSyncsToTheVideo(t *testing.T) {
	t0 := time.Now()
	v := Video{Path: "recording.mp4", Start: t0, End: t0.Add(10 * time.Second)}
	caps := []annotate.Caption{{Start: 900 * time.Millisecond, End: 1500 * time.Millisecond, Text: "Tap on \"Go\""}}
	touches := []annotate.Touch{
		{At: 1 * time.Second, Kind: "tap"}, {At: 2 * time.Second, Kind: "tap"}, {At: 3 * time.Second, Kind: "tap"},
	}
	for _, c := range []struct {
		name    string
		changes []time.Duration
		shift   time.Duration
	}{
		{"late", []time.Duration{1850 * time.Millisecond, 2900 * time.Millisecond, 3820 * time.Millisecond, 7 * time.Second}, 820 * time.Millisecond},
		{"in sync", []time.Duration{1080 * time.Millisecond, 2100 * time.Millisecond, 3060 * time.Millisecond}, 0},
		{"no changes", nil, 0},
	} {
		r := New(&fakeDriver{settled: true}, Options{Annotate: true})
		r.annotateChanges = func(context.Context, string) ([]time.Duration, error) { return c.changes, nil }
		gotCaps, gotTouches := r.syncToVideo(context.Background(), v, caps, touches)
		if gotCaps[0].Start != caps[0].Start+c.shift || gotCaps[0].End != caps[0].End+c.shift {
			t.Errorf("%s: caption %+v, want shifted by %s", c.name, gotCaps[0], c.shift)
		}
		for i := range touches {
			if gotTouches[i].At != touches[i].At+c.shift {
				t.Errorf("%s: touch %d at %s, want %s", c.name, i, gotTouches[i].At, touches[i].At+c.shift)
			}
		}
	}
}

// The driver's word on when the video began wins over when StartRecording returned.
func TestVideoStartFromTheDriver(t *testing.T) {
	asked := time.Now()
	early := asked.Add(-300 * time.Millisecond)
	r := New(&clockDriver{fakeDriver: &fakeDriver{settled: true}, t0: early}, Options{})
	if got := r.videoStart(asked, asked.Add(time.Minute)); !got.Equal(early) {
		t.Errorf("start %v, want the driver's %v", got, early)
	}
	r = New(&clockDriver{fakeDriver: &fakeDriver{settled: true}}, Options{})
	if got := r.videoStart(asked, asked.Add(time.Minute)); !got.Equal(asked) {
		t.Errorf("start %v, want asked %v when the driver does not know", got, asked)
	}
}

type clockDriver struct {
	*fakeDriver
	t0 time.Time
}

func (d *clockDriver) RecordingStarted() time.Time { return d.t0 }
