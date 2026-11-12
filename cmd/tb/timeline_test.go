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

// Issue #65: a timeline keeps its screenshots in a temp file until its page is written.
// The file is gone once the flow's timeline is closed, and closeAll removes those of
// flows that never reported their end (the run ends however it ends).
func TestFlowTimelinesRemoveTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	left := func() []string {
		m, _ := filepath.Glob(filepath.Join(tmp, "tb-timeline-*"))
		return m
	}
	dir := t.TempDir()
	fl, err := flow.ParseBytes(filepath.Join(dir, "a.yaml"), []byte("appId: app\n---\n- launchApp\n"))
	if err != nil {
		t.Fatal(err)
	}
	timelines := newFlowTimelines(true)
	tl := timelines.open(filepath.Join(dir, "a"), engine.NewTrace(&bytes.Buffer{}))
	tl.Shot("", "image/jpeg", []byte("jpeg"), 10, 10)
	if len(left()) != 1 {
		t.Fatalf("temp files %q, want the timeline's one", left())
	}
	r := orchestrate.Result{Job: &orchestrate.Job{Flow: fl}, Status: engine.Passed, Start: time.Now()}
	if timelines.close(filepath.Join(dir, "a"), r) == "" {
		t.Fatal("no page")
	}
	if l := left(); len(l) != 0 {
		t.Errorf("temp files left after the flow ended: %q", l)
	}

	for _, d := range []string{"b", "c"} {
		timelines.open(filepath.Join(dir, d), engine.NewTrace(&bytes.Buffer{})).Shot("", "image/jpeg", []byte("jpeg"), 10, 10)
	}
	timelines.closeAll()
	if l := left(); len(l) != 0 {
		t.Errorf("temp files left after closeAll: %q", l)
	}
}
