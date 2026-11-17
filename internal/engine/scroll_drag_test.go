package engine

import (
	"fmt"
	"sync"
	"testing"

	"github.com/elforama/testbuddy/internal/ui"
)

// rowsList makes the screen (400x800) a list filling it, with rows of the given heights from
// its top, and over it the nodes over() gives (drawn over the list: listed after it). It notes
// which rows each read could show whole within seen (the part of the list a person sees: above
// a bar drawn over it), and returns them.
func (f *fakeDriver) rowsList(heights []int, seen ui.Rect, over func() []*ui.Node) (whole func() map[int]bool) {
	var mu sync.Mutex
	off := 0
	total := 0
	for _, h := range heights {
		total += h
	}
	shown := map[int]bool{}
	note := func() { // under mu
		top := -off
		for i, h := range heights {
			if top >= seen.T && top+h <= seen.B {
				shown[i] = true
			}
			top += h
		}
	}
	note()
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		nodes := []*ui.Node{{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		top := -off
		for i, h := range heights {
			if top+h > 0 && top < 800 {
				nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Item %d", i), Depth: 1, Enabled: true, Clickable: true,
					Bounds: ui.Rect{T: top, R: 400, B: top + h}})
			}
			top += h
		}
		if over != nil {
			nodes = append(nodes, over()...)
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		mu.Lock()
		o := max(0, min(off+y1-y2, total-800))
		changed := o != off
		off = o
		note()
		mu.Unlock()
		if changed {
			f.change(true)
		}
	}
	return func() map[int]bool {
		mu.Lock()
		defer mu.Unlock()
		return shown
	}
}

// Issue #372: scrollUntilVisible dragged 40% of the screen whatever the list's size, so a long
// scroll took about twice the drags it needs. Each drag now spans most of the list it scrolls,
// and still never steps over a row: every row on the way shows whole in some read, a tall one
// too (the drags shorten while one shows).
func TestScrollDragSpansTheList(t *testing.T) {
	t.Parallel()
	uniform := make([]int, 200)
	for i := range uniform {
		uniform[i] = 50
	}
	mixed := make([]int, 200)
	for i := range mixed {
		mixed[i] = 50
		if i%10 == 5 {
			mixed[i] = 300 // taller than the room a 640 px drag leaves in an 800 px list
		}
	}
	for _, tc := range []struct {
		name, plat string
		heights    []int
		target     int
		maxDrags   int
	}{
		// Item 120's bottom at 6050: 5250 px to scroll. 40% of the screen (320 px): 17 drags.
		{"android", "android", uniform, 120, 10},
		// iOS started 70% into the list (480 px drags): 11.
		{"ios", "ios", uniform, 120, 10},
		// 4 tall rows on the way (Item 45 at 3500-3550): the old drags took 9.
		{"tall rows", "android", mixed, 45, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: tc.plat}
			whole := f.rowsList(tc.heights, ui.Rect{R: 400, B: 800}, nil)
			mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: 'Item %d', timeout: 10000}", tc.target)))
			t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
			if n := len(f.swipeAt); n > tc.maxDrags {
				t.Errorf("%d drags to Item %d, want at most %d: %v", n, tc.target, tc.maxDrags, f.swipeAt)
			}
			shown := whole()
			for i := 0; i <= tc.target; i++ {
				if !shown[i] {
					t.Errorf("Item %d (%d px) never showed whole: a drag stepped over it (drags %v)", i, tc.heights[i], f.swipeAt)
				}
			}
		})
	}
}

// Issue #372: a long drag starts near the list's edge, where an app draws a floating button or
// a bottom bar over it. No drag may start on one (it would be a tap on it, not a drag of the
// list), and a bar drawn over the list leaves less of it to see: every row still shows whole
// above the bar in some read.
func TestScrollDragStartsClearOfWhatIsOverTheList(t *testing.T) {
	t.Parallel()
	rows := make([]int, 200)
	for i := range rows {
		rows[i] = 50
	}
	fab := ui.Rect{L: 150, T: 660, R: 250, B: 740}
	bar := ui.Rect{T: 700, R: 400, B: 800}
	for _, tc := range []struct {
		name  string
		plat  string
		cover ui.Rect
		seen  ui.Rect
	}{
		{"fab android", "android", fab, ui.Rect{R: 400, B: 800}},
		{"fab ios", "ios", fab, ui.Rect{R: 400, B: 800}},
		{"bottom bar", "android", bar, ui.Rect{R: 400, B: 700}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: tc.plat}
			whole := f.rowsList(rows, tc.seen, func() []*ui.Node {
				class := "android.widget.Button"
				if tc.plat == "ios" {
					class = "Button"
				}
				return []*ui.Node{{Class: class, Desc: "Compose", Enabled: true, Clickable: true, Bounds: tc.cover}}
			})
			mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: 'Item 120', timeout: 10000}"))
			for _, s := range f.swipeAt {
				if tc.cover.Contains(s[0], s[1]) {
					t.Errorf("a drag started on the button drawn over the list: %v", s)
				}
			}
			t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
			if n := len(f.swipeAt); n > 10 {
				t.Errorf("%d drags to Item 120, want at most 10: %v", n, f.swipeAt)
			}
			shown := whole()
			for i := 0; i <= 120; i++ {
				if !shown[i] {
					t.Errorf("Item %d never showed whole within %v (drags %v)", i, tc.seen, f.swipeAt)
					break
				}
			}
		})
	}
}
