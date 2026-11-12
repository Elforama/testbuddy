package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elforama/testbuddy/internal/flow"
)

// issue #213: on iOS `back` does nothing (iOS has no system back key; the flow format says
// so), but it passed in 0 ms with nothing in the step log, so two backs in a row looked like
// they ran and the screen just did not change. Each skipped back now says so in the trace.
func TestIOSBackSaysItDoesNothing(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: static(btn("Level 3", "level_title", 300))}
	res, trace := traced(t, f, "- back\n- back\n- assertVisible: Level 3")
	mustPass(t, res)
	wantLines(t, trace,
		`▶ Press back`, `back does nothing on iOS \(no system back key\); tap the screen's Back button instead`,
		`▶ Press back`, `back does nothing on iOS \(no system back key\); tap the screen's Back button instead`)
	if len(f.calls) != 0 || len(f.taps) != 0 || len(f.keys) != 0 || len(f.swipes) != 0 {
		t.Errorf("iOS back reached the device: calls %q, taps %v, keys %q, swipes %d",
			f.calls, f.taps, f.keys, len(f.swipes))
	}
}

// issue #213: the console hears it once per flow, however many backs the flow has (a
// subflow's included), and again in the next flow. Android's back says nothing.
func TestIOSBackNoteOncePerFlow(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: static()}
	rec := &recorder{}
	o := quick
	o.Reporter = rec
	r := New(f, o)
	src := "appId: app\n---\n- back\n- back\n- runFlow:\n    commands:\n      - back\n"
	for i := 0; i < 2; i++ {
		fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "flow.yaml"), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		mustPass(t, r.RunFlow(context.Background(), fl))
	}
	var notes []string
	for _, e := range rec.all() {
		if e.Note != "" {
			notes = append(notes, e.Note)
		}
	}
	if len(notes) != 2 || notes[0] != iosBackNote || notes[1] != iosBackNote {
		t.Errorf("notes %q: want the iOS back note once in each of the 2 flows", notes)
	}
	if len(f.calls) != 0 || len(f.keys) != 0 {
		t.Errorf("iOS back reached the device: calls %q, keys %q", f.calls, f.keys)
	}

	g := &fakeDriver{screen: static()}
	rec = &recorder{}
	o.Reporter = rec
	mustPass(t, runWith(t, g, o, "- back"))
	for _, e := range rec.all() {
		if e.Note != "" {
			t.Errorf("Android back noted %q", e.Note)
		}
	}
}
