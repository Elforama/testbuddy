package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// --slow holds the screen after each step that acts on the app, and only those: a check, a
// script or a runFlow is not held (its steps are), and without --slow nothing is.
func TestSlowHoldsAfterStepsThatAct(t *testing.T) {
	yaml := "- tapOn: Go\n- assertVisible: Go\n- evalScript: ${1}\n- runFlow:\n    commands:\n      - tapOn: Go\n"
	for _, slow := range []bool{false, true} {
		f := &fakeDriver{settled: true}
		f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
		fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("appId: app\n---\n"+yaml))
		if err != nil {
			t.Fatal(err)
		}
		r := New(f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second, Slow: slow})
		var holds []time.Duration
		r.slowWait = func(_ context.Context, d time.Duration) { holds = append(holds, d) }
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
			if d < slowHoldMin || d > slowHoldMax {
				t.Errorf("hold %s outside [%s, %s]", d, slowHoldMin, slowHoldMax)
			}
		}
	}
}

// The hold is the step's own time (the run takes about twice as long), between a floor a
// quick tap is seen at and a cap that keeps a long wait from becoming dead video.
func TestSlowHoldLength(t *testing.T) {
	for _, c := range []struct{ took, want time.Duration }{
		{50 * time.Millisecond, slowHoldMin},
		{1500 * time.Millisecond, 1500 * time.Millisecond},
		{20 * time.Second, slowHoldMax},
	} {
		if got := slowHold(c.took); got != c.want {
			t.Errorf("slowHold(%s) = %s, want %s", c.took, got, c.want)
		}
	}
}

// A failed step is not held: the flow ends there.
func TestSlowDoesNotHoldAFailedStep(t *testing.T) {
	f := &fakeDriver{settled: true}
	f.screen = func() []*ui.Node { return []*ui.Node{btn("Go", "go", 100)} }
	fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "f.yaml"), []byte("appId: app\n---\n- tapOn: Missing\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, Options{LookupTimeout: 200 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond, Slow: true})
	held := false
	r.slowWait = func(context.Context, time.Duration) { held = true }
	if res := r.RunFlow(context.Background(), fl); res.Status != Failed {
		t.Fatalf("status %s", res.Status)
	}
	if held {
		t.Error("a failed step was held")
	}
}
