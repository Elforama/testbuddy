package engine

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/js"
	"github.com/elforama/testbuddy/internal/ui"
)

func TestTapAgainAfterTargetMovedFromUnderIt(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	for _, tc := range []struct {
		name     string
		plat     string
		clicked  *bool
		moves    bool
		replaced bool
		late     bool
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
					label.Class, label.Bounds.B = "TextView", 320
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
					shifted.Store(true)
				}
				if tc.late {
					f.clicks.Add(1)
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

func TestTapNotRepeatedWhenItsOwnEffectMovesTarget(t *testing.T) {
	t.Parallel()
	no := false
	for _, tc := range []struct {
		name     string
		before   bool
		known    bool
		byText   bool
		settle   driver.SettleResult
		wantTaps int
	}{
		{"slid by the tap's own effect, unseen", false, true, false, driver.NoChange, 1},
		{"slid by the tap's own effect, seen after the touch", false, true, false, driver.Changed, 1},
		{"slid by the tap's own effect, no id", false, true, true, driver.NoChange, 1},
		{"shifted just before the touch, reported after the lift", true, true, false, driver.NoChange, 2},
		{"shifted just before the touch, more change after", true, true, false, driver.Changed, 2},
		{"shifted just before the touch, no id", true, true, true, driver.NoChange, 2},
		{"unknown where it was at the touch", false, false, false, driver.NoChange, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{tapSettle: tc.settle}
			var moved atomic.Bool
			toggle := func() ui.Rect {
				if moved.Load() {
					return ui.Rect{L: 310, T: 50, R: 360, B: 100}
				}
				return ui.Rect{L: 10, T: 50, R: 60, B: 100}
			}
			f.screen = func() []*ui.Node {
				b := toggle()
				t := &ui.Node{Class: "Button", Desc: "Show navigation menu", ID: "drawer_toggle", Depth: 1, Enabled: true, Clickable: true, Bounds: b}
				if tc.byText {
					t.ID, t.Desc, t.Text = "", "", "Menu"
				}
				return []*ui.Node{
					{Class: "ViewGroup", Bounds: ui.Rect{L: b.L - 10, R: 400, B: 800}},
					t,
					{Class: "TextView", Text: "Home", Depth: 1, Bounds: ui.Rect{L: b.R + 10, T: 50, R: min(400, b.R+210), B: 100}},
				}
			}
			var at ui.Rect
			var taps atomic.Int32
			f.clicked = func(x, y int) *bool {
				if tc.before && taps.Add(1) == 1 {
					moved.Store(true)
				}
				at = toggle()
				return &no
			}
			f.onTap = func(x, y int) {
				if !tc.before {
					moved.Store(!moved.Load())
				}
				f.change(true)
			}
			f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
				f.mu.Lock()
				aim := f.tapOpts[n-1].Aim
				f.mu.Unlock()
				a := at
				if tc.known && aim != nil && aim.Window == 0 && aim.Ref == 2 && aim.Read != 0 {
					res.AimAt = &a
				}
				return res
			}
			step := "- tapOn: {id: drawer_toggle}"
			if tc.byText {
				step = "- tapOn: Menu"
			}
			mustPass(t, runWith(t, f, quick, step))
			f.mu.Lock()
			defer f.mu.Unlock()
			if a := f.tapOpts[0].Aim; a == nil || a.Ref != 2 || a.Read == 0 {
				t.Errorf("the tap aimed at %+v, want the toggle, the second node of the read", a)
			}
			if len(f.taps) != tc.wantTaps {
				t.Fatalf("taps %v, want %d", f.taps, tc.wantTaps)
			}
			if tc.wantTaps == 2 && f.taps[1] != [2]int{335, 75} {
				t.Errorf("second tap %v, want the toggle where it is now (335, 75)", f.taps[1])
			}
		})
	}
}

func TestTapBringsBackTargetPushedOutOfSight(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		jump  bool
		gone  bool
		atTap bool
		late  bool
		toast bool
		snack bool
	}{
		{"pushed below the edge", false, false, false, false, false, false},
		{"pushed out between two reads", true, false, false, false, false, false},
		{"pushed out, then gone", false, true, false, false, false, false},
		{"pushed out as the tap lands", true, false, true, false, false, false},
		{"pushed out with a late click", true, false, true, true, false, false},
		{"pushed out, a toast goes as it is dragged back", true, false, true, false, true, false},
		{"pushed out, a snackbar goes as it is dragged back", true, false, true, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{}
			var off atomic.Int64
			var dragged atomic.Bool
			var shiftAt atomic.Int64
			const targetAt = 650
			banner := func() int {
				at := shiftAt.Load()
				if at == 0 {
					return 0
				}
				if tc.jump {
					return 200
				}
				return min(200, int(time.Since(time.Unix(0, at)).Milliseconds())*200/300)
			}
			target := func() (ui.Rect, bool) {
				b := banner()
				top := targetAt + b - int(off.Load())
				return ui.Rect{T: top, R: 400, B: top + 50}, !(tc.gone && b >= 200)
			}
			screen := ui.Rect{R: 400, B: 800}
			f.screen = func() []*ui.Node {
				nodes := []*ui.Node{{Class: "ScrollView", Scrollable: true, Bounds: screen}}
				add := func(n *ui.Node) {
					n.Bounds = n.Bounds.Intersect(screen)
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
				if tc.snack && !dragged.Load() {
					nodes = append(nodes, &ui.Node{Class: "TextView", ID: "snackbar_text", Text: "Saved", Bounds: ui.Rect{T: 760, R: 400, B: 800}})
				}
				return nodes
			}
			f.onWait = func() {
				if !tc.atTap && shiftAt.CompareAndSwap(0, time.Now().UnixNano()) {
					f.change(true)
				}
			}
			if tc.toast {
				f.sysAlert = func() []*ui.Node {
					if dragged.Load() {
						return nil
					}
					return []*ui.Node{{Class: "android.widget.Toast", Text: "Saved", Bounds: ui.Rect{L: 100, T: 650, R: 300, B: 700}}}
				}
			}
			f.onSwipe = func(x1, y1, x2, y2 int) {
				dragged.Store(true)
				o := max(0, min(off.Load()+int64(y1-y2), 100+11*50+200+50+20*50-800))
				if o != off.Load() {
					off.Store(o)
					f.change(true)
				}
			}
			var hits []bool
			var aimAt *ui.Rect
			f.clicked = func(x, y int) *bool {
				if tc.atTap && shiftAt.CompareAndSwap(0, time.Now().UnixNano()) {
					f.change(true)
				}
				tb, there := target()
				hit := there && tb.Intersect(screen).Contains(x, y)
				hits = append(hits, hit)
				aimAt = nil
				if b := tb.Intersect(screen); there && !b.Empty() {
					aimAt = &b
				}
				return &hit
			}
			f.tapReply = func(n int, res driver.ActionResult) driver.ActionResult {
				res.AimAt = aimAt
				return res
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

func TestTapDoesNotReplayWhenAdjacentResultChanged(t *testing.T) {
	f := &fakeDriver{}
	count, calls := 0, 0
	shifted := false
	f.screen = func() []*ui.Node {
		top := 300
		if shifted {
			top = 500
		}
		n := btn("Tap", "action", top)
		n.Depth = 1
		return []*ui.Node{
			{Class: "ViewGroup", Bounds: ui.Rect{R: 400, B: 800}}, n,
			{Depth: 1, Class: "TextView", ID: "result", Text: fmt.Sprintf("Count: %d", count), Bounds: ui.Rect{T: top + 60, R: 400, B: top + 100}},
		}
	}
	f.clicked = func(_, _ int) *bool { no := false; return &no }
	f.onTap = func(_, _ int) {
		calls++
		count++
		if calls == 2 {
			shifted = true
		}
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, `
- tapOn: {id: action}
- tapOn: {id: action}
- tapOn: {id: action}
- assertVisible: 'Count: 3'
`))
	if calls != 3 {
		t.Fatalf("three requested actions sent %d taps", calls)
	}
}

func TestTapRecoversShiftWithUnchangedAdjacentResult(t *testing.T) {
	f := &fakeDriver{}
	shifted := false
	count, calls := 0, 0
	f.screen = func() []*ui.Node {
		top, ticker := 300, "Tick: 0"
		if shifted {
			top, ticker = 500, "Tick: 1"
		}
		n := btn("Tap", "action", top)
		n.Depth = 1
		return []*ui.Node{
			{Class: "ViewGroup", Bounds: ui.Rect{R: 400, B: 800}},
			{Depth: 1, Class: "TextView", ID: "ticker", Text: ticker, Bounds: ui.Rect{R: 400, B: 50}}, n,
			{Depth: 1, Class: "TextView", ID: "result", Text: fmt.Sprintf("Count: %d", count), Bounds: ui.Rect{T: top + 60, R: 400, B: top + 100}},
		}
	}
	f.clicked = func(_, _ int) *bool { no := false; return &no }
	f.onTap = func(_, _ int) {
		calls++
		if calls == 1 {
			shifted = true
		} else {
			count++
		}
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, "- tapOn: {id: action}\n- assertVisible: 'Count: 1'"))
	if calls != 2 {
		t.Fatalf("lost tap was not recovered: %d touches", calls)
	}
}

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

func benchList141(gone bool, shift, shrink int) []*ui.Node {
	nodes := []*ui.Node{
		{Class: "TextView", ID: "scenarios_title", Text: "Scenarios", Bounds: ui.Rect{R: 400, B: 50}},
		{Class: "ScrollView", ID: "scenarios", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		{Depth: 1, Class: "LinearLayout", Bounds: ui.Rect{T: 50, R: 400, B: 800 - shrink}},
	}
	for i, name := range []string{"journey", "slow", "permissions", "overlays", "controls", "navigation", "onboarding", "lists"} {
		top := 50 + i*61
		if i >= 5 {
			top += shift
		}
		if !(name == "navigation" && gone) {
			nodes = append(nodes, &ui.Node{Depth: 2, Class: "Button", ID: "scenario_" + name, Text: name,
				Clickable: true, Enabled: true, Bounds: ui.Rect{T: top, R: 400, B: top + 60}})
		}
		nodes = append(nodes, &ui.Node{Depth: 2, Class: "View", Bounds: ui.Rect{T: top + 60, R: 400, B: top + 61}})
	}
	return nodes
}

func benchOpened141() []*ui.Node {
	return []*ui.Node{
		{Class: "Button", ID: "back", Text: "Back", Clickable: true, Enabled: true, Bounds: ui.Rect{R: 100, B: 50}},
		{Class: "TextView", ID: "scenario_title", Text: "Navigation and back stack", Bounds: ui.Rect{L: 100, R: 400, B: 50}},
		{Class: "ScrollView", ID: "page", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		{Depth: 1, Class: "LinearLayout", Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		{Depth: 2, Class: "TextView", ID: "nav_depth", Text: "Depth 0", Bounds: ui.Rect{T: 60, R: 400, B: 100}},
	}
}

func TestTapRowThatVanishesInPlaceIsNotScrolledBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		back   bool
		shrink int
		notCI  bool
	}{
		{"the screen opens", false, 0, false},
		{"the screen opens, the column shrinks", false, 61, false},
		{"listed again a moment later", true, 0, false},
		{"the screen opens, not in CI", false, 0, true},
	} {
		back, shrink := tc.back, tc.shrink
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", map[bool]string{true: "0", false: "1"}[tc.notCI])
			f := &fakeDriver{plat: "android", tapSettle: driver.NoChange}
			var tappedAt atomic.Int64
			var after atomic.Int64
			const opensAfter = 800 * time.Millisecond
			f.screen = func() []*ui.Node {
				at := tappedAt.Load()
				switch {
				case at == 0:
					return benchList141(false, 0, 0)
				case back:
					return benchList141(after.Add(1) == 1, 0, 0)
				case time.Since(time.Unix(0, at)) >= opensAfter:
					return benchOpened141()
				}
				return benchList141(true, 0, shrink)
			}
			no := false
			f.clicked = func(_, _ int) *bool { return &no }
			f.onTap = func(_, _ int) {
				if !tappedAt.CompareAndSwap(0, time.Now().UnixNano()) {
					return
				}
				f.change(true)
				if !back {
					go func() {
						time.Sleep(opensAfter)
						f.change(true)
					}()
				}
			}
			o := Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}
			steps := "- tapOn: {id: scenario_navigation}\n- assertVisible: {id: scenario_title}"
			if back {
				steps = "- tapOn: {id: scenario_navigation}"
			}
			res := runWith(t, f, o, steps)
			f.mu.Lock()
			defer f.mu.Unlock()
			if tc.notCI {
				mustFail(t, res, "did not bring it back")
				if len(f.taps) != 1 || len(f.swipeAt) == 0 {
					t.Fatalf("taps %v, drags %v: outside CI want one tap and a drag back, as before #141's fix", f.taps, f.swipeAt)
				}
				return
			}
			mustPass(t, res)
			if len(f.taps) != 1 || len(f.swipeAt) != 0 {
				t.Fatalf("taps %v, drags %v: want one tap and no drag (the row went in place, as the tap took effect)", f.taps, f.swipeAt)
			}
		})
	}
}

func TestBringBackStopsWhenTheScreenChanges(t *testing.T) {
	t.Setenv("TB_CI", "1")
	settings := func() []*ui.Node {
		nodes := []*ui.Node{
			{Class: "TextView", ID: "settings_title", Text: "Settings", Bounds: ui.Rect{R: 400, B: 50}},
			{Class: "ScrollView", ID: "scenarios", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
			{Depth: 1, Class: "LinearLayout", Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		}
		for i, name := range []string{"sound", "display", "storage"} {
			top := 50 + i*61
			nodes = append(nodes, &ui.Node{Depth: 2, Class: "Button", ID: "setting_" + name, Text: name,
				Clickable: true, Enabled: true, Bounds: ui.Rect{T: top, R: 400, B: top + 60}})
		}
		return nodes
	}
	for _, tc := range []struct {
		name   string
		opened func() []*ui.Node
		wait   string
	}{
		{"its list goes", benchOpened141, "scenario_title"},
		{"the same list, other labels", settings, "settings_title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "android", tapSettle: driver.NoChange}
			var tapped, opened atomic.Bool
			f.screen = func() []*ui.Node {
				switch {
				case opened.Load():
					return tc.opened()
				case tapped.Load():
					return benchList141(true, 30, 0)
				}
				return benchList141(false, 0, 0)
			}
			no := false
			f.clicked = func(_, _ int) *bool { return &no }
			f.onTap = func(_, _ int) { tapped.Store(true); f.change(true) }
			f.onSwipe = func(_, _, _, _ int) { opened.Store(true); f.change(true) }
			o := Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}
			res := runWith(t, f, o, "- tapOn: {id: scenario_navigation}\n- assertVisible: {id: "+tc.wait+"}")
			f.mu.Lock()
			defer f.mu.Unlock()
			mustPass(t, res)
			if len(f.taps) != 1 || len(f.swipeAt) != 1 {
				t.Fatalf("taps %v, drags %v: want one tap, and one drag before the screen changed", f.taps, f.swipeAt)
			}
		})
	}
}

type swiftUIDriver struct {
	*fakeDriver
	clicked bool
	changed bool
}

func (d *swiftUIDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	res, err := d.fakeDriver.Tap(ctx, x, y, o)
	if err != nil || o.Target == nil {
		return res, err
	}
	res.Guarded, res.Probed, res.Targeted, res.Touch = true, true, true, "ended"
	res.Clicked, res.ClicksBefore = nil, nil
	res.ChangedSinceRead = d.changed
	if d.clicked {
		yes := true
		res.Clicked = &yes
	}
	return res, nil
}

func TestIOSTapOnButtonShiftedJustBeforeTheTouch(t *testing.T) {
	type effect int
	const (
		none effect = iota
		status
		result
		newButton
		newRow
	)
	const shiftedAway = "tap at (200, 325): the target moved from under the tap (now at [0,520][400,570]); the app may not have taken it"
	for _, tc := range []struct {
		name       string
		effect     effect
		shift      bool
		clicked    bool
		notCI      bool
		quiet      bool
		stale      bool
		bannerOver bool
		flow       string
		fail       string
	}{
		{name: "shifted just before the touch, not in CI", shift: true, notCI: true,
			flow: "- tapOn: {id: anim_target}"},
		{name: "shifted, no change seen before the touch", shift: true, quiet: true,
			flow: "- tapOn: {id: anim_target}"},
		{name: "shifted just before the touch", shift: true,
			flow: "- tapOn: {id: anim_target}\n- assertVisible: 'Target tapped'", fail: shiftedAway},
		{name: "shifted just before the touch, frames stale for 280 ms", shift: true, stale: true,
			flow: "- tapOn: {id: anim_target}\n- assertVisible: 'Target tapped'", fail: shiftedAway},
		{name: "shifted just before the touch, no assertion next", shift: true,
			flow: "- tapOn: {id: anim_target}", fail: shiftedAway},
		{name: "shifted just before the touch, the banner's frame where it was", shift: true, bannerOver: true,
			flow: "- tapOn: {id: anim_target}"},
		{name: "took, then moved: the next assertion holds", effect: status, shift: true,
			flow: "- tapOn: {id: anim_target}\n- assertVisible: 'Target tapped'"},
		{name: "took, then moved: its result changed", effect: result, shift: true,
			flow: "- tapOn: {id: anim_target}"},
		{name: "took, then moved: a new button where it was", effect: newButton, shift: true,
			flow: "- tapOn: {id: anim_target}"},
		{name: "took, then moved: a new text row where it was", effect: newRow, shift: true,
			flow: "- tapOn: {id: anim_target}"},
		{name: "still under the point", effect: status,
			flow: "- tapOn: {id: anim_target}\n- assertVisible: 'Target tapped'"},
		{name: "shifted, a control confirmed it", shift: true, clicked: true,
			flow: "- tapOn: {id: anim_target}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", map[bool]string{true: "0", false: "1"}[tc.notCI])
			f := &fakeDriver{plat: "ios"}
			d := &swiftUIDriver{fakeDriver: f, clicked: tc.clicked, changed: !tc.quiet}
			var shifted, undo, row atomic.Bool
			var shiftAt atomic.Int64
			var statusText atomic.Value
			statusText.Store("Faded tapped")
			var count atomic.Int32
			realTop := func() int {
				if shifted.Load() {
					return 520
				}
				return 300
			}
			seen := func() bool {
				return shifted.Load() && (!tc.stale || time.Since(time.Unix(0, shiftAt.Load())) >= 280*time.Millisecond)
			}
			targetTop := func() int {
				if seen() {
					return 520
				}
				return 300
			}
			f.screen = func() []*ui.Node {
				nodes := []*ui.Node{
					{Class: "View", Bounds: ui.Rect{R: 400, B: 800}},
					{Depth: 1, Class: "StaticText", ID: "anim_status", Text: statusText.Load().(string), Bounds: ui.Rect{T: 100, R: 400, B: 140}},
				}
				switch {
				case undo.Load():
					u := btn("Undo", "undo", 300)
					u.Depth, u.Class = 1, "Button"
					nodes = append(nodes, u)
				case row.Load() && seen():
					nodes = append(nodes, &ui.Node{Depth: 1, Class: "StaticText", Text: "Row 1", Bounds: ui.Rect{T: 300, R: 60, B: 320}})
				case seen() && tc.bannerOver:
					nodes = append(nodes, &ui.Node{Depth: 1, Class: "StaticText", Text: "Banner", Bounds: ui.Rect{T: 300, R: 400, B: 500}})
				case seen():
					nodes = append(nodes, &ui.Node{Depth: 1, Class: "StaticText", Text: "Banner", Bounds: ui.Rect{L: 170, T: 390, R: 230, B: 410}})
				}
				target := btn("Target", "anim_target", targetTop())
				target.Depth, target.Class = 1, "Button"
				nodes = append(nodes, target)
				return append(nodes, &ui.Node{Depth: 1, Class: "StaticText", ID: "result",
					Text: fmt.Sprintf("Count: %d", count.Load()), Bounds: ui.Rect{T: targetTop() + 60, R: 400, B: targetTop() + 100}})
			}
			taps := 0
			f.onTap = func(x, y int) {
				taps++
				if taps == 1 {
					shiftAt.Store(time.Now().UnixNano())
					shifted.Store(tc.shift)
					switch tc.effect {
					case status:
						statusText.Store("Target tapped")
					case result:
						count.Add(1)
					case newButton:
						undo.Store(true)
					case newRow:
						row.Store(true)
					}
					if tc.effect == none && !tc.clicked && tc.shift {
						f.change(true)
						return
					}
				}
				top := realTop()
				if y >= top && y < top+50 {
					statusText.Store("Target tapped")
				}
				f.change(true)
			}
			var buf syncBuf
			o := quick
			o.Trace = NewTrace(&buf)
			start := time.Now()
			res := runWith(t, d, o, tc.flow)
			took := time.Since(start)
			if tc.fail != "" {
				mustFail(t, res, tc.fail)
			} else if res.Err != nil || res.Status != Passed {
				t.Fatalf("flow %s: %v; trace:\n%s", res.Status, res.Err, buf.String())
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.taps) != 1 {
				t.Fatalf("taps %v, want exactly 1 (never a second tap); trace:\n%s", f.taps, buf.String())
			}
			if (tc.notCI || tc.quiet) && (took >= iosLayoutLag || strings.Contains(buf.String(), "the target is now elsewhere")) {
				t.Errorf("the tap waited %s or looked again (want no wait: outside CI, or no change before the touch):\n%s", took, buf.String())
			}
			if tc.fail != "" && !strings.Contains(buf.String(), "the target is now elsewhere, at [0,520][400,570], and nothing shows the tap took") {
				t.Errorf("the trace does not say why the step failed:\n%s", buf.String())
			}
			if (tc.effect == newRow || tc.bannerOver) && !strings.Contains(buf.String(), "new content where the target was") {
				t.Errorf("the trace does not say the new content let the tap stand:\n%s", buf.String())
			}
		})
	}
}

func TestBringBackOutsideCIScrollsAsBefore(t *testing.T) {
	t.Setenv("TB_CI", "0")
	f := &fakeDriver{plat: "android", tapSettle: driver.NoChange}
	var tapped, opened atomic.Bool
	f.screen = func() []*ui.Node {
		switch {
		case opened.Load():
			return benchOpened141()
		case tapped.Load():
			return benchList141(true, 30, 0)
		}
		return benchList141(false, 0, 0)
	}
	no := false
	f.clicked = func(_, _ int) *bool { return &no }
	f.onTap = func(_, _ int) { tapped.Store(true); f.change(true) }
	f.onSwipe = func(_, _, _, _ int) { opened.Store(true); f.change(true) }
	o := Options{LookupTimeout: 2 * time.Second, OptionalTimeout: time.Second}
	res := runWith(t, f, o, "- tapOn: {id: scenario_navigation}\n- assertVisible: {id: scenario_title}")
	f.mu.Lock()
	defer f.mu.Unlock()
	mustFail(t, res, "did not bring it back")
	if len(f.swipeAt) < 2 {
		t.Fatalf("drags %v: outside CI mode the scroll back should go on over the new screen", f.swipeAt)
	}
}
