package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	marks "github.com/elforama/testbuddy/internal/mark"
)

// tb mark sends its texts as one burst, for the device -d names.
func TestMarkSendsToTheRunsEndpoint(t *testing.T) {
	hub := marks.NewHub()
	srv, err := marks.Serve(hub, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var out, errOut bytes.Buffer
	if code := runMark([]string{"--url", srv.URL, "-d", "pixel", "screen_view", "button_tap"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := hub.Between(time.Now().Add(-time.Minute), time.Now(), "pixel")
	if len(got) != 2 || got[0].Text != "screen_view" || got[1].Text != "button_tap" || got[0].Device != "pixel" {
		t.Errorf("%+v", got)
	}
	if !strings.Contains(out.String(), "sent 2") {
		t.Errorf("out %q", out.String())
	}
}

// tb mark - reads one event a line.
func TestMarkReadsStdin(t *testing.T) {
	hub := marks.NewHub()
	srv, err := marks.Serve(hub, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var out, errOut bytes.Buffer
	if code := runMark([]string{"--url", srv.URL, "-"}, strings.NewReader("a\n\nb\n"), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if kept, _ := hub.Count(); kept != 2 {
		t.Errorf("kept %d", kept)
	}
}

// With no run taking events, tb mark says so and fails; with no text, it shows its usage.
func TestMarkWithoutARun(t *testing.T) {
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv(marks.EnvURL, "http://127.0.0.1:1/event")
	var out, errOut bytes.Buffer
	if code := runMark([]string{"a"}, nil, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no tb run takes events") {
		t.Errorf("exit %d: %s", code, errOut.String())
	}
	errOut.Reset()
	if code := runMark(nil, nil, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "Usage: tb mark") {
		t.Errorf("exit %d: %s", code, errOut.String())
	}
}

// A job's runners get the events for their own device: by role, serial or name, and those
// that name no device.
func TestRunEventsForJob(t *testing.T) {
	hub := marks.NewHub()
	e := &runEvents{hub: hub}
	hub.Add("emulator-5554", "for alice")
	hub.Add("bob", "for bob")
	hub.Add("", "for everyone")
	ds := map[string]device.Info{"alice": {Serial: "emulator-5554", Name: "Pixel"}, "bob": {Serial: "SIM-1", Name: "iPhone"}}
	get := e.forJob(ds)
	texts := func(role string) string {
		var s []string
		for _, ev := range get(role, time.Now().Add(-time.Minute), time.Now()) {
			s = append(s, ev.Text)
		}
		return strings.Join(s, ",")
	}
	if got := texts("alice"); got != "for alice,for everyone" {
		t.Errorf("alice %q", got)
	}
	if got := texts("bob"); got != "for bob,for everyone" {
		t.Errorf("bob %q", got)
	}
	var none *runEvents
	if none.forJob(ds) != nil {
		t.Error("no endpoint, yet events")
	}
}

// An Android device gets an adb reverse to the endpoint's port, undone at the end; a simulator
// or a browser needs none.
func TestRunEventsReachAndroid(t *testing.T) {
	old := reverseADB
	defer func() { reverseADB = old }()
	var calls []string
	reverseADB = func(_ context.Context, serial string, port int, remove bool) error {
		op := "add"
		if remove {
			op = "remove"
		}
		calls = append(calls, op+" "+serial)
		return nil
	}
	srv, err := marks.Serve(marks.NewHub(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	e := &runEvents{hub: marks.NewHub(), srv: srv, reversed: map[string]bool{}}
	e.reach(context.Background(), device.Info{Platform: device.Android, Serial: "emulator-5554"})
	e.reach(context.Background(), device.Info{Platform: device.IOS, Serial: "SIM-1"})
	e.close()
	if strings.Join(calls, "|") != "add emulator-5554|remove emulator-5554" {
		t.Errorf("calls %v", calls)
	}
}

// --events-port means nothing without --annotate: tb says so rather than ignoring it (found in
// review).
func TestEventsPortNeedsAnnotate(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.yaml")
	os.WriteFile(f, []byte("appId: app\n---\n- launchApp\n"), 0o644)
	if code := cmdTest([]string{f, "--record", "--events-port", "8000"}); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}
