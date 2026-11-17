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

// nodeCacheFake is an Android device whose plain reads come from the agent's node cache: a
// view that changes its text without an accessibility event stays as it was first read until
// a fresh read (ScreenFresh) drops the cache.
type nodeCacheFake struct {
	*fakeDriver
	live    func() []*ui.Node // what the app shows
	mu      sync.Mutex
	cached  []*ui.Node
	fresh   atomic.Int64 // ScreenFresh calls
	kinds   []bool       // per read, in order: whether it was fresh
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
// asking every app again) is kept for the silence re-reads and a step's first read (issue
// #331), where the cache is in doubt.
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
	// The step's first read is fresh (issue #331); the reads that follow events are not.
	if n := c.fresh.Load(); n != 1 {
		t.Errorf("%d fresh reads with the device reporting the change, want 1 (the step's first)", n)
	}
	if n := c.reads.Load(); n < 2 {
		t.Errorf("%d reads: the event's read did not happen", n)
	}
}

// Issue #322 (review): scroll readiness's silence re-read (issue #291) reads past the node
// cache too. A list whose rows come with no event read as still empty in the cache, so the
// scroll judged it on a stale tree and swiped an empty list.
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
	c.onSwipe = func(x1, y1, x2, y2 int) {} // a drag on the empty list moves nothing
	go func() {
		time.Sleep(50 * time.Millisecond)
		drawn.Store(true) // the rows come with no event
	}()
	mustPass(t, runWith(t, c, quick, "- scrollUntilVisible: {element: \"Row 3\", timeout: 1500}"))
	if len(c.swipeAt) != 0 {
		t.Errorf("swiped %d times on a list judged from a stale tree", len(c.swipeAt))
	}
}

// Issue #331: a step's first read goes past the node cache too. The app changes a label with
// no accessibility event after the tap, before the step that checks it starts (the bench's
// "Silent text" screen). Read from the cache, the step's first read still shows the old text,
// and a verdict true on that read ends the step at once, wrongly: assertNotVisible of the new
// text passes, assertVisible of the old one passes, a when: condition on the old one holds,
// and assertVisible of the new one waits for a silence re-read.
func TestFirstReadOfStepReadsPastNodeCache(t *testing.T) {
	for _, tc := range []struct {
		step string
		want string // "" passes, else the failure
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
			c.onTap = func(x, y int) { changed.Store(true) } // the app sets the text: no event
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

// silentLabelFake is the bench's "Silent text" screen on the node-cache fake: a tap on Bump
// sets the label to "Silent count 1" with no event.
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

// Issue #331 (review): a repeat's while: is checked again after each pass, past the node
// cache. Read from the cache, the label the tap changed with no event still said "Silent
// count 0" on the second check, and the loop tapped again.
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

// Issue #331 (review): only a step whose verdict its first read can settle at once pays for a
// fresh first read. A tap's first read stays cached (its later reads follow events, and the
// silence re-reads are fresh); an assertion's is fresh; a when: condition's fresh read serves
// the first step inside it too, nothing having ended or been reported in between.
func TestFirstReadFreshOnlyForVerdicts(t *testing.T) {
	for _, tc := range []struct {
		step  string
		first bool  // the step's first read is fresh
		fresh int64 // fresh reads in all (-1: not checked; a tap checks that its target is at rest with one)
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
