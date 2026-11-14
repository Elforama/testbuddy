package engine

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// switcherNodes is what the iOS agent reported in issue #142: SpringBoard's app switcher, with
// the flow's app as a card.
func switcherNodes() []*ui.Node {
	return []*ui.Node{
		{ID: "SBSwitcherWindow:Main", Class: "View", Depth: 0, Visible: true, Bounds: ui.Rect{R: 400, B: 800}},
		{ID: "AppSwitcherContentView", Class: "View", Depth: 1, Visible: true, Bounds: ui.Rect{R: 400, B: 800}},
		{Text: "Buddy Bench", ID: "card:app:sceneID:app-default", Class: "View", Depth: 2, Visible: true, Bounds: ui.Rect{L: 40, T: 100, R: 360, B: 700}},
	}
}

// awayFake is an iOS device whose foreground app the test switches: front holds the bundle
// id in front, and the screen shows the app's own nodes, or other's for any other app.
func awayFake(app []*ui.Node, other func() []*ui.Node) (*fakeDriver, *atomic.Value) {
	var front atomic.Value
	front.Store("app")
	f := &fakeDriver{plat: "ios"}
	f.pkg = func() string { return front.Load().(string) }
	f.screen = func() []*ui.Node {
		if front.Load().(string) == "app" {
			return app
		}
		return other()
	}
	return f, &front
}

// after makes fn the device's next change, d from now.
func after(f *fakeDriver, d time.Duration, fn func()) {
	time.AfterFunc(d, func() {
		fn()
		f.change(true)
	})
}

// Issue #142: after a tap, SpringBoard's app switcher was in front instead of the flow's app,
// and the next assertVisible waited out its whole timeout (28.6 s) on it without saying so. A
// wait that finds SpringBoard alone in front, with no alert of its own, fails once that has
// lasted a short grace, and names what is in front.
func TestAssertFailsPromptlyWhenSpringBoardIsInFront(t *testing.T) {
	t.Setenv("TB_CI", "0")
	f, front := awayFake([]*ui.Node{btn("Slow list", "slow_list_open", 500)}, switcherNodes)
	f.onTap = func(x, y int) {
		front.Store("com.apple.springboard")
		f.change(true)
	}
	start := time.Now()
	res := runWith(t, f, Options{LookupTimeout: 12 * time.Second}, `
- tapOn:
    id: slow_list_open
- assertVisible: "Slow item 1"
`)
	took := time.Since(start)
	mustFail(t, res, "the app switcher (com.apple.springboard) is in front, not app")
	if took > 9*time.Second {
		t.Fatalf("the assertion took %s: it should fail soon after SpringBoard came in front, not wait out its 12 s", took.Round(time.Millisecond))
	}
}

// Issue #142: an app that comes back within the grace is waited for, and found.
func TestAnAppBackWithinTheGraceIsFound(t *testing.T) {
	var items atomic.Bool
	f, front := awayFake(nil, switcherNodes)
	f.screen = func() []*ui.Node {
		switch {
		case front.Load().(string) != "app":
			return switcherNodes()
		case items.Load():
			return []*ui.Node{btn("Slow item 1", "slow_item_1", 100)}
		}
		return []*ui.Node{btn("Slow list", "slow_list_open", 500)}
	}
	f.onTap = func(x, y int) {
		front.Store("com.apple.springboard")
		f.change(true)
		after(f, 300*time.Millisecond, func() { items.Store(true); front.Store("app") })
	}
	mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, AwayGrace: time.Second}, `
- tapOn:
    id: slow_list_open
- assertVisible: "Slow item 1"
`))
}

// Guards for issue #142: what is in front of the flow's app on purpose, or as the system's own
// part of a flow, is waited on as before, however short the grace.
func TestAwayGuards(t *testing.T) {
	const sheet = "com.apple.springboard/sheet" // SpringBoard alone, showing a sheet
	alert := func(class string) []*ui.Node {
		return []*ui.Node{
			{Class: class, Text: "Allow “Buddy Bench” to use your location?", Depth: 0, Bounds: ui.Rect{L: 40, T: 300, R: 360, B: 500}},
			{Class: "Button", Text: "Allow Once", Depth: 1, Enabled: true, Clickable: true, Bounds: ui.Rect{L: 40, T: 440, R: 360, B: 480}},
		}
	}
	home := []*ui.Node{{ID: "SBHomeScreenWindow", Class: "View", Bounds: ui.Rect{R: 400, B: 800}}}
	// Each case's action puts SpringBoard (or another app) in front for 600 ms; the app is then
	// back, showing "Allowed".
	type device struct {
		f     *fakeDriver
		front *atomic.Value
		shown *atomic.Bool
	}
	cases := []struct {
		name string
		set  func(d device)
		src  string
	}{
		{"a permission alert over the app", func(d device) {
			var up atomic.Bool
			d.f.sysAlert = func() []*ui.Node {
				if up.Load() {
					return alert("Alert")
				}
				return nil
			}
			d.f.onTap = func(x, y int) {
				up.Store(true)
				d.f.change(true)
				after(d.f, 600*time.Millisecond, func() { up.Store(false); d.shown.Store(true) })
			}
		}, "- tapOn: Ask\n- assertVisible: Allowed\n"},
		{"SpringBoard alone with its permission alert", func(d device) {
			d.f.onTap = func(x, y int) {
				d.front.Store("com.apple.springboard")
				d.f.change(true)
				after(d.f, 600*time.Millisecond, func() { d.front.Store("app"); d.shown.Store(true) })
			}
		}, "- tapOn: Ask\n- assertVisible: Allowed\n"},
		{"SpringBoard alone with a system sheet", func(d device) {
			d.f.onTap = func(x, y int) {
				d.front.Store(sheet)
				d.f.change(true)
				after(d.f, 600*time.Millisecond, func() { d.front.Store("app"); d.shown.Store(true) })
			}
		}, "- tapOn: Ask\n- assertVisible: Allowed\n"},
		{"openLink to another app, through SpringBoard", func(d device) {
			d.f.onLink = func(string) {
				d.front.Store("com.apple.springboard")
				d.f.change(true)
				after(d.f, 300*time.Millisecond, func() { d.front.Store("com.apple.mobilesafari") })
				after(d.f, 600*time.Millisecond, func() { d.shown.Store(true) })
			}
		}, "- assertVisible: Ask\n- openLink: https://example.com\n- assertVisible: Allowed\n"},
		{"pressKey home, then a wait on the home screen", func(d device) {
			d.f.onPress = func(string) {
				d.front.Store("com.apple.springboard")
				d.f.change(true)
				after(d.f, 600*time.Millisecond, func() { d.shown.Store(true) })
			}
		}, "- assertVisible: Ask\n- pressKey: home\n- assertVisible: Allowed\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var front atomic.Value
			front.Store("app")
			var shown atomic.Bool
			f := &fakeDriver{plat: "ios"}
			f.pkg = func() string {
				if p := front.Load().(string); p != sheet {
					return p
				}
				return "com.apple.springboard"
			}
			alertUp := tc.name == "SpringBoard alone with its permission alert"
			f.screen = func() []*ui.Node {
				p := front.Load().(string)
				switch {
				case shown.Load():
					return []*ui.Node{btn("Allowed", "status", 100)}
				case p == sheet:
					return alert("Sheet")
				case p == "com.apple.springboard" && alertUp:
					return alert("Alert")
				case p == "com.apple.springboard":
					return home
				}
				return []*ui.Node{btn("Ask", "ask", 500)}
			}
			tc.set(device{f, &front, &shown})
			mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, AwayGrace: 100 * time.Millisecond}, tc.src))
		})
	}
}

// Issue #142's own reads: the two that showed SpringBoard took 5.4 and 9.9 s, while the agent
// failed to list the active apps (an accessibility timeout): a starved simulator can be misread
// as SpringBoard alone. Slow reads do not fail the step early: it waits its full time and finds
// the app if it is there after all; if not, its message says what the reads showed, and that
// they were slow.
func TestSlowReadsOfSpringBoardDoNotFailEarly(t *testing.T) {
	defer func(v time.Duration) { awayRead = v }(awayRead)
	awayRead = 50 * time.Millisecond
	for _, back := range []bool{true, false} {
		var items atomic.Bool
		f, front := awayFake(nil, switcherNodes)
		f.screen = func() []*ui.Node {
			switch {
			case front.Load().(string) != "app":
				return switcherNodes()
			case items.Load():
				return []*ui.Node{btn("Slow item 1", "slow_item_1", 100)}
			}
			return []*ui.Node{btn("Slow list", "slow_list_open", 500)}
		}
		f.onTap = func(x, y int) {
			front.Store("com.apple.springboard")
			f.change(true)
			if back {
				after(f, 900*time.Millisecond, func() { items.Store(true); front.Store("app") })
			}
		}
		d := &stallDriver{fakeDriver: f, all: true, hold: 80 * time.Millisecond}
		res := runWith(t, d, Options{LookupTimeout: 1500 * time.Millisecond, AwayGrace: 100 * time.Millisecond, StallFloor: time.Hour}, `
- tapOn:
    id: slow_list_open
- assertVisible: "Slow item 1"
`)
		if back {
			mustPass(t, res)
			continue
		}
		mustFail(t, res, "the app switcher (com.apple.springboard) is in front, not app (as read while the device was slow to answer")
		if d := res.Steps[len(res.Steps)-1].Duration; d < 1400*time.Millisecond {
			t.Fatalf("the assertion failed after %s: slow reads should not fail it early", d)
		}
	}
}

// Issue #142, the other side: SpringBoard in front shows none of the flow's app's elements, so
// a wait for one to be gone (assertNotVisible, extendedWaitUntil notVisible) passed on it at
// once. It is never taken for their absence: the step fails saying what is in front (after the
// grace; at its deadline when the reads were slow), as does a wait for one to be visible.
func TestWaitsDoNotPassOnSpringBoard(t *testing.T) {
	spinner := []*ui.Node{btn("Open", "open", 500), btn("Loading", "spinner", 100)}
	cases := []struct {
		name, src, want string
		slow            bool
	}{
		{"assertNotVisible", `- assertNotVisible: Loading`,
			`the app switcher (com.apple.springboard) is in front, not app: Could not check that "Loading" is not visible`, false},
		{"assertNotVisible, slow reads", `- assertNotVisible: Loading`,
			`the app switcher (com.apple.springboard) is in front, not app (as read while the device was slow to answer`, true},
		{"extendedWaitUntil notVisible", "- extendedWaitUntil:\n    notVisible: Loading\n    timeout: 3000",
			`the app switcher (com.apple.springboard) is in front, not app: Assertion is false: "Loading" is not visible`, false},
		{"extendedWaitUntil notVisible, slow reads", "- extendedWaitUntil:\n    notVisible: Loading\n    timeout: 1500",
			`the app switcher (com.apple.springboard) is in front, not app (as read while the device was slow to answer`, true},
		{"extendedWaitUntil visible", "- extendedWaitUntil:\n    visible: Done\n    timeout: 3000",
			`the app switcher (com.apple.springboard) is in front, not app: Assertion is false: "Done" is visible`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func(v time.Duration) { awayRead = v }(awayRead)
			f, front := awayFake(spinner, switcherNodes)
			f.onTap = func(x, y int) {
				front.Store("com.apple.springboard")
				f.change(true)
			}
			var d driver.Driver = f
			lookup := 3 * time.Second
			if tc.slow {
				awayRead = 50 * time.Millisecond
				d = &stallDriver{fakeDriver: f, all: true, hold: 80 * time.Millisecond}
				lookup = 1500 * time.Millisecond
			}
			res := runWith(t, d, Options{LookupTimeout: lookup, AwayGrace: 200 * time.Millisecond, StallFloor: time.Hour},
				"- tapOn: Open\n"+tc.src+"\n")
			mustFail(t, res, tc.want)
			took := res.Steps[len(res.Steps)-1].Duration
			if !tc.slow && took > 2*time.Second {
				t.Fatalf("the wait took %s: it should fail soon after the grace", took)
			}
			if tc.slow && took < 1400*time.Millisecond {
				t.Fatalf("the wait failed after %s: slow reads should not fail it early", took)
			}
		})
	}
}

// Guards for the waits for absence (issue #142): the app back without the element within the
// grace passes; so does a flow that went home itself, and a `when` that SpringBoard in front
// for a moment leaves unmet.
func TestWaitsForAbsenceStillPass(t *testing.T) {
	t.Run("the app back without the element", func(t *testing.T) {
		var done atomic.Bool
		f, front := awayFake(nil, switcherNodes)
		f.screen = func() []*ui.Node {
			switch {
			case front.Load().(string) != "app":
				return switcherNodes()
			case done.Load():
				return []*ui.Node{btn("Done", "done", 100)}
			}
			return []*ui.Node{btn("Open", "open", 500), btn("Loading", "spinner", 100)}
		}
		f.onTap = func(x, y int) {
			front.Store("com.apple.springboard")
			f.change(true)
			after(f, 400*time.Millisecond, func() { done.Store(true); front.Store("app") })
		}
		mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, AwayGrace: time.Second},
			"- tapOn: Open\n- assertNotVisible: Loading\n"))
	})
	t.Run("pressKey home, then assertNotVisible", func(t *testing.T) {
		f, front := awayFake([]*ui.Node{btn("Loading", "spinner", 100)}, func() []*ui.Node {
			return []*ui.Node{{ID: "SBHomeScreenWindow", Class: "View", Bounds: ui.Rect{R: 400, B: 800}}}
		})
		f.onPress = func(string) {
			front.Store("com.apple.springboard")
			f.change(true)
		}
		mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, AwayGrace: 100 * time.Millisecond},
			"- assertVisible: Loading\n- pressKey: home\n- assertNotVisible: Loading\n"))
	})
	t.Run("a when left unmet by SpringBoard for a moment", func(t *testing.T) {
		f, front := awayFake([]*ui.Node{btn("Open", "open", 500), btn("Loading", "spinner", 100)}, switcherNodes)
		f.onTap = func(x, y int) {
			front.Store("com.apple.springboard")
			f.change(true)
			after(f, 300*time.Millisecond, func() { front.Store("app") })
		}
		res := runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: 100 * time.Millisecond, AwayGrace: time.Second}, `
- tapOn: Open
- runFlow:
    when:
      notVisible: Loading
    commands:
      - tapOn: Missing
- assertVisible: Loading
`)
		mustPass(t, res)
	})
}

// Review of #142: a when/while condition never fails because SpringBoard is in front, however
// long it stays (the grace, 5 s locally, is shorter than a condition's 7 s; locally it failed
// the flow, in CI it skipped the block): the condition is not met, its block skipped. Only
// assertions fail.
func TestConditionsAreNotMetOnSpringBoardNeverFailed(t *testing.T) {
	f, front := awayFake([]*ui.Node{btn("Open", "open", 500)}, switcherNodes)
	f.onTap = func(x, y int) {
		front.Store("com.apple.springboard")
		f.change(true)
	}
	res := runWith(t, f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second, AwayGrace: 100 * time.Millisecond}, `
- tapOn: Open
- runFlow:
    when:
      visible: Done
    commands:
      - tapOn: Missing
- runFlow:
    when:
      notVisible: Loading
    commands:
      - tapOn: Missing
`)
	mustPass(t, res)
	if got := stepStatuses(res); len(got) != 3 || got[1] != Skipped || got[2] != Skipped {
		t.Fatalf("steps %v: both blocks should be skipped", got)
	}
}

// Review of #142: the Runner is kept across flows, and across a session's command batches.
// What one saw in front is not the next one's: a flow, or a batch after a person pressed home,
// that starts on SpringBoard and waits there is not failed for it.
func TestTheAppInFrontIsForgottenBetweenFlowsAndBatches(t *testing.T) {
	var shown atomic.Bool
	home := func() []*ui.Node {
		n := []*ui.Node{{ID: "Home screen icons", Class: "View", Bounds: ui.Rect{R: 400, B: 800}}}
		if shown.Load() {
			n = append(n, btn("Settings", "Settings", 100))
		}
		return n
	}
	o := Options{LookupTimeout: 3 * time.Second, AwayGrace: 100 * time.Millisecond}

	t.Run("flows", func(t *testing.T) {
		shown.Store(false)
		f, front := awayFake([]*ui.Node{btn("Open", "open", 500)}, home)
		r := New(f, o)
		parse := func(src string) *flow.Flow {
			fl, err := flow.ParseBytes(filepath.Join(t.TempDir(), "flow.yaml"), []byte("appId: app\n---\n"+src))
			if err != nil {
				t.Fatal(err)
			}
			return fl
		}
		mustPass(t, r.RunFlow(context.Background(), parse("- assertVisible: Open\n")))
		front.Store("com.apple.springboard") // flow A's app left: flow B starts on the home screen
		f.change(true)
		after(f, 600*time.Millisecond, func() { shown.Store(true) })
		mustPass(t, r.RunFlow(context.Background(), parse("- assertVisible: Settings\n")))
	})
	t.Run("session batches", func(t *testing.T) {
		shown.Store(false)
		f, front := awayFake([]*ui.Node{btn("Open", "open", 500)}, home)
		r := New(f, o)
		batch := func(src string) error {
			cmds, err := flow.ParseCommands(".", []byte(src))
			if err != nil {
				t.Fatal(err)
			}
			return r.RunCommands(context.Background(), cmds, "app", nil)
		}
		if err := batch("- assertVisible: Open\n"); err != nil {
			t.Fatal(err)
		}
		front.Store("com.apple.springboard") // a person pressed home between the batches
		f.change(true)
		after(f, 600*time.Millisecond, func() { shown.Store(true) })
		if err := batch("- assertVisible: Settings\n"); err != nil {
			t.Fatalf("the second batch failed: %v", err)
		}
	})
}

// contactsPicker is SpringBoard's tree while iOS's contacts access picker is up, as read on an
// iOS 27 simulator (bench permissions scenario): the picker is inside the app's card in
// SpringBoard's switcher.
func contactsPicker() []*ui.Node {
	return []*ui.Node{
		{ID: "SBSwitcherWindow:Main", Class: "View", Depth: 0, Bounds: ui.Rect{R: 400, B: 800}},
		{ID: "AppSwitcherContentView", Class: "View", Depth: 1, Bounds: ui.Rect{R: 400, B: 800}},
		{Text: "Buddy Bench", ID: "card:app:sceneID:app-default", Class: "View", Depth: 2, Bounds: ui.Rect{R: 400, B: 800}},
		{Class: "ScrollView", Depth: 3, Bounds: ui.Rect{T: 100, R: 400, B: 700}},
		{Text: "You have 6 contacts.", Class: "StaticText", Depth: 4, Bounds: ui.Rect{L: 20, T: 400, R: 380, B: 420}},
		{Text: "How do you want to share contacts?", Class: "StaticText", Depth: 4, Bounds: ui.Rect{L: 20, T: 450, R: 380, B: 470}},
		{Text: "Select Contacts", Class: "Button", Depth: 3, Clickable: true, Enabled: true, Bounds: ui.Rect{L: 20, T: 700, R: 380, B: 740}},
		{Text: "Share All 6 Contacts", Class: "Button", Depth: 3, Clickable: true, Enabled: true, Bounds: ui.Rect{L: 20, T: 750, R: 380, B: 790}},
	}
}

// Review of #142: only screens tb knows count as the app gone (the home screen, the app
// switcher with bare cards). SpringBoard's other trees, such as the contacts access picker it
// shows over the app, are not judged: a wait on the app is not failed early on them, and a
// wait for an element to go does not pass on them either.
func TestSystemUIOverTheAppIsNotJudged(t *testing.T) {
	t.Run("a wait on the app outlasts the grace", func(t *testing.T) {
		var granted atomic.Bool
		f, front := awayFake(nil, contactsPicker)
		f.screen = func() []*ui.Node {
			if front.Load().(string) != "app" {
				return contactsPicker()
			}
			if granted.Load() {
				return []*ui.Node{btn("Contacts: granted", "status", 100)}
			}
			return []*ui.Node{btn("Ask contacts", "ask", 500)}
		}
		f.onTap = func(x, y int) {
			front.Store("com.apple.springboard")
			f.change(true)
			after(f, 800*time.Millisecond, func() { granted.Store(true); front.Store("app") })
		}
		mustPass(t, runWith(t, f, Options{LookupTimeout: 3 * time.Second, AwayGrace: 100 * time.Millisecond},
			"- tapOn: Ask contacts\n- assertVisible: \"Contacts: granted\"\n"))
	})
	t.Run("a wait for absence does not pass on it", func(t *testing.T) {
		f, front := awayFake([]*ui.Node{btn("Ask contacts", "ask", 500), btn("Asking", "asking", 100)}, contactsPicker)
		f.onTap = func(x, y int) {
			front.Store("com.apple.springboard")
			f.change(true)
		}
		res := runWith(t, f, Options{LookupTimeout: time.Second, AwayGrace: 100 * time.Millisecond},
			"- tapOn: Ask contacts\n- assertNotVisible: Asking\n")
		mustFail(t, res, `SpringBoard (com.apple.springboard) is in front, not app: Could not check that "Asking" is not visible`)
		if d := res.Steps[len(res.Steps)-1].Duration; d < 900*time.Millisecond {
			t.Fatalf("failed after %s: SpringBoard's own UI must not fail the step early", d)
		}
	})
}
