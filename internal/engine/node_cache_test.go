package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

type nodeCacheFake struct {
	*fakeDriver
	live    func() []*ui.Node
	mu      sync.Mutex
	cached  []*ui.Node
	fresh   atomic.Int64
	kinds   []bool
	inFresh bool
}

func newNodeCacheFake(live func() []*ui.Node) *nodeCacheFake {
	c := &nodeCacheFake{fakeDriver: &fakeDriver{}, live: live}
	c.fakeDriver.screen = func() []*ui.Node {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.kinds = append(c.kinds, c.inFresh)
		c.inFresh = false
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
	c.inFresh = true
	c.mu.Unlock()
	return c.fakeDriver.Screen(ctx)
}

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
				changed.Store(true)
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
		c.cached = nil
		c.mu.Unlock()
		c.change(true)
	}()
	mustPass(t, runWith(t, c, quick, "- assertVisible: Login"))
	if n := c.fresh.Load(); n != 1 {
		t.Errorf("%d fresh reads with the device reporting the change, want 1 (the step's first)", n)
	}
	if n := c.reads.Load(); n < 2 {
		t.Errorf("%d reads: the event's read did not happen", n)
	}
}

func TestScrollReadySilenceRereadReadsPastNodeCache(t *testing.T) {
	t.Setenv("TB_CI", "0")
	defer func(d time.Duration) { silentAfter = d }(silentAfter)
	silentAfter = 150 * time.Millisecond
	var drawn atomic.Bool
	c := newNodeCacheFake(func() []*ui.Node {
		nodes := []*ui.Node{{Class: "RecyclerView", ID: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		if drawn.Load() {
			for i := range 14 {
				nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Row %d", i), Depth: 1, Enabled: true, Clickable: true,
					Bounds: ui.Rect{T: 100 + 50*i, R: 400, B: 150 + 50*i}})
			}
		}
		return nodes
	})
	c.onSwipe = func(x1, y1, x2, y2 int) {}
	go func() {
		time.Sleep(50 * time.Millisecond)
		drawn.Store(true)
	}()
	mustPass(t, runWith(t, c, quick, "- scrollUntilVisible: {element: \"Row 3\", timeout: 1500}"))
	if len(c.swipeAt) != 0 {
		t.Errorf("swiped %d times on a list judged from a stale tree", len(c.swipeAt))
	}
}

func TestFirstReadOfStepReadsPastNodeCache(t *testing.T) {
	for _, tc := range []struct {
		step string
		want string
	}{
		{"- assertNotVisible: Silent count 1", "Silent count 1"},
		{"- assertVisible: Silent count 0", "Silent count 0"},
		{"- assertVisible: Silent count 1", ""},
		{"- runFlow:\n    when: {visible: Silent count 0}\n    commands:\n      - assertTrue: false", ""},
	} {
		t.Run(tc.step[2:20], func(t *testing.T) {
			t.Setenv("TB_CI", "0")
			var changed atomic.Bool
			c := newNodeCacheFake(func() []*ui.Node {
				label := "Silent count 0"
				if changed.Load() {
					label = "Silent count 1"
				}
				return []*ui.Node{btn("Bump", "silent_bump", 0),
					{Text: label, ID: "silent_label", Bounds: ui.Rect{T: 100, R: 400, B: 150}, Enabled: true}}
			})
			c.onTap = func(x, y int) { changed.Store(true) }
			start := time.Now()
			res := runWith(t, c, quick, "- assertVisible: Silent count 0\n- tapOn: Bump\n"+tc.step)
			if tc.want != "" {
				mustFail(t, res, tc.want)
				return
			}
			mustPass(t, res)
			if d := time.Since(start); d > time.Second {
				t.Errorf("done after %s: the step's first read did not see the change", d)
			}
		})
	}
}

func silentLabelFake() *nodeCacheFake {
	var changed atomic.Bool
	c := newNodeCacheFake(func() []*ui.Node {
		label := "Silent count 0"
		if changed.Load() {
			label = "Silent count 1"
		}
		return []*ui.Node{btn("Bump", "silent_bump", 0),
			{Text: label, ID: "silent_label", Bounds: ui.Rect{T: 100, R: 400, B: 150}, Enabled: true}}
	})
	c.onTap = func(x, y int) { changed.Store(true) }
	return c
}

func TestRepeatWhileRecheckReadsPastNodeCache(t *testing.T) {
	t.Setenv("TB_CI", "0")
	c := silentLabelFake()
	mustPass(t, runWith(t, c, quick, `- repeat:
    times: 4
    while: {notVisible: Silent count 1}
    commands:
      - tapOn: Bump`))
	if n := len(c.taps); n != 1 {
		t.Errorf("%d taps, want 1: the while: re-check read the stale cache", n)
	}
}

func TestFirstReadFreshOnlyForVerdicts(t *testing.T) {
	for _, tc := range []struct {
		step  string
		first bool
		fresh int64
	}{
		{"- tapOn: Bump", false, -1},
		{"- assertVisible: Bump", true, 1},
		{"- copyTextFrom: {id: silent_label}", true, 1},
		{"- runFlow:\n    when: {visible: Bump}\n    commands:\n      - assertVisible: Silent count 0", true, 1},
	} {
		t.Run(tc.step[2:min(len(tc.step), 16)], func(t *testing.T) {
			t.Setenv("TB_CI", "0")
			c := silentLabelFake()
			c.onTap = nil
			mustPass(t, runWith(t, c, quick, tc.step))
			c.mu.Lock()
			kinds := c.kinds
			c.mu.Unlock()
			if len(kinds) == 0 || kinds[0] != tc.first {
				t.Errorf("reads (fresh or not) %v: want the first fresh: %v", kinds, tc.first)
			}
			if n := c.fresh.Load(); tc.fresh >= 0 && n != tc.fresh {
				t.Errorf("%d fresh reads, want %d", n, tc.fresh)
			}
		})
	}
}
