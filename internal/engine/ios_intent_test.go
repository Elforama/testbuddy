package engine

import (
	"context"
	"testing"

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
// take even the flow's no-change retry.
func TestGuardedIOSTapReplay(t *testing.T) {
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
