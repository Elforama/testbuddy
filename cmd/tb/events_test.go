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
	"github.com/elforama/testbuddy/internal/engine"
	marks "github.com/elforama/testbuddy/internal/mark"
)

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
		for _, ev := range get(role, time.Now().Add(-time.Minute), time.Now().Add(-time.Minute), time.Now()) {
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

func TestEventsPortNeedsAnnotate(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.yaml")
	os.WriteFile(f, []byte("appId: app\n---\n- launchApp\n"), 0o644)
	if code := cmdTest([]string{f, "--record", "--events-port", "8000"}); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func eventTexts(evs []engine.AnnotateEvent) string {
	var s []string
	for _, ev := range evs {
		s = append(s, ev.Text)
	}
	return strings.Join(s, ",")
}

func TestIssue413EventsBeforeTheRecordingStartsAreShown(t *testing.T) {
	hub := marks.NewHub()
	e := &runEvents{hub: hub}
	get := e.forJob(map[string]device.Info{"": {Serial: "SIM-1", Name: "iPhone"}})
	tick := func() time.Time { time.Sleep(15 * time.Millisecond); return time.Now() }
	hub.Add("", "stale")
	flow1 := tick()
	hub.Add("", "launch", "sign_in")
	start1 := tick()
	hub.Add("", "screen_view")
	end1 := tick()
	hub.Add("", "between")
	flow2 := tick()
	start2 := tick()
	end2 := tick()
	if got := eventTexts(get("", flow1, start1, end1)); got != "launch,sign_in,screen_view" {
		t.Errorf("issue #413: the first video shows %q, want the events sent after its flow began, before it recorded too", got)
	}
	if got := eventTexts(get("", flow2, start2, end2)); got != "between" {
		t.Errorf("issue #413: the second video shows %q, want the event sent between the flows", got)
	}
	tick()
	hub.Add("", "too late")
	hub.Add("nowhere", "for a device that never recorded")
	if n, _ := e.unshown(); n != 3 {
		t.Errorf("issue #413: %d events reported as in no video, want 3 (stale, too late, nowhere)", n)
	}
}

func TestIssue413DevicesRecordingTogetherKeepTheirOwnEvents(t *testing.T) {
	hub := marks.NewHub()
	e := &runEvents{hub: hub}
	tick := func() time.Time { time.Sleep(15 * time.Millisecond); return time.Now() }
	a := e.forJob(map[string]device.Info{"": {Serial: "A"}})
	b := e.forJob(map[string]device.Info{"": {Serial: "B"}})
	flowB := tick()
	startB := tick()
	hub.Add("B", "b1")
	flowA := tick()
	startA := tick()
	hub.Add("", "both")
	endA := tick()
	hub.Add("B", "b2")
	endB := tick()
	if got := eventTexts(a("", flowA, startA, endA)); got != "both" {
		t.Errorf("A's video shows %q", got)
	}
	if got := eventTexts(b("", flowB, startB, endB)); got != "b1,both,b2" {
		t.Errorf("issue #413 review: B's video, annotated after A's, shows %q, want b1,both,b2", got)
	}
	if got := eventTexts(b("", flowB, startB, endB)); got != "b1,both,b2" {
		t.Errorf("asked again out of order, B's video shows %q", got)
	}
}

func TestIssue413SecondVideoOfAFlowDoesNotRepeatTheFirst(t *testing.T) {
	hub := marks.NewHub()
	e := &runEvents{hub: hub}
	get := e.forJob(map[string]device.Info{"": {Serial: "SIM-1"}})
	tick := func() time.Time { time.Sleep(15 * time.Millisecond); return time.Now() }
	flow := tick()
	start1 := tick()
	hub.Add("", "in part 1")
	end1 := tick()
	hub.Add("", "in part 2")
	end2 := tick()
	if got := eventTexts(get("", flow, start1, end1)); got != "in part 1" {
		t.Errorf("part 1 shows %q", got)
	}
	if got := eventTexts(get("", flow, end1, end2)); got != "in part 2" {
		t.Errorf("issue #413 review: part 2 shows %q, want only its own events", got)
	}
}
