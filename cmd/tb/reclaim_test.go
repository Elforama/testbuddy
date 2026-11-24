package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/session"
)

func TestReclaimAsksTheOwnDaemon(t *testing.T) {
	f := startFakeDaemon(t)
	b, _ := json.Marshal(session.Yielded{Yielded: true, Label: "alice (Pixel 7)"})
	f.answer("yield", daemon.Response{OK: true, JSON: b})
	if r := reclaimFromOwnSession(context.Background(), "emulator-5750"); r != (orchestrate.Reclaimed{OK: true, Label: "alice (Pixel 7)"}) {
		t.Errorf("given up: %+v", r)
	}
	cmd, args, _ := f.last(t)
	if cmd != "yield" || args["serial"] != "emulator-5750" {
		t.Errorf("asked %s %v", cmd, args)
	}
	b, _ = json.Marshal(session.Yielded{Busy: true, Label: "alice (Pixel 7)"})
	f.answer("yield", daemon.Response{OK: true, JSON: b})
	if r := reclaimFromOwnSession(context.Background(), "emulator-5750"); r != (orchestrate.Reclaimed{Busy: true, Label: "alice (Pixel 7)"}) {
		t.Errorf("busy: %+v", r)
	}

	f.answer("yield", daemon.Response{Exit: 2, Text: "unknown command yield"})
	if r := reclaimFromOwnSession(context.Background(), "emulator-5750"); r != (orchestrate.Reclaimed{Unknown: true}) {
		t.Errorf("a daemon that cannot answer: %+v", r)
	}

	t.Setenv("TB_HOME", t.TempDir())
	if r := reclaimFromOwnSession(context.Background(), "emulator-5750"); r != (orchestrate.Reclaimed{}) {
		t.Errorf("no daemon: %+v", r)
	}
	if daemon.Running() {
		t.Error("asking started a daemon")
	}
}
