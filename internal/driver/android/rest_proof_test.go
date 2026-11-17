package android

import (
	"context"
	"sync"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// restProver is driver.RestProver (issue #369), named here so the test builds without it.
type restProver interface {
	AtRest(ctx context.Context, s *ui.Screen, n *ui.Node) bool
}

// Issue #369: a read past the agent's node cache says how long each window had drawn no frame
// ("still", ms, where its frames show motion). A target in a window still for 50 ms or more is
// at rest on that read; one in a window that drew more recently, one of a read that said
// nothing (a cached read, an older agent), or of a read before the latest, is not proven.
func TestAtRestFromTheWindowsFrames(t *testing.T) {
	a := newFakeAgent(t)
	var mu sync.Mutex
	still := map[int]any{} // per window id: its "still", when the agent says one
	read := 0
	a.on("tree", func(req map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		read++
		win := func(id int, top int) map[string]any {
			w := map[string]any{"id": id, "type": 1, "active": id == 7, "pkg": "com.example.app", "b": []int{0, 0, 1080, 2400},
				"nodes": []any{map[string]any{"d": 0, "b": []int{40, top, 1040, top + 120}, "c": "android.widget.Button", "t": "Go", "f": 4099}}}
			if v, ok := still[id]; ok && req["fresh"] == true {
				w["still"] = v
			}
			return w
		}
		return map[string]any{"seq": 3, "read": read, "windows": []any{win(8, 1000), win(7, 400)}}
	})
	d := newDriver(t, a)
	rp, ok := any(d).(restProver)
	if !ok {
		t.Fatal("issue #369: the Android driver cannot prove a target at rest")
	}
	ctx := ctxT(t)
	target := func(s *ui.Screen, win int) *ui.Node {
		for _, n := range s.Nodes {
			if s.Windows[n.Window].ID == win {
				return n
			}
		}
		t.Fatalf("no node in window %d", win)
		return nil
	}

	mu.Lock()
	still[7], still[8] = 120, 20
	mu.Unlock()
	s, err := d.ScreenFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rp.AtRest(ctx, s, target(s, 7)) {
		t.Error("issue #369: a target in a window still for 120 ms is not proven at rest")
	}
	if rp.AtRest(ctx, s, target(s, 8)) {
		t.Error("a target in a window that drew 20 ms before the read is proven at rest")
	}

	// A plain read (from the agent's cache) says nothing of frames: nothing proven.
	plain, err := d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rp.AtRest(ctx, plain, target(plain, 7)) {
		t.Error("a target of a cached read is proven at rest")
	}
	// Nor is a target of a read past the cache older than the latest such read.
	if _, err := d.ScreenFresh(ctx); err != nil {
		t.Fatal(err)
	}
	if rp.AtRest(ctx, s, target(s, 7)) {
		t.Error("a target of a read older than the latest is proven at rest")
	}

	// A window the agent does not trust (its content draws elsewhere) has no "still".
	mu.Lock()
	delete(still, 7)
	mu.Unlock()
	s, err = d.ScreenFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rp.AtRest(ctx, s, target(s, 7)) {
		t.Error("a target in a window without \"still\" is proven at rest")
	}
}
