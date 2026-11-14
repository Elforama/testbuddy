package engine

import (
	"sync/atomic"
	"testing"
	"time"

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
	// toSpringBoard: the action puts SpringBoard (or what) in front for 600 ms; the app is then
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
