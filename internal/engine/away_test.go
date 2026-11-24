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

func switcherNodes() []*ui.Node {
	return []*ui.Node{
		{ID: "SBSwitcherWindow:Main", Class: "View", Depth: 0, Visible: true, Bounds: ui.Rect{R: 400, B: 800}},
		{ID: "AppSwitcherContentView", Class: "View", Depth: 1, Visible: true, Bounds: ui.Rect{R: 400, B: 800}},
		{Text: "Buddy Bench", ID: "card:app:sceneID:app-default", Class: "View", Depth: 2, Visible: true, Bounds: ui.Rect{L: 40, T: 100, R: 360, B: 700}},
	}
}

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

func after(f *fakeDriver, d time.Duration, fn func()) {
	time.AfterFunc(d, func() {
		fn()
		f.change(true)
	})
}

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

func TestAbsenceNeverPassesOnSpringBoardOutsideCI(t *testing.T) {
	t.Setenv("TB_CI", "0")
	f, front := awayFake([]*ui.Node{btn("Open", "open", 500), btn("Loading", "spinner", 100)}, switcherNodes)
	f.onTap = func(x, y int) {
		front.Store("com.apple.springboard")
		f.change(true)
	}
	res := runWith(t, f, Options{LookupTimeout: 12 * time.Second}, "- tapOn: Open\n- assertNotVisible: Loading\n")
	mustFail(t, res, `the app switcher (com.apple.springboard) is in front, not app: Could not check that "Loading" is not visible`)
	if took := res.Steps[len(res.Steps)-1].Duration; took > 9*time.Second {
		t.Errorf("the wait took %s: outside CI it should fail soon after the 5 s grace", took)
	}
}

func TestAwayGraceIsLongerInCI(t *testing.T) {
	for mode, want := range map[string]time.Duration{"0": 5 * time.Second, "1": 10 * time.Second} {
		t.Setenv("TB_CI", mode)
		if got := (&Runner{}).awayGrace(); got != want {
			t.Errorf("TB_CI=%s: grace %s, want %s", mode, got, want)
		}
	}
}

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

func TestAwayGuards(t *testing.T) {
	const sheet = "com.apple.springboard/sheet"
	alert := func(class string) []*ui.Node {
		return []*ui.Node{
			{Class: class, Text: "Allow “Buddy Bench” to use your location?", Depth: 0, Bounds: ui.Rect{L: 40, T: 300, R: 360, B: 500}},
			{Class: "Button", Text: "Allow Once", Depth: 1, Enabled: true, Clickable: true, Bounds: ui.Rect{L: 40, T: 440, R: 360, B: 480}},
		}
	}
	home := []*ui.Node{{ID: "SBHomeScreenWindow", Class: "View", Bounds: ui.Rect{R: 400, B: 800}}}
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
		front.Store("com.apple.springboard")
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
		front.Store("com.apple.springboard")
		f.change(true)
		after(f, 600*time.Millisecond, func() { shown.Store(true) })
		if err := batch("- assertVisible: Settings\n"); err != nil {
			t.Fatalf("the second batch failed: %v", err)
		}
	})
}

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
