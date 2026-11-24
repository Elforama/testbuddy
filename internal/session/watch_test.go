package session

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

type nodeCacheDriver struct {
	*fakeDriver
	cmu    sync.Mutex
	cached *ui.Screen
}

func (c *nodeCacheDriver) Screen(ctx context.Context) (*ui.Screen, error) {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	if c.cached == nil {
		s, err := c.fakeDriver.Screen(ctx)
		if err != nil {
			return nil, err
		}
		c.cached = s
	}
	return c.cached, nil
}

func (c *nodeCacheDriver) ScreenFresh(ctx context.Context) (*ui.Screen, error) {
	c.cmu.Lock()
	c.cached = nil
	c.cmu.Unlock()
	return c.Screen(ctx)
}

func TestWatchSilenceRereadReadsPastNodeCache(t *testing.T) {
	s, f := watchDev(t)
	s.devs["emulator-5554"].Drv = &nodeCacheDriver{fakeDriver: f}
	start := time.Now()
	resp, _ := watchWith(t, s, context.Background(), map[string]any{"until": "Silent count 1", "timeout": 4000}, func() {
		time.Sleep(100 * time.Millisecond)
		f.showSilently(label("Silent count 1", 100))
	})
	if !resp.OK || resp.Text != "found Silent count 1\n" {
		t.Fatalf("final %+v after %s", resp, time.Since(start).Round(time.Millisecond))
	}
}

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

func TestWatchStreamsChangesUntil(t *testing.T) {
	s, f := watchDev(t)
	resp, parts := watchWith(t, s, context.Background(), map[string]any{"until": "done", "timeout": 5000}, func() {
		f.show(label("Loading", 100), label("Step 1", 200))
		f.push("changed")
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
	for _, p := range parts {
		if strings.Contains(p.Text, "silence") {
			t.Errorf("a reported change is not a silent re-read: %q", p.Text)
		}
	}
}

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

func TestWatchCancelled(t *testing.T) {
	s, _ := watchDev(t)
	ctx, cancel := context.WithCancel(context.Background())
	resp, _ := watchWith(t, s, ctx, map[string]any{"timeout": 5000}, cancel)
	if !resp.OK || resp.Text != "" {
		t.Errorf("final %+v", resp)
	}
}

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
		f.show(label("Done", 100))
		f.push("settled")
	})
	if resp.Exit != 1 || resp.Text != "boom" {
		t.Errorf("later: %+v", resp)
	}
}

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

func TestWatchSilenceNet(t *testing.T) {
	const gap = 1500 * time.Millisecond
	for name, repeat := range map[string]string{
		"one stale settled":      "",
		"repeated stale settled": "settled",
		"repeated stale changed": "changed",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, f := watchDev(t)
			done := make(chan struct{})
			defer close(done)
			start := time.Now()
			resp, parts := watchWith(t, s, context.Background(), map[string]any{"until": "Login", "timeout": 8000}, func() {
				f.push("settled")
				time.Sleep(200 * time.Millisecond)
				f.showSilently(label("Login", 100))
				for repeat != "" {
					select {
					case <-done:
						return
					case <-time.After(100 * time.Millisecond):
						f.push(repeat)
					}
				}
			})
			took := time.Since(start)
			if !resp.OK || resp.Text != "found Login\n" {
				t.Fatalf("final %+v after %s", resp, took.Round(time.Millisecond))
			}
			if took > 2*gap+500*time.Millisecond {
				t.Errorf("found Login only after %s, want within about %s", took.Round(time.Millisecond), 2*gap)
			}
			if n := len(parts); n != 2 || !strings.Contains(strings.SplitN(parts[1].Text, "\n", 2)[0], " (re-read after 1.5s of silence)") ||
				!strings.Contains(parts[1].Text, `Text "Login"`) {
				t.Errorf("parts %+v", parts)
			}
		})
	}
}

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
