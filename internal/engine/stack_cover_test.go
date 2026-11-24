package engine

import (
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

func TestTapOnTopScreenOverPreviousScreenListedLater(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		root := at("FrameLayout", 0, 0, 0, 400, 800)
		stack := at("ViewGroup", 1, 0, 0, 400, 800)
		top := at("ViewGroup", 2, 0, 0, 400, 800)
		top.DrawOrder, top.DrawOrderKnown = 2, true
		body := at("ScrollView", 3, 0, 100, 400, 800)
		back := clickable(4, 20, 480, 380, 520)
		back.Text = "Back to home"
		prev := at("ViewGroup", 2, 0, 0, 400, 800)
		prev.DrawOrder, prev.DrawOrderKnown = 1, true
		prevBody := at("ScrollView", 3, 0, 100, 400, 800)
		nav := clickable(4, 0, 470, 400, 530)
		nav.Text = "Navigate to Details"
		return []*ui.Node{root, stack, top, body, back, prev, prevBody, nav}
	}
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, `- tapOn: "Back to home"`))
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s: waited for the previous screen, drawn under the target, to go", d)
	}
	if len(f.taps) != 1 || f.taps[0][1] < 480 || f.taps[0][1] >= 520 {
		t.Errorf("taps %v, want one on Back to home", f.taps)
	}
}
