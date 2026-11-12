package engine

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/js"
	"github.com/elforama/testbuddy/internal/ui"
)

// Android, a layout shift (bench/flows/scenarios/animations.yaml on CI's slow emulator): the
// target shows still in two reads, then a banner pushes it down just as the tap goes out. The
// tap reaches no view (none reports a click) and the target is now elsewhere: it is tapped
// again where it is. Not when a view took the click, when the target did not move (a Compose
// control reports no click: the tap may well have taken; issue #204's re-tap needs a window
// that changed just before the touch, TestTapUnconfirmedByAnUnrelatedRedrawIsRetried), when
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
			if tc.wantTaps == 2 && tc.moves && f.taps[1] != [2]int{200, 725} {
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
// Something beside the list going while it is dragged back (a toast timing out, a snackbar
// outside the list) does not stop the drag (issue #141 review): the target is still tapped.
func TestTapBringsBackTargetPushedOutOfSight(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		jump  bool // the banner shows at its full height at once
		gone  bool // the target is removed once pushed out of sight
		atTap bool // the layout shift starts only when input is injected
		late  bool // a delayed click report prevents recovery
		toast bool // a toast (its own system window) shows until the first drag
		snack bool // a snackbar outside the list shows until the first drag
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
			var off atomic.Int64     // how far the list is scrolled
			var dragged atomic.Bool  // the list was dragged
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
				if tc.snack && !dragged.Load() {
					nodes = append(nodes, &ui.Node{Class: "TextView", ID: "snackbar_text", Text: "Saved", Bounds: ui.Rect{T: 760, R: 400, B: 800}})
				}
				return nodes
			}
			f.onWait = func() { // the banner starts as the tap waits for a still screen
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

// AND-003: a timer moves a button after its handler increments an adjacent
// result label. Missing native click events do not justify a fourth action.
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

// AND-003: a new banner or an unrelated ticker above the control is not an
// action result. An unchanged adjacent result must still allow a lost tap retry.
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
			shifted = true // shift before delivery: the original coordinates missed
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

// benchList141 is the bench app's scenario list as the Android agent lists it: a title, the
// ScrollView, the column that holds the rows (unnamed, listed all the same), and per scenario
// a row button and a divider. gone leaves the navigation row out; shift moves the rows from
// the navigation one on down by that much (something grew above them); shrink makes the
// column that much shorter (the row's space given back).
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

// benchOpened141 is the navigation scenario's screen, which the tap on its row opens.
func benchOpened141() []*ui.Node {
	return []*ui.Node{
		{Class: "Button", ID: "back", Text: "Back", Clickable: true, Enabled: true, Bounds: ui.Rect{R: 100, B: 50}},
		{Class: "TextView", ID: "scenario_title", Text: "Navigation and back stack", Bounds: ui.Rect{L: 100, R: 400, B: 50}},
		{Class: "ScrollView", ID: "page", Scrollable: true, Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		{Depth: 1, Class: "LinearLayout", Bounds: ui.Rect{T: 50, R: 400, B: 800}},
		{Depth: 2, Class: "TextView", ID: "nav_depth", Text: "Depth 0", Bounds: ui.Rect{T: 60, R: 400, B: 100}},
	}
}

// Issue #141: Android, a tap on a row of a list (bench/flows/scenarios/steps/open.yaml, the
// scenario list, on CI's slow emulator). The app is behind: the tap's reply says nothing
// changed and no view has reported a click yet. A moment later the row is no longer listed,
// while the rest of its list is listed exactly as it was: the row did not move (no neighbour
// moved, nothing grew above it), the tap is taking effect (the screen it opens comes next).
// It must not be taken for a row pushed out of its list's view: tb scrolled the list "back"
// for 5 s, on the screen the tap had opened, and failed the step ("it went out of sight while
// moving"). The step passes, with one tap and no drag; the next step sees the new screen.
// A row missing for a moment only is found again, and not tapped twice.
func TestTapRowThatVanishesInPlaceIsNotScrolledBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		back   bool // listed again a moment later
		shrink int  // the column that holds the rows gets shorter as the row goes
	}{
		{"the screen opens", false, 0},
		{"the screen opens, the column shrinks", false, 61},
		{"listed again a moment later", true, 0},
	} {
		back, shrink := tc.back, tc.shrink
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: "android", tapSettle: driver.NoChange}
			var tappedAt atomic.Int64 // when the tap went out (0: not yet)
			var after atomic.Int64    // reads since the tap
			const opensAfter = 800 * time.Millisecond
			f.screen = func() []*ui.Node {
				at := tappedAt.Load()
				switch {
				case at == 0:
					return benchList141(false, 0, 0)
				case back:
					// Missing in the first read after the tap only (a node the agent could not read).
					return benchList141(after.Add(1) == 1, 0, 0)
				case time.Since(time.Unix(0, at)) >= opensAfter:
					return benchOpened141()
				}
				return benchList141(true, 0, shrink)
			}
			no := false
			f.clicked = func(_, _ int) *bool { return &no } // no click reported by the reply
			f.onTap = func(_, _ int) {
				if !tappedAt.CompareAndSwap(0, time.Now().UnixNano()) {
					return
				}
				f.change(true)
				if !back {
					go func() { // the new screen opens
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
			mustPass(t, res)
			if len(f.taps) != 1 || len(f.swipeAt) != 0 {
				t.Fatalf("taps %v, drags %v: want one tap and no drag (the row went in place, as the tap took effect)", f.taps, f.swipeAt)
			}
		})
	}
}

// Issue #141: when recovery does scroll to bring a target back (here the rows below it moved
// down, so it may have been pushed out of view), and the screen the tap opened comes while it
// scrolls, it stops there: no more drags on the new screen, and the tap step passes (the tap
// took effect; the next step judges it). In CI it went on dragging the new screen for 5 s and
// failed the tap.
func TestBringBackStopsWhenTheScreenChanges(t *testing.T) {
	t.Parallel()
	// settings: a screen of the same app with a list of the same shape (its list key is the
	// same) but none of the scenario list's labels: only its labels tell it apart.
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
			t.Parallel()
			f := &fakeDriver{plat: "android", tapSettle: driver.NoChange}
			var tapped, opened atomic.Bool
			f.screen = func() []*ui.Node {
				switch {
				case opened.Load():
					return tc.opened()
				case tapped.Load():
					return benchList141(true, 30, 0) // gone, and the rows below it moved down
				}
				return benchList141(false, 0, 0)
			}
			no := false
			f.clicked = func(_, _ int) *bool { return &no }
			f.onTap = func(_, _ int) { tapped.Store(true); f.change(true) }
			f.onSwipe = func(_, _, _, _ int) { opened.Store(true); f.change(true) } // the new screen opens
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
