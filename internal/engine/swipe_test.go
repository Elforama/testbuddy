package engine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

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

func TestSwipeOptions(t *testing.T) {
	f := &fakeDriver{screen: static(), onSwipe: func(x1, y1, x2, y2 int) {}}
	mustPass(t, runWith(t, f, quick, "- swipe: {direction: UP, duration: 1000, waitToSettleTimeoutMs: 250}"))
	if f.swipeMs[0] != time.Second || f.swipes[0].Timeout != 250*time.Millisecond {
		t.Errorf("swipe %s %+v", f.swipeMs[0], f.swipes[0])
	}
}

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

func TestSwipeFromElementRestsInCI(t *testing.T) {
	for _, tc := range []struct {
		tbCI, cmd string
		rest      bool
	}{
		{"1", "{from: {id: card}, direction: RIGHT}", true},
		{"1", "{from: {id: card, point: '50%,50%'}, direction: LEFT}", true},
		{"1", "{direction: UP}", false},
		{"1", "{start: '10%,50%', end: '90%,50%'}", false},
		{"0", "{from: {id: card}, direction: RIGHT}", false},
		{"0", "{from: {id: card, point: '50%,50%'}, direction: LEFT}", false},
	} {
		t.Run("TB_CI="+tc.tbCI+" "+tc.cmd, func(t *testing.T) {
			t.Setenv("TB_CI", tc.tbCI)
			f := &fakeDriver{screen: static(btn("Card", "card", 300)), plat: "ios", onSwipe: func(x1, y1, x2, y2 int) {}}
			mustPass(t, runWith(t, f, quick, "- swipe: "+tc.cmd))
			if len(f.swipes) != 1 || f.swipes[0].Rest != tc.rest || f.swipes[0].Drag {
				t.Errorf("swipe options %+v, want Rest %v", f.swipes, tc.rest)
			}
		})
	}
}

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

func TestDragHoldsThenMovesBetweenElements(t *testing.T) {
	for _, plat := range []string{"ios", "android"} {
		t.Run(plat, func(t *testing.T) { testDragHoldsThenMoves(t, plat) })
	}
}

func testDragHoldsThenMoves(t *testing.T, plat string) {
	rows := static(btn("A", "reorder_A", 200), btn("B", "reorder_B", 250), btn("C", "reorder_C", 300))
	for _, tc := range []struct {
		cmd       string
		want      [4]int
		hold, dur time.Duration
	}{
		{"{from: A, to: C}", [4]int{200, 225, 200, 325}, 800 * time.Millisecond, time.Second},
		{"{from: {id: reorder_C}, to: {id: reorder_A}, hold: 1200, duration: 500}", [4]int{200, 325, 200, 225}, 1200 * time.Millisecond, 500 * time.Millisecond},
		{"{start: '50%,28%', end: '50%,50%'}", [4]int{200, 224, 200, 400}, 800 * time.Millisecond, time.Second},
		{"{from: A, end: '900,900'}", [4]int{200, 225, 399, 799}, 800 * time.Millisecond, time.Second},
		{"{from: A, to: B, hold: 0}", [4]int{200, 225, 200, 275}, time.Millisecond, time.Second},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			f := &fakeDriver{screen: rows, plat: plat, onSwipe: func(x1, y1, x2, y2 int) {}}
			mustPass(t, runWith(t, f, quick, "- drag: "+tc.cmd))
			if len(f.swipeAt) != 1 || f.swipeAt[0] != tc.want {
				t.Fatalf("swipes %v, want %v", f.swipeAt, tc.want)
			}
			if o := f.swipes[0]; o.Hold != tc.hold || o.Drag || o.Rest || o.NoSettle {
				t.Errorf("options %+v, want a drag held %s", o, tc.hold)
			}
			if f.swipeMs[0] != tc.dur {
				t.Errorf("moved in %s, want %s", f.swipeMs[0], tc.dur)
			}
		})
	}
}

func TestDragFailures(t *testing.T) {
	rows := static(btn("A", "reorder_A", 200))
	f := &fakeDriver{screen: rows, plat: "ios"}
	mustFail(t, runWith(t, f, quick, "- drag: {from: A, to: {id: nope}}"), "Element not found: id: nope")
	mustFail(t, runWith(t, f, quick, "- drag: {from: {id: nope}, to: A}"), "Element not found: id: nope")
	res := runWith(t, f, quick, "- drag: {from: A, to: {id: nope, optional: true}}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("optional: status %s", res.Steps[0].Status)
	}
	mustFail(t, runWith(t, f, quick, "- drag: {start: nope, end: '1,1'}"), "invalid point")
	if len(f.swipeAt) != 0 {
		t.Errorf("dragged %v", f.swipeAt)
	}
	a := &fakeDriver{screen: rows, plat: "web"}
	mustFail(t, runWith(t, a, quick, "- drag: {from: A, end: '1,1'}"), "drag is not supported on web yet")
	if len(a.swipeAt) != 0 {
		t.Errorf("web: swiped %v", a.swipeAt)
	}
	for _, plat := range []string{"ios", "android"} {
		e := &fakeDriver{screen: rows, plat: plat}
		e.fail("Swipe", errors.New("touch lost"))
		mustFail(t, runWith(t, e, quick, "- drag: {from: A, end: '1,1'}"), "touch lost")
		if e.swipeTries != 1 {
			t.Errorf("%s: a failed drag was sent %d times, want once", plat, e.swipeTries)
		}
	}
}

func TestSwipeError(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("Swipe", errors.New("touch lost"))
	mustFail(t, runWith(t, f, quick, "- swipe: {direction: UP}"), "touch lost")
	mustFail(t, runWith(t, f, quick, "- scroll"), "touch lost")
	mustFail(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Nope}"), "touch lost")
}

func volume(value string) *ui.Node {
	return &ui.Node{ID: "slider_volume", Class: "Slider", Text: value, Value: value, Bounds: ui.Rect{L: 20, T: 591, R: 382, B: 622}, Enabled: true, Clickable: true}
}

func TestSwipeBetweenPointsOnASliderRestsInCI(t *testing.T) {
	for _, tc := range []struct {
		tbCI, plat, value, cmd string
		rest                   bool
	}{
		{"1", "ios", "50%", "{start: '201,606', end: '362,606'}", true},
		{"1", "ios", "50%", "{start: '50%,75%', end: '90%,75%'}", true},
		{"1", "ios", "10%", "{start: '201,606', end: '362,606'}", false},
		{"1", "ios", "50%", "{start: '201,300', end: '201,100'}", false},
		{"0", "ios", "50%", "{start: '201,606', end: '362,606'}", false},
		{"1", "android", "50%", "{start: '201,606', end: '362,606'}", false},
	} {
		t.Run("TB_CI="+tc.tbCI+" "+tc.plat+" "+tc.value+" "+tc.cmd, func(t *testing.T) {
			t.Setenv("TB_CI", tc.tbCI)
			f := &fakeDriver{screen: static(volume(tc.value)), plat: tc.plat, onSwipe: func(x1, y1, x2, y2 int) {}}
			mustPass(t, runWith(t, f, quick, "- swipe: "+tc.cmd))
			if len(f.swipes) == 0 || f.swipes[0].Rest != tc.rest || !tc.rest && len(f.swipes) != 1 {
				t.Errorf("swipe options %+v, want Rest %v", f.swipes, tc.rest)
			}
		})
	}
}

func TestASliderLeftShortIsDraggedAgainInCI(t *testing.T) {
	for _, tc := range []struct {
		name, tbCI, cmd string
		after           []string
		want            [][4]int
	}{
		{"reached", "1", "{from: {id: slider_volume}, direction: RIGHT}", []string{"100%"},
			[][4]int{{201, 606, 399, 606}}},
		{"short once", "1", "{from: {id: slider_volume}, direction: RIGHT}", []string{"66%", "100%"},
			[][4]int{{201, 606, 399, 606}, {254, 606, 399, 606}}},
		{"between points", "1", "{start: '201,606', end: '362,606'}", []string{"59%", "98%"},
			[][4]int{{201, 606, 362, 606}, {231, 606, 362, 606}}},
		{"to the left", "1", "{from: {id: slider_volume}, direction: LEFT}", []string{"40%", "0%"},
			[][4]int{{201, 606, 0, 606}, {168, 606, 0, 606}}},
		{"short every time", "1", "{from: {id: slider_volume}, direction: RIGHT}", []string{"60%", "70%", "80%"},
			[][4]int{{201, 606, 399, 606}, {234, 606, 399, 606}, {267, 606, 399, 606}}},
		{"not moved by a drag again", "1", "{from: {id: slider_volume}, direction: RIGHT}", []string{"60%", "60%"},
			[][4]int{{201, 606, 399, 606}, {234, 606, 399, 606}}},
		{"outside CI", "0", "{from: {id: slider_volume}, direction: RIGHT}", []string{"66%"},
			[][4]int{{201, 606, 399, 606}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", tc.tbCI)
			var mu sync.Mutex
			value, n := "50%", 0
			f := &fakeDriver{plat: "ios"}
			f.screen = func() []*ui.Node { mu.Lock(); defer mu.Unlock(); return []*ui.Node{volume(value)} }
			f.onSwipe = func(x1, y1, x2, y2 int) {
				mu.Lock()
				value = tc.after[min(n, len(tc.after)-1)]
				n++
				mu.Unlock()
				f.change(true)
			}
			mustPass(t, runWith(t, f, quick, "- swipe: "+tc.cmd))
			if fmt.Sprint(f.swipeAt) != fmt.Sprint(tc.want) {
				t.Errorf("swipes %v, want %v", f.swipeAt, tc.want)
			}
			for i, o := range f.swipes {
				if o.Rest != (tc.tbCI == "1") {
					t.Errorf("swipe %d: Rest %v", i, o.Rest)
				}
			}
		})
	}
}

func TestOnlyASwipeAlongASliderIsTakenForADragOfItsThumb(t *testing.T) {
	for _, tc := range []struct {
		cmd    string
		rest   bool
		swipes int
	}{
		{"{start: '201,606', end: '362,606'}", true, 2},
		{"{start: '201,606', end: '201,200'}", false, 1},
		{"{start: '201,606', end: '260,300'}", false, 1},
		{"{from: {id: slider_volume}, direction: RIGHT}", true, 2},
		{"{from: {id: slider_volume}, direction: UP}", true, 1},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			t.Setenv("TB_CI", "1")
			f := &fakeDriver{screen: static(volume("50%")), plat: "ios", onSwipe: func(x1, y1, x2, y2 int) {}}
			mustPass(t, runWith(t, f, quick, "- swipe: "+tc.cmd))
			if len(f.swipes) != tc.swipes || f.swipes[0].Rest != tc.rest {
				t.Errorf("swipes %v %+v, want %d, the first with Rest %v", f.swipeAt, f.swipes, tc.swipes, tc.rest)
			}
		})
	}
}

func TestASliderIsDraggedAgainOnlyWhenTheDragFellShortOnTheWay(t *testing.T) {
	full := func(value string) *ui.Node {
		n := volume(value)
		n.Bounds.L, n.Bounds.R = 0, 400
		return n
	}
	disabled := func(value string) *ui.Node { n := volume(value); n.Enabled = false; return n }
	for _, tc := range []struct {
		name, cmd string
		slider    func(string) *ui.Node
		start     string
		after     []string
		alert     bool
		swipes    int
	}{
		{"partial stall 50 to 60", "{start: '201,606', end: '362,606'}", volume, "50%", []string{"60%", "98%"}, false, 2},
		{"whole drag lost", "{start: '201,606', end: '362,606'}", volume, "50%", []string{"50%", "98%"}, false, 2},
		{"right to left", "{start: '201,606', end: '362,606'}", volume, "50%", []string{"0%"}, false, 1},
		{"disabled", "{from: {id: slider_volume}, direction: RIGHT}", disabled, "50%", []string{"50%"}, false, 1},
		{"clamped at 80", "{start: '201,606', end: '362,606'}", volume, "50%", []string{"80%", "80%"}, false, 2},
		{"stepped, snapped to 67", "{start: '201,606', end: '300,606'}", volume, "50%", []string{"67%"}, false, 1},
		{"a window came up", "{start: '201,606', end: '362,606'}", volume, "50%", []string{"60%"}, true, 1},
		{"thumb at the edge", "{start: '15,606', end: '360,606'}", full, "0%", []string{"0%"}, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TB_CI", "1")
			var mu sync.Mutex
			value, n := tc.start, 0
			f := &fakeDriver{plat: "ios"}
			f.screen = func() []*ui.Node { mu.Lock(); defer mu.Unlock(); return []*ui.Node{tc.slider(value)} }
			f.sysAlert = func() []*ui.Node {
				mu.Lock()
				defer mu.Unlock()
				if tc.alert && n > 0 {
					return []*ui.Node{btn("OK", "ok", 400)}
				}
				return nil
			}
			f.onSwipe = func(x1, y1, x2, y2 int) {
				mu.Lock()
				value = tc.after[min(n, len(tc.after)-1)]
				n++
				mu.Unlock()
				f.change(true)
			}
			mustPass(t, runWith(t, f, quick, "- swipe: "+tc.cmd))
			if len(f.swipeAt) != tc.swipes {
				t.Errorf("swipes %v, want %d", f.swipeAt, tc.swipes)
			}
			if tc.name == "partial stall 50 to 60" && len(f.swipeAt) == 2 && f.swipeAt[1] != [4]int{234, 606, 362, 606} {
				t.Errorf("dragged again %v, want from the thumb at 60%% (234, 606)", f.swipeAt[1])
			}
		})
	}
}
