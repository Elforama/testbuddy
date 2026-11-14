package android

import (
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// Issue #74 (review): settle's since is the agent's own seq (agent 0.1.48). Driver.Seq() adds
// the connection's base, which a reconnect moves past every count seen: Settle(d.Seq()) after
// a reconnect sent a count the restarted agent would not reach for a long time, and it
// answered "no_change" whatever the screen did. It sends the agent's count instead, and -1
// (every change counts) for a count from before the agent it lands on.
func TestSettleSendsTheAgentsOwnCountAfterAReconnect(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "settled", "seq": 40})
	waitEvent(t, events, "settled")
	before := d.Seq() // 40, on the first agent

	b := newFakeAgent(t) // the restarted agent, at its own count 3
	b.on("ping", func(map[string]any) map[string]any { return map[string]any{"seq": 3} })
	b.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 4} })
	adoptAgent(t, d, b)
	old := a
	old.dropConns()

	now := d.Seq()
	if now <= before {
		t.Fatalf("Seq after the reconnect = %d, want past %d", now, before)
	}
	if _, err := d.Settle(ctxT(t), now, 250, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := num(t, b.last("settle"), "since"); got != 3 {
		t.Errorf("settle since d.Seq() = %d sent %v, want the agent's own 3", now, got)
	}
	if _, err := d.Settle(ctxT(t), before, 250, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := num(t, b.last("settle"), "since"); got != -1 {
		t.Errorf("settle since %d (the first agent's) sent %v, want -1", before, got)
	}
}

// Issue #74 (review): the editor's Backspace settles on the count its editorTarget reply gave.
// When the connection is replaced between the two (an agent restart), that count is the old
// agent's: the settle on the new connection sends -1 instead.
func TestBackspaceSettleOnANewConnectionCountsEveryChange(t *testing.T) {
	ed := newEditorServer(t, true)
	d, a, _ := editorDriver(t, ed.port())
	b := newFakeAgent(t)
	b.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 2} })
	a.on("editorTarget", func(map[string]any) map[string]any {
		// The agent restarts while this reply is on its way: the driver moves to b.
		c, h, err := dialHello(b.port(), 2*time.Second)
		if err == nil {
			err = d.adopt(c, b.port(), h)
		}
		if err != nil {
			t.Error(err)
		}
		return map[string]any{"seq": 12, "target": map[string]any{"pkg": "example.app", "window": 7, "node": 42, "id": "field", "cls": "EditText"}}
	})
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(a.requests("settle")) != 0 || len(b.requests("settle")) != 1 {
		t.Fatalf("settles: old agent %d, new agent %d; want 0 and 1", len(a.requests("settle")), len(b.requests("settle")))
	}
	if got := num(t, b.last("settle"), "since"); got != -1 {
		t.Errorf("settle on the new agent sent since %v, want -1 (the old agent's 12 means nothing there)", got)
	}
	// On the same connection, the editorTarget's count goes as it is.
	a2 := b
	a2.on("editorTarget", func(map[string]any) map[string]any {
		return map[string]any{"seq": 5, "target": map[string]any{"pkg": "example.app", "window": 7, "node": 42, "id": "field", "cls": "EditText"}}
	})
	if _, err := d.PressKey(ctxT(t), "Backspace", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := num(t, b.last("settle"), "since"); got != 5 {
		t.Errorf("settle on the same agent sent since %v, want its editorTarget's 5", got)
	}
}
