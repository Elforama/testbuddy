package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/timeline"
	"github.com/elforama/testbuddy/internal/ui"
)

// runRecorded runs yaml with Options.Record into a temp artifacts folder.
func runRecorded(t *testing.T, f *fakeDriver, yaml string, tl *timeline.Recorder) (FlowResult, string) {
	t.Helper()
	dir := t.TempDir()
	fl, err := flow.ParseBytes(filepath.Join(dir, "f.yaml"), []byte("appId: app\n---\n"+yaml))
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second, ArtifactsDir: dir, Record: true, Timeline: tl})
	return r.RunFlow(context.Background(), fl), dir
}

func recordingCalls(f *fakeDriver) []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "StartRecording") || strings.HasPrefix(c, "StopRecording") || strings.HasPrefix(c, "Tap") {
			out = append(out, c)
		}
	}
	return out
}

// --record records the screen from before the first command to after the last.
func TestRecordCoversTheWholeFlow(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	f.onTap = func(int, int) { f.record("Tap") }
	res, dir := runRecorded(t, f, "- tapOn: Go\n", nil)
	if res.Status != Passed {
		t.Fatal(res.Err)
	}
	want := []string{"StartRecording " + filepath.Join(dir, "recording.mp4"), "Tap", "StopRecording"}
	if got := recordingCalls(f); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls %q\nwant  %q", got, want)
	}
	if len(res.Videos) != 1 || res.Videos[0].Path != filepath.Join(dir, "recording.mp4") || !res.Videos[0].End.After(res.Videos[0].Start) {
		t.Errorf("videos %+v", res.Videos)
	}
}

// A flow's own recording takes the device for its stretch; the run's recording stops before
// it and starts again after, so the videos still cover the whole run, in order.
func TestRecordGivesTheDeviceToTheFlowsRecording(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	res, dir := runRecorded(t, f, "- startRecording: mine\n- tapOn: Go\n- stopRecording\n- tapOn: Go\n", nil)
	if res.Status != Passed {
		t.Fatal(res.Err)
	}
	mine := filepath.Join(dir, "startRecording", "mine.mp4")
	want := []string{
		"StartRecording " + filepath.Join(dir, "recording.mp4"), "StopRecording",
		"StartRecording " + mine, "StopRecording",
		"StartRecording " + filepath.Join(dir, "recording-2.mp4"), "StopRecording",
	}
	if got := recordingCalls(f); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls %q\nwant  %q", got, want)
	}
	var paths []string
	for _, v := range res.Videos {
		paths = append(paths, filepath.Base(v.Path))
	}
	if strings.Join(paths, " ") != "recording.mp4 mine.mp4 recording-2.mp4" || !res.Videos[1].Flow {
		t.Errorf("videos %v (flow's flagged: %v)", paths, len(res.Videos) > 1 && res.Videos[1].Flow)
	}
}

// A device that cannot record says why in the run's log, and the flow goes on.
func TestRecordFailureDoesNotFailTheFlow(t *testing.T) {
	f := &fakeDriver{settled: true, errs: map[string]error{"StartRecording": errors.New("no encoder")}}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	res, _ := runRecorded(t, f, "- tapOn: Go\n", nil)
	if res.Status != Passed || len(res.Videos) != 0 {
		t.Errorf("status %s, videos %v", res.Status, res.Videos)
	}
	if n := len(f.called("StopRecording")); n != 0 {
		t.Errorf("stopped a recording that never started (%d)", n)
	}
}

// Without --record nothing is recorded.
func TestNoRecordWithoutTheOption(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	if _, err := run(t, f, "- tapOn: Go\n"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.called("StartRecording")) + len(f.called("StopRecording")); n != 0 {
		t.Errorf("recorded without --record: %v", recordingCalls(f))
	}
}

// partsDriver saves recordings in parts, as Android before 14 does past 3 minutes.
type partsDriver struct {
	*fakeDriver
	parts []string
}

func (d *partsDriver) RecordingParts() []string { return d.parts }

// A recording saved in parts gives one video per part, in order.
func TestRecordingPartsAreVideos(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	d := &partsDriver{fakeDriver: f, parts: []string{"/r/recording.mp4", "/r/recording-part2.mp4"}}
	fl, _ := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("appId: app\n---\n- tapOn: Go\n"))
	res := New(d, Options{ArtifactsDir: t.TempDir(), Record: true, LookupTimeout: time.Second}).RunFlow(context.Background(), fl)
	if len(res.Videos) != 2 || res.Videos[1].Path != "/r/recording-part2.mp4" || res.Videos[1].Start.Before(res.Videos[0].Start) {
		t.Errorf("videos %+v", res.Videos)
	}
}

// The timeline shows the recording, linked next to it, and lets each step seek to itself.
func TestRecordingInTheTimeline(t *testing.T) {
	f := &fakeDriver{settled: true, shot: []byte("x")}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	tl := timeline.New()
	t.Cleanup(tl.Close)
	res, dir := runRecorded(t, f, "- tapOn: Go\n", tl)
	tl.Finish(timeline.Meta{Flow: "f", Status: string(res.Status)})
	page := filepath.Join(dir, "timeline.html")
	if err := tl.WriteFile(page); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(page)
	for _, want := range []string{`<video id="video-1" src="recording.mp4"`, `class="seek" data-video="video-1"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("timeline lacks %q", want)
		}
	}
}

// issue #107: a recording whose stop fails still lists its video when the device saved a
// partial file, and lists none when it saved nothing.
func TestRecordFailedStopKeepsAPartialVideo(t *testing.T) {
	for _, partial := range []bool{true, false} {
		f := &fakeDriver{settled: true, errs: map[string]error{"StopRecording": errors.New("lost the device")}}
		f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
		f.onTap = func(int, int) {
			if partial {
				f.mu.Lock()
				p := f.recs[0]
				f.mu.Unlock()
				os.WriteFile(p, []byte("partial"), 0o644)
			}
		}
		res, dir := runRecorded(t, f, "- tapOn: Go\n", nil)
		if res.Status != Passed {
			t.Fatalf("partial %v: %s %v (a recording does not fail the flow)", partial, res.Status, res.Err)
		}
		if n := len(f.called("StopRecording")); n != 1 {
			t.Errorf("partial %v: stopped %d times, want 1", partial, n)
		}
		var want []string
		if partial {
			want = []string{filepath.Join(dir, "recording.mp4")}
		}
		var got []string
		for _, v := range res.Videos {
			got = append(got, v.Path)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("partial %v: videos %q, want %q", partial, got, want)
		}
	}
}

// issue #107: the flow's own recording (startRecording), stopped at the flow's end, keeps
// its partial file too when its stop fails.
func TestRecordFailedFlowStopKeepsAPartialVideo(t *testing.T) {
	f := &fakeDriver{settled: true, errs: map[string]error{"StopRecording": errors.New("lost the device")}}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	f.onTap = func(int, int) {
		f.mu.Lock()
		p := f.recs[len(f.recs)-1] // the flow's own
		f.mu.Unlock()
		os.WriteFile(p, []byte("partial"), 0o644)
	}
	res, dir := runRecorded(t, f, "- startRecording: mine\n- tapOn: Go\n", nil)
	if res.Status != Passed {
		t.Fatal(res.Err)
	}
	mine := filepath.Join(dir, "startRecording", "mine.mp4")
	if len(res.Videos) != 1 || res.Videos[0].Path != mine || !res.Videos[0].Flow {
		t.Errorf("videos %+v, want only %s (the flow's)", res.Videos, mine)
	}
}
