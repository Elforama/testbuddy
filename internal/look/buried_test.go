package look

import (
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #320: a branch drawn under what hides it whole (ui.Node.Buried, the previous screen of
// a stack) is not listed; the screen on top is.
func TestBuildLeavesOutBuried(t *testing.T) {
	buried := func(n *ui.Node) { n.Buried = true }
	s := android(
		nd(0, "android.widget.FrameLayout", rect(0, 0, 1080, 2400)),
		nd(1, "android.widget.TextView", row(0), text("Pushed screen")),
		nd(1, "android.view.ViewGroup", rect(0, 0, 1080, 2400), buried),
		nd(2, "android.widget.TextView", row(2), text("Spring Vegetable Frittata"), buried),
	)
	got := strings.Join(lines(Build(s, NewRefs())), "\n")
	if !strings.Contains(got, "Pushed screen") || strings.Contains(got, "Frittata") {
		t.Errorf("listing:\n%s", got)
	}
}
