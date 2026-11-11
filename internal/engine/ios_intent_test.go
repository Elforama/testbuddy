package engine

import (
	"context"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

type editIntentDriver struct {
	*fakeDriver
	eraseTarget *driver.ActionTarget
	typeTarget  *driver.ActionTarget
}

func (d *editIntentDriver) EraseText(ctx context.Context, n int, opts driver.ActionOpts) (driver.ActionResult, error) {
	d.eraseTarget = opts.Target
	return d.fakeDriver.EraseText(ctx, n, opts)
}

func (d *editIntentDriver) TypeText(ctx context.Context, text string, opts driver.ActionOpts) (driver.ActionResult, error) {
	d.typeTarget = opts.Target
	return d.fakeDriver.TypeText(ctx, text, opts)
}

func TestIOSEraseAndTypeKeepTheRequestedField(t *testing.T) {
	d := &editIntentDriver{fakeDriver: &fakeDriver{plat: "ios"}}
	d.screen = func() []*ui.Node {
		return []*ui.Node{{
			ID: "amount", Class: "TextField", Editable: true, Enabled: true,
			Bounds: ui.Rect{T: 100, R: 100, B: 140},
		}}
	}
	cmds, err := flow.ParseCommands(".", []byte("- tapOn: {id: amount}\n- eraseText: 5\n- inputText: '40'"))
	if err != nil {
		t.Fatal(err)
	}
	if err = New(d, Options{}).RunCommands(context.Background(), cmds, "app", nil); err != nil {
		t.Fatal(err)
	}
	if d.eraseTarget == nil || d.eraseTarget.ID != "amount" || d.typeTarget != d.eraseTarget {
		t.Fatalf("erase target %+v, type target %+v", d.eraseTarget, d.typeTarget)
	}
}

type guardedButtonDriver struct {
	*fakeDriver
	rejected int
	attempts []int
	clicked  bool
}

func (d *guardedButtonDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	d.attempts = append(d.attempts, y)
	if o.Target == nil || o.Target.ID != "button" {
		return d.fakeDriver.Tap(ctx, x, y, o)
	}
	if len(d.attempts) <= d.rejected {
		d.change(true)
		return driver.ActionResult{Withheld: "delivery", Guarded: true}, nil
	}
	out := driver.ActionResult{Settle: driver.NoChange, Guarded: true}
	if d.clicked {
		out.Clicked = &d.clicked
	}
	return out, nil
}

// A proven non-delivery may be re-aimed once. An ambiguous completed gesture
// cannot take the implicit swallowed-tap retry, and a confirmed action cannot
// take even the flow's no-change retry. A touch withheld because its point was off the target
// is re-aimed in and out of CI mode alike (only one withheld for a target that moved after the
// agent read it is a CI allowance: TestIOSTapOnATargetThatNeverStopsMoving).
func TestGuardedIOSTapReplay(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			testGuardedIOSTapReplay(t)
		})
	}
}

func testGuardedIOSTapReplay(t *testing.T) {
	for _, tc := range []struct {
		name              string
		reject            int
		clicked, explicit bool
		want              int
		fail              bool
	}{
		{"withheld once", 1, false, false, 2, false}, {"withheld twice", 2, false, false, 2, true},
		{"ambiguous", 0, false, false, 1, false}, {"confirmed", 0, true, true, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &guardedButtonDriver{fakeDriver: &fakeDriver{plat: "ios"}, rejected: tc.reject, clicked: tc.clicked}
			d.screen = func() []*ui.Node {
				top := 100
				if len(d.attempts) > 0 && tc.reject > 0 {
					top = 300
				}
				return []*ui.Node{{ID: "button", Class: "Button", Text: "Button", Enabled: true, Clickable: true, Bounds: ui.Rect{T: top, R: 100, B: top + 40}}}
			}
			yaml := "- tapOn: {id: button}"
			if tc.explicit {
				yaml = "- tapOn: {id: button, retryTapIfNoChange: true}"
			}
			cmds, err := flow.ParseCommands(".", []byte(yaml))
			if err != nil {
				t.Fatal(err)
			}
			err = New(d, Options{}).RunCommands(context.Background(), cmds, "app", nil)
			if (err != nil) != tc.fail || len(d.attempts) != tc.want {
				t.Fatalf("err=%v attempts=%v", err, d.attempts)
			}
			if tc.reject > 0 && (d.attempts[0] != 120 || d.attempts[1] != 320) {
				t.Fatalf("stale point reused: %v", d.attempts)
			}
		})
	}
}

// tickerDriver: a button that never stops moving. The probe withholds every touch that must find
// it where the agent read it (issue #103), and takes one that only needs the point on it.
type tickerDriver struct {
	*fakeDriver
	moving []bool // Target.Moving of each tap
}

func (d *tickerDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target == nil {
		return d.fakeDriver.Tap(ctx, x, y, o)
	}
	d.moving = append(d.moving, o.Target.Moving)
	if !o.Target.Moving {
		return driver.ActionResult{Withheld: "moved", Guarded: true, Probed: true}, nil
	}
	return driver.ActionResult{Guarded: true, Probed: true, Targeted: true, Settle: driver.Changed}, nil
}

// Issue #103: the first touch must find the target still. In CI mode, after one withheld for
// that, the target is found again (once at rest, or after restWait), and that touch only needs
// the point on it, so a ticker stays tappable. Outside CI there is no second touch: the step
// fails and says the target was moving, never passing on a touch that did not reach the app.
func TestIOSTapOnATargetThatNeverStopsMoving(t *testing.T) {
	run := func(t *testing.T) (*tickerDriver, error) {
		d := &tickerDriver{fakeDriver: &fakeDriver{plat: "ios"}}
		d.screen = static(&ui.Node{ID: "ticker", Class: "Button", Text: "Ticker", Enabled: true, Clickable: true, Bounds: ui.Rect{T: 100, R: 100, B: 140}})
		cmds, err := flow.ParseCommands(".", []byte("- tapOn: {id: ticker}"))
		if err != nil {
			t.Fatal(err)
		}
		return d, New(d, Options{}).RunCommands(context.Background(), cmds, "app", nil)
	}
	t.Run("ci", func(t *testing.T) {
		t.Setenv("TB_CI", "1")
		d, err := run(t)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.moving) != 2 || d.moving[0] || !d.moving[1] {
			t.Fatalf("taps sent as moving: %v, want [false true]", d.moving)
		}
	})
	t.Run("not ci", func(t *testing.T) {
		t.Setenv("TB_CI", "0")
		d, err := run(t)
		if err == nil || !contains(err.Error(), "the target was moving when the touch went down") ||
			!contains(err.Error(), "did not reach the app") {
			t.Fatalf("err = %v: want the step to fail saying the target was moving", err)
		}
		if len(d.moving) != 1 {
			t.Fatalf("taps sent: %v, want 1 (no automatic tap again outside CI)", d.moving)
		}
	})
}

type observedButtonDriver struct {
	*fakeDriver
	firstTapped     bool
	confirmFirst    bool
	targetFirst     bool
	waitsAfterFirst int
}

func (d *observedButtonDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target == nil {
		return d.fakeDriver.Tap(ctx, x, y, o)
	}
	if o.Target.ID == "first" {
		d.firstTapped = true
		d.change(true)
	}
	result := driver.ActionResult{Guarded: true, Probed: true, Settle: driver.Changed}
	result.Targeted = o.Target.ID == "first" && d.targetFirst
	if o.Target.ID == "first" && d.confirmFirst {
		result.Clicked = &d.confirmFirst
	}
	return result, nil
}

// A witnessed touch can use a still-visible second button without waiting for
// unrelated events. A touch without a receipt can still be presenting a cover.
func TestConsecutiveGuardedButtonReadiness(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		confirmed, targeted, navigate, wait bool
	}{
		{"confirmed on the same screen", true, false, false, false},
		{"confirmed on a new screen", true, false, true, true},
		{"targeted without a control action", false, true, false, false},
		{"targeted on a new screen", false, true, true, true},
		{"unwitnessed on the same screen", false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &observedButtonDriver{fakeDriver: &fakeDriver{plat: "ios"}, confirmFirst: tc.confirmed, targetFirst: tc.targeted}
			d.onWait = func() {
				if d.firstTapped {
					d.waitsAfterFirst++
				}
			}
			d.screen = func() []*ui.Node {
				title := "Controls"
				if tc.navigate && d.firstTapped {
					title = "Other screen"
				}
				return []*ui.Node{
					{Text: title, Bounds: ui.Rect{R: 400, B: 50}},
					{Class: "ScrollView", ID: "list", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
					{Class: "Button", ID: "first", Depth: 1, Enabled: true, Clickable: true, Bounds: ui.Rect{T: 100, R: 150, B: 150}},
					{Class: "Button", ID: "second", Depth: 1, Enabled: true, Clickable: true, Bounds: ui.Rect{T: 200, R: 150, B: 250}},
				}
			}
			cmds, err := flow.ParseCommands(".", []byte("- tapOn: {id: first}\n- tapOn: {id: second}"))
			if err != nil {
				t.Fatal(err)
			}
			if err = New(d, Options{}).RunCommands(context.Background(), cmds, "app", nil); err != nil {
				t.Fatal(err)
			}
			if (d.waitsAfterFirst > 0) != tc.wait {
				t.Fatalf("waits=%d, want wait=%t", d.waitsAfterFirst, tc.wait)
			}
		})
	}
}

// cancellingDriver: a dialog whose "Discard" button acts only on a touch that ends normally. The
// first `cancels` touches on it reach the app on the button and end cancelled (the probe's
// receipt says so): the press shows (the screen changes), and the button does not act.
type cancellingDriver struct {
	*fakeDriver
	cancels   int
	touches   int
	discarded bool
}

func (d *cancellingDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.Target == nil || o.Target.ID != "discard_yes" {
		return d.fakeDriver.Tap(ctx, x, y, o)
	}
	d.touches++
	if d.touches <= d.cancels {
		d.change(true)
		return driver.ActionResult{Cancelled: true, Guarded: true, Probed: true, Settle: driver.Changed}, nil
	}
	d.discarded = true
	d.change(true)
	return driver.ActionResult{Guarded: true, Probed: true, Targeted: true, Settle: driver.Changed}, nil
}

// Issue #131: a touch on a dialog's button ended cancelled, so the button did not act, yet the
// tap counted as done because the screen changed (the press showed). A cancelled touch is no
// tap: in CI mode the target is found again and tapped again, a bounded number of times; outside
// CI the step fails at the first cancelled touch and says so.
func TestIOSTapWhoseTouchWasCancelledIsSentAgain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ci      bool
		cancels int
		touches int
		fail    string // "" passes
	}{
		{"not cancelled", true, 0, 1, ""},
		{"cancelled once", true, 1, 2, ""},
		{"cancelled twice", true, 2, 3, ""},
		{"cancelled every time", true, 100, 3, "the app cancelled every touch"},
		{"not cancelled, not ci", false, 0, 1, ""},
		{"cancelled once, not ci", false, 1, 1, "the touch reached the app and was cancelled before it ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", map[bool]string{true: "1", false: "0"}[tc.ci])
			d := &cancellingDriver{fakeDriver: &fakeDriver{plat: "ios"}, cancels: tc.cancels}
			d.screen = func() []*ui.Node {
				if d.discarded {
					return []*ui.Node{{Text: "Depth 0", Bounds: ui.Rect{T: 100, R: 100, B: 120}}}
				}
				return []*ui.Node{
					{ID: "discard_dialog", Class: "Alert", Bounds: ui.Rect{T: 62, R: 402, B: 840}},
					{ID: "discard_yes", Class: "Button", Text: "Discard", Enabled: true, Clickable: true, Bounds: ui.Rect{L: 226, T: 452, R: 308, B: 486}},
				}
			}
			cmds, err := flow.ParseCommands(".", []byte("- tapOn: {id: discard_yes}\n- extendedWaitUntil: {visible: \"Depth 0\", timeout: 1000}"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = New(d, Options{}).RunCommands(ctx, cmds, "app", nil)
			if (err != nil) != (tc.fail != "") || (err != nil && !contains(err.Error(), tc.fail)) || d.touches != tc.touches {
				t.Fatalf("err=%v touches=%d, want failure %q and %d touches", err, d.touches, tc.fail, tc.touches)
			}
		})
	}
}
