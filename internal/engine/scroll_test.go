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

// A swipe from an element starts at its center (or the point in it) and goes toward the edge.
func TestSwipeFromElement(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want [4]int
	}{
		{"{from: {id: card}, direction: UP}", [4]int{200, 325, 200, 80}},
		{"{from: {id: card}, direction: DOWN}", [4]int{200, 325, 200, 720}},
		{"{from: {id: card}, direction: LEFT}", [4]int{200, 325, 40, 325}},
		{"{from: {id: card}, direction: RIGHT}", [4]int{200, 325, 360, 325}},
		{"{from: Card, direction: LEFT}", [4]int{200, 325, 40, 325}},
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
