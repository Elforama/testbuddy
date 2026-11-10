package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/orchestrate"
)

// Without --timeline nothing is recorded or written; with it, each flow's timeline.html is
// written in its debug folder when it ends, with the run's devices and error, and fed the
// flow's trace.
func TestFlowTimelines(t *testing.T) {
	dir := t.TempDir()
	if tl := newFlowTimelines(false).open(dir, nil); tl != nil {
		t.Fatal("a timeline without --timeline")
	}
	fl, err := flow.ParseBytes(filepath.Join(dir, "login.yaml"), []byte("appId: app\nname: Login\n---\n- launchApp\n"))
	if err != nil {
		t.Fatal(err)
	}
	timelines := newFlowTimelines(true)
	trace := engine.NewTrace(&bytes.Buffer{})
	tl := timelines.open(dir, trace)
	if tl == nil {
		t.Fatal("no timeline with --timeline")
	}
	trace.Printf("", 0, "a line from the run")
	r := orchestrate.Result{Job: &orchestrate.Job{Flow: fl}, Status: engine.Failed, Err: errors.New("Element not found: Welcome"),
		Devices: map[string]device.Info{"": {Platform: device.Android, Name: "Pixel", Serial: "emulator-5554"}},
		Start:   time.Now(), Duration: 3 * time.Second}
	path := timelines.close(dir, r)
	if path != filepath.Join(dir, "timeline.html") {
		t.Fatalf("path = %q", path)
	}
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Login", "android Pixel", "Element not found: Welcome", "a line from the run", "3.0 s"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("timeline lacks %q", want)
		}
	}
	if timelines.close(dir, r) != "" {
		t.Error("a flow's timeline was written twice")
	}
}
