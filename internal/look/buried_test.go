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

// Issue #320: an id is shown when it is unique among what is on screen: the same id on the
// buried screen under it (the same screen, pushed again over itself) does not hide it, on the
// element nor on its container.
func TestBuildIDCountSkipsBuried(t *testing.T) {
	buried := func(n *ui.Node) { n.Buried = true }
	s := android(
		nd(0, "android.widget.FrameLayout", rect(0, 0, 1080, 2400)),
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), id("cart"), scrollable),
		nd(2, "android.widget.Button", row(0), text("Save"), id("app:id/save"), click),
		nd(2, "android.widget.Button", row(1), text("Cancel"), id("app:id/cancel"), click),
		nd(1, "androidx.recyclerview.widget.RecyclerView", rect(0, 200, 1080, 2200), id("cart"), scrollable, buried),
		nd(2, "android.widget.Button", row(0), text("Save"), id("app:id/save"), click, buried),
	)
	l := Build(s, NewRefs())
	got := strings.Join(lines(l), "\n")
	if !strings.Contains(got, "#save") {
		t.Errorf("listing has no #save:\n%s", got)
	}
	if !strings.Contains(l.Text(), "#cart") {
		t.Errorf("outline has no #cart:\n%s", l.Text())
	}
}
