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

// A tree reply on the old connection that is decoded after a reconnect moved the base must not
// be mapped with the new base: the old agent's 500 plus the new base would push Seq far past
// the restarted agent's count, and every screen read on the new connection would look stale
// until that count caught up (issue #68). The old connection's tree handler is held until the
// reconnect is done, so the order is fixed.
func TestSeqOldReplyAfterReconnect(t *testing.T) {
	a := newFakeAgent(t)
	treeHeld, release := make(chan struct{}), make(chan struct{})
	a.on("tree", func(map[string]any) map[string]any {
		close(treeHeld)
		<-release
		return map[string]any{"seq": 500, "windows": []any{}}
	})
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "settled", "seq": 499})
	waitEvent(t, events, "settled")

	type res struct {
		seq int64
		err error
	}
	old := make(chan res, 1)
	go func() {
		s, err := d.Screen(ctxT(t))
		if err != nil {
			old <- res{err: err}
			return
		}
		old <- res{seq: s.Seq}
	}()
	<-treeHeld

	b := newFakeAgent(t) // the restarted agent, counting from 0
	b.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 0, "windows": []any{}} })
	c, h, err := dialHello(b.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.adopt(c, b.port(), h); err != nil {
		t.Fatal(err)
	}
	close(release)
	r := <-old
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.seq >= d.Seq() {
		t.Errorf("the old connection's screen (seq %d) reads as current (Driver.Seq() %d)", r.seq, d.Seq())
	}
	s, err := d.Screen(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Seq != d.Seq() {
		t.Errorf("screen on the new connection: seq %d, Driver.Seq() %d; want equal (current)", s.Seq, d.Seq())
	}
}
