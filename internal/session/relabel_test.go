package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/engine"
)

func TestIssue421TapOnALabelThatChangedSinceTheLookFailsFast(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("Search", 100), button("Ask", 300))
	d := addDev(s, f, "")
	d.runner = engine.New(f, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts"),
		LookupTimeout: 12 * time.Second, OptionalTimeout: 100 * time.Millisecond})
	call(t, s.look, nil, "")
	f.show(button("Search for, Roof Repair", 100), button("Ask", 300))
	start := time.Now()
	resp, _ := call(t, s.do, map[string]any{"commands": `tapOn: "Search"`}, "")
	took := time.Since(start)
	if resp.Exit != 1 {
		t.Fatalf("the tap passed: %s", resp.Text)
	}
	if took > 6*time.Second {
		t.Errorf("the tap waited %s for a label that changed in place after the last look; want it to fail in a few seconds", took.Round(time.Millisecond))
	}
	if !strings.Contains(resp.Text, `"Search" (in your last look) is gone; at its place now: Button "Search for, Roof Repair"`) {
		t.Errorf("the failure does not say what took the element's place:\n%s", resp.Text)
	}
}

func TestIssue421ALabelThatComesBackIsStillTapped(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(button("Save", 100))
	d := addDev(s, f, "")
	d.runner = engine.New(f, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts"),
		LookupTimeout: 12 * time.Second, OptionalTimeout: 100 * time.Millisecond})
	call(t, s.look, nil, "")
	f.show(button("Saving…", 100))
	go func() {
		time.Sleep(time.Second)
		f.show(button("Save", 100))
	}()
	resp, _ := call(t, s.do, map[string]any{"commands": `tapOn: "Save"`}, "")
	if resp.Exit != 0 {
		t.Errorf("a label back in place within the grace was not tapped:\n%s", resp.Text)
	}
}
