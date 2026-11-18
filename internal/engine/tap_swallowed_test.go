package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// Issue #280: a dialog that takes no touch while it comes in (a guard on its first second, an
// enter transition, Android's own permission dialogs). The first tap on its button, the first
// input its window ever got, changed nothing and no view took a click: it provably did nothing,
// and is sent again. Each case says what the first tap's reply was and how many taps go.

// swallowingDialog: an Android dialog whose "Allow" (a text in a Compose button: not clickable
// itself) answers only from the tap numbered answersFrom (from 1). first rewrites the first
// tap's reply (Settle NoChange and Clicked false before it).
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

// Issue #317: Android's runtime permission dialog (the permission controller's Activity, a
// clickable Button with its own view id) swallows a tap while it comes in. The agent says its
// window came over the app (windowOver) when it floats on the app's window: the tap is sent
// again, once, and the permission granted. An Activity's window not over the app (a new
// screen) keeps the one tap (issue #280).
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

// The status bar's clock ticking between the tap and the look is not the tap's doing (seen on an
// emulator: it turned a minute then). Anything else there is: a notification's icon in the bar
// (issue #280 review), a toast.
func TestOnlyTheClockDoesNotHoldBackTheReplay(t *testing.T) {
	t.Parallel()
	bar := ui.Rect{R: 400, B: 30}
	for _, c := range []struct {
		name  string
		after func(taps int64) []*ui.Node // what the bar lists once tapped
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

// Every case here gets exactly one tap.
func TestTapThatMayHaveActedIsNotSentAgain(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, yaml string
		first      func(driver.ActionResult) driver.ActionResult
		tweak      func(f *fakeDriver)
	}{
		// The window already took input (the app's own window, back after a modal closed):
		// a press that acts late and draws nothing would act twice.
		{name: "not the window's first input", yaml: "- tapOn: Allow",
			first: func(res driver.ActionResult) driver.ActionResult { res.WindowAge = 150 * time.Millisecond; return res }},
		// Issue #280 review: a new Activity, the app's screen, not a window over it: a press
		// there that acts late and draws nothing would act twice. (Compose's clickable reports
		// no click, so the click count cannot tell.)
		{name: "a new screen of the app", yaml: "- tapOn: Allow",
			first: func(res driver.ActionResult) driver.ActionResult {
				res.FirstInWindow, res.WindowAge = true, 150*time.Millisecond
				return res
			}},
		// A dialog's guard lasts about a second: one 2 s old is not coming in any more.
		{name: "a dialog past its guard", yaml: "- tapOn: Allow", first: firstIn(2 * time.Second)},
		// The flow turned the replay off.
		{name: "retryTapIfNoChange false", yaml: "- tapOn: {text: Allow, retryTapIfNoChange: false}",
			first: firstIn(150 * time.Millisecond)},
		// A window long on screen that never took input is not one coming in.
		{name: "an old window", yaml: "- tapOn: Allow", first: firstIn(30 * time.Second)},
		// The view reported its click after the reply: the tap acted.
		{name: "a click after the reply", yaml: "- tapOn: Allow", first: firstIn(150 * time.Millisecond),
			tweak: func(f *fakeDriver) {
				var once atomic.Bool
				f.onTap = func(x, y int) {
					if once.CompareAndSwap(false, true) {
						f.clicks.Add(1)
					}
				}
			}},
		// What the screen lists changed after the reply: the tap may have landed.
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

// A target that never answers is tapped at most twice more, and the step stands as before.
func TestSwallowedTapIsSentAtMostTwiceMore(t *testing.T) {
	t.Parallel()
	f := swallowingDialog(100, firstIn(150*time.Millisecond))
	mustPass(t, runWith(t, f, quick, "- tapOn: Allow"))
	if n := tapCount(f); n != 3 {
		t.Errorf("%d taps, want 3 (the first and two more)", n)
	}
}

// iOS: the same rule, fed by the agent's appActive: false (issue #290): a tap that went to an
// app still on its way to the foreground, on an element that is not a button, is sent again.
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
	// Without it, a text that changed nothing is tapped once, as before.
	g := swallowingDialog(2, func(res driver.ActionResult) driver.ActionResult { res.Clicked = nil; return res })
	g.plat = "ios"
	runWith(t, g, quick, "- tapOn: Allow")
	if n := tapCount(g); n != 1 {
		t.Errorf("not entering: %d taps, want 1", n)
	}
}

// iOS, a guarded tap (issue #280 review): its receipt says whether the touch reached the app.
// One that ended reached it, whatever appActive said: never sent again. Only one with no receipt
// ("none") is.
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

// iOS's own case (a button that shows every touch showed none: something hidden took it) is
// unchanged, and an explicit retryTapIfNoChange: false turns it off too.
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
			f.change(true) // the hidden veil went
		}()
		runWith(t, f, Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}, c.yaml)
		if n := tapCount(f); n != c.want {
			t.Errorf("%q: %d taps, want %d", c.yaml, n, c.want)
		}
	}
}

// probedApp makes f answer as the iOS agent with the probe does for a tap with Watch (issue
// #324): Reached when the touch went to the watched button's view, which veiled says it
// did not (something over the target took it). No Watch: no receipt, nothing reached.
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

// Issue #324: on iOS, a button whose press shows nothing accessibility reports (React Native's
// Button and TouchableOpacity animate their opacity) is tapped where tapping changes nothing
// (the option already selected: "Threshold: OFF" while it is off). The probe saw the touch go
// to the button's own view, so nothing hidden took it: the step ends at once, with one tap,
// instead of waiting the whole lookup timeout (17 s) for a veil to go.
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

// Issue #324: the veil's protection stays. A touch the probe saw go elsewhere (to a veil hidden
// from accessibility over the button) is tapped again once the veil goes, and that tap reaches
// the button, however long the veil stayed still.
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
		time.Sleep(2500 * time.Millisecond) // still for a while: no change at all until it goes
		veiled.Store(false)
		f.mu.Lock()
		f.tapSettle = driver.Changed
		f.mu.Unlock()
		f.change(true) // the veil went
	}()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"))
	if n := tapCount(f); n != 2 || reached.Load() != 1 {
		t.Errorf("%d taps, %d reached the button: want 2 taps, the second reaching it", n, reached.Load())
	}
}

// inCell is button under (depth 1) in a list cell (depth 0) with its bounds: a SwiftUI List's
// row, as the bench's long list has them. UIKit highlights a cell on every touch.
func inCell(under *ui.Node) func() []*ui.Node {
	under.Depth = 1
	cell := &ui.Node{Class: "Cell", Enabled: true, Bounds: under.Bounds}
	return static(cell, under)
}

// lists.yaml's flake, its recovery: the first touch on a button went to something else (in the
// bench, a pinned section header the cover rules did not know) and the screen then stayed
// perfectly still. tb said it would tap again "when the screen changes", no change ever came, and
// after the whole lookup timeout the step passed with that one lost tap. A still screen now gets
// a bounded number of taps again too, for a button in a list cell (it shows every touch): here
// the second reaches the button.
func TestIOSTapLostOnAStillScreenIsSentAgain(t *testing.T) {
	t.Parallel()
	under := &ui.Node{Text: "Under", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 0, T: 300, R: 400, B: 350}}
	var lost atomic.Int64
	lost.Store(1) // only the first touch goes astray
	f := &fakeDriver{plat: "ios", screen: inCell(under), tapSettle: driver.NoChange}
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

// ... and a tap step that never got a touch through to its button is not reported as passed:
// with every touch taken by something over it on a still screen, the step fails, saying so,
// after at most two more taps (a button in a list cell).
func TestIOSTapThatNeverGetsThroughIsNotPassedSilently(t *testing.T) {
	t.Parallel()
	under := &ui.Node{Text: "Under", Class: "Button", Enabled: true, Bounds: ui.Rect{L: 0, T: 300, R: 400, B: 350}}
	f := &fakeDriver{plat: "ios", screen: inCell(under), tapSettle: driver.NoChange}
	probedApp(f, func() bool { return true })
	mustFail(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second}, "- tapOn: Under"), "no tap reached")
	if n := tapCount(f); n != 3 {
		t.Errorf("%d taps, want 3: two more on a still screen", n)
	}
}

// A button whose touch tb may not see (no cell: a Flutter or custom-drawn button, a React
// Native press whose touch went to a hosting view) that changed nothing on a still screen may
// have had nothing to do: no tap again, and the step is not failed on that ground (as before).
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
