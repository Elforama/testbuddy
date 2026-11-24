package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func swallowingDialog(answersFrom int, first func(driver.ActionResult) driver.ActionResult) *fakeDriver {
	allow := &ui.Node{Text: "Allow", Class: "TextView", Enabled: true, Bounds: ui.Rect{L: 300, T: 400, R: 360, B: 440}}
	given := &ui.Node{Text: "Consent: given", Class: "TextView", Enabled: true, Bounds: ui.Rect{L: 0, T: 100, R: 300, B: 140}}
	var answered atomic.Bool
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		if answered.Load() {
			return []*ui.Node{given}
		}
		return []*ui.Node{allow}
	}
	no := false
	f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
		if n >= answersFrom {
			answered.Store(true)
			f.change(true)
			res.Settle = driver.Changed
			return res
		}
		res.Settle, res.Clicked = driver.NoChange, &no
		if n == 1 {
			res = first(res)
		}
		return res
	}
	return f
}

func firstIn(age time.Duration) func(driver.ActionResult) driver.ActionResult {
	return func(res driver.ActionResult) driver.ActionResult {
		res.FirstInWindow, res.WindowAge, res.WindowOver = true, age, true
		return res
	}
}

func tapCount(f *fakeDriver) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.taps)
}

func TestSwallowedTapInANewWindowIsSentAgain(t *testing.T) {
	t.Parallel()
	f := swallowingDialog(2, firstIn(150*time.Millisecond))
	mustPass(t, runWith(t, f, quick, "- tapOn: Allow\n- assertVisible: \"Consent: given\""))
	if n := tapCount(f); n != 2 {
		t.Errorf("%d taps %v, want 2: the first, the first input to a dialog just added, provably did nothing", n, f.taps)
	}
}

func TestSwallowedTapOnThePermissionDialogIsSentAgain(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		over bool
		want int
	}{{"floating over the app", true, 2}, {"a new screen", false, 1}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			allow := &ui.Node{Text: "While using the app", Class: "Button", Clickable: true, Enabled: true,
				ID:     "com.android.permissioncontroller:id/permission_allow_foreground_only_button",
				Bounds: ui.Rect{L: 133, T: 1164, R: 947, B: 1311}}
			granted := &ui.Node{Text: "Camera: granted", Class: "TextView", Enabled: true, Bounds: ui.Rect{L: 42, T: 552, R: 1038, B: 609}}
			var answered atomic.Bool
			f := &fakeDriver{}
			f.screen = func() []*ui.Node {
				if answered.Load() {
					return []*ui.Node{granted}
				}
				return []*ui.Node{allow}
			}
			no := false
			f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
				if n >= 2 {
					answered.Store(true)
					f.change(true)
					res.Settle = driver.Changed
					return res
				}
				res.Settle, res.Clicked = driver.NoChange, &no
				res.FirstInWindow, res.WindowAge, res.WindowOver = true, 120*time.Millisecond, c.over
				return res
			}
			err := runWith(t, f, quick, "- tapOn: \"While using the app\"\n- assertVisible: \"Camera: granted\"")
			if n := tapCount(f); n != c.want {
				t.Errorf("%d taps %v, want %d", n, f.taps, c.want)
			}
			if c.over {
				mustPass(t, err)
			}
		})
	}
}

func TestOnlyTheClockDoesNotHoldBackTheReplay(t *testing.T) {
	t.Parallel()
	bar := ui.Rect{R: 400, B: 30}
	for _, c := range []struct {
		name  string
		after func(taps int64) []*ui.Node
		want  int
	}{
		{"the clock ticks", func(n int64) []*ui.Node {
			return []*ui.Node{{Text: fmt.Sprintf("12:%02d", 40+n), Desc: fmt.Sprintf("12:%02d\u202fPM", 40+n), Enabled: true, Bounds: ui.Rect{R: 60, B: 30}}}
		}, 2},
		{"the cell signal changes", func(n int64) []*ui.Node {
			return []*ui.Node{{Text: "12:40", Enabled: true, Bounds: ui.Rect{R: 60, B: 30}},
				{ID: "com.android.systemui:id/mobile_combo", Desc: "T-Mobile, signal full.", Enabled: true, Bounds: ui.Rect{L: 300, R: 330, B: 30}}}
		}, 2},
		{"a notification icon comes", func(n int64) []*ui.Node {
			return []*ui.Node{{Text: "12:40", Enabled: true, Bounds: ui.Rect{R: 60, B: 30}},
				{Desc: "Bench notification", Enabled: true, Bounds: ui.Rect{L: 70, R: 100, B: 30}}}
		}, 1},
		{"a toast comes", func(n int64) []*ui.Node {
			return []*ui.Node{{Text: "12:40", Enabled: true, Bounds: ui.Rect{R: 60, B: 30}},
				{Text: "Saved", Enabled: true, Bounds: ui.Rect{L: 50, T: 600, R: 350, B: 660}}}
		}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := swallowingDialog(2, firstIn(150*time.Millisecond))
			var taps atomic.Int64
			f.onTap = func(x, y int) { taps.Add(1) }
			f.appAbove = func() *ui.Window {
				nodes := []*ui.Node{{Text: "12:40", Enabled: true, Bounds: ui.Rect{R: 60, B: 30}},
					{ID: "com.android.systemui:id/mobile_combo", Desc: "T-Mobile, one bar.", Enabled: true, Bounds: ui.Rect{L: 300, R: 330, B: 30}}}
				if n := taps.Load(); n > 0 {
					nodes = c.after(n)
				}
				return &ui.Window{Type: "system", Package: "com.android.systemui", Bounds: bar, Nodes: nodes}
			}
			runWith(t, f, quick, "- tapOn: Allow")
			if n := tapCount(f); n != c.want {
				t.Errorf("%d taps, want %d", n, c.want)
			}
		})
	}
}

func TestTapThatMayHaveActedIsNotSentAgain(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, yaml string
		first      func(driver.ActionResult) driver.ActionResult
		tweak      func(f *fakeDriver)
	}{
		{name: "not the window's first input", yaml: "- tapOn: Allow",
			first: func(res driver.ActionResult) driver.ActionResult { res.WindowAge = 150 * time.Millisecond; return res }},
		{name: "a new screen of the app", yaml: "- tapOn: Allow",
			first: func(res driver.ActionResult) driver.ActionResult {
				res.FirstInWindow, res.WindowAge = true, 150*time.Millisecond
				return res
			}},
		{name: "a dialog past its guard", yaml: "- tapOn: Allow", first: firstIn(2 * time.Second)},
		{name: "retryTapIfNoChange false", yaml: "- tapOn: {text: Allow, retryTapIfNoChange: false}",
			first: firstIn(150 * time.Millisecond)},
		{name: "an old window", yaml: "- tapOn: Allow", first: firstIn(30 * time.Second)},
		{name: "a click after the reply", yaml: "- tapOn: Allow", first: firstIn(150 * time.Millisecond),
			tweak: func(f *fakeDriver) {
				var once atomic.Bool
				f.onTap = func(x, y int) {
					if once.CompareAndSwap(false, true) {
						f.clicks.Add(1)
					}
				}
			}},
		{name: "the screen changed", yaml: "- tapOn: Allow", first: firstIn(150 * time.Millisecond),
			tweak: func(f *fakeDriver) {
				screen := f.screen
				var tapped atomic.Bool
				f.onTap = func(x, y int) { tapped.Store(true) }
				f.screen = func() []*ui.Node {
					nodes := screen()
					if tapped.Load() {
						nodes = append(nodes, &ui.Node{Text: "Sending", Enabled: true, Bounds: ui.Rect{T: 600, R: 200, B: 640}})
					}
					return nodes
				}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := swallowingDialog(2, c.first)
			if c.tweak != nil {
				c.tweak(f)
			}
			runWith(t, f, quick, c.yaml)
			if n := tapCount(f); n != 1 {
				t.Errorf("%d taps %v, want 1", n, f.taps)
			}
		})
	}
}

func TestSwallowedTapIsSentAtMostTwiceMore(t *testing.T) {
	t.Parallel()
	f := swallowingDialog(100, firstIn(150*time.Millisecond))
	mustPass(t, runWith(t, f, quick, "- tapOn: Allow"))
	if n := tapCount(f); n != 3 {
		t.Errorf("%d taps, want 3 (the first and two more)", n)
	}
}

func TestIOSTapIntoAnAppStillEnteringIsSentAgain(t *testing.T) {
	t.Parallel()
	f := swallowingDialog(2, func(res driver.ActionResult) driver.ActionResult {
		res.Clicked, res.AppEntering = nil, true
		return res
	})
	f.plat = "ios"
	mustPass(t, runWith(t, f, quick, "- tapOn: Allow\n- assertVisible: \"Consent: given\""))
	if n := tapCount(f); n != 2 {
		t.Errorf("%d taps, want 2", n)
	}
	g := swallowingDialog(2, func(res driver.ActionResult) driver.ActionResult { res.Clicked = nil; return res })
	g.plat = "ios"
	runWith(t, g, quick, "- tapOn: Allow")
	if n := tapCount(g); n != 1 {
		t.Errorf("not entering: %d taps, want 1", n)
	}
}

func TestIOSGuardedTapIntoAnAppStillEntering(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		touch string
		want  int
	}{{"ended", 1}, {"none", 2}} {
		f := swallowingDialog(2, func(res driver.ActionResult) driver.ActionResult {
			res.Clicked, res.AppEntering, res.Guarded, res.Probed, res.Touch = nil, true, true, true, c.touch
			return res
		})
		f.plat = "ios"
		runWith(t, f, quick, "- tapOn: Allow")
		if n := tapCount(f); n != c.want {
			t.Errorf("touch %q: %d taps, want %d", c.touch, n, c.want)
		}
	}
}

func TestIOSHiddenSwallowerRuleIsKept(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		yaml string
		want int
	}{{"- tapOn: Under", 2}, {"- tapOn: {text: Under, retryTapIfNoChange: false}", 1}} {
		under := btn("Under", "under", 300)
		under.Class = "Button"
		f := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
		go func() {
			time.Sleep(300 * time.Millisecond)
			f.mu.Lock()
			f.tapSettle = driver.Changed
			f.mu.Unlock()
			f.change(true)
		}()
		runWith(t, f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}, c.yaml)
		if n := tapCount(f); n != c.want {
			t.Errorf("%q: %d taps, want %d", c.yaml, n, c.want)
		}
	}
}

func probedApp(f *fakeDriver, veiled func() bool) {
	f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
		f.mu.Lock()
		o, at := f.tapOpts[n-1], f.taps[n-1]
		f.mu.Unlock()
		if o.Watch != nil && o.Watch.Contains(at[0], at[1]) && !veiled() {
			res.Reached = true
		}
		return res
	}
}

func TestIOSNoOpTapThatReachedTheButtonIsNotWaitedOn(t *testing.T) {
	t.Parallel()
	off := &ui.Node{Text: "Threshold: OFF", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 16, T: 733, R: 155, B: 771}}
	other := &ui.Node{Text: "Threshold: 100", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 170, T: 733, R: 320, B: 771}}
	f := &fakeDriver{plat: "ios", screen: static(off, other), tapSettle: driver.NoChange}
	probedApp(f, func() bool { return false })
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 17 * time.Second, OptionalTimeout: 7 * time.Second},
		"- tapOn: \"Threshold: OFF\"\n- assertVisible: \"Threshold: 100\""))
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s: a tap that reached its button waited for a hidden veil", took.Round(time.Millisecond))
	}
	if n := tapCount(f); n != 1 {
		t.Errorf("%d taps, want 1", n)
	}
	if o := f.tapOpts[0]; o.Watch == nil || *o.Watch != off.Bounds {
		t.Errorf("the tap watched %v, want the button's bounds %v", o.Watch, off.Bounds)
	}
}

func TestIOSTouchThatWentToAHiddenVeilIsMadeAgain(t *testing.T) {
	t.Parallel()
	under := &ui.Node{Text: "Under", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 0, T: 300, R: 400, B: 350}}
	var veiled atomic.Bool
	veiled.Store(true)
	f := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
	probedApp(f, veiled.Load)
	var reached atomic.Int64
	f.onTap = func(x, y int) {
		if !veiled.Load() {
			reached.Add(1)
		}
	}
	go func() {
		time.Sleep(2500 * time.Millisecond)
		veiled.Store(false)
		f.mu.Lock()
		f.tapSettle = driver.Changed
		f.mu.Unlock()
		f.change(true)
	}()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if n := tapCount(f); n != 2 || reached.Load() != 1 {
		t.Errorf("%d taps, %d reached the button: want 2 taps, the second reaching it", n, reached.Load())
	}
}

func inCell(under *ui.Node, cellAt ui.Rect) func() []*ui.Node {
	list := &ui.Node{Class: "CollectionView", Scrollable: true, Enabled: true, Bounds: ui.Rect{R: 400, B: 800}}
	under.Depth = 2
	cell := &ui.Node{Class: "Cell", Depth: 1, Enabled: true, Bounds: cellAt}
	return static(list, cell, under)
}

func TestIOSTapLostOnAStillScreenIsSentAgain(t *testing.T) {
	t.Parallel()
	under := &ui.Node{Text: "Under", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 0, T: 300, R: 400, B: 350}}
	var lost atomic.Int64
	lost.Store(1)
	f := &fakeDriver{plat: "ios", screen: inCell(under, under.Bounds), tapSettle: driver.NoChange}
	probedApp(f, func() bool { return lost.Add(-1) >= 0 })
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 17 * time.Second, OptionalTimeout: 7 * time.Second}, "- tapOn: Under"))
	if n := tapCount(f); n != 2 {
		t.Errorf("%d taps, want 2: the second, on a still screen, reaching the button", n)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s: the tap again waited for a change that never comes", took.Round(time.Millisecond))
	}
}

func TestIOSTapThatNeverGetsThroughIsNotPassedSilently(t *testing.T) {
	t.Parallel()
	under := &ui.Node{Text: "Under", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 0, T: 300, R: 400, B: 350}}
	f := &fakeDriver{plat: "ios", screen: inCell(under, under.Bounds), tapSettle: driver.NoChange}
	probedApp(f, func() bool { return true })
	mustFail(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"), "no tap reached")
	if n := tapCount(f); n != 3 {
		t.Errorf("%d taps, want 3: two more on a still screen", n)
	}
}

func TestIOSStillTapWithNoTouchSignalIsNotSentAgainNorFailed(t *testing.T) {
	t.Parallel()
	under := &ui.Node{Text: "Under", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 0, T: 300, R: 400, B: 350}}
	f := &fakeDriver{plat: "ios", screen: static(under), tapSettle: driver.NoChange}
	probedApp(f, func() bool { return true })
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if n := tapCount(f); n != 1 {
		t.Errorf("%d taps, want 1: nothing known of its touch on a still screen", n)
	}
}

func TestIOSStillTapOnAButtonInsideARowIsNotSentAgainNorFailed(t *testing.T) {
	t.Parallel()
	under := &ui.Node{Text: "Under", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 300, T: 310, R: 380, B: 340}}
	f := &fakeDriver{plat: "ios", screen: inCell(under, ui.Rect{T: 300, R: 400, B: 350}), tapSettle: driver.NoChange}
	probedApp(f, func() bool { return true })
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if n := tapCount(f); n != 1 {
		t.Errorf("%d taps, want 1: a button inside a row does not show every touch", n)
	}
}
