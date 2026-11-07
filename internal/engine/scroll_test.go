package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

// A swipe in a direction crosses the screen; on iOS an upward swipe starts lower.
func TestSwipeDirection(t *testing.T) {
	for _, tc := range []struct {
		plat, dir string
		want      [4]int
	}{
		{"android", "UP", [4]int{200, 400, 200, 80}},
		{"ios", "UP", [4]int{200, 720, 200, 80}},
		{"android", "DOWN", [4]int{200, 160, 200, 720}},
		{"android", "LEFT", [4]int{360, 400, 40, 400}},
		{"android", "right", [4]int{40, 400, 360, 400}},
	} {
		t.Run(tc.plat+" "+tc.dir, func(t *testing.T) {
			f := &fakeDriver{screen: static(), plat: tc.plat, onSwipe: func(x1, y1, x2, y2 int) {}}
			mustPass(t, runWith(t, f, quick, "- swipe: {direction: "+tc.dir+"}"))
			if len(f.swipeAt) != 1 || f.swipeAt[0] != tc.want {
				t.Errorf("swipes %v, want %v", f.swipeAt, tc.want)
			}
			if f.swipeMs[0] != 400*time.Millisecond || f.swipes[0].Drag || f.swipes[0].NoSettle {
				t.Errorf("swipe %s %+v: want a plain 400 ms swipe", f.swipeMs[0], f.swipes[0])
			}
		})
	}
}

// A swipe's duration and settle timeout come from the command.
func TestSwipeOptions(t *testing.T) {
	f := &fakeDriver{screen: static(), onSwipe: func(x1, y1, x2, y2 int) {}}
	mustPass(t, runWith(t, f, quick, "- swipe: {direction: UP, duration: 1000, waitToSettleTimeoutMs: 250}"))
	if f.swipeMs[0] != time.Second || f.swipes[0].Timeout != 250*time.Millisecond {
		t.Errorf("swipe %s %+v", f.swipeMs[0], f.swipes[0])
	}
}

// A swipe from start to end: pixels or percentages; iOS keeps it on screen.
func TestSwipeStartEnd(t *testing.T) {
	for _, tc := range []struct {
		plat, start, end string
		want             [4]int
	}{
		{"android", "10,20", "30,40", [4]int{10, 20, 30, 40}},
		{"android", "10%,50%", "90%,50%", [4]int{40, 400, 360, 400}},
		{"android", "${'5,5'}", "500,900", [4]int{5, 5, 500, 900}},
		{"ios", "500,900", "0,0", [4]int{399, 799, 0, 0}},
		{"ios", "100%,100%", "0%,0%", [4]int{399, 799, 0, 0}},
	} {
		t.Run(tc.plat+" "+tc.start, func(t *testing.T) {
			f := &fakeDriver{screen: static(), plat: tc.plat, onSwipe: func(x1, y1, x2, y2 int) {}}
			mustPass(t, runWith(t, f, quick, fmt.Sprintf("- swipe: {start: %q, end: %q}", tc.start, tc.end)))
			if len(f.swipeAt) != 1 || f.swipeAt[0] != tc.want {
				t.Errorf("swipes %v, want %v", f.swipeAt, tc.want)
			}
		})
	}
	for _, tc := range []struct{ start, end, want string }{
		{"nope", "1,1", "invalid point"},
		{"1,1", "nope", "invalid point"},
		{"${(}", "1,1", "JavaScript error"},
		{"1,1", "${(}", "JavaScript error"},
	} {
		f := &fakeDriver{screen: static()}
		mustFail(t, runWith(t, f, quick, fmt.Sprintf("- swipe: {start: %q, end: %q}", tc.start, tc.end)), tc.want)
		if len(f.swipeAt) != 0 {
			t.Errorf("%s -> %s: swiped", tc.start, tc.end)
		}
	}
}

// A swipe from an element starts at its center (or the point in it) and goes toward the edge:
// to 10% from it, or farther, until it has crossed 70% of the element (a full-width pager
// turns a page only for a swipe past its middle).
func TestSwipeFromElement(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want [4]int
	}{
		{"{from: {id: card}, direction: UP}", [4]int{200, 325, 200, 80}},
		{"{from: {id: card}, direction: DOWN}", [4]int{200, 325, 200, 720}},
		{"{from: {id: card}, direction: LEFT}", [4]int{200, 325, 0, 325}},
		{"{from: {id: card}, direction: RIGHT}", [4]int{200, 325, 399, 325}},
		{"{from: Card, direction: LEFT}", [4]int{200, 325, 0, 325}},
		{"{from: {id: card, point: '10%,50%'}, direction: RIGHT}", [4]int{40, 325, 360, 325}},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			f := &fakeDriver{screen: static(btn("Card", "card", 300)), onSwipe: func(x1, y1, x2, y2 int) {}}
			mustPass(t, runWith(t, f, quick, "- swipe: "+tc.cmd))
			if len(f.swipeAt) != 1 || f.swipeAt[0] != tc.want {
				t.Errorf("swipes %v, want %v", f.swipeAt, tc.want)
			}
		})
	}
}

// A swipe from an element that is not there fails; an optional one warns.
func TestSwipeFromMissing(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	mustFail(t, runWith(t, f, quick, "- swipe: {from: {id: nope}, direction: UP}"), "Element not found: id: nope")
	res := runWith(t, f, quick, "- swipe: {from: {id: nope, optional: true}, direction: UP}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("status %s", res.Steps[0].Status)
	}
	if len(f.swipeAt) != 0 {
		t.Errorf("swiped %v", f.swipeAt)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static(btn("C", "card", 0))}, quick, "- swipe: {from: {id: card, point: '900,1'}, direction: UP}"), "outside the element")
	mustFail(t, runWith(t, &fakeDriver{screen: static(btn("C", "card", 0))}, quick, "- swipe: {from: {id: card, point: '${(}'}, direction: UP}"), "JavaScript error")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- swipe: {from: {id: '${(}'}, direction: UP}"), "JavaScript error")
}

// A swipe the device fails fails the command.
func TestSwipeError(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("Swipe", errors.New("touch lost"))
	mustFail(t, runWith(t, f, quick, "- swipe: {direction: UP}"), "touch lost")
	mustFail(t, runWith(t, f, quick, "- scroll"), "touch lost")
	mustFail(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Nope}"), "touch lost")
}

// scroll is a swipe up from the middle: 400 ms on Android, 333 ms on iOS.
func TestScroll(t *testing.T) {
	for plat, d := range map[string]time.Duration{"android": 400 * time.Millisecond, "ios": 333 * time.Millisecond} {
		f := &fakeDriver{screen: static(), plat: plat, onSwipe: func(x1, y1, x2, y2 int) {}}
		mustPass(t, runWith(t, f, quick, "- scroll\n- action: scroll"))
		if len(f.swipeAt) != 2 || f.swipeAt[0] != [4]int{200, 400, 200, 80} || f.swipeMs[0] != d {
			t.Errorf("%s: swipes %v %v", plat, f.swipeAt, f.swipeMs)
		}
	}
}

// scroller makes the screen a list of n rows (50 px tall, or 100 px wide columns at y 300 when
// sideways), scrolled by drags between 0 and its end, starting at offset start. It returns the
// offset and the row count (which the test may grow).
func (f *fakeDriver) scroller(n int, sideways bool, start int64) (off, rows *atomic.Int64) {
	off, rows = &atomic.Int64{}, &atomic.Int64{}
	off.Store(start)
	rows.Store(int64(n))
	size := 50
	if sideways {
		size = 100
	}
	end := func() int64 {
		screen := 800
		if sideways {
			screen = 400
		}
		return int64(max(0, 100+int(rows.Load())*size-screen))
	}
	f.screen = func() []*ui.Node {
		box := &ui.Node{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		nodes := []*ui.Node{box}
		for i := 0; i < int(rows.Load()); i++ {
			p := 100 + i*size - int(off.Load())
			var row *ui.Node
			if sideways {
				if p+size <= 0 || p >= 400 {
					continue
				}
				row = &ui.Node{Text: fmt.Sprintf("Col %d", i), Bounds: ui.Rect{L: p, T: 300, R: p + size, B: 400}, Enabled: true, Clickable: true}
			} else {
				if p+size <= 0 || p >= 800 {
					continue
				}
				row = &ui.Node{Text: fmt.Sprintf("Item %d", i), Bounds: ui.Rect{T: p, R: 400, B: p + size}, Enabled: true, Clickable: true}
			}
			row.Depth = 1
			nodes = append(nodes, row)
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		delta := int64(y1 - y2)
		if sideways {
			delta = int64(x1 - x2)
		}
		o := max(0, min(off.Load()+delta, end()))
		if o != off.Load() {
			off.Store(o)
			f.change(true)
		}
	}
	return off, rows
}

// An element already on screen needs no scrolling.
func TestScrollUntilVisibleAlreadyThere(t *testing.T) {
	f := &fakeDriver{}
	f.scroller(30, false, 0)
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 2}"))
	if len(f.swipeAt) != 0 {
		t.Errorf("%d swipes for a visible element", len(f.swipeAt))
	}
}

// scrollUntilVisible goes in every direction, dragging opposite to the content's travel.
func TestScrollUntilVisibleDirections(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir, target string
		sideways    bool
		start       int64
		drag        [4]int
	}{
		{"DOWN", "Item 30", false, 0, [4]int{200, 400, 200, 80}},
		{"UP", "Item 2", false, 2300, [4]int{200, 400, 200, 720}},
		{"RIGHT", "Col 20", true, 0, [4]int{340, 400, 60, 400}},
		{"LEFT", "Col 0", true, 1700, [4]int{60, 400, 340, 400}},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			f := &fakeDriver{}
			f.scroller(60, tc.sideways, tc.start)
			start := time.Now()
			mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: %q, direction: %s, timeout: 5000}", tc.target, tc.dir)))
			if d := time.Since(start); d > 2*time.Second {
				t.Errorf("took %s", d)
			}
			if len(f.swipeAt) == 0 || f.swipeAt[0] != tc.drag {
				t.Errorf("drags %v, want them like %v", f.swipeAt, tc.drag)
			}
			for _, o := range f.swipes {
				if !o.Drag || !o.NoSettle {
					t.Errorf("swipe options %+v: want drags", o)
				}
			}
		})
	}
}

// visibilityPercentage below 100 takes any part showing (the format divides it by 100 as an
// integer); 100, the default, waits for the whole element.
func TestScrollUntilVisiblePercentage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		opt    string
		swipes bool
	}{{"", true}, {", visibilityPercentage: 100", true}, {", visibilityPercentage: 50", false}, {", visibilityPercentage: 0", false}} {
		f := &fakeDriver{}
		f.scroller(30, false, 30) // Item 14 at 770-820: 60% shows
		mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 14"+tc.opt+"}"))
		if got := len(f.swipeAt) > 0; got != tc.swipes {
			t.Errorf("%q: swiped %v, want %v", tc.opt, got, tc.swipes)
		}
	}
}

// centerElement drags just far enough to bring the element near the middle.
func TestScrollUntilVisibleCenter(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	off, _ := f.scroller(60, false, 0)
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 16, centerElement: true}"))
	cy := 100 + 16*50 - int(off.Load()) + 25
	if cy < 400-160 || cy > 400+160 {
		t.Errorf("Item 16's center at %d, want near 400", cy)
	}
	// After a full drag shows it at 580-630, one shorter drag centers it.
	if want := [][4]int{{200, 400, 200, 80}, {200, 400, 200, 195}}; !reflect.DeepEqual(f.swipeAt, want) {
		t.Errorf("drags %v, want %v", f.swipeAt, want)
	}
	// Sideways too.
	f2 := &fakeDriver{}
	off2, _ := f2.scroller(30, true, 0)
	mustPass(t, runWith(t, f2, quick, "- scrollUntilVisible: {element: Col 5, direction: RIGHT, centerElement: true}"))
	cx := 100 + 5*100 - int(off2.Load()) + 50
	if cx < 200-80 || cx > 200+80 {
		t.Errorf("Col 5's center at %d, want near 200", cx)
	}
}

func TestScrollCenterStopsAtKnownAndroidEnd(t *testing.T) {
	f := &fakeDriver{plat: "android"}
	f.scroller(30, false, 800)
	original := f.screen
	f.screen = func() []*ui.Node {
		nodes := original()
		nodes[0].MoreBefore = true
		return nodes
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 27, centerElement: true, timeout: 4000}"))
	if len(f.swipeAt) != 0 {
		t.Errorf("dragged %d times despite the fully visible row at the collection end", len(f.swipeAt))
	}
}

// An element that never shows fails when the timeout runs out.
func TestScrollUntilVisibleTimeout(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 0)), onSwipe: func(x1, y1, x2, y2 int) {}}
	start := time.Now()
	mustFail(t, runWith(t, f, quick, "- scrollUntilVisible: {element: {id: nope}, timeout: \"${300}\"}"), "No visible element found: id: nope")
	if d := time.Since(start); d < 250*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("gave up after %s, want 300 ms", d)
	}
	res := runWith(t, f, quick, "- scrollUntilVisible: {element: nope, timeout: 100, optional: true}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("optional: %s", res.Steps[0].Status)
	}
}

// Bad timeout, speed or selector values fail scrollUntilVisible.
func TestScrollUntilVisibleBadValues(t *testing.T) {
	for _, tc := range []struct{ opts, want string }{
		{"timeout: soon", `invalid timeout "soon"`},
		{"timeout: '${(}'", "JavaScript error"},
		{"speed: 200", `speed must be 0-100, got "200"`},
		{"speed: fast", "speed must be 0-100"},
		{"speed: '${(}'", "JavaScript error"},
	} {
		f := &fakeDriver{screen: static()}
		mustFail(t, runWith(t, f, quick, "- scrollUntilVisible: {element: X, "+tc.opts+"}"), tc.want)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- scrollUntilVisible: {element: '${(}'}"), "JavaScript error")
}

// speed sets each drag's duration: 0 is slowest (1 s), 100 fastest.
func TestScrollUntilVisibleSpeed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		speed string
		want  time.Duration
	}{{"", 601 * time.Millisecond}, {"speed: 100,", time.Millisecond}, {"speed: 0,", 1001 * time.Millisecond}, {"speed: \"${50}\",", 501 * time.Millisecond}} {
		f := &fakeDriver{}
		f.scroller(30, false, 0)
		mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {"+tc.speed+" element: Item 20}"))
		if len(f.swipeMs) == 0 || f.swipeMs[0] != tc.want {
			t.Errorf("%q: drag durations %v, want %s", tc.speed, f.swipeMs, tc.want)
		}
	}
}

// On iOS up and down drags start 70% into the list, from the side the content heads to.
func TestScrollUntilVisibleIOSDragStart(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir, target string
		start       int64
		want        [4]int
	}{
		{"DOWN", "Item 40", 0, [4]int{200, 560, 200, 80}},
		{"UP", "Item 2", 2300, [4]int{200, 240, 200, 720}},
	} {
		f := &fakeDriver{plat: "ios"}
		f.scroller(60, false, tc.start)
		mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: %q, direction: %s}", tc.target, tc.dir)))
		if len(f.swipeAt) == 0 || f.swipeAt[0] != tc.want {
			t.Errorf("%s: drags %v, want %v", tc.dir, f.swipeAt, tc.want)
		}
	}
}

// dragStart: Android drags start where asked; iOS moves into the list, or reports no list.
func TestDragStart(t *testing.T) {
	box := at("list", 0, 0, 100, 400, 700)
	box.Scrollable = true
	s := linked("ios", box)
	for _, tc := range []struct {
		plat, dir string
		s         *ui.Screen
		want      int
		placed    bool
	}{
		{"android", "DOWN", s, 400, true},
		{"ios", "DOWN", s, 520, true},
		{"ios", "UP", s, 280, true},
		{"ios", "DOWN", linked("ios"), 400, false},
	} {
		got, placed := dragStart(tc.s, tc.plat, 200, 400, tc.dir)
		if got != tc.want || placed != tc.placed {
			t.Errorf("%s %s: start %d placed %v, want %d %v", tc.plat, tc.dir, got, placed, tc.want, tc.placed)
		}
	}
	// A short list: the start is never above the screen's middle for DOWN, below it for UP.
	small := at("list", 0, 0, 0, 400, 300)
	small.Scrollable = true
	if got, _ := dragStart(linked("ios", small), "ios", 200, 150, "DOWN"); got != 210 {
		t.Errorf("small list DOWN: %d", got)
	}
}

// A list that loads more rows after a drag hit its end: the next drag waits for the change
// (a loading footer going away) rather than dragging over and over.
func TestScrollUntilVisibleListLoadsMore(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, rows := f.scroller(20, false, 0)
	list := f.screen
	f.screen = func() []*ui.Node {
		nodes := list()
		if rows.Load() < 30 {
			footer := &ui.Node{Text: "Loading more", Depth: 1, Bounds: ui.Rect{T: 750, R: 400, B: 800}}
			nodes = append(nodes, footer)
		}
		return nodes
	}
	go func() {
		time.Sleep(800 * time.Millisecond) // past the 500 ms a drag gets to show
		rows.Store(30)
		f.change(true)
	}()
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 25, timeout: 5000}"))
	if d := time.Since(start); d < 700*time.Millisecond || d > 1600*time.Millisecond {
		t.Errorf("took %s; the change at 800 ms should end the wait", d)
	}
	if n := len(f.swipeAt); n > 4 {
		t.Errorf("%d drags: it dragged at the end instead of waiting", n)
	}
}

// helpers: centered, clamp, abs, scrollContainer, newTexts, listSignature.
func TestScrollHelpers(t *testing.T) {
	screen := ui.Rect{R: 400, B: 800}
	if !centered(ui.Rect{T: 300, R: 400, B: 400}, screen, "DOWN") || centered(ui.Rect{T: 700, R: 400, B: 800}, screen, "UP") {
		t.Error("centered vertical")
	}
	if !centered(ui.Rect{L: 150, R: 250, B: 10}, screen, "LEFT") || centered(ui.Rect{L: 0, R: 50, B: 10}, screen, "RIGHT") {
		t.Error("centered sideways")
	}
	if clamp(5, 0, 3) != 3 || clamp(-5, 0, 3) != 0 || clamp(2, 0, 3) != 2 || abs(-4) != 4 || abs(4) != 4 {
		t.Error("clamp/abs")
	}
	outer := at("outer", 0, 0, 0, 400, 800)
	outer.Scrollable = true
	inner := at("inner", 1, 0, 300, 400, 500)
	inner.Scrollable = true
	row := btn("Row", "", 300)
	row.Depth = 2
	top := btn("Top", "", 0)
	top.Depth = 1
	s := linked("android", outer, top, inner, row)
	if c := scrollContainer(s, 200, 350); c != inner {
		t.Errorf("container at 350: %v, want the inner list", c)
	}
	if c := scrollContainer(s, 200, 100); c != outer {
		t.Errorf("container at 100: %v, want the outer list", c)
	}
	if c := scrollContainer(linked("android", btn("x", "", 0)), 1, 1); c != nil {
		t.Errorf("no list: %v", c)
	}
	seen := map[string]bool{}
	if n := newTexts(s, 200, 350, seen); n != 1 || !seen["Row"] || seen["Top"] {
		t.Errorf("new texts in the inner list: %d %v", n, seen)
	}
	if n := newTexts(s, 200, 350, seen); n != 0 {
		t.Errorf("seen again: %d", n)
	}
	plain := linked("android", btn("A", "", 0), btn("B", "", 100))
	if n := newTexts(plain, 1, 1, seen); n != 2 {
		t.Errorf("no list: %d new, want the whole screen's 2", n)
	}
	// The signature changes when the list's rows move along the drag's axis, not across it.
	sig := listSignature(s, 200, 350, true)
	row.Bounds.L += 10
	if listSignature(s, 200, 350, true) != sig {
		t.Error("a sideways move changed a vertical drag's signature")
	}
	row.Bounds.T += 10
	if listSignature(s, 200, 350, true) == sig {
		t.Error("a vertical move left the signature as it was")
	}
	if listSignature(plain, 1, 1, false) == listSignature(linked("android", btn("A", "", 0), btn("C", "", 100)), 1, 1, false) {
		t.Error("different texts, same signature")
	}
}

// A negative timeout means the default.
func TestScrollUntilVisibleNegativeTimeout(t *testing.T) {
	f := &fakeDriver{}
	f.scroller(30, false, 0)
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 20, timeout: -1}"))
}

// waitAny returns on any newer change, the deadline, the context, or a lost connection.
func TestWaitAny(t *testing.T) {
	f := &fakeDriver{}
	r := New(f, quick)
	ctx := context.Background()
	soon := func() time.Time { return time.Now().Add(2 * time.Second) }
	took := func(fn func()) time.Duration { s := time.Now(); fn(); return time.Since(s) }

	f.change(false)
	if d := took(func() { r.waitAny(ctx, nil, 0, soon()) }); d > 100*time.Millisecond {
		t.Errorf("a change already counted: %s", d)
	}
	if d := took(func() { r.waitAny(ctx, nil, f.Seq(), time.Now().Add(50*time.Millisecond)) }); d < 40*time.Millisecond || d > time.Second {
		t.Errorf("deadline: %s", d)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if d := took(func() { r.waitAny(cctx, nil, f.Seq(), soon()) }); d > 100*time.Millisecond {
		t.Errorf("cancelled: %s", d)
	}
	for _, tc := range []struct {
		name string
		send func(ch chan driver.Event)
	}{
		{"newer event", func(ch chan driver.Event) { ch <- driver.Event{Kind: "changed", Seq: 99} }},
		{"disconnected", func(ch chan driver.Event) { ch <- driver.Event{Kind: "disconnected"} }},
		{"closed", func(ch chan driver.Event) { close(ch) }},
	} {
		ch := make(chan driver.Event, 2)
		ch <- driver.Event{Kind: "changed", Seq: 1} // older: ignored
		tc.send(ch)
		if d := took(func() { r.waitAny(ctx, ch, f.Seq(), soon()) }); d > 100*time.Millisecond {
			t.Errorf("%s: %s", tc.name, d)
		}
	}
}

// After two drags that bring nothing new, a fling the device fails fails the command.
func TestScrollUntilVisibleFlingError(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Page", "", 300))}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		f.mu.Lock()
		n := len(f.swipes)
		f.mu.Unlock()
		if n == 2 {
			f.fail("Swipe", errors.New("fling failed"))
		}
	}
	mustFail(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Next, timeout: 10000}"), "fling failed")
}

// An element that keeps moving after a drag (a bounce, a snap) counts once the UI settles,
// after a second of reads that disagree.
func TestScrollUntilVisibleElementKeepsMoving(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var dragged atomic.Bool
	var reads atomic.Int64
	f.screen = func() []*ui.Node {
		list := &ui.Node{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		if !dragged.Load() {
			return []*ui.Node{list}
		}
		row := btn("Target", "", 300+int(reads.Add(1)%2)*10) // never the same twice
		row.Depth = 1
		return []*ui.Node{list, row}
	}
	stop := make(chan struct{})
	defer close(stop)
	f.onSwipe = func(x1, y1, x2, y2 int) {
		dragged.Store(true)
		go func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(40 * time.Millisecond):
					f.change(false)
				}
			}
		}()
	}
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Target, timeout: 5000}"))
	if d := time.Since(start); d < 900*time.Millisecond || d > 2500*time.Millisecond {
		t.Errorf("took %s, want about a second of disagreeing reads", d)
	}
	if len(f.swipeAt) != 1 {
		t.Errorf("%d drags", len(f.swipeAt))
	}
	settled := false
	for _, d := range f.waitDur {
		if d == 2*time.Second {
			settled = true
		}
	}
	if !settled {
		t.Errorf("settle waits %v: want the 2 s settle", f.waitDur)
	}
}

// A list that redraws a beat after each drag (the screen changes at once, elsewhere): a read
// right after a drag shows nothing new, but the settled screen does. Such a drag is not
// fruitless, so the scroll keeps dragging (no flinging, which could carry the element past
// the screen) and finds the element.
func TestScrollUntilVisibleLaggingList(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, _ = f.scroller(60, false, 0)
	moveList := f.onSwipe
	list := f.screen
	var pending [][4]int
	var wobble atomic.Int64 // something on screen that changes at once with each drag
	f.screen = func() []*ui.Node {
		// A ticker in the list, cycling through a few texts it soon has shown already.
		tick := &ui.Node{Text: fmt.Sprintf("Live %d", wobble.Load()%3), Depth: 1, Bounds: ui.Rect{L: 300, T: 0, R: 400, B: 20}}
		return append(list(), tick)
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		f.mu.Lock()
		pending = append(pending, [4]int{x1, y1, x2, y2})
		f.mu.Unlock()
		wobble.Add(1)
		f.change(false)
	}
	f.onWait = func() { // the list catches up once the screen settles
		f.mu.Lock()
		p := pending
		pending = nil
		f.mu.Unlock()
		for _, s := range p {
			moveList(s[0], s[1], s[2], s[3])
		}
	}
	done := make(chan FlowResult, 1)
	go func() { done <- runWith(t, f, quick, "- scrollUntilVisible: {element: Item 45, timeout: 3000}") }()
	select {
	case res := <-done:
		mustPass(t, res)
	case <-time.After(10 * time.Second):
		t.Fatal("the scroll did not end: flinging at a list that lags (the drags were taken for fruitless)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, o := range f.swipes {
		if !o.Drag {
			t.Fatalf("swipe %d of %d flung: the lagging drags were taken for fruitless", i+1, len(f.swipes))
		}
	}
}

// Android: a row reported fully visible but flush with its list's edge may be cut off there
// (Android cuts bounds to what shows). With 100% asked, it is brought in once more.
func TestScrollUntilVisibleFlushRowBroughtIn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	off, _ := f.scroller(40, false, 0)
	list := f.screen
	f.screen = func() []*ui.Node {
		nodes := list()
		for _, n := range nodes {
			if n.Text == "Item 18" && n.Bounds.B > 800 {
				n.Bounds.B = 800 // cut to the screen, as Android reports it
			}
		}
		return nodes
	}
	off.Store(250) // Item 18 (rows are 50 px from y=100) at 750-800: flush with the list's bottom
	res := runWith(t, f, quick, "- scrollUntilVisible: {element: Item 18, visibilityPercentage: 100, timeout: 5000}")
	mustPass(t, res)
	if len(f.swipeAt) != 1 {
		t.Errorf("%d drags, want 1 to bring the flush row in", len(f.swipeAt))
	}
}

// A snapping pager can briefly remove a row after reporting it flush with the
// viewport. A pending edge correction must not dereference that missing row.
func TestScrollUntilVisibleFlushRowDisappearsWhileSnapping(t *testing.T) {
	var phase atomic.Int32
	f := &fakeDriver{}
	f.onSwipe = func(_, _, _, _ int) { phase.CompareAndSwap(0, 1) }
	f.screen = func() []*ui.Node {
		list := &ui.Node{Class: "ScrollView", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		switch phase.Load() {
		case 0:
			return []*ui.Node{list}
		case 1:
			phase.Store(2)
			f.seq++ // a subsequent render is already queued
			return []*ui.Node{list, {Depth: 1, Text: "Discover", Bounds: ui.Rect{L: 300, T: 100, R: 400, B: 200}}}
		case 2:
			phase.Store(3)
			f.seq++
			return []*ui.Node{list}
		default:
			return []*ui.Node{list, {Depth: 1, Text: "Discover", Bounds: ui.Rect{L: 150, T: 100, R: 250, B: 200}}}
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Discover, direction: LEFT, visibilityPercentage: 100, timeout: 2000}"))
}

// After two drags that brought nothing new (a list that did not move for a moment), a fling
// that brings new rows into view returns to drags: flings could carry the element past.
func TestScrollUntilVisibleBackToDragsAfterFling(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, _ = f.scroller(80, false, 0)
	moveList := f.onSwipe
	list := f.screen
	var swipes, tick atomic.Int64
	f.screen = func() []*ui.Node {
		t := &ui.Node{Text: fmt.Sprintf("Live %d", tick.Load()%2), Depth: 1, Bounds: ui.Rect{L: 300, R: 400, B: 20}}
		return append(list(), t)
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		tick.Add(1)
		if swipes.Add(1) <= 3 {
			f.change(false) // the first drags move nothing (the screen still changes)
			return
		}
		moveList(x1, y1, x2, y2)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 60, timeout: 10000}"))
	f.mu.Lock()
	defer f.mu.Unlock()
	// Drag 1 shows the ticker's other text (new); drags 2 and 3 show nothing new.
	if len(f.swipes) < 5 || !f.swipes[2].Drag || f.swipes[3].Drag || !f.swipes[4].Drag {
		var kinds []bool
		for _, o := range f.swipes {
			kinds = append(kinds, o.Drag)
		}
		t.Errorf("drag per swipe %v: want three drags, a fling, then drags again", kinds)
	}
}

// Two panes: start in the list that shows the target or related rows, and
// retain the other-pane search when both lists have similar labels.
func TestScrollUntilVisibleTwoPanes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		platform                      string
		listed, similarRight, batched bool
	}{
		{"android", true, false, false}, {"android", false, false, false},
		{"android", false, true, false}, {"android", false, false, true},
		{"ios", false, false, true},
	} {
		f := &fakeDriver{plat: tc.platform}
		var off, tick atomic.Int64
		drags := 0
		f.screen = func() []*ui.Node {
			root := &ui.Node{Class: "root", Bounds: ui.Rect{R: 400, B: 800}}
			left := &ui.Node{Class: "list", Depth: 1, Scrollable: true, Bounds: ui.Rect{R: 160, B: 800}}
			if tc.platform == "ios" {
				left.Class = "CollectionView"
			} else if tc.batched {
				left.Coll = &ui.Coll{Rows: 80, Cols: 1}
			}
			nodes := []*ui.Node{root, left}
			for i := 1; i <= 80; i++ {
				top := i*60 - int(off.Load())
				if !tc.listed && (top < -60 || top > 800) {
					continue // a recycling list lists only what shows
				}
				nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Mail %d", i), Depth: 2, Bounds: ui.Rect{T: top, R: 160, B: top + 60}, Enabled: true, Clickable: true})
			}
			right := &ui.Node{Class: "body", Depth: 1, Scrollable: true, Bounds: ui.Rect{L: 160, R: 400, B: 800}}
			label := fmt.Sprintf("Live %d", tick.Load()%2)
			if tc.similarRight {
				label = fmt.Sprintf("Mail 6%d", 1+tick.Load()%2)
			}
			live := &ui.Node{Text: label, Depth: 2, Bounds: ui.Rect{L: 170, T: 10, R: 390, B: 40}}
			return append(nodes, right, live)
		}
		f.onSwipe = func(x1, y1, x2, y2 int) {
			tick.Add(1)
			if x1 < 160 { // only the left list moves
				off.Store(min(off.Load()+int64(y1-y2), 80*60-700))
				if tc.batched {
					drags++
					switch drags {
					case 1:
						off.Store(25) // only the beginning of the first drag is visible
					case 2:
						off.Store(2 * int64(y1-y2)) // the rest arrives with the second drag
					}
				}
				f.change(true)
			}
		}
		target, timeout := "Mail 60", 10000
		if tc.batched {
			target, timeout = "Mail 16", 1800
		}
		mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: %s, timeout: %d}", target, timeout)))
		f.mu.Lock()
		last := f.swipeAt[len(f.swipeAt)-1]
		if last[0] >= 160 {
			t.Errorf("%+v: the last drag was at x=%d, in the right pane", tc, last[0])
		}
		if rightFirst := f.swipeAt[0][0] >= 160; rightFirst != tc.similarRight {
			t.Errorf("%+v: unexpected starting pane: %v", tc, f.swipeAt[0])
		}
		f.mu.Unlock()
	}
}

// A sideways scroll whose list is mostly below the screen's edge first drags the page to bring
// it in, then drags across it (not along the few pixels that show).
func TestScrollSidewaysBringsListIn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var pageOff, shelfOff atomic.Int64
	f.screen = func() []*ui.Node {
		page := &ui.Node{Class: "page", Depth: 1, Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		top := 760 - int(pageOff.Load()) // the shelf starts 40 px above the bottom edge
		shelf := &ui.Node{Class: "shelf", Depth: 2, Scrollable: true, Bounds: ui.Rect{T: top, R: 400, B: top + 120}}
		nodes := []*ui.Node{{Class: "root", Bounds: ui.Rect{R: 400, B: 800}}, page, shelf}
		for i := 1; i <= 15; i++ {
			l := (i-1)*140 - int(shelfOff.Load())
			if l+140 <= 0 || l >= 400 {
				continue
			}
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Card 6-%d", i), Depth: 3, Bounds: ui.Rect{L: l, T: top + 10, R: l + 140, B: top + 110}, Enabled: true, Clickable: true})
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		top := 760 - int(pageOff.Load())
		if y1 >= top && y1 < top+120 && y1 == y2 { // across the shelf
			shelfOff.Store(min(shelfOff.Load()+int64(x1-x2), 15*140-400))
		} else if x1 == x2 { // the page
			pageOff.Store(max(0, min(pageOff.Load()+int64(y1-y2), 400)))
		}
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Card 6-12, direction: RIGHT, timeout: 5000}"))
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.swipeAt) < 2 || f.swipeAt[0][0] != f.swipeAt[0][2] {
		t.Errorf("swipes %v: want the page brought up first, then sideways drags", f.swipeAt)
	}
}

// Rows taller than the room a drag leaves (bench/flows/scenarios/lists.yaml's grid on web: a
// 420 px cell where 580 px of the list shows): a row is whole on screen only within a window
// shorter than one drag, so full drags step past it. With 100% asked, once the row shows in
// part, the drag goes just far enough to bring it in whole.
func TestScrollUntilVisibleTallRows(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	const size = 760 // in an 800 px list: whole only while its top is within 0-40
	var off atomic.Int64
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		for i := 0; i < 12; i++ {
			p := 100 + i*size - int(off.Load())
			if p+size <= 0 || p >= 800 {
				continue
			}
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Item %d", i), Depth: 1, Enabled: true, Clickable: true,
				Bounds: ui.Rect{T: p, R: 400, B: p + size}})
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		if o := max(0, min(off.Load()+int64(y1-y2), int64(100+12*size-800))); o != off.Load() {
			off.Store(o)
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 5, timeout: 5000}"))
	if top := 100 + 5*size - int(off.Load()); top < 0 || top+size > 800 {
		t.Errorf("Item 5 at %d-%d: not whole in the 0-800 list", top, top+size)
	}
}

// scrollUntilVisibleIn, on the screen bench/flows/scenarios/lists.yaml met on an iPhone 17e:
// shelf 1 in view, shelf 6's heading on the bottom line and its cards below, not listed. The
// page scrolls until the shelf's first card is in, then that shelf, never shelf 1, is
// dragged sideways until the card shows. Every shelf's cards have the same texts, so only the
// list item (not a name shared with the element) tells which shelf to drag.
func TestScrollUntilVisibleIn(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var pageOff atomic.Int64
	var shelfOff [2]atomic.Int64 // shelf 1, shelf 6
	tops := [2]int{300, 790}     // shelf 6's cards start below the 800 px screen
	names := [2]int{1, 6}
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{{Class: "root", Bounds: ui.Rect{R: 400, B: 800}},
			{Class: "page", Depth: 1, Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		for s := range 2 {
			top := tops[s] - int(pageOff.Load())
			if top+120 <= 0 || top >= 800 {
				continue
			}
			nodes = append(nodes, &ui.Node{Class: "shelf", Depth: 2, Scrollable: true, Bounds: ui.Rect{T: top, R: 400, B: top + 120}})
			for i := 1; i <= 15; i++ {
				l := (i-1)*140 - int(shelfOff[s].Load())
				if l+140 <= 0 || l >= 400 {
					continue
				}
				nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Card %d", i), ID: fmt.Sprintf("card_%d_%d", names[s], i), Depth: 3, Enabled: true, Clickable: true,
					Bounds: ui.Rect{L: l, T: top + 10, R: l + 140, B: top + 110}})
			}
		}
		return nodes
	}
	move := func(v *atomic.Int64, by, end int64) {
		if o := max(0, min(v.Load()+by, end)); o != v.Load() {
			v.Store(o)
			f.change(true)
		}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		if x1 == x2 { // the page, which ends 120 px down: both shelves stay in view
			move(&pageOff, int64(y1-y2), 120)
		}
		for s := range 2 {
			if top := tops[s] - int(pageOff.Load()); y1 == y2 && y1 >= top && y1 < top+120 {
				move(&shelfOff[s], int64(x1-x2), 15*140-400)
			}
		}
	}
	res := runWith(t, f, quick, "- scrollUntilVisibleIn: {list: {id: card_6_1}, element: {id: card_6_12}, direction: RIGHT, timeout: 5000}")
	mustPass(t, res)
	if shelfOff[0].Load() != 0 {
		t.Errorf("shelf 1 was dragged to %d: want only shelf 6 dragged", shelfOff[0].Load())
	}
	if want := "Scroll to id: card_6_12 in the list of id: card_6_1"; res.Steps[0].Desc != want {
		t.Errorf("step %q, want %q", res.Steps[0].Desc, want)
	}
}

func TestScrollUntilVisibleInFollowsMovingShelf(t *testing.T) {
	f := &fakeDriver{plat: "android"}
	top, offset := 560, 0
	f.screen = func() []*ui.Node {
		nodes := []*ui.Node{
			{Class: "page", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}},
			{Class: "shelf", ID: "shelf_6", Depth: 1, Scrollable: true, Bounds: ui.Rect{T: top, R: 400, B: top + 100}},
		}
		for i := range 12 {
			x := i*140 - offset
			if x+140 <= 0 || x >= 400 {
				continue
			}
			nodes = append(nodes, &ui.Node{Depth: 2, ID: fmt.Sprintf("card_%d", i+1), Text: fmt.Sprintf("Card %d", i+1),
				Enabled: true, Bounds: ui.Rect{L: x, T: top + 10, R: x + 140, B: top + 90}})
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		if y1 != y2 {
			return
		}
		if top == 560 {
			top = 390 // the parent finishes scrolling just as the first sideways drag starts
		} else if y1 >= top && y1 < top+100 {
			offset = min(offset+x1-x2, 12*140-400)
		} else {
			return
		}
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisibleIn: {list: {id: card_1}, element: {id: card_12}, direction: RIGHT, timeout: 2500}"))
}

// Under load a recycling list may expose the end of one drag together with
// the next, jumping over a page that neither accessibility read saw.
func TestScrollCoversSkippedCollectionRows(t *testing.T) {
	for _, tc := range []struct {
		dir, target string
		initial     int
	}{
		{"DOWN", "Mail 21", 0}, {"UP", "Mail 60", 3200},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			f := &fakeDriver{}
			offset := tc.initial
			first := true
			reversed := false
			f.screen = func() []*ui.Node {
				screen := ui.Rect{R: 400, B: 800}
				nodes := []*ui.Node{{Class: "Root", Bounds: screen}, {Depth: 1, Class: "RecyclerView", ID: "mail", Scrollable: true, Bounds: screen, Coll: &ui.Coll{Rows: 80, Cols: 1}}}
				for i := 0; i < 80; i++ {
					b := ui.Rect{T: i*50 - offset, R: 400, B: (i+1)*50 - offset}.Intersect(screen)
					if !b.Empty() {
						nodes = append(nodes, &ui.Node{Depth: 2, Text: fmt.Sprintf("Mail %d", i+1), Item: &ui.Coll{Rows: i, Cols: 0}, Enabled: true, Bounds: b})
					}
				}
				return nodes
			}
			f.onSwipe = func(_, y1, _, y2 int) {
				if first {
					first = false
					offset = 32 * 50
				} else {
					reversed = reversed || (tc.dir == "DOWN" && y2 > y1) || (tc.dir == "UP" && y2 < y1)
					offset = max(0, min(3200, offset+y1-y2))
				}
				f.change(true)
			}
			mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: '%s', direction: %s, timeout: 1500}", tc.target, tc.dir)))
			if !reversed {
				t.Fatal("the skipped rows were never searched")
			}
		})
	}
}

// A read after a seemingly fruitless drag can catch the recycler's delayed
// layout. It must check for a skipped interval before advancing again.
func TestScrollChecksCollectionGapAfterSettling(t *testing.T) {
	f := &fakeDriver{plat: "android"}
	offset, _ := f.scroller(80, false, 10)
	screen := f.screen
	f.screen = func() []*ui.Node {
		nodes := screen()
		nodes[0].Coll = &ui.Coll{Rows: 80, Cols: 1}
		for _, row := range nodes[1:] {
			var index int
			fmt.Sscanf(row.Text, "Item %d", &index)
			row.Item = &ui.Coll{Rows: index}
		}
		return nodes
	}
	dragged, pending := false, false
	f.onSwipe = func(_, y1, _, y2 int) {
		if !dragged {
			dragged, pending = true, true
			offset.Store(11) // same rows, slightly moved
		} else {
			offset.Store(max(0, min(3300, offset.Load()+int64(y1-y2))))
		}
		f.change(true)
	}
	f.onWait = func() {
		if pending {
			pending = false
			offset.Store(1600) // the delayed layout jumps past Item 20
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 20, timeout: 1800}"))
}
