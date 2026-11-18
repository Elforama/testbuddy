package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// rowsList makes the screen (400x800) a list filling it, with rows of the given heights from
// its top, and over it the nodes over() gives (drawn over the list: listed after it). It notes
// which rows each read could show whole within seen (the part of the list a person sees: above
// a bar drawn over it), and returns them.
func (f *fakeDriver) rowsList(heights []int, seen ui.Rect, over func() []*ui.Node) (whole func() map[int]bool) {
	return f.rowsListWith(heights, seen, over, rowsOpts{})
}

// rowsOpts: the list is a collection that does not number its rows (coll: Android's
// CollectionInfo without CollectionItemInfo, as a Compose LazyColumn reports, with info's row
// count when set, else the rows'), and how far the n-th drag (from 1) moves it, as a multiple
// of its length (moves: 2 is two drags landing in one update, 0 a drag whose touches were
// lost; nil: 1). label names row i (nil: "Item i"). A row nested says is a list of its own
// (a nested scroll view): a drag that starts on it scrolls it, not the list, and counts in
// nestedStarts. sticky is the height of a section header pinned at the list's top, drawn over
// its rows.
type rowsOpts struct {
	coll         bool
	info         *ui.Coll
	moves        func(n int) int
	label        func(i int) string
	nested       func(i int) bool
	nestedStarts *int
	sticky       int
	delta        func(n, d int) int // how far the n-th drag, d px long, moves the list (nil: by moves)
}

func (f *fakeDriver) rowsListWith(heights []int, seen ui.Rect, over func() []*ui.Node, opts rowsOpts) (whole func() map[int]bool) {
	var mu sync.Mutex
	drags := 0
	off := 0
	label := opts.label
	if label == nil {
		label = func(i int) string { return fmt.Sprintf("Item %d", i) }
	}
	nested := func(i int) bool { return opts.nested != nil && opts.nested(i) }
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
		if opts.coll {
			nodes[0].Coll = &ui.Coll{Rows: len(heights), Cols: 1}
			if opts.info != nil {
				nodes[0].Coll = opts.info
			}
			if f.Platform() == "ios" {
				nodes[0].Class = "CollectionView"
			}
		}
		top := -off
		for i, h := range heights {
			if top+h > 0 && top < 800 {
				b := ui.Rect{T: top, R: 400, B: top + h}
				if nested(i) {
					nodes = append(nodes, &ui.Node{Class: "android.widget.ScrollView", Depth: 1, Scrollable: true, Bounds: b},
						&ui.Node{Text: fmt.Sprintf("Inner %d", i), Depth: 2, Bounds: ui.Rect{T: top, R: 400, B: top + 40}})
					top += h
					continue
				}
				nodes = append(nodes, &ui.Node{Text: label(i), Depth: 1, Enabled: true, Clickable: true, Bounds: b})
			}
			top += h
		}
		if opts.sticky > 0 {
			nodes = append(nodes, &ui.Node{Text: "Section", Depth: 1, Bounds: ui.Rect{R: 400, B: opts.sticky}})
		}
		if over != nil {
			nodes = append(nodes, over()...)
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		mu.Lock()
		drags++
		k := 1
		if opts.moves != nil {
			k = opts.moves(drags)
		}
		top := -off
		for i, h := range heights {
			if y1 >= top && y1 < top+h && nested(i) {
				k = 0 // it scrolls the nested list
				if opts.nestedStarts != nil {
					*opts.nestedStarts++
				}
			}
			top += h
		}
		dy := k * (y1 - y2)
		if opts.delta != nil && k != 0 {
			dy = opts.delta(drags, y1-y2)
		}
		o := max(0, min(off+dy, total-800))
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

// Issue #372: a collection that does not number its rows (a Compose LazyColumn) gets the long
// drags too, guarded: two drags can land in one update, and with no row numbers the gap check
// cannot see the rows that skips. A long drag counts once a row that showed whole before it
// shows again, moved by about the drag's length; else the drag goes back that length and the
// rest of the scroll drags shorter, so nothing is skipped.
func TestScrollDragUnnumberedCollection(t *testing.T) {
	t.Parallel()
	rows := make([]int, 200)
	for i := range rows {
		rows[i] = 50
	}
	twice := func(at int) func(n int) int {
		return func(n int) int {
			if n == at {
				return 2
			}
			return 1
		}
	}
	for _, tc := range []struct {
		name, plat string
		moves      func(n int) int
		target     int
		maxDrags   int
	}{
		// Item 120 as in TestScrollDragSpansTheList: 17 drags of 40% of the screen.
		{"long", "android", nil, 120, 10},
		{"long ios", "ios", nil, 120, 10},
		// The first drag lands with the travel of two (two drags in one update): Items 16-25
		// never show unless the drag is checked.
		{"merged", "android", twice(1), 20, 10},
		{"merged ios", "ios", twice(1), 20, 10},
		// The third: Items 42-50 skipped.
		{"merged later", "android", twice(3), 45, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: tc.plat}
			whole := f.rowsListWith(rows, ui.Rect{R: 400, B: 800}, nil, rowsOpts{coll: true, moves: tc.moves})
			mustPass(t, runWith(t, f, quick, fmt.Sprintf("- scrollUntilVisible: {element: 'Item %d', timeout: 6000}", tc.target)))
			t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
			if n := len(f.swipeAt); n > tc.maxDrags {
				t.Errorf("%d drags to Item %d, want at most %d: %v", n, tc.target, tc.maxDrags, f.swipeAt)
			}
			if !whole()[tc.target] {
				t.Errorf("Item %d never showed whole", tc.target)
			}
		})
	}
}

// Issue #372 (review): only a list whose skips can be seen (an Android collection numbering its
// rows: the gap check; the web, whose drags are an instant scrollBy) gets a long drag unchecked.
// A collection reporting no row count (Compose's -1), a list with no collection info at all
// (a ScrollView, an iOS table or a LazyVStack) gets it checked too: the first drag lands with the
// travel of two, and Item 20 shows anyway. The check holds when the drag back is lost (its
// touches never reached the app: sent again) and when a label repeated about once a screen
// ("Sponsored") shows where a row of the drag's would: it takes two rows moved alike.
func TestScrollDragMergedDragsNeverSkip(t *testing.T) {
	t.Parallel()
	rows := make([]int, 200)
	for i := range rows {
		rows[i] = 50
	}
	merged := func(n int) int {
		if n == 1 {
			return 2
		}
		return 1
	}
	for _, tc := range []struct {
		name, plat string
		opts       rowsOpts
	}{
		{"rows -1", "android", rowsOpts{coll: true, info: &ui.Coll{Rows: -1, Cols: 1}, moves: merged}},
		{"no collection info", "android", rowsOpts{moves: merged}},
		{"ios table", "ios", rowsOpts{moves: merged}},
		{"drag back lost", "android", rowsOpts{coll: true, moves: func(n int) int {
			return map[int]int{1: 2, 2: 0}[n] + map[bool]int{true: 1}[n > 2]
		}}},
		{"repeated label", "android", rowsOpts{coll: true, moves: merged, label: func(i int) string {
			if i%17 == 14 { // rows 14 and 31: Item 15 is 430 px past the first, about a drag
				return "Sponsored"
			}
			return fmt.Sprintf("Item %d", i)
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeDriver{plat: tc.plat}
			whole := f.rowsListWith(rows, ui.Rect{R: 400, B: 800}, nil, tc.opts)
			res := runWith(t, f, quick, "- scrollUntilVisible: {element: 'Item 20', timeout: 4000}")
			t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
			mustPass(t, res)
			if !whole()[20] {
				t.Errorf("Item 20 never showed whole")
			}
		})
	}
}

// Issue #372 (review): a long drag starts near the list's edge, where a row can be a list of its
// own (a nested scroll view): a drag that starts there scrolls it, not the list. None may.
func TestScrollDragNotFromANestedList(t *testing.T) {
	t.Parallel()
	rows := make([]int, 120)
	for i := range rows {
		rows[i] = 50
		if i%6 == 3 {
			rows[i] = 250
		}
	}
	starts := 0
	f := &fakeDriver{}
	whole := f.rowsListWith(rows, ui.Rect{R: 400, B: 800}, nil, rowsOpts{nestedStarts: &starts, nested: func(i int) bool { return i%6 == 3 }})
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: 'Item 60', timeout: 6000}"))
	t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
	if starts > 0 {
		t.Errorf("%d drags started on a nested list: %v", starts, f.swipeAt)
	}
	if !whole()[60] {
		t.Errorf("Item 60 never showed whole")
	}
}

// Issue #372 (review): a section header pinned at the list's top (Compose's stickyHeader) is
// drawn over the rows under it: they do not show there. Every row still shows whole below it in
// some read.
func TestScrollDragUnderAStickyHeader(t *testing.T) {
	t.Parallel()
	rows := make([]int, 200)
	for i := range rows {
		rows[i] = 50
	}
	f := &fakeDriver{}
	whole := f.rowsListWith(rows, ui.Rect{T: 150, R: 400, B: 800}, nil, rowsOpts{sticky: 150})
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: 'Item 120', timeout: 6000}"))
	t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
	shown := whole()
	for i := 3; i <= 120; i++ { // Items 0-2 start under the header
		if !shown[i] {
			t.Errorf("Item %d never showed whole below the header (drags %v)", i, f.swipeAt)
			break
		}
	}
}

// Issue #372 (review): on iOS, a list a long drag cannot be checked on (no row named once on
// screen) keeps the drag that starts 70% into it (dragStart).
func TestScrollDragIOSUncheckedKeepsDragStart(t *testing.T) {
	t.Parallel()
	rows := make([]int, 60)
	for i := range rows {
		rows[i] = 50
	}
	f := &fakeDriver{plat: "ios"}
	f.rowsListWith(rows, ui.Rect{R: 400, B: 800}, nil, rowsOpts{label: func(i int) string {
		if i == 40 {
			return "Item 40"
		}
		return "Item"
	}})
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: 'Item 40'}"))
	if want := [4]int{200, 560, 200, 80}; len(f.swipeAt) == 0 || f.swipeAt[0] != want {
		t.Errorf("drags %v, want them like %v", f.swipeAt, want)
	}
}

// Issue #372: an endless feed without row numbers that loads more once a drag nears its end
// (bench/flows/scenarios/lists.yaml's "Post 55"): the long drags still find the row.
func TestScrollDragUnnumberedFeedLoadsMore(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	_, rows := f.scroller(20, false, 0)
	list := f.screen
	f.screen = func() []*ui.Node {
		nodes := list()
		nodes[0].Coll = &ui.Coll{Rows: int(rows.Load()), Cols: 1}
		return nodes
	}
	move := f.onSwipe
	f.onSwipe = func(x1, y1, x2, y2 int) {
		move(x1, y1, x2, y2)
		if n := rows.Load(); n < 80 {
			go func() {
				time.Sleep(300 * time.Millisecond)
				rows.CompareAndSwap(n, n+20)
				f.change(true)
			}()
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: Item 55, timeout: 10000}"))
}

// Issue #372 (review): a checked drag that moved the content well short of its length was
// taken for a list at its end once the app settled; the rest of it can still land with the
// next drag. The next drag leaves room for that. Here only Items 3, 4 and 12 are named once
// (the other rows say "Row"): the first drag (432 px) shows 72 px; the second, held at the cap
// (360 px) for want of two rows to check it by, brings the other 360 px with it: 720 px in all
// steps over row 7 of 112 px rows, unless it is kept to 312.
func TestScrollDragAfterAShortOne(t *testing.T) {
	t.Parallel()
	rows := make([]int, 40)
	for i := range rows {
		rows[i] = 112
	}
	first := 0
	f := &fakeDriver{}
	whole := f.rowsListWith(rows, ui.Rect{R: 400, B: 800}, nil, rowsOpts{
		label: func(i int) string {
			if i == 3 || i == 4 || i == 12 {
				return fmt.Sprintf("Item %d", i)
			}
			return "Row"
		},
		delta: func(n, d int) int {
			switch n {
			case 1:
				first = d
				return 72
			case 2:
				return d + first - 72
			}
			return d
		},
	})
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: 'Item 12', timeout: 6000}"))
	t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
	shown := whole()
	for i := 0; i <= 12; i++ {
		if !shown[i] {
			t.Errorf("row %d never showed whole (drags %v)", i, f.swipeAt)
		}
	}
}

// Issue #372 (review): the element is in a short list of its own in a scrolling page (ownList
// chose it, as placeVertical drags it): the long drags scroll that list, never the page around
// it (it shows less than half as much, which otherwise takes the page for the list dragged).
func TestScrollDragKeepsTheListHoldingTheElement(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	page, inner := 0, 0
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		mu.Lock()
		defer mu.Unlock()
		nodes := []*ui.Node{{Class: "page", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}}
		for i := 0; i < 10; i++ {
			top := i*50 - page
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Page %d", i), Depth: 1, Bounds: ui.Rect{T: top, R: 400, B: top + 50}})
		}
		nodes = append(nodes, &ui.Node{Class: "list", Depth: 1, Scrollable: true, Bounds: ui.Rect{T: 500 - page, R: 400, B: 750 - page}})
		for j := 0; j < 40; j++ { // a list that lists all its rows, as iOS does
			top := 500 - page + j*50 - inner
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Inner %d", j), Depth: 2, Enabled: true, Clickable: true,
				Bounds: ui.Rect{T: top, R: 400, B: top + 50}})
		}
		for i := 0; i < 20; i++ {
			top := 750 + i*50 - page
			nodes = append(nodes, &ui.Node{Text: fmt.Sprintf("Footer %d", i), Depth: 1, Bounds: ui.Rect{T: top, R: 400, B: top + 50}})
		}
		return nodes
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		mu.Lock()
		was := [2]int{page, inner}
		if y1 >= 500-page && y1 < 750-page {
			inner = clamp(inner+y1-y2, 0, 40*50-250)
		} else {
			page = clamp(page+y1-y2, 0, 1750-800)
		}
		changed := was != [2]int{page, inner}
		mu.Unlock()
		if changed {
			f.change(true)
		}
	}
	mustPass(t, runWith(t, f, quick, "- scrollUntilVisible: {element: 'Inner 30', timeout: 5000}"))
	t.Logf("%d drags: %v", len(f.swipeAt), f.swipeAt)
	mu.Lock()
	defer mu.Unlock()
	if page != 0 {
		t.Errorf("the page moved %d px: the drags went to it, not the list holding Inner 30 (%v)", page, f.swipeAt)
	}
}
