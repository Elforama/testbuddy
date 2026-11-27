package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
)

func settingsScreen() []item {
	return []item{
		label("Settings", 0),
		{text: "Dark theme", class: "android.widget.Switch", top: 100},
		{text: "Notifications", class: "android.widget.Switch", top: 200, checked: true},
		{class: "android.widget.EditText", hint: "Email", text: "bob@x.io", top: 300},
		label("Total: €24.99", 400),
		label("Price: $1", 500),
		label("Price: $2", 600),
		label("Summary", 700),
	}
}

func readSession(t *testing.T) (*Service, *fakeDriver, *Dev) {
	t.Helper()
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(settingsScreen()...)
	return s, f, addDev(s, f, "")
}

func TestReadPrintsTheValue(t *testing.T) {
	s, f, d := readSession(t)
	for label, want := range map[string]string{
		"Dark theme":    "ok \"Dark theme\" = \"off\"\n",
		"Notifications": "ok \"Notifications\" = \"on\"\n",
		"Email":         "ok \"Email\" = \"bob@x.io\"\n",
		"Total":         "ok \"Total\" = \"€24.99\"\n",
	} {
		resp, _ := call(t, s.read, map[string]any{"label": label}, "")
		if !resp.OK || resp.Exit != 0 || resp.Text != want {
			t.Errorf("read %q: exit %d, text %q; want %q", label, resp.Exit, resp.Text, want)
		}
	}
	if len(f.taps) != 0 || len(f.typed) != 0 || len(d.steps) != 0 {
		t.Errorf("read touched the app or was recorded: taps %v, typed %v, steps %v", f.taps, f.typed, d.steps)
	}
	if d.last == nil {
		t.Error("read should leave its listing as the latest one, for the refs it prints")
	}
}

func TestReadEquals(t *testing.T) {
	s, _, _ := readSession(t)
	resp, _ := call(t, s.read, map[string]any{"label": "Total", "equals": "€24.99"}, "")
	if !resp.OK || resp.Exit != 0 || resp.Text != "ok \"Total\" = \"€24.99\"\n" {
		t.Errorf("equal: exit %d, text %q", resp.Exit, resp.Text)
	}
	resp, _ = call(t, s.read, map[string]any{"label": "Total", "equals": "€20.00"}, "")
	if resp.OK || resp.Exit != 1 || resp.Text != "\"Total\" shows \"€24.99\", not \"€20.00\"\n" {
		t.Errorf("different: exit %d, text %q", resp.Exit, resp.Text)
	}
	resp, _ = call(t, s.read, map[string]any{"label": "Email", "equals": ""}, "")
	if resp.Exit != 1 || resp.Text != "\"Email\" shows \"bob@x.io\", not \"\"\n" {
		t.Errorf("an empty --equals is a value to compare with: exit %d, text %q", resp.Exit, resp.Text)
	}
}

func TestReadNotFoundSuggestsSimilar(t *testing.T) {
	s, _, _ := readSession(t)
	resp, _ := call(t, s.read, map[string]any{"label": "Dark mode"}, "")
	want := "\"Dark mode\" is not on screen\n  similar: @2 Switch \"Dark theme\" unchecked\n"
	if resp.OK || resp.Exit != 1 || resp.Text != want {
		t.Errorf("exit %d, text %q; want %q", resp.Exit, resp.Text, want)
	}
}

func TestReadLabelWithoutValue(t *testing.T) {
	s, _, _ := readSession(t)
	resp, _ := call(t, s.read, map[string]any{"label": "Summary"}, "")
	if resp.OK || resp.Exit != 1 || !strings.HasPrefix(resp.Text, "\"Summary\" is on screen, but no value shows with it") {
		t.Errorf("exit %d, text %q", resp.Exit, resp.Text)
	}
}

func TestReadAmbiguousSaysHowMany(t *testing.T) {
	s, _, _ := readSession(t)
	resp, _ := call(t, s.read, map[string]any{"label": "Price"}, "")
	want := "ok \"Price\" = \"$1\" (2 elements match \"Price\": this is the first)\n"
	if !resp.OK || resp.Text != want {
		t.Errorf("exit %d, text %q; want %q", resp.Exit, resp.Text, want)
	}
}

func TestReadJSON(t *testing.T) {
	s, _, _ := readSession(t)
	resp, _ := call(t, s.read, map[string]any{"label": "Total", "equals": "€20.00", "json": true}, "")
	var got struct {
		Exit    int    `json:"exit"`
		Query   string `json:"query"`
		Label   string `json:"label"`
		Value   string `json:"value"`
		From    string `json:"from"`
		Matches int    `json:"matches"`
		Equals  string `json:"equals"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(resp.JSON, &got); err != nil {
		t.Fatalf("json %s: %v", resp.JSON, err)
	}
	if got.Exit != 1 || got.Query != "Total" || got.Label != "Total: €24.99" || got.Value != "€24.99" || got.From != "label" || got.Matches != 1 || got.Equals != "€20.00" || got.Error == "" {
		t.Errorf("json %s", resp.JSON)
	}
	resp, _ = call(t, s.read, map[string]any{"label": "Dark mode", "json": true}, "")
	var missed struct {
		Exit    int              `json:"exit"`
		Similar []map[string]any `json:"similar"`
	}
	if err := json.Unmarshal(resp.JSON, &missed); err != nil || missed.Exit != 1 || len(missed.Similar) != 1 {
		t.Errorf("json %s (%v)", resp.JSON, err)
	}
}

func TestReadNeedsALabel(t *testing.T) {
	s, _, _ := readSession(t)
	if resp, _ := call(t, s.read, map[string]any{"label": " "}, ""); resp.Exit != 2 {
		t.Errorf("exit %d, text %q; want 2 for a missing label", resp.Exit, resp.Text)
	}
}

type listFake struct {
	*fakeDriver
	rows        []string
	top, bottom int
	rowH        int
	offset      int
	stride      int
	swipes      [][4]int
	drags       int
	samePrice   map[string]bool
}

func (f *listFake) price(i int) string {
	if f.samePrice[f.rows[i]] {
		return "$0"
	}
	return fmt.Sprintf("$%d", i+1)
}

func numberedRows(n int) []string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf("Item %d", i+1)
	}
	return rows
}

func newListFake(rows []string) *listFake {
	return &listFake{fakeDriver: newFake("emulator-5554", "Pixel 7", device.Android), rows: rows, top: 100, bottom: 700, rowH: 100}
}

func (f *listFake) maxOffset() int { return max(0, len(f.rows)*f.rowH-(f.bottom-f.top)) }

func (f *listFake) Screen(ctx context.Context) (*ui.Screen, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	view := ui.Rect{L: 0, T: f.top, R: f.w, B: f.bottom}
	node := func(depth int, class, text string, b ui.Rect) *ui.Node {
		return &ui.Node{Depth: depth, Class: class, Text: text, Bounds: b, Enabled: true, Visible: true}
	}
	nodes := []*ui.Node{
		node(0, "android.widget.FrameLayout", "", ui.Rect{R: f.w, B: f.h}),
		node(1, "android.widget.TextView", "Orders", ui.Rect{R: f.w, B: f.top}),
		node(1, "androidx.recyclerview.widget.RecyclerView", "", view),
	}
	nodes[2].Scrollable = true
	for i, title := range f.rows {
		y := f.top + i*f.rowH - f.offset
		box := ui.Rect{L: 0, T: y, R: f.w, B: y + f.rowH}.Intersect(view)
		if box.Empty() {
			continue
		}
		nodes = append(nodes, node(2, "android.view.ViewGroup", "", box))
		for _, part := range []struct {
			text string
			b    ui.Rect
		}{{title, ui.Rect{L: 10, T: y + 10, R: 200, B: y + 50}}, {f.price(i), ui.Rect{L: 300, T: y + 25, R: 390, B: y + 65}}} {
			if shown := part.b.Intersect(view); !shown.Empty() {
				nodes = append(nodes, node(3, "android.widget.TextView", part.text, shown))
			}
		}
	}
	w := &ui.Window{Type: "app", Package: f.fg, Active: true, Nodes: nodes, Bounds: ui.Rect{R: f.w, B: f.h}}
	s := &ui.Screen{Platform: "android", Width: f.w, Height: f.h, Seq: f.seq, Windows: []*ui.Window{w}}
	s.Link()
	return s, nil
}

func (f *listFake) Swipe(ctx context.Context, x1, y1, x2, y2 int, d time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.swipes = append(f.swipes, [4]int{x1, y1, x2, y2})
	if o.Drag && !o.NoSettle {
		f.drags++
	}
	by := y1 - y2
	if f.stride != 0 && by != 0 {
		by = f.stride * by / max(by, -by)
	}
	f.offset = max(0, min(f.maxOffset(), f.offset+by))
	f.seq++
	return driver.ActionResult{}, nil
}

func collectSession(t *testing.T, rows []string) (*Service, *listFake, *Dev) {
	t.Helper()
	s := New()
	f := newListFake(rows)
	d := addDev(s, f.fakeDriver, "")
	d.Drv = f
	return s, f, d
}

func collected(t *testing.T, text string) (first string, rows []string, screen string) {
	t.Helper()
	head, screen, ok := strings.Cut(text, "screen now:\n")
	if !ok {
		t.Fatalf("no \"screen now:\" in\n%s", text)
	}
	lines := strings.Split(strings.TrimRight(head, "\n"), "\n")
	for _, l := range lines[1:] {
		rows = append(rows, strings.TrimPrefix(l, "  "))
	}
	return lines[0], rows, screen
}

func itemRows(from, to int) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("Text \"Item %d\"  Text \"$%d\"", i, i))
	}
	return out
}

func expectRows(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCollectThreePagesToTheEnd(t *testing.T) {
	s, f, d := collectSession(t, numberedRows(12))
	resp, _ := call(t, s.collect, map[string]any{}, "")
	if !resp.OK || resp.Exit != 0 {
		t.Fatalf("exit %d: %s", resp.Exit, resp.Text)
	}
	first, rows, screen := collected(t, resp.Text)
	if first != "collected 12 rows in 3 scrolls: the list ended" {
		t.Errorf("first line %q", first)
	}
	expectRows(t, rows, itemRows(1, 12))
	if strings.Contains(strings.Join(rows, "\n"), "@") {
		t.Errorf("collected rows carry refs:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(screen, "Text \"Item 12\"") || !strings.Contains(screen, "@") || strings.Contains(screen, "\"Item 1\"") {
		t.Errorf("the screen as it is now, with refs, should follow the rows:\n%s", screen)
	}
	if d.last == nil || len(d.steps) != 0 {
		t.Errorf("collect should leave its last listing for the refs and record nothing: last %v, steps %v", d.last != nil, d.steps)
	}
	if f.drags != len(f.swipes) || len(f.swipes) != 3 {
		t.Errorf("%d swipes, %d of them settled drags; want 3 drags", len(f.swipes), f.drags)
	}
	for _, sw := range f.swipes {
		if sw[0] != 200 || sw[1] <= sw[3] || sw[1] > f.bottom || sw[3] < f.top {
			t.Errorf("swipe %v: want an upward drag inside the list (%d to %d)", sw, f.top, f.bottom)
		}
	}
}

func TestCollectTakesARowCutAtThePageEdgeOnceAndWhole(t *testing.T) {
	s, f, _ := collectSession(t, numberedRows(22))
	f.offset = 0
	f.bottom = 720
	resp, _ := call(t, s.collect, map[string]any{}, "")
	first, rows, _ := collected(t, resp.Text)
	if !strings.HasPrefix(first, "collected 22 rows in ") || !strings.HasSuffix(first, ": the list ended") {
		t.Errorf("first line %q", first)
	}
	expectRows(t, rows, itemRows(1, 22))
}

func TestCollectStopsAtRows(t *testing.T) {
	s, f, _ := collectSession(t, numberedRows(40))
	resp, _ := call(t, s.collect, map[string]any{"rows": 8}, "")
	first, rows, _ := collected(t, resp.Text)
	if first != "collected 8 rows in 1 scroll: reached --rows 8" {
		t.Errorf("first line %q", first)
	}
	expectRows(t, rows, itemRows(1, 8))
	if len(f.swipes) != 1 {
		t.Errorf("%d swipes, want 1", len(f.swipes))
	}
}

func TestCollectStopsAtMaxScrolls(t *testing.T) {
	s, f, _ := collectSession(t, numberedRows(40))
	resp, _ := call(t, s.collect, map[string]any{"max_scrolls": 2}, "")
	first, rows, _ := collected(t, resp.Text)
	if first != "collected 13 rows in 2 scrolls: stopped at --max-scrolls 2" {
		t.Errorf("first line %q", first)
	}
	expectRows(t, rows, itemRows(1, 13))
	if len(f.swipes) != 2 {
		t.Errorf("%d swipes, want 2", len(f.swipes))
	}
}

func TestCollectDefaultsAre100RowsAnd30Scrolls(t *testing.T) {
	s, f, _ := collectSession(t, numberedRows(400))
	resp, _ := call(t, s.collect, map[string]any{}, "")
	first, rows, _ := collected(t, resp.Text)
	if !strings.HasPrefix(first, "collected 100 rows in ") || !strings.HasSuffix(first, ": reached --rows 100") || len(rows) != 100 {
		t.Errorf("first line %q, %d rows", first, len(rows))
	}
	f.offset, f.swipes, f.stride = 0, nil, 100
	resp, _ = call(t, s.collect, map[string]any{"rows": 5000}, "")
	first, _, _ = collected(t, resp.Text)
	if !strings.HasSuffix(first, " in 30 scrolls: stopped at --max-scrolls 30") {
		t.Errorf("first line %q", first)
	}
}

func TestCollectKeepsRowsWithTheSameText(t *testing.T) {
	names := numberedRows(12)
	names[4], names[5], names[9] = "Dup", "Dup", "Dup"
	s, f, _ := collectSession(t, names)
	f.samePrice = map[string]bool{"Dup": true}
	resp, _ := call(t, s.collect, map[string]any{}, "")
	first, rows, _ := collected(t, resp.Text)
	var want []string
	for i, n := range names {
		want = append(want, fmt.Sprintf("Text %q  Text %q", n, f.price(i)))
	}
	expectRows(t, rows, want)
	if first != "collected 12 rows in 3 scrolls: the list ended" {
		t.Errorf("first line %q", first)
	}
}

func TestCollectUpGivesListOrder(t *testing.T) {
	s, f, _ := collectSession(t, numberedRows(12))
	f.offset = f.maxOffset()
	resp, _ := call(t, s.collect, map[string]any{"direction": "up"}, "")
	first, rows, screen := collected(t, resp.Text)
	if first != "collected 12 rows in 3 scrolls: the list ended" {
		t.Errorf("first line %q", first)
	}
	expectRows(t, rows, itemRows(1, 12))
	if !strings.Contains(screen, "\"Item 1\"") {
		t.Errorf("the list should be at its top now:\n%s", screen)
	}
	for _, sw := range f.swipes {
		if sw[1] >= sw[3] {
			t.Errorf("swipe %v: want a downward drag", sw)
		}
	}
}

func TestCollectSaysWhenAPageSharesNoRowWithTheOneBefore(t *testing.T) {
	s, f, _ := collectSession(t, numberedRows(30))
	f.stride = 900
	resp, _ := call(t, s.collect, map[string]any{"max_scrolls": 1}, "")
	first, rows, _ := collected(t, resp.Text)
	want := "collected 11 rows in 1 scroll: stopped at --max-scrolls 1 (1 scroll showed no row of the page before it: rows may be missing)"
	if first != want {
		t.Errorf("first line %q, want %q", first, want)
	}
	expectRows(t, rows, append(itemRows(1, 6), itemRows(10, 14)...))
}

func TestCollectJSONAndBadInput(t *testing.T) {
	s, _, _ := collectSession(t, numberedRows(12))
	resp, _ := call(t, s.collect, map[string]any{"json": true}, "")
	var got struct {
		Rows    []string `json:"rows"`
		Scrolls int      `json:"scrolls"`
		Stopped string   `json:"stopped"`
		Screen  struct {
			Elements []map[string]any `json:"elements"`
		} `json:"screen"`
	}
	if err := json.Unmarshal(resp.JSON, &got); err != nil {
		t.Fatalf("json %s: %v", resp.JSON, err)
	}
	if len(got.Rows) != 12 || got.Rows[11] != "Text \"Item 12\"  Text \"$12\"" || got.Scrolls != 3 || got.Stopped != "end" || len(got.Screen.Elements) == 0 {
		t.Errorf("json %s", resp.JSON)
	}
	for _, bad := range []map[string]any{{"direction": "left"}, {"rows": -1}, {"max_scrolls": -3}} {
		if resp, _ := call(t, s.collect, bad, ""); resp.Exit != 2 {
			t.Errorf("%v: exit %d, text %q; want 2", bad, resp.Exit, resp.Text)
		}
	}
}

func TestCollectWithoutAListGivesTheScreen(t *testing.T) {
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(label("One", 100), label("Two", 200))
	addDev(s, f, "")
	resp, _ := call(t, s.collect, map[string]any{}, "")
	first, rows, _ := collected(t, resp.Text)
	if first != "collected 2 rows in 1 scroll: the list ended" {
		t.Errorf("first line %q", first)
	}
	expectRows(t, rows, []string{"Text \"One\"", "Text \"Two\""})
}
