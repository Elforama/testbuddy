package engine

import (
	"context"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

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
	return driver.ActionResult{Guarded: true, Probed: true, Settle: driver.Changed}, nil
}

// A pre-existing, unmoved button on the same screen should not incur a second
// global wait. A header change on the same underlying window invalidates it.
func TestConsecutiveGuardedButtonReadiness(t *testing.T) {
	for _, navigate := range []bool{false, true} {
		t.Run(map[bool]string{false: "same screen", true: "new screen"}[navigate], func(t *testing.T) {
			d := &observedButtonDriver{fakeDriver: &fakeDriver{plat: "ios"}}
			d.onWait = func() {
				if d.firstTapped {
					d.waitsAfterFirst++
				}
			}
			d.screen = func() []*ui.Node {
				title := "Controls"
				if navigate && d.firstTapped {
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
			if (d.waitsAfterFirst > 0) != navigate {
				t.Fatalf("waits=%d after navigation=%t", d.waitsAfterFirst, navigate)
			}
		})
	}
}
