package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

func TestTapAtScreenPoint(t *testing.T) {
	for _, tc := range []struct {
		point string
		want  [2]int
	}{
		{"10,20", [2]int{10, 20}},
		{"50%,50%", [2]int{200, 400}},
		{"0%,100%", [2]int{0, 800}},
		{" 25% , 10% ", [2]int{100, 80}},
		{"${'30,40'}", [2]int{30, 40}},
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

func TestTapAtPointInElementInterpolated(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 100))
	mustPass(t, runWith(t, f, quick, `- tapOn: {id: go, point: "${'10,5'}"}`))
	if f.taps[0] != [2]int{10, 105} {
		t.Errorf("taps = %v", f.taps)
	}
}

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
	f := &fakeDriver{}
	f.screen = static(plain)
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go}"))
	if f.tapOpts[0].Hold != 0 {
		t.Errorf("android hold %s, want 0", f.tapOpts[0].Hold)
	}
}

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

func TestTapRepeatTapsAreTimedByTheDelay(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go, repeat: 3, delay: 100}"))
	if len(f.tapOpts) != 3 {
		t.Fatalf("%d taps, want 3", len(f.tapOpts))
	}
	for i, o := range f.tapOpts {
		if o.InRepeat != (i > 0) {
			t.Errorf("tap %d: InRepeat %v, want %v", i, o.InRepeat, i > 0)
		}
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go}"))
	if o := f.tapOpts[len(f.tapOpts)-1]; o.InRepeat {
		t.Errorf("a single tap was marked InRepeat: %+v", o)
	}
}

func TestTapRepeatOnce(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go, repeat: 1}"))
	if len(f.taps) != 1 || f.tapOpts[0].NoSettle {
		t.Errorf("taps %v opts %+v", f.taps, f.tapOpts)
	}
}

func TestTapRepeatStopsOnError(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	f.fail("Tap", errors.New("injection failed"))
	mustFail(t, runWith(t, f, quick, "- tapOn: {id: go, repeat: 3, delay: 1}"), "injection failed")
	if len(f.taps) != 1 {
		t.Errorf("%d taps after the first failed", len(f.taps))
	}
}

func TestTapRepeatDelayCancelled(t *testing.T) {
	t.Parallel()
	for _, delay := range []int{1500, 0} {
		t.Run(fmt.Sprintf("delay %d", delay), func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{}
			f.screen = static(btn("Go", "go", 0))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.onTap = func(x, y int) { cancel() }
			src := fmt.Sprintf("appId: app\n---\n- tapOn: {id: go, repeat: 3, delay: %d}", delay)
			fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte(src))
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			res := New(f, quick).RunFlow(ctx, fl)
			d := time.Since(start)
			f.mu.Lock()
			taps := len(f.taps)
			f.mu.Unlock()
			if taps != 1 {
				t.Errorf("%d taps reached the driver, want 1: none after the run was cancelled", taps)
			}
			if !errors.Is(res.Err, context.Canceled) || res.Status != Failed {
				t.Errorf("flow %s, err %v: want failed with the context's error", res.Status, res.Err)
			}
			if d > time.Second {
				t.Errorf("cancelled repeated tap took %s: it waited out the delays", d)
			}
		})
	}
}

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
	f := &fakeDriver{tapSettle: driver.NoChange}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go}"))
	if len(f.taps) != 1 {
		t.Errorf("%d taps without retryTapIfNoChange", len(f.taps))
	}
}

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

func TestLongPressAtPoint(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- longPressOn: {point: '50%,25%'}"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 200} || f.tapKind[0] != "long" {
		t.Errorf("taps %v %v", f.taps, f.tapKind)
	}
}

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
	f2 := &fakeDriver{}
	f2.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f2, quick, "- tapOn: {id: go}\n- inputText: x"))
	want = []time.Duration{2 * time.Second, 5 * time.Second}
	if fmt.Sprint(f2.waitDur) != fmt.Sprint(want) {
		t.Errorf("default settle waits %v, want %v", f2.waitDur, want)
	}
}

func TestTapNoChangeDefersNothing(t *testing.T) {
	f := &fakeDriver{tapSettle: driver.NoChange}
	f.screen = static(btn("Go", "go", 0))
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: go}\n- inputText: x"))
	if n := f.waits.Load(); n != 1 {
		t.Errorf("%d settle waits, want 1", n)
	}
}

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

func TestTapDriverError(t *testing.T) {
	f := &fakeDriver{}
	f.screen = static(btn("Go", "go", 0))
	f.fail("Tap", errors.New("agent gone"))
	mustFail(t, runWith(t, f, quick, "- tapOn: {id: go}"), "agent gone")
}

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
			return nil
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

func TestTapAnyElement(t *testing.T) {
	f := &fakeDriver{}
	label := &ui.Node{Text: "Title", Bounds: ui.Rect{R: 400, B: 50}, Enabled: true}
	f.screen = static(label, btn("Go", "go", 100))
	mustPass(t, runWith(t, f, quick, "- tapOn: {optional: true}"))
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 125} {
		t.Errorf("taps %v, want the clickable one", f.taps)
	}
}

func TestTapLandsWhereElementEndsUp(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		appears bool
		moveTo  int
		want    int
	}{
		{"appeared late, slid in", true, 300, 325},
		{"moved while settling", false, 500, 525},
		{"vanished while settling", false, 0, 125},
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

func TestTapWaitsForTargetAtRest(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	start := time.Now()
	f.screen = func() []*ui.Node {
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

func TestTapAtPointWaitsForLastTapToSettle(t *testing.T) {
	for _, plat := range []string{"ios", "android"} {
		var mu sync.Mutex
		tooltip, presenting := false, false
		f := &fakeDriver{plat: plat}
		f.screen = func() []*ui.Node {
			mu.Lock()
			defer mu.Unlock()
			nodes := []*ui.Node{btn("Show tooltip", "show_tooltip", 100)}
			if tooltip {
				nodes = append(nodes, &ui.Node{Text: "Tip: long press for more", Bounds: ui.Rect{L: 0, T: 160, R: 200, B: 200}, Enabled: true})
			}
			return nodes
		}
		f.onTap = func(x, y int) {
			mu.Lock()
			defer mu.Unlock()
			switch {
			case y < 150 && !tooltip:
				tooltip, presenting = true, true
			case tooltip && !presenting:
				tooltip = false
			}
		}
		f.onWait = func() {
			mu.Lock()
			presenting = false
			mu.Unlock()
		}
		res := runWith(t, f, quick, `- tapOn: {id: show_tooltip}
- assertVisible: "Tip: long press for more"
- tapOn: {point: "90%,45%"}
- assertNotVisible: "Tip: long press for more"`)
		if res.Err != nil {
			t.Errorf("%s: %v (settle waits %v)", plat, res.Err, f.waitDur)
		}
	}
}
