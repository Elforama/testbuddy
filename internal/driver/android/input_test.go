package android

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func actionAgent(t *testing.T) *fakeAgent {
	a := newFakeAgent(t)
	for _, op := range []string{"tap", "doubleTap", "press", "swipe", "type", "erase", "key"} {
		a.on(op, func(map[string]any) map[string]any {
			return map[string]any{"result": "changed", "injectMs": 2, "settleMs": 340, "seq": 21}
		})
	}
	return a
}

// Each input op sends its own params plus the settle params, and decodes the action result.
func TestCheckedTypingReportsNoInjectionForHostFallback(t *testing.T) {
	a := actionAgent(t)
	a.on("type", func(map[string]any) map[string]any {
		return map[string]any{"verifyUnavailable": true, "seq": 21}
	})
	_, err := newDriver(t, a).TypeTextChecked(ctxT(t), "custom", driver.ActionOpts{})
	if !errors.Is(err, driver.ErrTypingVerificationUnavailable) {
		t.Fatalf("fallback error: %v", err)
	}
}

func TestActions(t *testing.T) {
	a := actionAgent(t)
	d := newDriver(t, a)
	ctx := ctxT(t)
	d.seqBase = 1000
	cases := []struct {
		name string
		run  func() (driver.ActionResult, error)
		op   string
		want map[string]any
		nope []string
	}{
		{"tap", func() (driver.ActionResult, error) { return d.Tap(ctx, 10, 20, driver.ActionOpts{}) },
			"tap", map[string]any{"x": 10.0, "y": 20.0, "noChange": 1000.0}, []string{"settle", "react", "timeout"}},
		{"tap react", func() (driver.ActionResult, error) {
			return d.Tap(ctx, 1, 2, driver.ActionOpts{React: true, Timeout: 3 * time.Second})
		}, "tap", map[string]any{"react": true, "timeout": 3000.0}, nil},
		{"double tap", func() (driver.ActionResult, error) { return d.DoubleTap(ctx, 5, 6, driver.ActionOpts{NoSettle: true}) },
			"doubleTap", map[string]any{"x": 5.0, "y": 6.0, "settle": false}, nil},
		{"long press", func() (driver.ActionResult, error) {
			return d.LongPress(ctx, 7, 8, 1500*time.Millisecond, driver.ActionOpts{})
		}, "press", map[string]any{"x": 7.0, "y": 8.0, "ms": 1500.0}, nil},
		{"swipe", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, 300*time.Millisecond, driver.ActionOpts{})
		}, "swipe", map[string]any{"x1": 1.0, "y1": 2.0, "x2": 3.0, "y2": 4.0, "ms": 300.0}, []string{"drag"}},
		{"drag", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, 0, driver.ActionOpts{Drag: true})
		}, "swipe", map[string]any{"drag": true, "ms": 0.0}, nil},
		{"type", func() (driver.ActionResult, error) { return d.TypeText(ctx, "héllo", driver.ActionOpts{}) },
			"type", map[string]any{"text": "héllo", "noChange": 300.0}, nil},
		{"checked type", func() (driver.ActionResult, error) { return d.TypeTextChecked(ctx, "héllo", driver.ActionOpts{}) },
			"type", map[string]any{"text": "héllo", "verifyFirst": true, "noChange": 300.0}, nil},
		{"type keeps noChange", func() (driver.ActionResult, error) {
			return d.TypeText(ctx, "x", driver.ActionOpts{NoChangeMs: 50})
		}, "type", map[string]any{"noChange": 50.0}, nil},
		{"erase", func() (driver.ActionResult, error) { return d.EraseText(ctx, 4, driver.ActionOpts{}) },
			"erase", map[string]any{"n": 4.0, "noChange": 1000.0}, nil},
		{"key", func() (driver.ActionResult, error) { return d.PressKey(ctx, "Volume_Up", driver.ActionOpts{}) },
			"key", map[string]any{"code": 24.0}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := c.run()
			if err != nil {
				t.Fatal(err)
			}
			want := driver.ActionResult{Settle: driver.Changed, InjectMs: 2, SettleMs: 340, Seq: 1021}
			if r != want {
				t.Errorf("result %+v, want %+v", r, want)
			}
			req := a.last(c.op)
			for k, v := range c.want {
				if !reflect.DeepEqual(req[k], v) {
					t.Errorf("%s = %#v, want %#v (request %v)", k, req[k], v, req)
				}
			}
			for _, k := range c.nope {
				if _, ok := req[k]; ok {
					t.Errorf("request has %q: %v", k, req)
				}
			}
		})
	}
}

// An unknown key fails before anything is sent.
func TestPressKeyUnknown(t *testing.T) {
	a := actionAgent(t)
	d := newDriver(t, a)
	a.reset()
	if _, err := d.PressKey(ctxT(t), "f13", driver.ActionOpts{}); err == nil || !strings.Contains(err.Error(), `unknown key "f13"`) {
		t.Errorf("err = %v", err)
	}
	if ops := a.ops(); len(ops) != 0 {
		t.Errorf("sent %v", ops)
	}
}

// An action the agent rejects returns its error and an empty result.
func TestActionError(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tap", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "inject failed"} })
	d := newDriver(t, a)
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), "inject failed") || r != (driver.ActionResult{}) {
		t.Errorf("Tap = %+v, %v", r, err)
	}
}

// The context's deadline caps a request's timeout: an unanswered request fails when the context ends.
func TestCallUsesContextDeadline(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tap", func(map[string]any) map[string]any { return noReply })
	d := newDriver(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, err := d.Tap(ctx, 1, 1, driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("err = %v", err)
	}
	if el := time.Since(t0); el > 2*time.Second {
		t.Errorf("took %s, want about the context's 80 ms", el)
	}
}

// Settle sends since/noChange/timeout and returns the agent's result; WaitSettled sends quiet.
func TestSettleAndWaitSettled(t *testing.T) {
	a := newFakeAgent(t)
	a.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "no_change", "seq": 3} })
	a.on("quiet", func(map[string]any) map[string]any { return map[string]any{"quiet": true} })
	d := newDriver(t, a)
	ctx := ctxT(t)
	r, err := d.Settle(ctx, 42, 250, 2*time.Second)
	if err != nil || r != driver.NoChange {
		t.Errorf("Settle = %q, %v", r, err)
	}
	req := a.last("settle")
	if num(t, req, "since") != 42 || num(t, req, "noChange") != 250 || num(t, req, "timeout") != 2000 {
		t.Errorf("settle request %v", req)
	}
	if err := d.WaitSettled(ctx, 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if num(t, a.last("quiet"), "timeout") != 1500 {
		t.Errorf("quiet request %v", a.last("quiet"))
	}
	a.on("settle", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if r, err := d.Settle(ctx, 0, 1, time.Second); err == nil || r != "" {
		t.Errorf("failed Settle = %q, %v", r, err)
	}
}

// Keyboard detection and dismissal run together on the device, avoiding a
// host round trip between detecting the IME and sending Back.
func TestHideKeyboard(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	initialInfo := len(a.requests("info"))
	a.on("hideKeyboard", func(req map[string]any) map[string]any {
		if num(t, req, "timeout") != 3000 {
			t.Error("missing keyboard deadline")
		}
		return map[string]any{"hidden": true}
	})
	if err := d.HideKeyboard(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if len(a.requests("info")) != initialInfo || len(a.requests("key")) != 0 || len(a.requests("hideKeyboard")) != 1 {
		t.Fatal("keyboard dismissal should use one on-device operation")
	}
}

func TestHideKeyboardErrors(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("hideKeyboard", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "gone"} })
	if err := d.HideKeyboard(ctxT(t)); err == nil {
		t.Fatal("agent failure not reported")
	}
	a.on("hideKeyboard", func(map[string]any) map[string]any { return map[string]any{"hidden": false} })
	if err := d.HideKeyboard(ctxT(t)); err == nil {
		t.Fatal("visible keyboard not reported")
	}
}
