package engine

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/timeline"
	"github.com/elforama/testbuddy/internal/ui"
)

// runTimeline runs yaml on f with a timeline and a trace, and returns the page.
func runTimeline(t *testing.T, f *fakeDriver, yaml string) (FlowResult, string) {
	t.Helper()
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n"+yaml))
	if err != nil {
		t.Fatal(err)
	}
	tl := timeline.New()
	t.Cleanup(tl.Close) // its screenshots' temp file (issue #65)
	var log bytes.Buffer
	tr := NewTrace(&log)
	tr.Tee(tl.Line)
	res := New(f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second, Timeline: tl, Trace: tr}).
		RunFlow(context.Background(), fl)
	tl.Finish(timeline.Meta{Flow: "f", Status: string(res.Status)})
	return res, string(tl.HTML())
}

// A timeline screenshots the screen when the flow starts and as each step ends when the
// screen changed (not for an assertion that changed nothing), draws the tap where it went,
// and files the trace lines under their step.
func TestTimelineRecordsScreensAndTaps(t *testing.T) {
	f := &fakeDriver{shot: []byte("jpeg"), settled: true}
	var tapped atomic.Bool
	f.screen = func() []*ui.Node {
		if tapped.Load() {
			return []*ui.Node{btn("Done", "done", 300)}
		}
		return []*ui.Node{btn("Go", "go", 100)}
	}
	f.onTap = func(x, y int) { tapped.Store(true); f.change(true) }
	res, page := runTimeline(t, f, "- tapOn: Go\n- assertVisible: Done\n")
	if res.Status != Passed {
		t.Fatalf("flow: %v", res.Err)
	}
	if n := strings.Count(strings.Join(f.calls, "\n"), "Screenshot 0.5 jpeg"); n != 2 {
		t.Errorf("screenshots = %d, want 2 (the start, after the tap): %v", n, f.calls)
	}
	for _, want := range []string{
		`<circle cx="200.0" cy="125.0"`, // the tap, at Go's centre in the 400x800 screen
		`aspect-ratio:400 / 800`,
		`Tap on &#34;Go&#34;`,
		`Assert that &#34;Done&#34; is visible`,
		"found", // a lookup line, under its step
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "▶ Tap") {
		t.Error("a command's own start line was repeated in its trace")
	}
}

// Recording a timeline does not change what the flow does: the same commands reach the
// device, plus screenshots.
func TestTimelineDoesNotChangeTheRun(t *testing.T) {
	calls := func(withTimeline bool) []string {
		f := &fakeDriver{shot: []byte("x"), settled: true}
		f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
		yaml := "- tapOn: Go\n- swipe: {direction: UP}\n- assertVisible: Go\n"
		if withTimeline {
			runTimeline(t, f, yaml)
		} else if _, err := run(t, f, yaml); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range f.calls {
			if !strings.HasPrefix(c, "Screenshot") {
				out = append(out, c)
			}
		}
		return out
	}
	without, with := calls(false), calls(true)
	if strings.Join(without, "|") != strings.Join(with, "|") {
		t.Errorf("device calls differ:\nwithout %v\nwith    %v", without, with)
	}
}

// A failed step gets its screen and error on the page, and the page links to it.
func TestTimelineShowsTheFailure(t *testing.T) {
	f := &fakeDriver{shot: []byte("x"), settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	res, page := runTimeline(t, f, "- assertVisible: Nowhere\n")
	if res.Status != Failed {
		t.Fatal("the flow passed")
	}
	if !strings.Contains(page, `href="#s1"`) || !strings.Contains(page, "Nowhere") || !strings.Contains(page, `class="step failed"`) {
		t.Error("the page does not show or link the failed step")
	}
}

// Without a timeline, nothing of it costs time: no screenshots and no extra settle waits
// (the waits a timeline adds come only from its screenshots).
func TestNoTimelineTakesNoScreenshotsOrWaits(t *testing.T) {
	waits := func(withTimeline bool) (shots int, waits int64) {
		f := &fakeDriver{shot: []byte("x"), settled: true}
		f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
		yaml := "- tapOn: Go\n- swipe: {direction: UP}\n- assertVisible: Go\n"
		if withTimeline {
			runTimeline(t, f, yaml)
		} else if _, err := run(t, f, yaml); err != nil {
			t.Fatal(err)
		}
		return strings.Count(strings.Join(f.calls, "\n"), "Screenshot"), f.waits.Load()
	}
	offShots, offWaits := waits(false)
	onShots, onWaits := waits(true)
	if offShots != 0 {
		t.Errorf("%d screenshots without a timeline", offShots)
	}
	if onShots == 0 {
		t.Fatal("the timeline took no screenshots: the comparison proves nothing")
	}
	// Each timeline screenshot may add one settle wait; nothing else differs.
	if extra := onWaits - offWaits; extra < 0 || extra > int64(onShots)+3 {
		t.Errorf("settle waits: %d without, %d with a timeline (%d screenshots)", offWaits, onWaits, onShots)
	}
}
