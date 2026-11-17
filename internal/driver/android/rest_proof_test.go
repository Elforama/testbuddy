package android

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// restProver is driver.RestProver (issue #369), named here so the test builds without it.
type restProver interface {
	AtRest(ctx context.Context, s *ui.Screen, n *ui.Node, span time.Duration) bool
}

// Issue #369: the proof is the agent's, asked at proof time (op restProof) about the read, the
// target's window and the span, so frames drawn during or after the read's walk count. Whatever
// the agent does not prove (or an agent that does not know the op) is not proven.
func TestAtRestAsksTheAgentAboutTheReadAndSpan(t *testing.T) {
	a := newFakeAgent(t)
	var mu sync.Mutex
	still := map[float64]bool{} // per window id: what the agent answers
	a.on("tree", func(req map[string]any) map[string]any {
		win := func(id int, top int) map[string]any {
			return map[string]any{"id": id, "type": 1, "active": id == 7, "pkg": "com.example.app", "b": []int{0, 0, 1080, 2400},
				"nodes": []any{map[string]any{"d": 0, "b": []int{40, top, 1040, top + 120}, "c": "android.widget.Button", "t": "Go", "f": 4099}}}
		}
		return map[string]any{"seq": 3, "read": 41, "windows": []any{win(8, 1000), win(7, 400)}}
	})
	a.on("restProof", func(req map[string]any) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		if s, ok := still[req["window"].(float64)]; ok && s {
			return map[string]any{"still": true}
		}
		return map[string]any{"still": false, "why": "it drew a frame 10 ms ago"}
	})
	d := newDriver(t, a)
	rp, ok := any(d).(restProver)
	if !ok {
		t.Fatal("issue #369: the Android driver cannot prove a target at rest for a span")
	}
	ctx := ctxT(t)
	s, err := d.ScreenFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target := func(win int) *ui.Node {
		for _, n := range s.Nodes {
			if s.Windows[n.Window].ID == win {
				return n
			}
		}
		t.Fatalf("no node in window %d", win)
		return nil
	}
	mu.Lock()
	still[7] = true
	mu.Unlock()
	if !rp.AtRest(ctx, s, target(7), 80*time.Millisecond) {
		t.Error("issue #369: a target the agent proves at rest is not proven")
	}
	req := a.last("restProof")
	if num(t, req, "read") != 41 || num(t, req, "window") != 7 || num(t, req, "span") != 80 {
		t.Errorf("issue #369: restProof asked %v, want read 41, window 7, span 80", req)
	}
	if rp.AtRest(ctx, s, target(8), 40*time.Millisecond) {
		t.Error("a target the agent does not prove at rest is proven")
	}
	// A read with no number (an older agent) is not proven, and nothing is asked.
	before := a.arrived("restProof")
	s.Read = 0
	if rp.AtRest(ctx, s, target(7), 40*time.Millisecond) || a.arrived("restProof") != before {
		t.Error("a read with no number is proven, or the agent asked about it")
	}
}

// An agent that does not know restProof (an error reply) proves nothing.
func TestAtRestWithAnAgentThatDoesNotKnowTheProof(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any {
		return map[string]any{"seq": 3, "read": 5, "windows": []any{map[string]any{"id": 7, "type": 1, "pkg": "com.example.app", "b": []int{0, 0, 1080, 2400},
			"nodes": []any{map[string]any{"d": 0, "b": []int{40, 400, 1040, 520}, "c": "android.widget.Button", "t": "Go", "f": 4099}}}}}
	})
	a.on("restProof", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "unknown op"} })
	d := newDriver(t, a)
	rp, ok := any(d).(restProver)
	if !ok {
		t.Fatal("issue #369: the Android driver cannot prove a target at rest for a span")
	}
	ctx := ctxT(t)
	s, err := d.ScreenFresh(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rp.AtRest(ctx, s, s.Nodes[0], 40*time.Millisecond) {
		t.Error("an agent that does not know restProof proved a target at rest")
	}
}
