package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// nodeCacheFake is an Android device whose plain reads come from the agent's node cache: a
// view that changes its text without an accessibility event stays as it was first read until
// a fresh read (ScreenFresh) drops the cache.
type nodeCacheFake struct {
	*fakeDriver
	live   func() []*ui.Node // what the app shows
	mu     sync.Mutex
	cached []*ui.Node
	fresh  atomic.Int64 // ScreenFresh calls
}

func newNodeCacheFake(live func() []*ui.Node) *nodeCacheFake {
	c := &nodeCacheFake{fakeDriver: &fakeDriver{}, live: live}
	c.fakeDriver.screen = func() []*ui.Node {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.cached == nil {
			c.cached = c.live()
		}
		return c.cached
	}
	return c
}

func (c *nodeCacheFake) ScreenFresh(ctx context.Context) (*ui.Screen, error) {
	c.fresh.Add(1)
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
	return c.fakeDriver.Screen(ctx)
}

// Issue #322: on Android, a view whose text changes with no accessibility event (React
// Native's new-architecture text, a custom-drawn label) stays stale in the agent's node cache.
// The silence re-read, the safety net for exactly a change the device did not report, must
// read past that cache: the lookup then finds the new text, and absence waits see the old
// one go. Before the fix every re-read asked the cache, and the wait ran to its timeout.
func TestSilenceRereadReadsPastNodeCache(t *testing.T) {
	for _, tc := range []struct{ step, gone string }{
		{"- assertVisible: text onPress", ""},
		{"- extendedWaitUntil: {visible: text onPress, timeout: 2000}", ""},
		{"- assertNotVisible: measure 0 by 0", "measure 0 by 0"},
		{"- extendedWaitUntil: {notVisible: measure 0 by 0, timeout: 2000}", "measure 0 by 0"},
	} {
		t.Run(tc.step[2:], func(t *testing.T) {
			t.Setenv("TB_CI", "0")
			defer func(d time.Duration) { silentAfter = d }(silentAfter)
			silentAfter = 100 * time.Millisecond
			var changed atomic.Bool
			c := newNodeCacheFake(func() []*ui.Node {
				console := ""
				if tc.gone != "" {
					console = tc.gone
				}
				if changed.Load() {
					console = "text onPress"
					if tc.gone != "" {
						console = "measure 411 by 190"
					}
				}
				return []*ui.Node{btn("Text has built-in onPress handling", "tappable_text", 0),
					{Text: console, ID: "tappable_text_console", Bounds: ui.Rect{T: 100, R: 400, B: 150}, Enabled: true}}
			})
			go func() {
				time.Sleep(50 * time.Millisecond)
				changed.Store(true) // the app sets the text: no event
			}()
			rec := &recorder{}
			o := quick
			o.Reporter = rec
			o.LookupTimeout = 2 * time.Second
			start := time.Now()
			mustPass(t, runWith(t, c, o, tc.step))
			if d := time.Since(start); d > 1200*time.Millisecond {
				t.Errorf("done after %s: the silence re-read did not see the change", d)
			}
			if c.fresh.Load() == 0 {
				t.Errorf("no fresh read")
			}
		})
	}
}

// Issue #322: reads while the device reports changes stay cached: a fresh read (the agent
// asking every app again) is kept for the silence re-reads, where the cache is in doubt.
func TestEventDrivenReadsKeepNodeCache(t *testing.T) {
	t.Setenv("TB_CI", "0")
	var shown atomic.Bool
	c := newNodeCacheFake(func() []*ui.Node {
		if shown.Load() {
			return []*ui.Node{btn("Login", "", 0)}
		}
		return []*ui.Node{btn("Loading", "", 0)}
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		shown.Store(true)
		c.mu.Lock()
		c.cached = nil // an event: the platform invalidates what changed
		c.mu.Unlock()
		c.change(true)
	}()
	mustPass(t, runWith(t, c, quick, "- assertVisible: Login"))
	if n := c.fresh.Load(); n != 0 {
		t.Errorf("%d fresh reads with the device reporting the change", n)
	}
}
