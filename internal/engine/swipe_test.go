package engine

import (
	"errors"
	"fmt"
	"testing"
	"time"
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

// Issue #127: a swipe from a slider left it at 71 instead of 80+ on a slow CI simulator: the
// finger lifted while still moving, and the slider settled short of where it lifted. In CI mode
// a swipe from an element (it places what it starts on) asks for a finger that rests before it
// moves and before it lifts; outside CI it is as before. A swipe in a direction or between two
// points never rests.
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
