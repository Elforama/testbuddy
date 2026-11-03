package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/look"
)

// watchWith runs watch; once it reports "watching", act runs (in another goroutine) to
// change the device. It returns the final response and the streamed parts.
func watchWith(t *testing.T, s *Service, ctx context.Context, args map[string]any, act func()) (daemon.Response, []daemon.Response) {
	t.Helper()
	b := mustJSON(t, args)
	var parts []daemon.Response
	done := make(chan daemon.Response, 1)
	partc := make(chan daemon.Response, 64)
	go func() {
		started := false
		done <- s.watch(ctx, daemon.Request{Args: b}, func(r daemon.Response) {
			partc <- r
			if !started && strings.HasPrefix(r.Text, "watching ") {
				started = true
				if act != nil {
					go act()
				}
			}
		})
	}()
	select {
	case resp := <-done:
		close(partc)
		for p := range partc {
			parts = append(parts, p)
		}
		return resp, parts
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not return")
	}
	return daemon.Response{}, nil
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func watchDev(t *testing.T) (*Service, *fakeDriver) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(label("Loading", 100))
	addDev(s, f, "alice")
	return s, f
}

// watch announces itself, then streams each settled change as a timed diff, until the
// awaited text appears.
func TestWatchStreamsChangesUntil(t *testing.T) {
	s, f := watchDev(t)
	resp, parts := watchWith(t, s, context.Background(), map[string]any{"until": "done", "timeout": 5000}, func() {
		f.show(label("Loading", 100), label("Step 1", 200))
		f.push("changed") // not settled yet: nothing is read
		f.push("settled")
		time.Sleep(20 * time.Millisecond)
		f.show(label("All Done", 100))
		f.push("settled")
	})
	if !resp.OK || resp.Text != "found done\n" {
		t.Fatalf("final %+v", resp)
	}
	if len(parts) != 3 {
		t.Fatalf("parts %+v", parts)
	}
	if parts[0].Text != "watching alice (Pixel 7) (5s)\n" {
		t.Errorf("first part %q", parts[0].Text)
	}
	if !strings.HasPrefix(parts[1].Text, "+") || !strings.HasSuffix(parts[1].Text, "\n+ @2 Text \"Step 1\"\n") {
		t.Errorf("second part %q", parts[1].Text)
	}
	if !strings.Contains(parts[2].Text, `Text "All Done"`) {
		t.Errorf("third part %q", parts[2].Text)
	}
}

// When the awaited element is already there, watch returns at once.
func TestWatchUntilAlreadyShown(t *testing.T) {
	s, _ := watchDev(t)
	resp, parts := watchWith(t, s, context.Background(), map[string]any{"until": "LOADING"}, nil)
	if !resp.OK || resp.Text != "found LOADING\n" || len(parts) != 1 {
		t.Errorf("final %+v parts %+v", resp, parts)
	}
	if !strings.HasSuffix(parts[0].Text, "(30s)\n") {
		t.Errorf("default timeout not 30 s: %q", parts[0].Text)
	}
}

// Without --until, watch ends quietly at the timeout; with it, it fails.
func TestWatchTimeout(t *testing.T) {
	s, _ := watchDev(t)
	resp, _ := watchWith(t, s, context.Background(), map[string]any{"timeout": 30}, nil)
	if !resp.OK || resp.Text != "watch ended\n" {
		t.Errorf("plain: %+v", resp)
	}
	resp, _ = watchWith(t, s, context.Background(), map[string]any{"timeout": 30, "until": "Done"}, nil)
	if resp.OK || resp.Exit != 1 || resp.Text != "\"Done\" did not appear within 30ms\n" {
		t.Errorf("until: %+v", resp)
	}
}

// A settled event with nothing new on screen streams nothing.
func TestWatchIgnoresUnchanged(t *testing.T) {
	s, f := watchDev(t)
	resp, parts := watchWith(t, s, context.Background(), map[string]any{"timeout": 200}, func() {
		f.push("settled")
		f.push("changed")
	})
	if resp.Text != "watch ended\n" || len(parts) != 1 {
		t.Errorf("final %+v parts %+v", resp, parts)
	}
}

// Losing the device ends the watch with exit 1, whether it says so or its events just stop.
func TestWatchLostDevice(t *testing.T) {
	s, f := watchDev(t)
	resp, _ := watchWith(t, s, context.Background(), map[string]any{"timeout": 5000}, func() { f.push("disconnected") })
	if resp.Exit != 1 || resp.Text != "lost the device" {
		t.Errorf("disconnected: %+v", resp)
	}
	resp, _ = watchWith(t, s, context.Background(), map[string]any{"timeout": 5000}, f.closeEvents)
	if resp.Exit != 1 || resp.Text != "lost the device" {
		t.Errorf("events closed: %+v", resp)
	}
}

// The caller going away ends the watch.
func TestWatchCancelled(t *testing.T) {
	s, _ := watchDev(t)
	ctx, cancel := context.WithCancel(context.Background())
	resp, _ := watchWith(t, s, ctx, map[string]any{"timeout": 5000}, cancel)
	if !resp.OK || resp.Text != "" {
		t.Errorf("final %+v", resp)
	}
}

// Screen read failures end the watch with exit 1, at the start or after a change.
func TestWatchScreenErrors(t *testing.T) {
	s, f := watchDev(t)
	f.screenErr = errBoom
	resp, parts := watchWith(t, s, context.Background(), map[string]any{"timeout": 5000}, nil)
	if resp.Exit != 1 || resp.Text != "boom" || len(parts) != 0 {
		t.Errorf("start: %+v parts %v", resp, parts)
	}
	f.get(func() { f.screenErr = nil })
	resp, _ = watchWith(t, s, context.Background(), map[string]any{"timeout": 5000}, func() {
		f.get(func() { f.screenErr = errBoom })
		f.push("settled")
	})
	if resp.Exit != 1 || resp.Text != "boom" {
		t.Errorf("later: %+v", resp)
	}
}

// watch needs a device; it leaves the latest screen as the one @refs resolve against.
func TestWatchDeviceAndLast(t *testing.T) {
	resp, _ := watchWith(t, New(), context.Background(), map[string]any{"device": "nobody"}, nil)
	if resp.Exit != 2 {
		t.Errorf("no device: %+v", resp)
	}
	s, f := watchDev(t)
	watchWith(t, s, context.Background(), map[string]any{"until": "Ready", "timeout": 5000}, func() {
		f.show(label("Ready", 100))
		f.push("settled")
	})
	d := s.devs["emulator-5554"]
	if d.last == nil || !hasText(d.last, "Ready") {
		t.Error("watch should leave the latest listing for refs")
	}
}

// hasText matches a label substring (any case) or an exact id (any case).
func TestHasText(t *testing.T) {
	l := &look.Listing{Elements: []*look.Element{{Label: "Sign in now"}, {ID: "submit_btn"}}}
	for text, want := range map[string]bool{
		"sign in": true, "NOW": true, "Sign in now": true, "submit_btn": true, "SUBMIT_BTN": true,
		"submit": false, "log out": false,
	} {
		if got := hasText(l, text); got != want {
			t.Errorf("hasText(%q) = %v, want %v", text, got, want)
		}
	}
	if hasText(&look.Listing{}, "x") {
		t.Error("empty listing")
	}
}
