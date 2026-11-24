package android

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
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

func TestCheckedTypingReportsHowTheKeysWentIn(t *testing.T) {
	a := actionAgent(t)
	a.on("type", func(map[string]any) map[string]any {
		return map[string]any{"result": "changed", "typing": "acknowledged", "seq": 21}
	})
	res, err := newDriver(t, a).TypeTextChecked(ctxT(t), "quick", driver.ActionOpts{})
	if err != nil || res.Typing != "acknowledged" {
		t.Fatalf("result %+v, %v: want typing acknowledged", res, err)
	}
}

func TestCheckedTypingBudgetCoversKeyByKeyTyping(t *testing.T) {
	text := strings.Repeat("ab cd ", 50)
	for _, c := range []struct {
		ci  bool
		cap time.Duration
	}{{false, 300 * time.Millisecond}, {true, time.Second}} {
		firstKey := 4*time.Second + c.cap
		settings := 2 * time.Second
		perChar := time.Second + (c.cap + time.Second) +
			(c.cap + time.Second) + c.cap +
			c.cap +
			time.Second + (c.cap + time.Second)
		worst := firstKey + settings + 300*perChar + (c.cap + time.Second) + c.cap + 15*c.cap
		if old := 7*time.Second + 300*3*time.Second + (4*300+17)*c.cap; old >= worst {
			t.Errorf("ci %v: the old budget %v covers %v: the test would not catch it", c.ci, old, worst)
		}
		if got := checkedTypingBudget(text, c.ci); got < worst {
			t.Errorf("ci %v: budget %v for 300 characters, the agent can take %v", c.ci, got, worst)
		}
	}
	if a, b := checkedTypingBudget("éé", false), checkedTypingBudget("ee", false); a != b {
		t.Errorf("budget counts bytes: %v for \"éé\", %v for \"ee\"", a, b)
	}
}

func TestEraseSendsCIModeAndItsBudgetCoversARepair(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			a := actionAgent(t)
			var got map[string]any
			a.on("erase", func(p map[string]any) map[string]any {
				got = p
				return map[string]any{"result": "changed", "erase": "repaired", "seq": 21}
			})
			if _, err := newDriver(t, a).EraseText(ctxT(t), 40, driver.ActionOpts{}); err != nil {
				t.Fatal(err)
			}
			if ci, _ := got["ci"].(bool); ci != (mode == "1") || got["n"] != float64(40) {
				t.Fatalf("erase params %v: want n 40, ci %v", got, mode == "1")
			}
		})
	}
	for _, c := range []struct {
		ci  bool
		cap time.Duration
	}{{false, 300 * time.Millisecond}, {true, time.Second}} {
		worst := 50*20*time.Millisecond + 50*c.cap + 6*c.cap
		if got := eraseBudget(50, c.ci); got < worst {
			t.Errorf("ci %v: budget %v for 50 characters, the agent can take %v", c.ci, got, worst)
		}
	}
}

func TestTapSendsCIModeAndReportsInjection(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			a := actionAgent(t)
			a.on("tap", func(map[string]any) map[string]any {
				return map[string]any{"result": "no_change", "notInjected": "up", "windowWaitMs": 70, "seq": 3}
			})
			res, err := newDriver(t, a).Tap(ctxT(t), 10, 20, driver.ActionOpts{})
			if err != nil {
				t.Fatal(err)
			}
			v, sent := a.last("tap")["ci"]
			if want := mode == "1"; sent != want || (sent && v != true) {
				t.Errorf("tap request %v: \"ci\" sent %v, want %v", a.last("tap"), sent, want)
			}
			if res.NotInjected != "up" || res.WindowWait != 70*time.Millisecond {
				t.Errorf("result %+v: want notInjected up, window wait 70 ms", res)
			}
		})
	}
}

func TestTapReportsFirstInputToItsWindow(t *testing.T) {
	for _, c := range []struct {
		name  string
		reply map[string]any
		first bool
		age   time.Duration
	}{
		{"first", map[string]any{"result": "no_change", "firstInWindow": true, "windowAgeMs": 180, "windowOver": true, "seq": 3}, true, 180 * time.Millisecond},
		{"not first", map[string]any{"result": "no_change", "firstInWindow": false, "windowAgeMs": 9000, "seq": 3}, false, 9 * time.Second},
		{"an older agent", map[string]any{"result": "no_change", "seq": 3}, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := actionAgent(t)
			a.on("tap", func(map[string]any) map[string]any { return c.reply })
			res, err := newDriver(t, a).Tap(ctxT(t), 10, 20, driver.ActionOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if res.FirstInWindow != c.first || res.WindowAge != c.age || res.WindowOver != c.first {
				t.Errorf("first in window %v, age %v; want %v, %v", res.FirstInWindow, res.WindowAge, c.first, c.age)
			}
		})
	}
}

func TestTapAsksWhereItsTargetIsAtTheTouch(t *testing.T) {
	for _, c := range []struct {
		name  string
		aim   *driver.Aim
		reply map[string]any
		want  *ui.Rect
	}{
		{"found", &driver.Aim{Read: 41, Window: 7, Ref: 3}, map[string]any{"result": "no_change", "aimAt": []int{11, 157, 137, 283}, "aimMs": 4, "seq": 3}, &ui.Rect{L: 11, T: 157, R: 137, B: 283}},
		{"not said", &driver.Aim{Read: 41, Window: 7, Ref: 3}, map[string]any{"result": "no_change", "seq": 3}, nil},
		{"no aim", nil, map[string]any{"result": "no_change", "seq": 3}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := actionAgent(t)
			a.on("tap", func(map[string]any) map[string]any { return c.reply })
			res, err := newDriver(t, a).Tap(ctxT(t), 74, 220, driver.ActionOpts{Aim: c.aim})
			if err != nil {
				t.Fatal(err)
			}
			aim, sent := a.last("tap")["aim"]
			if sent != (c.aim != nil) {
				t.Fatalf("tap request %v: aim sent %v, want %v", a.last("tap"), sent, c.aim != nil)
			}
			if sent {
				m, _ := aim.(map[string]any)
				if m["read"] != float64(c.aim.Read) || m["ref"] != float64(c.aim.Ref) || m["window"] != float64(c.aim.Window) {
					t.Errorf("aim %v, want %+v", aim, *c.aim)
				}
			}
			if !reflect.DeepEqual(res.AimAt, c.want) {
				t.Errorf("AimAt %v, want %v", res.AimAt, c.want)
			}
		})
	}
}

func TestCheckedTypingSendsCIMode(t *testing.T) {
	for _, mode := range []string{"1", "0"} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			a := actionAgent(t)
			if _, err := newDriver(t, a).TypeTextChecked(ctxT(t), "abc", driver.ActionOpts{}); err != nil {
				t.Fatal(err)
			}
			v, sent := a.last("type")["ci"]
			if want := mode == "1"; sent != want || (sent && v != true) {
				t.Errorf("type request %v: \"ci\" sent %v, want %v", a.last("type"), sent, want)
			}
		})
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
		{"press-and-hold drag", func() (driver.ActionResult, error) {
			return d.Swipe(ctx, 1, 2, 3, 4, time.Second, driver.ActionOpts{Hold: 800 * time.Millisecond, Drag: true})
		}, "swipe", map[string]any{"hold": 800.0, "ms": 1000.0, "x2": 3.0}, []string{"drag"}},
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

func TestActionError(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tap", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "inject failed"} })
	d := newDriver(t, a)
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), "inject failed") || r != (driver.ActionResult{}) {
		t.Errorf("Tap = %+v, %v", r, err)
	}
}

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

func TestHideKeyboardWaitsForAKeyboardComingUp(t *testing.T) {
	for mode, want := range map[string]float64{"0": 1000, "1": 2500} {
		t.Run("TB_CI="+mode, func(t *testing.T) {
			t.Setenv("TB_CI", mode)
			a := newFakeAgent(t)
			d := newDriver(t, a)
			a.on("hideKeyboard", func(req map[string]any) map[string]any {
				if got, _ := req["pendingMs"].(float64); got != want {
					t.Errorf("pendingMs %v, want %v", req["pendingMs"], want)
				}
				return map[string]any{"hidden": true}
			})
			if err := d.HideKeyboard(ctxT(t)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHideKeyboardLateKeyboard(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("hideKeyboard", func(map[string]any) map[string]any { return map[string]any{"hidden": true, "late": true} })
	if err := d.HideKeyboard(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if n := d.Notes(); len(n) != 1 || !strings.Contains(n[0], "came up after hideKeyboard") {
		t.Errorf("notes %q", n)
	}
	a.on("hideKeyboard", func(map[string]any) map[string]any { return map[string]any{"hidden": false, "late": true} })
	if err := d.HideKeyboard(ctxT(t)); err == nil || !strings.Contains(err.Error(), "came up late") {
		t.Errorf("err %v", err)
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
