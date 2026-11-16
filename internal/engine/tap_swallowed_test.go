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
		res.FirstInWindow, res.WindowAge = true, age
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

// The status bar's clock ticking between the tap and the look is not the tap's doing (seen on an
// emulator: it turned a minute then); a toast is.
func TestSystemBarsDoNotHoldBackTheReplay(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		bounds ui.Rect
		want   int
	}{{"the status bar", ui.Rect{R: 400, B: 30}, 2}, {"a toast", ui.Rect{L: 50, T: 600, R: 350, B: 660}, 1}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := swallowingDialog(2, firstIn(150*time.Millisecond))
			var minute atomic.Int64
			f.onTap = func(x, y int) { minute.Add(1) }
			f.appAbove = func() *ui.Window {
				clock := &ui.Node{Text: fmt.Sprintf("12:%02d", 40+minute.Load()), Enabled: true, Bounds: c.bounds}
				return &ui.Window{Type: "system", Package: "com.android.systemui", Bounds: c.bounds, Nodes: []*ui.Node{clock}}
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
