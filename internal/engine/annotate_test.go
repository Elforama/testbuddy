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
			want = 2
		}
		if len(holds) != want {
			t.Errorf("slow=%v: %d holds %v, want %d", slow, len(holds), holds, want)
		}
		for _, d := range holds {
			if d <= 0 || d > defaultAnnotateStep {
				t.Errorf("hold %s outside (0, %s]", d, defaultAnnotateStep)
			}
		}
	}
}

func TestAnnotateHoldLength(t *testing.T) {
	for _, c := range []struct {
		took, sinceTouch time.Duration
		touched          bool
		want             time.Duration
	}{
		{120 * time.Millisecond, 20 * time.Millisecond, true, 980 * time.Millisecond},
		{2200 * time.Millisecond, 40 * time.Millisecond, true, 960 * time.Millisecond},
		{3 * time.Second, 1500 * time.Millisecond, true, 0},
		{0, 0, false, time.Second},
		{120 * time.Millisecond, 0, false, 880 * time.Millisecond},
		{time.Second, 0, false, 0},
		{3 * time.Second, 0, false, 0},
	} {
		if got := annotateHold(defaultAnnotateStep, c.took, c.sinceTouch, c.touched); got != c.want {
			t.Errorf("annotateHold(took %s, since touch %s, touched %v) = %s, want %s", c.took, c.sinceTouch, c.touched, got, c.want)
		}
	}
}

func TestAnnotateHoldsAfterALateTap(t *testing.T) {
	f := &fakeDriver{settled: true}
	shown := time.Now().Add(1200 * time.Millisecond)
	f.screen = func() []*ui.Node {
		if time.Now().Before(shown) {
			return nil
		}
		return []*ui.Node{btn("Go", "go", 100)}
	}
	fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("appId: app\n---\n- tapOn: Go\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: time.Second, Annotate: true})
	var holds []time.Duration
	r.annotateWait = func(_ context.Context, d time.Duration) { holds = append(holds, d) }
	res := r.RunFlow(context.Background(), fl)
	if res.Status != Passed {
		t.Fatal(res.Err)
	}
	if res.Steps[0].Duration < 1200*time.Millisecond {
		t.Fatalf("the step took %s: the target did not show late", res.Steps[0].Duration)
	}
	if len(holds) != 1 || holds[0] < 700*time.Millisecond || holds[0] > time.Second {
		t.Errorf("holds %v, want one of about a second after the tap", holds)
	}
}

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

func TestVideoNotesCaptions(t *testing.T) {
	t0 := time.Now()
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	caps, _ := videoNotes([]stepNote{
		{at(100), at(600), "a"},
		{at(700), at(900), "b"},
		{at(5000), at(5100), "c"},
	}, nil, t0, at(10000), time.Second)
	want := []annotate.Caption{
		{Start: 100 * time.Millisecond, End: 700 * time.Millisecond, Text: "a"},
		{Start: 700 * time.Millisecond, End: 900 * time.Millisecond, Text: "b"},
		{Start: 5000 * time.Millisecond, End: 6100 * time.Millisecond, Text: "c"},
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

func TestAnnotateStepIsSettable(t *testing.T) {
	for _, c := range []struct {
		set, want time.Duration
	}{
		{0, time.Second},
		{500 * time.Millisecond, 500 * time.Millisecond},
		{3 * time.Second, 3 * time.Second},
		{-1, 0},
	} {
		f := &fakeDriver{settled: true}
		f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
		fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("appId: app\n---\n- tapOn: Go\n"))
		if err != nil {
			t.Fatal(err)
		}
		r := New(f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second, Annotate: true, AnnotateStep: c.set})
		var holds []time.Duration
		r.annotateWait = func(_ context.Context, d time.Duration) { holds = append(holds, d) }
		if res := r.RunFlow(context.Background(), fl); res.Status != Passed {
			t.Fatal(res.Err)
		}
		if c.want == 0 {
			if len(holds) != 0 {
				t.Errorf("step %s: holds %v, want none", c.set, holds)
			}
			continue
		}
		if len(holds) != 1 || holds[0] > c.want || holds[0] < c.want-300*time.Millisecond {
			t.Errorf("step %s: holds %v, want one just under %s", c.set, holds, c.want)
		}
	}
}

func TestAnnotateTrustsAnExactVideoClock(t *testing.T) {
	t0 := time.Now()
	v := Video{Path: "recording.mp4", Start: t0, End: t0.Add(10 * time.Second)}
	touches := []annotate.Touch{{At: time.Second, Kind: "tap"}, {At: 2 * time.Second, Kind: "tap"}, {At: 3 * time.Second, Kind: "tap"}}
	r := New(&exactClockDriver{fakeDriver: &fakeDriver{settled: true}}, Options{Annotate: true})
	r.annotateChanges = func(context.Context, string) ([]time.Duration, error) {
		return []time.Duration{1850 * time.Millisecond, 2900 * time.Millisecond, 3820 * time.Millisecond}, nil
	}
	_, got := r.syncToVideo(context.Background(), v, nil, touches)
	for i := range touches {
		if got[i].At != touches[i].At {
			t.Errorf("touch %d moved to %s on an exact clock", i, got[i].At)
		}
	}
}

type exactClockDriver struct{ *fakeDriver }

func (d *exactClockDriver) RecordingClockExact() bool { return true }

func TestAnnotateTimesFromTheStartMark(t *testing.T) {
	t0 := time.Now()
	v := Video{Path: "recording.mp4", Start: t0, End: t0.Add(60 * time.Second)}
	caps := []annotate.Caption{{Start: 1817 * time.Millisecond, End: 20 * time.Second, Text: "Launch app"}}
	touches := []annotate.Touch{{At: 21 * time.Second, Kind: "tap"}, {At: 23 * time.Second, Kind: "tap"}, {At: 25 * time.Second, Kind: "tap"}}
	d := &markDriver{fakeDriver: &fakeDriver{settled: true}, shown: t0, held: 450 * time.Millisecond}
	r := New(d, Options{Annotate: true})
	r.annotateMarks = func(context.Context, string) ([]time.Duration, error) {
		return []time.Duration{2433 * time.Millisecond, 3600 * time.Millisecond, 6167 * time.Millisecond, 17933 * time.Millisecond}, nil
	}
	r.annotateChanges = func(context.Context, string) ([]time.Duration, error) { return nil, nil }
	gotCaps, got := r.syncToVideo(context.Background(), v, caps, touches)
	for i := range touches {
		if want := touches[i].At + 2433*time.Millisecond; i >= len(got) || got[i].At != want {
			t.Fatalf("touches %v, want each 2.433 s later", got)
		}
	}
	if len(gotCaps) != 1 || gotCaps[0].Start != 4250*time.Millisecond {
		t.Errorf("captions %v, want the first from 4.25 s", gotCaps)
	}
	r.annotateMarks = func(context.Context, string) ([]time.Duration, error) { return nil, nil }
	if _, got := r.syncToVideo(context.Background(), v, caps, touches); got[0].At != touches[0].At {
		t.Errorf("with no mark found, touch 0 moved to %s", got[0].At)
	}
}

type markDriver struct {
	*fakeDriver
	shown time.Time
	held  time.Duration
}

func (d *markDriver) RecordingMark() (time.Time, time.Duration, bool) { return d.shown, d.held, true }

func TestAnnotateTimesFromTheFrameClock(t *testing.T) {
	t0 := time.Now()
	v := Video{Path: "recording.mp4", Start: t0, End: t0.Add(60 * time.Second)}
	touches := []annotate.Touch{{At: 5 * time.Second, Kind: "tap"}, {At: 7 * time.Second, Kind: "tap"}, {At: 9 * time.Second, Kind: "tap"}}
	d := &frameClockDriver{fakeDriver: &fakeDriver{settled: true}, first: t0.Add(700 * time.Millisecond), ok: true}
	r := New(d, Options{Annotate: true})
	r.annotateChanges = func(context.Context, string) ([]time.Duration, error) { return nil, nil }
	_, got := r.syncToVideo(context.Background(), v, nil, touches)
	for i := range touches {
		if want := touches[i].At - 700*time.Millisecond; i >= len(got) || got[i].At != want {
			t.Fatalf("touches %v, want each 0.7 s earlier", got)
		}
	}
	d.first = t0.Add(-time.Minute)
	if _, got := r.syncToVideo(context.Background(), v, nil, touches); got[0].At != touches[0].At {
		t.Errorf("frame times a minute off moved touch 0 to %s", got[0].At)
	}
	d.ok = false
	if _, got := r.syncToVideo(context.Background(), v, nil, touches); got[0].At != touches[0].At {
		t.Errorf("no frame times, touch 0 moved to %s", got[0].At)
	}
}

type frameClockDriver struct {
	*fakeDriver
	first time.Time
	ok    bool
}

func (d *frameClockDriver) VideoStarted(context.Context, string) (time.Time, bool) {
	return d.first, d.ok
}
