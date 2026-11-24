package engine

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

func (f *fakeDriver) rowsList(heights []int, seen ui.Rect, over func() []*ui.Node) (whole func() map[int]bool) {
	return f.rowsListWith(heights, seen, over, rowsOpts{})
}

type rowsOpts struct {
	coll         bool
	info         *ui.Coll
	moves        func(n int) int
	label        func(i int) string
	nested       func(i int) bool
	nestedStarts *int
	sticky       int
	delta        func(n, d int) int
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
	note := func() {
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
				k = 0
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
			mixed[i] = 300
		}
	}
	for _, tc := range []struct {
		name, plat string
		heights    []int
		target     int
		maxDrags   int
	}{
		{"android", "android", uniform, 120, 10},
		{"ios", "ios", uniform, 120, 10},
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
		{"long", "android", nil, 120, 10},
		{"long ios", "ios", nil, 120, 10},
		{"merged", "android", twice(1), 20, 10},
		{"merged ios", "ios", twice(1), 20, 10},
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
			if i%17 == 14 {
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
	for i := 3; i <= 120; i++ {
		if !shown[i] {
			t.Errorf("Item %d never showed whole below the header (drags %v)", i, f.swipeAt)
			break
		}
	}
}

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

func TestScrollDragAfterAPartlyShownOne(t *testing.T) {
	t.Parallel()
	rows := make([]int, 40)
	for i := range rows {
		rows[i] = 170
	}
	rows[0] = 120
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
				return 339
			case 2:
				return d + first - 339
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
		for j := 0; j < 40; j++ {
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
