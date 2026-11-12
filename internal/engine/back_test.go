package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/elforama/testbuddy/internal/flow"
)

// issue #213: on iOS `back` does nothing (iOS has no system back key; the flow format says
// so), but it passed in 0 ms with nothing in the step log, so two backs in a row looked like
// they ran and the screen just did not change. Each skipped back now says so: in the trace and
// in its step result.
func TestIOSBackSaysItDoesNothing(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: static(btn("Level 3", "level_title", 300))}
	res, trace := traced(t, f, "- back\n- back\n- assertVisible: Level 3")
	mustPass(t, res)
	wantLines(t, trace,
		`▶ Press back`, `hint: back does nothing on iOS \(no system back key\); tap the screen's Back button instead`,
		`▶ Press back`, `hint: back does nothing on iOS \(no system back key\); tap the screen's Back button instead`)
	if len(f.calls) != 0 || len(f.taps) != 0 || len(f.keys) != 0 || len(f.swipes) != 0 {
		t.Errorf("iOS back reached the device: calls %q, taps %v, keys %q, swipes %d",
			f.calls, f.taps, f.keys, len(f.swipes))
	}
	// Each back's own step result carries it too; the assertion's does not.
	if len(res.Steps) != 3 || len(res.Steps[0].Hints) != 1 || res.Steps[0].Hints[0] != iosBackHint ||
		len(res.Steps[1].Hints) != 1 || len(res.Steps[2].Hints) != 0 {
		t.Errorf("step hints: %+v", res.Steps)
	}
}

// issue #213: `pressKey: Back`, which the device refuses on iOS, does nothing there too, and
// says so the same way; another key iOS lacks says which one.
func TestIOSPressKeyBackSaysItDoesNothing(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: static()}
	f.fail("PressKey", errors.New("iOS has no back key"))
	res, trace := traced(t, f, "- pressKey: Back\n- pressKey: Volume Up")
	mustPass(t, res)
	wantLines(t, trace, `▶ Press Back`, `hint: back does nothing on iOS \(no system back key\)`,
		`▶ Press Volume Up`, `hint: pressKey Volume Up does nothing on iOS \(iOS has no back key\)`)
	if len(res.Steps) != 2 || len(res.Steps[0].Hints) != 1 || res.Steps[0].Hints[0] != iosBackHint {
		t.Errorf("step hints: %+v", res.Steps)
	}
}

// issue #213: the console hears it once per flow, however many backs the flow has (a
// subflow's included), and again in the next flow. Android's back says nothing.
func TestIOSBackHintOncePerFlow(t *testing.T) {
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
	var hints []Event
	for _, e := range rec.all() {
		if e.Hint != "" {
			hints = append(hints, e)
		}
	}
	if len(hints) != 2 || hints[0].Hint != iosBackHint || hints[1].Hint != iosBackHint || hints[0].Depth != 1 {
		t.Errorf("hints %+v: want the iOS back hint once in each of the 2 flows, under its step", hints)
	}
	if len(f.calls) != 0 || len(f.keys) != 0 {
		t.Errorf("iOS back reached the device: calls %q, keys %q", f.calls, f.keys)
	}

	g := &fakeDriver{screen: static()}
	rec = &recorder{}
	o.Reporter = rec
	mustPass(t, runWith(t, g, o, "- back"))
	for _, e := range rec.all() {
		if e.Hint != "" {
			t.Errorf("Android back noted %q", e.Hint)
		}
	}
}
