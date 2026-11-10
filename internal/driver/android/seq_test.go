package android

import (
	"testing"
	"time"
)

// Seq is the highest change count the host has seen, not just the latest pushed "changed":
// a tree read at seq 12 while the last pushed change was 11 (the agent coalesces a burst's
// trailing change) must not leave Seq at 11, or the engine never reuses that screen
// (issue #68).
func TestSeqCoversTreeReply(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 12, "windows": []any{}} })
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "changed", "seq": 11})
	waitEvent(t, events, "changed")
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Seq != 12 || d.Seq() != 12 {
		t.Errorf("screen seq %d, Driver.Seq() %d; want both 12", s.Seq, d.Seq())
	}
}

// A "settled" event carries the burst's final seq: once it has arrived, Seq is at least that,
// so a screen read mid-burst (seq 11) is seen as stale instead of reused until the 1.5 s
// silence re-read (issue #68).
func TestSeqCoversSettled(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 11, "windows": []any{}} })
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "changed", "seq": 11})
	waitEvent(t, events, "changed")
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	a.push(map[string]any{"ev": "settled", "seq": 12})
	waitEvent(t, events, "settled")
	if got := d.Seq(); got != 12 || s.Seq == got {
		t.Errorf("after settled 12: Driver.Seq() %d, screen seq %d; want 12, and the screen stale", got, s.Seq)
	}
}

// Attaching to an agent that is already running starts Seq at the agent's count (from ping),
// not 0, so the first screen read there is current at once (issue #68).
func TestSeqSeededOnAttach(t *testing.T) {
	a := newFakeAgent(t)
	a.greet(func(int) map[string]any {
		return map[string]any{"ev": "hello", "platform": "android", "version": agentVersion(), "w": 1080, "h": 2400, "seq": 498}
	})
	a.on("ping", func(map[string]any) map[string]any { return map[string]any{"seq": 500} })
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 500, "windows": []any{}} })
	d := newDriver(t, a)
	if got := d.Seq(); got != 500 {
		t.Errorf("Driver.Seq() after attach = %d, want 500", got)
	}
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Seq != d.Seq() {
		t.Errorf("screen seq %d, Driver.Seq() %d: the first read is not current", s.Seq, d.Seq())
	}
}

// Reconnecting, even to the same running agent, moves Seq past every seq seen before, counted
// from the agent's ping: Seq never goes backwards and no screen read before the reconnect
// reads as current (issue #68).
func TestSeqAcrossReconnect(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "settled", "seq": 40})
	waitEvent(t, events, "settled")
	if got := d.Seq(); got != 40 {
		t.Errorf("Seq after settled 40 = %d, want 40", got)
	}
	a.on("ping", func(map[string]any) map[string]any { return map[string]any{"seq": 40} })
	c, h, err := dialHello(a.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	old := d.conn()
	if err := d.adopt(c, a.port(), h); err != nil {
		t.Fatal(err)
	}
	old.Close()
	if got := d.Seq(); got != 81 {
		t.Errorf("Seq after reconnecting at agent seq 40 = %d, want 81 (base 41 + 40)", got)
	}
}
