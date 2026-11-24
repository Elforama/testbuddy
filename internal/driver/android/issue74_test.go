package android

import (
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func TestSettleSendsTheAgentsOwnCountAfterAReconnect(t *testing.T) {
	a := newFakeAgent(t)
	b := newFakeAgent(t)
	d := newDriver(t, a)
	events, cancel := d.Events()
	defer cancel()
	a.push(map[string]any{"ev": "settled", "seq": 40})
	waitEvent(t, events, "settled")
	before := d.Seq()

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

func TestBackspaceSettleOnANewConnectionCountsEveryChange(t *testing.T) {
	ed := newEditorServer(t, true)
	b := newFakeAgent(t)
	d, a, _ := editorDriver(t, ed.port())
	b.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 2} })
	a.on("editorTarget", func(map[string]any) map[string]any {
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
