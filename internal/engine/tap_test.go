package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/js"
	"github.com/elforama/testbuddy/internal/ui"
)

// A tap at a point needs no element: absolute pixels or percentages of the screen.
func TestTapAtScreenPoint(t *testing.T) {
	for _, tc := range []struct {
		point string
		want  [2]int
	}{
		{"10,20", [2]int{10, 20}},
		{"50%,50%", [2]int{200, 400}},
		{"0%,100%", [2]int{0, 800}},
		{" 25% , 10% ", [2]int{100, 80}},
		{"${'30,40'}", [2]int{30, 40}}, // evaluated first
	} {
		t.Run(tc.point, func(t *testing.T) {
			f := &fakeDriver{screen: static()}
			mustPass(t, runWith(t, f, quick, fmt.Sprintf("- tapOn: {point: %q}", tc.point)))
			if len(f.taps) != 1 || f.taps[0] != tc.want {
				t.Errorf("taps = %v, want one at %v", f.taps, tc.want)
			}
			if !f.tapOpts[0].React {
				t.Errorf("tap options %+v: want React", f.tapOpts[0])
			}
		})
	}
}

// A malformed point fails the command, even an optional one: it is a mistake in the flow,
// not a screen that differs.
func TestTapInvalidPointFailsEvenOptional(t *testing.T) {
	for _, p := range []string{"abc", "10", "-5,10", "10,-5", "150%,20%", "x%,10%", "10,y", "5%,-1%"} {
		t.Run(p, func(t *testing.T) {
			f := &fakeDriver{screen: static()}
			res := runWith(t, f, quick, fmt.Sprintf("- tapOn: {point: %q, optional: true}", p))
			mustFail(t, res, "invalid point")
			if len(f.taps) != 0 {
				t.Errorf("tapped %v", f.taps)
			}
		})
	}
}

// A point given with a selector is relative to the element: pixels from its corner or
// percentages of its size; a point outside it is an error.
func TestTapAtPointInElement(t *testing.T) {
	for _, tc := range []struct {
		point string
		want  [2]int
		err   string
	}{
		{"10,5", [2]int{10, 105}, ""},
		{"50%,50%", [2]int{200, 125}, ""},
		{"100%,0%", [2]int{400, 100}, ""},
		{"12.5%, 50%", [2]int{50, 125}, ""},
		{"100.5%,0%", [2]int{}, "percentages are 0-100"},
		{"400,10", [2]int{}, "outside the element"},
		{"10,50", [2]int{}, "outside the element"},
		{"nope", [2]int{}, "invalid point"},
	} {
		t.Run(tc.point, func(t *testing.T) {
			f := &fakeDriver{}
			f.screen = static(btn("Go", "go", 100))
			res := runWith(t, f, quick, fmt.Sprintf("- tapOn: {id: go, point: %q}", tc.point))
			if tc.err != "" {
				mustFail(t, res, tc.err)
				return
			}
			mustPass(t, res)
			if len(f.taps) != 1 || f.taps[0] != tc.want {
				t.Errorf("taps = %v, want %v", f.taps, tc.want)
			}
		})
	}
}

// An element-relative point is interpolated like every other value.
func TestTapAtPointInElementInterpolated(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 100))
	mustPass(t, runWith(t, f, quick, `- tapOn: {id: go, point: "${'10,5'}"}`))
	if f.taps[0] != [2]int{10, 105} {
		t.Errorf("taps = %v", f.taps)
	}
}

// iOS taps are held 10 ms, 20 ms for tracking controls, alert/sheet buttons and points.
func TestIOSTapHold(t *testing.T) {
	alert := at("Alert", 0, 0, 200, 400, 600)
	inAlert := btn("OK", "ok", 300)
	inAlert.Class, inAlert.Depth = "Button", 1
	sw := btn("", "sw", 100)
	sw.Class = "Switch"
	plain := btn("Go", "go", 0)
	plain.Class = "Button"
	for _, tc := range []struct {
		name, cmd string
		want      time.Duration
	}{
		{"button", "- tapOn: {id: go}", iosShortHold},
		{"switch", "- tapOn: {id: sw}", iosFrameHold},
		{"alert button", "- tapOn: {id: ok}", iosFrameHold},
		{"point", "- tapOn: {point: '10,10'}", iosFrameHold},
		{"point in element", "- tapOn: {id: go, point: '1,1'}", iosFrameHold},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			f.screen = static(plain, sw, alert, inAlert)
			mustPass(t, runWith(t, f, quick, tc.cmd))
			if got := f.tapOpts[0].Hold; got != tc.want {
				t.Errorf("hold %s, want %s", got, tc.want)
			}
		})
	}
	// Android taps leave the hold to the agent.
	f := &fakeDriver{}
	f.screen = static(plain)
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go}"))
	if f.tapOpts[0].Hold != 0 {
		t.Errorf("android hold %s, want 0", f.tapOpts[0].Hold)
	}
}

// iosTapHold walks up to the nearest frame-hold ancestor (a control inside a picker).
func TestIOSTapHoldAncestors(t *testing.T) {
	picker := &ui.Node{Class: "Picker"}
	row := &ui.Node{Class: "Other", Parent: picker}
	sheet := &ui.Node{Class: "Sheet"}
	inSheet := &ui.Node{Class: "Button", Parent: &ui.Node{Class: "Other", Parent: sheet}}
	for _, tc := range []struct {
		name string
		n    *ui.Node
		pt   bool
		want time.Duration
	}{
		{"nil", nil, false, iosFrameHold},
		{"at point", &ui.Node{Class: "Button"}, true, iosFrameHold},
		{"in picker", row, false, iosFrameHold},
		{"in sheet", inSheet, false, iosFrameHold},
		{"slider", &ui.Node{Class: "Slider"}, false, iosFrameHold},
		{"cell", &ui.Node{Class: "Cell"}, false, iosShortHold},
	} {
		if got := iosTapHold(tc.n, tc.pt); got != tc.want {
			t.Errorf("%s: hold %s, want %s", tc.name, got, tc.want)
		}
	}
}

// repeat taps n times at one point; all but the last return at once, with the flow's delay
// between them.
func TestTapRepeatWithDelay(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go, repeat: 3, delay: 60}"))
	if d := time.Since(start); d < 120*time.Millisecond {
		t.Errorf("3 taps 60 ms apart took %s", d)
	}
	if len(f.taps) != 3 {
		t.Fatalf("%d taps, want 3", len(f.taps))
	}
	for i, o := range f.tapOpts {
		if o.NoSettle != (i < 2) || !o.React {
			t.Errorf("tap %d options %+v", i, o)
		}
		if f.taps[i] != [2]int{200, 25} {
			t.Errorf("tap %d at %v", i, f.taps[i])
		}
	}
}

// repeat: 1 is a single ordinary tap.
func TestTapRepeatOnce(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go, repeat: 1}"))
	if len(f.taps) != 1 || f.tapOpts[0].NoSettle {
		t.Errorf("taps %v opts %+v", f.taps, f.tapOpts)
	}
}

// A repeated tap that fails stops at the failing tap.
func TestTapRepeatStopsOnError(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	f.fail("Tap", errors.New("injection failed"))
	mustFail(t, runWith(t, f, quick, "- tapOn: {id: go, repeat: 3, delay: 1}"), "injection failed")
	if len(f.taps) != 1 {
		t.Errorf("%d taps after the first failed", len(f.taps))
	}
}

// retryTapIfNoChange taps a second time when the first changed nothing, and only then.
func TestRetryTapIfNoChange(t *testing.T) {
	for _, tc := range []struct {
		res  driver.SettleResult
		want int
	}{
		{driver.NoChange, 2},
		{driver.Changed, 1},
		{driver.Restless, 1},
	} {
		t.Run(string(tc.res), func(t *testing.T) {
			f := &fakeDriver{tapSettle: tc.res}
			f.screen = static(btn("Go", "go", 0))
			mustPass(t, runWith(t, f, quick, "- tapOn: {id: go, retryTapIfNoChange: true}"))
			if len(f.taps) != tc.want {
				t.Errorf("%d taps, want %d", len(f.taps), tc.want)
			}
		})
	}
	// Without the option a tap that changed nothing is not repeated.
	f := &fakeDriver{tapSettle: driver.NoChange}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go}"))
	if len(f.taps) != 1 {
		t.Errorf("%d taps without retryTapIfNoChange", len(f.taps))
	}
}

// doubleTapOn is one double tap (the agent times the gap), and defers the settle like a tap.
func TestDoubleTapOn(t *testing.T) {
	for _, cmd := range []string{"- doubleTapOn: Go", "- doubleTapOn: {id: go}"} {
		f := &fakeDriver{}
		f.screen = static(btn("Go", "go", 300))
		mustPass(t, runWith(t, f, quick, cmd+"\n- inputText: x"))
		if len(f.tapKind) != 1 || f.tapKind[0] != "double" || f.taps[0] != [2]int{200, 325} {
			t.Errorf("%s: taps %v %v", cmd, f.tapKind, f.taps)
		}
		if !f.tapOpts[0].React {
			t.Errorf("%s: options %+v, want React", cmd, f.tapOpts[0])
		}
		if f.waitDur[len(f.waitDur)-1] != 5*time.Second {
			t.Errorf("%s: waits %v, want the deferred 5 s settle before typing", cmd, f.waitDur)
		}
	}
}

// longPressOn holds 3 s and waits for the UI to settle itself (no deferred settle).
func TestLongPressOn(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- longPressOn: {id: go}\n- inputText: x"))
	if len(f.tapKind) != 1 || f.tapKind[0] != "long" || f.holds[0] != 3*time.Second {
		t.Errorf("taps %v holds %v", f.tapKind, f.holds)
	}
	if f.tapOpts[0].React {
		t.Errorf("long press options %+v: want no React", f.tapOpts[0])
	}
	if n := f.waits.Load(); n != 1 {
		t.Errorf("%d settle waits, want 1 (findStable only)", n)
	}
}

// A long press at a point of the screen.
func TestLongPressAtPoint(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- longPressOn: {point: '50%,25%'}"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 200} || f.tapKind[0] != "long" {
		t.Errorf("taps %v %v", f.taps, f.tapKind)
	}
}

// waitToSettleTimeoutMs bounds the tap's settle, the settle before it, and the deferred one.
func TestTapWaitToSettleTimeout(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go, waitToSettleTimeoutMs: 700}\n- inputText: x"))
	if f.tapOpts[0].Timeout != 700*time.Millisecond {
		t.Errorf("tap timeout %s", f.tapOpts[0].Timeout)
	}
	want := []time.Duration{700 * time.Millisecond, 700 * time.Millisecond}
	if fmt.Sprint(f.waitDur) != fmt.Sprint(want) {
		t.Errorf("settle waits %v, want %v", f.waitDur, want)
	}
	// The default: 2 s before the tap, 5 s deferred.
	f2 := &fakeDriver{}
	f2.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f2, quick, "- tapOn: {id: go}\n- inputText: x"))
	want = []time.Duration{2 * time.Second, 5 * time.Second}
	if fmt.Sprint(f2.waitDur) != fmt.Sprint(want) {
		t.Errorf("default settle waits %v, want %v", f2.waitDur, want)
	}
}

// A tap that changed nothing leaves nothing to settle: the next command does not wait.
func TestTapNoChangeDefersNothing(t *testing.T) {
	f := &fakeDriver{tapSettle: driver.NoChange}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go}\n- inputText: x"))
	if n := f.waits.Load(); n != 1 {
		t.Errorf("%d settle waits, want 1", n)
	}
}

// Commands that look for their element as it comes in skip the deferred settle; the rest wait.
func TestDeferredSettleByCommand(t *testing.T) {
	for _, tc := range []struct {
		next string
		wait bool
	}{
		{"- assertVisible: Go", false},
		{"- assertNotVisible: Nope", false},
		{"- extendedWaitUntil: {visible: Go, timeout: 100}", false},
		{"- eraseText: 1", true},
		{"- pressKey: Enter", true},
		{"- hideKeyboard", true},
		{"- evalScript: ${1}", true},
	} {
		t.Run(tc.next, func(t *testing.T) {
			f := &fakeDriver{}
			f.screen = static(btn("Go", "go", 0))
			runWith(t, f, quick, "- tapOn: {id: go}\n"+tc.next)
			got := false
			for _, d := range f.waitDur {
				if d == 5*time.Second {
					got = true
				}
			}
			if got != tc.wait {
				t.Errorf("waited for the deferred settle: %v, want %v (waits %v)", got, tc.wait, f.waitDur)
			}
		})
	}
}

// A tap the device fails to inject fails the command.
func TestTapDriverError(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	f.fail("Tap", errors.New("agent gone"))
	mustFail(t, runWith(t, f, quick, "- tapOn: {id: go}"), "agent gone")
}

// A tap on an element that never shows fails with what was looked for.
func TestTapNotFound(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 0))}
	start := time.Now()
	res := runWith(t, f, quick, "- tapOn: Nope")
	mustFail(t, res, `Element not found: "Nope"`)
	if d := time.Since(start); d < 300*time.Millisecond || d > 2*time.Second {
		t.Errorf("gave up after %s, want the 400 ms lookup budget", d)
	}
	var nf *NotFound
	if !errors.As(res.Err, &nf) || nf.Screen == nil || nf.Query != "Nope" {
		t.Errorf("error %#v: want a NotFound with the screen", res.Err)
	}
}

// An element still disabled when the 3 s grace ends is tapped anyway.
func TestTapDisabledAfterGrace(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	b := btn("Save", "save", 0)
	b.Enabled = false
	f.screen = static(b)
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: save}"))
	if d := time.Since(start); d < disabledWait || d > disabledWait+time.Second {
		t.Errorf("tapped after %s, want after the %s grace", d, disabledWait)
	}
	if len(f.taps) != 1 {
		t.Errorf("taps %v", f.taps)
	}
}

// While waiting for a disabled element to turn on, it may vanish for a read; the wait goes on.
func TestTapDisabledVanishesThenEnables(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var phase atomic.Int64
	f.screen = func() []*ui.Node {
		switch phase.Load() {
		case 0:
			b := btn("Save", "save", 0)
			b.Enabled = false
			return []*ui.Node{b}
		case 1:
			return nil // re-rendering
		}
		return []*ui.Node{btn("Save", "save", 100)}
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		phase.Store(1)
		f.change(true)
		time.Sleep(100 * time.Millisecond)
		phase.Store(2)
		f.change(true)
	}()
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: save}"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 125} {
		t.Errorf("taps %v, want the enabled button where it ended up", f.taps)
	}
}

// A tap with an empty selector map (only optional) picks the first clickable element.
func TestTapAnyElement(t *testing.T) {
	f := &fakeDriver{}
	label := &ui.Node{Text: "Title", Bounds: ui.Rect{R: 400, B: 50}, Enabled: true}
	f.screen = static(label, btn("Go", "go", 100))
	mustPass(t, runWith(t, f, quick, "- tapOn: {optional: true}"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 125} {
		t.Errorf("taps %v, want the clickable one", f.taps)
	}
}

// A tap lands where the element ends up: when it only appeared after a wait, or the UI changed
// while settling, it is found again after the settle.
func TestTapLandsWhereElementEndsUp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		appears bool // shows only after a wait (else on the first read)
		moveTo  int  // where the settle moves it (0: it vanishes)
		want    int  // tapped y
	}{
		{"appeared late, slid in", true, 300, 325},
		{"moved while settling", false, 500, 525},
		{"vanished while settling", false, 0, 125}, // the first find stands
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{}
			var top atomic.Int64
			if tc.appears {
				top.Store(-1)
			} else {
				top.Store(100)
			}
			f.screen = func() []*ui.Node {
				switch v := top.Load(); v {
				case -1:
					return nil
				case 0:
					return []*ui.Node{btn("Other", "", 0)}
				default:
					return []*ui.Node{btn("Go", "go", int(v))}
				}
			}
			var settled atomic.Bool
			f.onWait = func() {
				if top.Load() > 0 && settled.CompareAndSwap(false, true) {
					top.Store(int64(tc.moveTo))
					f.change(true)
				}
			}
			if tc.appears {
				go func() {
					time.Sleep(50 * time.Millisecond)
					top.Store(100)
					f.change(true)
				}()
			}
			o := quick
			o.LookupTimeout = 2 * time.Second
			mustPass(t, runWith(t, f, o, "- tapOn: {id: go}"))
			if len(f.taps) != 1 || f.taps[0][1] != tc.want {
				t.Errorf("taps %v, want y %d", f.taps, tc.want)
			}
		})
	}
}

// A cancelled run ends the wait for a disabled element at once.
func TestTapDisabledCancelled(t *testing.T) {
	t.Parallel()
	b := btn("Save", "save", 0)
	b.Enabled = false
	f := &fakeDriver{screen: static(b)}
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- tapOn: {id: save}\n- inputText: after"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := New(f, quick).RunFlow(ctx, fl)
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s after the cancel", d)
	}
	if !errors.Is(res.Err, context.DeadlineExceeded) || len(f.typed) != 0 {
		t.Errorf("err %v typed %q", res.Err, f.typed)
	}
}

// A target that slides in without the device reporting it (no change events: Android sends
// none for a new position) is tapped where it comes to rest, not where the first read saw it.
func TestTapWaitsForTargetAtRest(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	start := time.Now()
	f.screen = func() []*ui.Node {
		// Slides from x=900 to x=40 over 300 ms, then rests.
		p := min(time.Since(start).Seconds()/0.3, 1)
		l := 900 - int(p*860)
		b := btn("Back", "back", 100)
		b.Bounds = ui.Rect{L: l, T: 100, R: l + 100, B: 150}
		return []*ui.Node{b}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: back}"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{90, 125} {
		t.Errorf("taps %v, want the resting place (90, 125)", f.taps)
	}
}

type checkedTypingDriver struct {
	*fakeDriver
	checked   []string
	verifyErr error
}

func (d *checkedTypingDriver) TypeTextChecked(_ context.Context, text string, _ driver.ActionOpts) (driver.ActionResult, error) {
	d.checked = append(d.checked, text)
	return driver.ActionResult{}, d.verifyErr
}

func TestUnavailableDeviceFocusKeepsTheScreenGuard(t *testing.T) {
	f := &fakeDriver{}
	text := ""
	f.screen = func() []*ui.Node {
		return []*ui.Node{{Class: "EditText", ID: "custom", Text: text, Editable: true, Focused: true}}
	}
	dropped := false
	f.onType = func(s string) {
		if !dropped {
			dropped = true
		} else {
			text += s
		}
	}
	d := &checkedTypingDriver{fakeDriver: f, verifyErr: driver.ErrTypingVerificationUnavailable}
	mustPass(t, runWith(t, d, quick, "- inputText: auto"))
	if text != "auto" || strings.Join(f.typed, "|") != "a|a|uto" {
		t.Fatalf("fallback text %q, injected %v", text, f.typed)
	}
}

func TestDeviceCheckedTypingAvoidsHierarchyReads(t *testing.T) {
	f := &fakeDriver{}
	f.screen = func() []*ui.Node { t.Fatal("device-verified typing must not read the whole screen"); return nil }
	d := &checkedTypingDriver{fakeDriver: f}
	mustPass(t, runWith(t, d, quick, "- inputText: auto"))
	if got := strings.Join(d.checked, "|"); got != "auto" {
		t.Fatalf("checked typing %q", got)
	}
	if len(f.typed) != 0 {
		t.Fatal("runner also used the unchecked typing path")
	}
}

// Drivers without device-side verification retain the runner's first-key retry.
func TestTypeFirstKeyChecked(t *testing.T) {
	t.Parallel()
	for _, drop := range []bool{true, false} {
		f := &fakeDriver{}
		var mu sync.Mutex
		text, dropped := "", false
		f.screen = func() []*ui.Node {
			mu.Lock()
			defer mu.Unlock()
			n := &ui.Node{Class: "EditText", ID: "field", Text: text, Editable: true, Focused: true, Enabled: true, Bounds: ui.Rect{T: 100, R: 400, B: 160}}
			return []*ui.Node{n}
		}
		f.onType = func(s string) {
			mu.Lock()
			if drop && !dropped {
				dropped = true // the first key is lost
			} else {
				text += s
			}
			mu.Unlock()
			f.change(true)
		}
		mustPass(t, runWith(t, f, quick, "- inputText: auto"))
		f.mu.Lock()
		typed := strings.Join(f.typed, "|")
		f.mu.Unlock()
		want := "a|uto"
		if drop {
			want = "a|a|uto"
		}
		if typed != want || text != "auto" {
			t.Errorf("drop %v: typed %q (want %q), field %q", drop, typed, want, text)
		}
	}
}

// A one-digit code box moves focus to the next box as a key lands: the first key counts as
// landed (focus moved), and is not typed again.
func TestTypeFirstKeyFocusMoves(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var mu sync.Mutex
	boxes := make([]string, 6)
	focus := 0
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		var ns []*ui.Node
		for i := range boxes {
			ns = append(ns, &ui.Node{Class: "EditText", ID: fmt.Sprintf("otp_%d", i+1), Text: boxes[i], Editable: true,
				Focused: i == focus, Enabled: true, Bounds: ui.Rect{L: i * 60, T: 100, R: i*60 + 50, B: 160}})
		}
		return ns
	}
	f.onType = func(s string) {
		mu.Lock()
		for _, ch := range s {
			if focus < len(boxes) {
				boxes[focus] = string(ch)
				focus++
			}
		}
		mu.Unlock()
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, `- inputText: "123456"`))
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(boxes, ""); got != "123456" {
		t.Errorf("boxes %q, want 123456 (the first key typed once)", got)
	}
}

// A WebView/custom provider may send no event for the rest of the text. The
// first-key verification snapshot must not remain current after typing it.
func TestTypingRemainderInvalidatesFirstKeySnapshotWithoutEvents(t *testing.T) {
	f := &fakeDriver{}
	var mu sync.Mutex
	text := ""
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		return []*ui.Node{{Class: "EditText", ID: "field", Text: text, Editable: true,
			Focused: true, Enabled: true, Bounds: ui.Rect{R: 400, B: 80}}}
	}
	f.onType = func(s string) { mu.Lock(); text += s; mu.Unlock() }
	mustPass(t, runWith(t, f, quick, "- inputText: hi\n- assertVisible: {id: field, text: hi}"))
}

// A button springing in overshoots past a clipping edge: for a few reads it is not on screen at
// all, then comes back to rest. The tap waits for it to rest instead of taking the miss as
// "gone" and tapping the sliver it last saw (the bench's Android "Sprung" button).
func TestTapWaitsForSpringBackIntoSight(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		at := func(l, r int) []*ui.Node {
			return []*ui.Node{{Text: "Sprung", Bounds: ui.Rect{L: l, T: 100, R: r, B: 150}, Enabled: true, Clickable: true}}
		}
		switch {
		case n == 1:
			return at(319, 566)
		case n == 2:
			return at(87, 334)
		case n == 3:
			return at(42, 90) // clipped by the lane's left edge
		case n <= 7:
			return nil // past the edge: out of sight
		default:
			return at(200, 447) // at rest
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Sprung"))
	if len(f.taps) != 1 || f.taps[0][0] < 200 || f.taps[0][0] > 447 {
		t.Errorf("taps %v, want one on the button at rest (x 200-447)", f.taps)
	}
}

// The same spring, but held at the clipping edge for a few reads: two reads agree on the
// clipped sliver, which is not the button at rest (it was full width a moment ago).
func TestTapWaitsForSpringHeldAtEdge(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		n := reads.Add(1)
		at := func(l, r int) []*ui.Node {
			return []*ui.Node{{Text: "Sprung", Bounds: ui.Rect{L: l, T: 100, R: r, B: 150}, Enabled: true, Clickable: true}}
		}
		switch {
		case n == 1:
			return at(447, 694)
		case n == 2:
			return at(93, 340)
		case n <= 6:
			return at(42, 94) // cut off by the lane's left edge, the same on each read
		default:
			return at(200, 447)
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Sprung"))
	if len(f.taps) != 1 || f.taps[0][0] < 200 || f.taps[0][0] > 447 {
		t.Errorf("taps %v, want one on the button at rest (x 200-447)", f.taps)
	}
}

// Right after an action, a spring pausing at a turn (two quick reads agree) is not taken for
// at rest: the reads must agree for restSpanAfterAction.
func TestTapAfterActionWaitsOutASpringsTurn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var opened atomic.Bool
	var since atomic.Int64
	f.screen = func() []*ui.Node {
		open := &ui.Node{Text: "Spring in", Bounds: ui.Rect{L: 0, T: 0, R: 200, B: 50}, Enabled: true, Clickable: true}
		if !opened.Load() {
			return []*ui.Node{open}
		}
		ms := time.Since(time.Unix(0, since.Load())).Milliseconds()
		l := 200 // at rest from 400 ms
		switch {
		case ms < 60:
			l = 280 // a turn: still for 60 ms
		case ms < 400:
			l = 50 + int(ms-60)/30*10 // moving: a new place every 30 ms
		}
		return []*ui.Node{open, {Text: "Sprung", Bounds: ui.Rect{L: l, T: 100, R: l + 100, B: 150}, Enabled: true, Clickable: true}}
	}
	f.onTap = func(x, y int) {
		if !opened.Load() {
			since.Store(time.Now().UnixNano())
			opened.Store(true)
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: Spring in\n- tapOn: Sprung"))
	if len(f.taps) != 2 || f.taps[1][0] != 250 {
		t.Errorf("taps %v, want the second on the button at rest (x 250)", f.taps)
	}
}

// A pulsing button (it grows and shrinks around its middle, forever) is not "cut off at an
// edge": it is tapped without waiting out restWait.
func TestTapOnPulseIsNotHeldAsCutOff(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		d := int(reads.Add(1) % 2) // 0 or 1: grown by 1 px each side, or not
		if reads.Load() > 3 {
			d = 0
		}
		return []*ui.Node{{Text: "Pulse", Bounds: ui.Rect{L: 42 - d, T: 100 - d, R: 273 + d, B: 226 + d}, Enabled: true, Clickable: true}}
	}
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- tapOn: Pulse"))
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %s: held as cut off", d)
	}
}

// A tap target under the keyboard, which Android leaves out of the tree, is found once the
// keyboard is hidden: after keyboardGrace, the tap hides it and looks again.
func TestTapHidesKeyboardOverTarget(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	kb := ui.Rect{T: 500, R: 400, B: 800}
	f.keyboard = &kb
	f.kbShown = true
	f.screen = func() []*ui.Node {
		field := btn("Bottom field", "kb_bottom", 400)
		if len(f.calls) > 0 && f.calls[len(f.calls)-1] == "HideKeyboard" {
			f.keyboard, f.kbShown = nil, false // hidden (f.mu is held here)
			return []*ui.Node{field, btn("Submit", "kb_submit", 600)}
		}
		return []*ui.Node{field}
	}
	start := time.Now()
	mustPass(t, runWith(t, f, Options{LookupTimeout: 5 * time.Second, OptionalTimeout: time.Second}, "- tapOn: {id: kb_submit}"))
	if len(f.called("HideKeyboard")) != 1 || len(f.taps) != 1 || f.taps[0][1] != 625 {
		t.Errorf("hides %v, taps %v", f.called("HideKeyboard"), f.taps)
	}
	if d := time.Since(start); d < keyboardGrace || d > keyboardGrace+2*time.Second {
		t.Errorf("took %s", d)
	}
}

// iOS (CI's iPhone 17 Pro simulator, on-screen keyboard): the keyboard's views are listed in
// the app's own window, so a Submit button under it is found but covered, and no drag brings
// it clear (bench/flows/scenarios/keyboard.yaml). Tapping its middle would hit the keyboard:
// the tap hides the keyboard and finds the button again instead.
func TestTapHidesKeyboardCoveringTarget(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios"}
	f.kbShown = true
	f.screen = func() []*ui.Node {
		field := btn("Bottom field", "kb_bottom", 400)
		submit := btn("Submit", "kb_submit", 600)
		submit.Class = "Button"
		for _, c := range f.calls { // f.mu is held here
			if c == "HideKeyboard" {
				return []*ui.Node{field, submit}
			}
		}
		host := &ui.Node{Class: "Other", Bounds: ui.Rect{T: 540, R: 400, B: 800}}
		key := &ui.Node{Class: "Button", Text: "q", Depth: 1, Enabled: true, Bounds: ui.Rect{T: 540, R: 400, B: 800}}
		return []*ui.Node{field, submit, host, key}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: kb_submit}"))
	if len(f.called("HideKeyboard")) != 1 || len(f.taps) != 1 || f.taps[0][1] != 625 {
		t.Errorf("hides %v, taps %v: want the keyboard hidden, then one tap on Submit", f.called("HideKeyboard"), f.taps)
	}
}

// Android, a layout shift (bench/flows/scenarios/animations.yaml on CI's slow emulator): the
// target shows still in two reads, then a banner pushes it down just as the tap goes out. The
// tap reaches no view (none reports a click) and the target is now elsewhere: it is tapped
// again where it is. Not when a view took the click, when the target did not move, when
// another element now matches (bench/flows/scenarios/controls.yaml: a dropdown's label showing
// the option just picked, which reported no click), or where the platform cannot tell (a
// second tap could act twice). Nor when a view reports the click after the tap's reply went
// out (bench/flows/scenarios/errors.yaml: the reply came 2 ms after the tap, the click event
// later; the message the tap brought up pushed the button down, and a second tap retried the
// load): tb asks again before tapping twice.
func TestTapAgainAfterTargetMovedFromUnderIt(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		plat     string
		clicked  *bool // what the first tap reports
		moves    bool
		replaced bool // after the tap another element matches, elsewhere
		late     bool // the click is reported after the tap's reply
		wantTaps int
	}{
		{"shifted, no click", "android", &no, true, false, false, 2},
		{"shifted, clicked", "android", &yes, true, false, false, 1},
		{"shifted, click reported late", "android", &no, true, false, true, 1},
		{"still, no click", "android", &no, false, false, false, 1},
		{"another element matches", "android", &no, true, true, false, 1},
		{"shifted, unknown", "ios", nil, true, false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: tc.plat}
			var shifted atomic.Bool
			f.screen = func() []*ui.Node {
				if shifted.Load() && tc.replaced {
					label := btn("Target", "anim_target", 300)
					label.Class, label.Bounds.B = "TextView", 320 // smaller, another type
					return []*ui.Node{label}
				}
				if shifted.Load() {
					return []*ui.Node{{Text: "Banner", Bounds: ui.Rect{T: 580, R: 400, B: 680}}, btn("Target", "anim_target", 700)}
				}
				return []*ui.Node{btn("Target", "anim_target", 600)}
			}
			taps := 0
			f.clicked = func(x, y int) *bool {
				taps++
				if taps == 1 {
					return tc.clicked
				}
				return &yes
			}
			f.onTap = func(x, y int) {
				if tc.moves && !shifted.Load() {
					shifted.Store(true) // the banner pushes the target down as the tap lands
				}
				if tc.late {
					f.clicks.Add(1) // the click event arrives after the reply's "no click"
				}
				f.change(true)
			}
			mustPass(t, runWith(t, f, quick, "- tapOn: {id: anim_target}"))
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.taps) != tc.wantTaps {
				t.Fatalf("taps %v, want %d", f.taps, tc.wantTaps)
			}
			if tc.wantTaps == 2 && f.taps[1] != [2]int{200, 725} {
				t.Errorf("second tap %v, want the target where it is now (200, 725)", f.taps[1])
			}
		})
	}
}

// Android, a layout shift on a slow device (bench/flows/scenarios/animations.yaml on CI's slow
// emulator): the target shows in its list, then a banner grows above it and pushes it down past
// the bottom edge: first a sliver at the edge (Android cuts bounds to what shows), then not
// listed at all. The tap does not act on the sliver it last saw (the tap reached nothing there):
// it drags the list until the target shows whole, and taps it there. Also when no read caught
// it moving (on the local emulator the banner came between two reads: in view, then not
// listed): its list is still there, so it left the list's view. A target that is really gone
// (it never comes back) fails the tap, within a bound, instead of a tap on the sliver.
func TestTapBringsBackTargetPushedOutOfSight(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		jump  bool // the banner shows at its full height at once
		gone  bool // the target is removed once pushed out of sight
		atTap bool // the layout shift starts only when input is injected
		late  bool // a delayed click report prevents recovery
	}{
		{"pushed below the edge", false, false, false, false},
		{"pushed out between two reads", true, false, false, false},
		{"pushed out, then gone", false, true, false, false},
		{"pushed out as the tap lands", true, false, true, false},
		{"pushed out with a late click", true, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{}
			var off atomic.Int64     // how far the list is scrolled
			var shiftAt atomic.Int64 // when the banner began to grow (0: not yet)
			const targetAt = 650     // the target's top in the list, before the banner
			banner := func() int {   // the banner's height: 0 to 200 px over 300 ms
				at := shiftAt.Load()
				if at == 0 {
					return 0
				}
				if tc.jump {
					return 200
				}
				return min(200, int(time.Since(time.Unix(0, at)).Milliseconds())*200/300)
			}
			// target is where the target is (unclipped), and whether it is in the list at all.
			target := func() (ui.Rect, bool) {
				b := banner()
				top := targetAt + b - int(off.Load())
				return ui.Rect{T: top, R: 400, B: top + 50}, !(tc.gone && b >= 200)
			}
			screen := ui.Rect{R: 400, B: 800}
			f.screen = func() []*ui.Node {
				nodes := []*ui.Node{{Class: "ScrollView", Scrollable: true, Bounds: screen}}
				add := func(n *ui.Node) {
					n.Bounds = n.Bounds.Intersect(screen) // Android lists what shows, cut to the screen
					if !n.Bounds.Empty() {
						n.Depth = 1
						nodes = append(nodes, n)
					}
				}
				o := int(off.Load())
				for i := 0; i < 11; i++ {
					top := 100 + i*50 - o
					add(&ui.Node{Text: fmt.Sprintf("Item %d", i), Bounds: ui.Rect{T: top, R: 400, B: top + 50}})
				}
				if b := banner(); b > 0 {
					add(&ui.Node{Text: "Banner", Bounds: ui.Rect{T: targetAt - o, R: 400, B: targetAt + b - o}})
				}
				tb, there := target()
				if there {
					add(&ui.Node{Class: "Button", Text: "Target", ID: "anim_target", Bounds: tb, Enabled: true, Clickable: true})
				}
				for i := 0; i < 20; i++ {
					top := tb.B + i*50
					add(&ui.Node{Text: fmt.Sprintf("After %d", i), Bounds: ui.Rect{T: top, R: 400, B: top + 50}})
				}
				return nodes
			}
			f.onWait = func() { // the banner starts as the tap waits for a still screen
				if !tc.atTap && shiftAt.CompareAndSwap(0, time.Now().UnixNano()) {
					f.change(true)
				}
			}
			f.onSwipe = func(x1, y1, x2, y2 int) {
				// The list ends with its last row at the bottom: 100 + 11 rows, the banner,
				// the target and 20 rows, less the screen.
				o := max(0, min(off.Load()+int64(y1-y2), 100+11*50+200+50+20*50-800))
				if o != off.Load() {
					off.Store(o)
					f.change(true)
				}
			}
			var hits []bool // per tap: whether it landed on the target, where it is then
			f.clicked = func(x, y int) *bool {
				if tc.atTap && shiftAt.CompareAndSwap(0, time.Now().UnixNano()) {
					f.change(true)
				}
				tb, there := target()
				hit := there && tb.Intersect(screen).Contains(x, y)
				hits = append(hits, hit)
				return &hit
			}
			if tc.late {
				f.onTap = func(x, y int) { f.clicks.Add(1) }
			}
			o := Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}
			start := time.Now()
			res := runWith(t, f, o, "- tapOn: {id: anim_target}")
			f.mu.Lock()
			defer f.mu.Unlock()
			if tc.gone {
				mustFail(t, res, "anim_target")
				if len(f.taps) != 0 {
					t.Errorf("taps %v: want none, the target is gone", f.taps)
				}
				if d := time.Since(start); d > 8*time.Second {
					t.Errorf("took %s: the search for it is not bounded", d)
				}
				return
			}
			mustPass(t, res)
			if tc.late {
				if len(f.taps) != 1 || len(f.swipeAt) != 0 {
					t.Fatalf("late click: taps %v, swipes %v; want no recovery", f.taps, f.swipeAt)
				}
				return
			}
			wantTaps := 1
			if tc.atTap {
				wantTaps = 2
			}
			if len(f.taps) != wantTaps || !hits[len(hits)-1] {
				t.Fatalf("taps %v (on the target: %v), want %d taps ending on the target brought back into view", f.taps, hits, wantTaps)
			}
			if len(f.swipeAt) == 0 {
				t.Errorf("no drag: want the list dragged to bring the target back")
			}
			if tb, _ := target(); tb.T < 0 || tb.B > 800 {
				t.Errorf("target at %v when tapped: want it whole on screen", tb)
			}
		})
	}
}

// A footer handler can append rows and hide its button before Android reports
// TYPE_VIEW_CLICKED. The row now covering the tap point is already the result.
func TestTapFooterReplacedByRows(t *testing.T) {
	f := &fakeDriver{}
	loaded := false
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{{Class: "ScrollView", ID: "items", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		n := &ui.Node{Depth: 1, Class: "Button", ID: "footer", Text: "More", Clickable: true, Enabled: true, Bounds: ui.Rect{L: 100, T: 650, R: 300, B: 750}}
		if loaded {
			n = &ui.Node{Depth: 1, Class: "TextView", ID: "row", Text: "New row", Clickable: true, Enabled: true, Bounds: ui.Rect{T: 650, R: 400, B: 750}}
		}
		return append(nodes, n)
	}
	f.clicked = func(x, y int) *bool { no := false; return &no }
	f.onTap = func(x, y int) { loaded = true; f.change(true) }
	res := runWith(t, f, Options{LookupTimeout: time.Second}, "- tapOn: {id: footer}\n- assertVisible: {id: row}")
	mustPass(t, res)
	if len(f.taps) != 1 || len(f.swipeAt) != 0 {
		t.Fatalf("taps=%v swipes=%v", f.taps, f.swipeAt)
	}
}

// AND-002: opening content moves a toggle with an unchanged ID/label. A
// successful tap must not be replayed just because Android reports no click.
func TestTapDoesNotReplayACompletedToggleAssertion(t *testing.T) {
	f := &fakeDriver{}
	open := false
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		top := 300
		if open {
			nodes = append(nodes, &ui.Node{Depth: 1, Class: "TextView", ID: "more-text", Text: "The whole story", Bounds: ui.Rect{T: 300, R: 400, B: 420}})
			top = 420
		}
		n := btn("Show more", "more-toggle", top)
		n.Depth = 1
		return append(nodes, n)
	}
	f.clicked = func(_, _ int) *bool { no := false; return &no }
	f.onTap = func(_, _ int) { open = !open; f.change(true) }
	mustPass(t, runWith(t, f, quick, `
- tapOn: {id: more-toggle}
- assertVisible: {id: more-text}
- tapOn: {id: more-toggle}
- assertNotVisible: {id: more-text}
- tapOn: {id: more-toggle, retryTapIfNoChange: false}
- assertVisible: {id: more-text}
`))
	if len(f.taps) != 3 {
		t.Fatalf("three requested toggles sent %d taps: %v", len(f.taps), f.taps)
	}
}

// AND-004: a transparent one-touch catcher can swallow the reference-field
// tap while the amount field retains focus. Never send reference text there.
func TestInputRefocusesRequestedFieldBeforeTyping(t *testing.T) {
	for _, canFocus := range []bool{true, false} {
		t.Run(fmt.Sprintf("canFocus=%v", canFocus), func(t *testing.T) {
			amount, reference := "40", ""
			focusedReference := false
			touches := 0
			f := &fakeDriver{tapSettle: driver.NoChange}
			f.screen = func() []*ui.Node {
				a, b := btn(amount, "amount", 100), btn(reference, "reference", 300)
				a.Editable, b.Editable = true, true
				a.Focused, b.Focused = !focusedReference, focusedReference
				return []*ui.Node{a, b}
			}
			f.onTap = func(_, _ int) {
				touches++
				if touches > 1 && canFocus {
					focusedReference = true
					f.change(true)
				}
			}
			f.onType = func(text string) {
				if focusedReference {
					reference += text
				} else {
					amount += text
				}
				f.change(true)
			}
			res := runWith(t, f, quick, `
- tapOn: {id: reference}
- inputText: rent
- assertVisible: {id: amount, text: '40'}
- assertVisible: {id: reference, text: rent}
`)
			if canFocus {
				mustPass(t, res)
				if reference != "rent" || amount != "40" || touches != 2 {
					t.Fatalf("amount=%q reference=%q touches=%d", amount, reference, touches)
				}
			} else {
				if res.Err == nil || len(f.typed) != 0 || touches > 2 {
					t.Fatalf("typed into wrong field: amount=%q keys=%v touches=%d error=%v", amount, f.typed, touches, res.Err)
				}
			}
		})
	}
}

// AND-002: an assertion already true before the tap proves nothing about it.
// Expressions must also retain their normal evaluation order and side effects.
func TestTapAssertionDoesNotConfirmAnUnchangedOrDynamicOutcome(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		t.Run(fmt.Sprintf("dynamic=%v", dynamic), func(t *testing.T) {
			f := &fakeDriver{}
			e := js.New("android")
			shifted := false
			f.screen = func() []*ui.Node {
				nodes := []*ui.Node{{Text: "Ready", Bounds: ui.Rect{R: 400, B: 50}}}
				if shifted {
					return append(nodes, &ui.Node{Text: "Banner", Bounds: ui.Rect{T: 580, R: 400, B: 680}}, btn("Target", "target", 700))
				}
				return append(nodes, btn("Target", "target", 600))
			}
			calls := 0
			f.clicked = func(_, _ int) *bool { calls++; hit := calls > 1; return &hit }
			f.onTap = func(_, _ int) {
				if dynamic && e.Output()["checked"] != nil {
					t.Error("evaluated the following assertion before tap/recovery completed")
				}
				shifted = true
				f.change(true)
			}
			assertion := "- assertVisible: Ready"
			if dynamic {
				assertion = `- assertVisible: "${(output.checked = (output.checked || 0) + 1, 'Ready')}"`
			}
			opts := quick
			opts.JS = e
			mustPass(t, runWith(t, f, opts, "- tapOn: {id: target}\n"+assertion))
			if len(f.taps) != 2 {
				t.Fatalf("an unchanged/dynamic assertion suppressed genuine recovery: %v", f.taps)
			}
		})
	}
}

// Android may send TYPE_VIEW_CLICKED after the action reply but before recovery
// starts. Once that event is known, rereading and settling the tree cannot justify
// another tap. Everyday's Compose buttons used to pay for those reads after each click.
func TestLateAndroidClickSkipsRecoveryReads(t *testing.T) {
	f := &fakeDriver{plat: "android", screen: static(btn("Target", "target", 100))}
	f.clicks.Store(1)
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	before := int64(0)
	again, err := r.tapMovedTarget(context.Background(), sel, btn("Target", "target", 100), nil, 200, 125, &before, driver.ActionOpts{})
	if err != nil || again {
		t.Fatalf("recovery = %v, %v", again, err)
	}
	if f.reads.Load() != 0 || f.waits.Load() != 0 {
		t.Fatalf("confirmed click performed %d reads and %d settle waits", f.reads.Load(), f.waits.Load())
	}
}

func TestAndroidTapStateConfirmsDeliveryWithoutMotionRecovery(t *testing.T) {
	for _, kind := range []string{"checkbox", "field", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			was := btn("Target", "target", 100)
			now := btn("Target", "target", 100)
			if kind == "checkbox" {
				was.Checkable = true
				now.Checkable = true
				now.Checked = true
			} else if kind == "field" {
				was.Editable = true
				now.Editable = true
				now.Focused = true
			} else {
				now.Enabled = false
			}
			f := &fakeDriver{plat: "android", screen: static(was)}
			before, err := f.Screen(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			f.screen = static(now)
			f.reads.Store(0)
			r := New(f, quick)
			sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
			if err != nil {
				t.Fatal(err)
			}
			count := int64(0)
			again, err := r.tapMovedTarget(context.Background(), sel, was, before, 200, 125, &count, driver.ActionOpts{})
			if err != nil || again {
				t.Fatalf("recovery = %v, %v", again, err)
			}
			if f.reads.Load() != 1 || f.waits.Load() != 0 {
				t.Fatalf("delivery proof required %d reads and %d waits", f.reads.Load(), f.waits.Load())
			}
		})
	}
}

// A newly disabled control displaced off the injected point is still ambiguous:
// a layout shift may have caused the original touch to miss it.
func TestDisabledDisplacedTargetStillTakesRecoveryChecks(t *testing.T) {
	was := btn("Target", "target", 100)
	now := btn("Target", "target", 300)
	now.Enabled = false
	f := &fakeDriver{plat: "android", screen: static(was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.screen = static(now)
	f.reads.Store(0)
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	_, err = r.tapMovedTarget(context.Background(), sel, was, before, 200, 125, &count, driver.ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if f.reads.Load() <= 1 || f.waits.Load() == 0 {
		t.Fatalf("displaced target skipped recovery: reads=%d waits=%d", f.reads.Load(), f.waits.Load())
	}
}

// A navigation action removes its own button. Recovery should inspect the settled
// result once, without spending a lookup timeout searching the screen it left.
func TestAndroidRecoveryDoesNotWaitForRemovedNavigationButton(t *testing.T) {
	was := btn("Menu", "menu", 100)
	f := &fakeDriver{plat: "android", screen: static(was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.screen = static(btn("Help", "help", 300))
	f.reads.Store(0)
	r := New(f, quick)
	r.unsettled = time.Second
	sel, err := r.resolveSelector(&flow.Selector{ID: "menu"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 125, &count, driver.ActionOpts{})
	if err != nil || again {
		t.Fatalf("recovery = %v,%v", again, err)
	}
	if ctx.Err() != nil {
		t.Fatalf("recovery exhausted its context waiting for a removed button: %v", ctx.Err())
	}
	if f.reads.Load() != 1 {
		t.Fatalf("removed button caused %d recovery reads", f.reads.Load())
	}
}

// Different routes can expose an identically shaped anonymous ScrollView. Once
// a tap removes its target and changes the screen context, it must not scroll
// the new route trying to recover a control from the old one.
func TestAndroidRecoveryDoesNotScrollANewAnonymousList(t *testing.T) {
	was := &ui.Node{Depth: 1, Class: "Button", ID: "scene-still", Text: "Still", Clickable: true,
		Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}
	f := &fakeDriver{plat: "android", screen: static(
		&ui.Node{ID: "heading", Text: "Choose scene", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}}, was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.screen = static(
		&ui.Node{ID: "heading", Text: "Still scene", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		&ui.Node{Depth: 1, Text: "Nothing moves", Bounds: ui.Rect{T: 100, R: 400, B: 150}})
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "scene-still"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 425, &count, driver.ActionOpts{})
	if err != nil || again || len(f.swipeAt) != 0 || ctx.Err() != nil {
		t.Fatalf("recovered an old route's control: again=%v error=%v swipes=%v context=%v", again, err, f.swipeAt, ctx.Err())
	}
}

// Navigation can commit after the first post-tap read. The context boundary
// must also apply to the later read that reports the old target missing.
func TestAndroidRecoveryDoesNotScrollAfterDelayedRouteChange(t *testing.T) {
	was := &ui.Node{Depth: 1, Class: "Button", ID: "key-4", Text: "4", Clickable: true,
		Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}
	old := static(
		&ui.Node{Text: "Security", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}}, was)
	f := &fakeDriver{screen: old}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	f.screen = func() []*ui.Node {
		reads++
		f.seq++
		if reads == 1 {
			return old()
		}
		return static(
			&ui.Node{Text: "Profile", Bounds: ui.Rect{R: 400, B: 50}},
			&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}})()
	}
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "key-4"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 425, &count, driver.ActionOpts{})
	if err != nil || again || len(f.swipeAt) != 0 || ctx.Err() != nil {
		t.Fatalf("recovered after delayed navigation: again=%v error=%v swipes=%v context=%v", again, err, f.swipeAt, ctx.Err())
	}
}

// AND-001: the keypad can disappear only after stabilization has already
// observed it. Keeping the last-seen node must not hide the new route.
func TestRecoveryStopsWhenRouteChangesDuringStabilization(t *testing.T) {
	was := &ui.Node{Depth: 1, Class: "Button", ID: "key-4", Text: "4", Clickable: true,
		Enabled: true, Bounds: ui.Rect{T: 400, R: 400, B: 450}}
	old := static(
		&ui.Node{Text: "Security", Bounds: ui.Rect{R: 400, B: 50}},
		&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}}, was)
	f := &fakeDriver{screen: old}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	f.screen = func() []*ui.Node {
		reads++
		f.seq++
		if reads <= 3 {
			return old()
		}
		return static(
			&ui.Node{Text: "Profile", Bounds: ui.Rect{R: 400, B: 50}},
			&ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}})()
	}
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "key-4"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	again, err := r.tapMovedTarget(ctx, sel, was, before, 200, 425, &count, driver.ActionOpts{Timeout: 100 * time.Millisecond})
	if err != nil || again || len(f.swipeAt) != 0 || ctx.Err() != nil {
		t.Fatalf("recovered an obsolete keypad: again=%v error=%v swipes=%v context=%v", again, err, f.swipeAt, ctx.Err())
	}
}

// Focus retained from before a tap cannot prove that the tap reached a field
// with a custom click handler. Keep the ordinary motion checks in that case.
func TestAlreadyFocusedFieldStillTakesRecoveryChecks(t *testing.T) {
	was := btn("Target", "target", 100)
	was.Editable, was.Focused = true, true
	f := &fakeDriver{plat: "android", screen: static(was)}
	before, err := f.Screen(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.reads.Store(0)
	r := New(f, quick)
	sel, err := r.resolveSelector(&flow.Selector{ID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	_, err = r.tapMovedTarget(context.Background(), sel, was, before, 200, 125, &count, driver.ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if f.waits.Load() == 0 {
		t.Fatal("existing focus was mistaken for a new interaction result")
	}
}
