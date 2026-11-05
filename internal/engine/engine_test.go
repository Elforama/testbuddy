package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// fakeDriver is a device whose screen and events the test controls. It counts screen
// reads, so tests can prove waits do not poll. It records every call it gets (calls, and
// per-method slices) and can be told to fail any method (errs, keyed by method name).
type fakeDriver struct {
	mu       sync.Mutex
	screen   func() []*ui.Node
	seq      int64
	settled  bool
	reads    atomic.Int64
	taps     []([2]int)
	swipes   []driver.ActionOpts
	swipeMs  []time.Duration
	subs     []chan driver.Event
	onSwipe  func(x1, y1, x2, y2 int) // what a swipe does to the screen (default: shows the target after 3)
	waits    atomic.Int64             // WaitSettled calls
	tapOpts  []driver.ActionOpts
	keyboard *ui.Rect // a keyboard window over the app, if set
	plat     string   // "android" unless set
	onTap    func(x, y int)
	clicked  func(x, y int) *bool // what a tap reports about a view taking its click (nil: unknown)
	clicks   atomic.Int64         // the agent's click count (Clicks); taps report it as it was before them
	sysAlert func() []*ui.Node    // nodes of a system window over the app (an iOS alert), if set

	// Recording and configuration added for the wider command tests.
	calls     []string            // every non-read call, "Method arg..." in order
	tapKind   []string            // per tap: "tap", "double" or "long"
	holds     []time.Duration     // per long press: the hold
	swipeAt   [][4]int            // per swipe: x1, y1, x2, y2
	typed     []string            // TypeText texts
	typeOpts  []driver.ActionOpts // TypeText options
	erased    []int               // EraseText counts
	keys      []string            // PressKey keys
	keyOpts   []driver.ActionOpts // PressKey options
	launches  []launchCall        // LaunchApp calls
	perms     []permCall          // SetPermissions calls
	links     []linkCall          // OpenLink calls
	locs      [][2]float64        // SetLocation calls
	orients   []string            // SetOrientation calls
	media     [][]string          // AddMedia calls
	recs      []string            // StartRecording paths
	waitDur   []time.Duration     // WaitSettled timeouts
	errs      map[string]error    // method name -> error to return
	tapSettle driver.SettleResult // Tap/DoubleTap/LongPress result ("" = Changed)
	keySettle driver.SettleResult // PressKey result ("" = none, as an agent that does not say)
	shot      []byte              // Screenshot bytes
	kbShown   bool                // Screen reports the keyboard as shown
	onWait    func()              // runs inside WaitSettled (a change while settling)
	fg        string              // Foreground ("app" unless set)
	onType    func(text string)   // what typing does to the screen
	onPress   func(key string)    // what a key press does to the screen
	onLaunch  func(app string)    // what launching does
}

type launchCall struct {
	app string
	o   driver.LaunchOpts
}

type permCall struct {
	app   string
	perms map[string]string
}

type linkCall struct {
	link, app       string
	verify, browser bool
}

func (f *fakeDriver) node(text, id string, top int) *ui.Node {
	return &ui.Node{Text: text, ID: id, Bounds: ui.Rect{L: 0, T: top, R: 400, B: top + 50}, Enabled: true, Clickable: true}
}

// change bumps the change counter and pushes "changed"; settle pushes "settled".
func (f *fakeDriver) change(settle bool) {
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.settled = settle
	subs := append([]chan driver.Event(nil), f.subs...)
	f.mu.Unlock()
	for _, s := range subs {
		s <- driver.Event{Kind: "changed", Seq: seq}
		if settle {
			s <- driver.Event{Kind: "settled", Seq: seq}
		}
	}
}

// push sends one event to every subscriber as is.
func (f *fakeDriver) push(e driver.Event) {
	f.mu.Lock()
	subs := append([]chan driver.Event(nil), f.subs...)
	f.mu.Unlock()
	for _, s := range subs {
		s <- e
	}
}

// record logs a call and returns the error configured for the method.
func (f *fakeDriver) record(method string, args ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	parts := []string{method}
	for _, a := range args {
		parts = append(parts, fmt.Sprint(a))
	}
	f.calls = append(f.calls, strings.Join(parts, " "))
	return f.errs[method]
}

// called reports the recorded calls whose method is m.
func (f *fakeDriver) called(m string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c == m || strings.HasPrefix(c, m+" ") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeDriver) fail(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[method] = err
}

func (f *fakeDriver) Platform() string {
	if f.plat != "" {
		return f.plat
	}
	return "android"
}
func (f *fakeDriver) Serial() string   { return "fake" }
func (f *fakeDriver) Name() string     { return "fake" }
func (f *fakeDriver) Size() (int, int) { return 400, 800 }
func (f *fakeDriver) Seq() int64       { f.mu.Lock(); defer f.mu.Unlock(); return f.seq }
func (f *fakeDriver) Screen(ctx context.Context) (*ui.Screen, error) {
	f.reads.Add(1)
	f.mu.Lock()
	if err := f.errs["Screen"]; err != nil {
		f.mu.Unlock()
		return nil, err
	}
	seq := f.seq
	var nodes []*ui.Node
	if f.screen != nil {
		nodes = f.screen()
	}
	kbShown := f.kbShown
	f.mu.Unlock()
	w := &ui.Window{Type: "app", Package: "app", Active: true, Nodes: nodes, Bounds: ui.Rect{R: 400, B: 800}}
	s := &ui.Screen{Platform: f.Platform(), Width: 400, Height: 800, Seq: seq, Windows: []*ui.Window{w}, Keyboard: kbShown}
	if f.keyboard != nil {
		kb := &ui.Window{Type: "keyboard", Package: "ime", Bounds: *f.keyboard}
		s.Windows = []*ui.Window{kb, w}
	}
	if f.sysAlert != nil {
		if a := f.sysAlert(); len(a) > 0 {
			sys := &ui.Window{Type: "system", Package: "com.apple.springboard", Nodes: a, Bounds: ui.Rect{R: 400, B: 800}}
			s.Windows = append([]*ui.Window{sys}, s.Windows...)
		}
	}
	s.Link()
	return s, nil
}
func (f *fakeDriver) Events() (<-chan driver.Event, func()) {
	ch := make(chan driver.Event, 64)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, s := range f.subs {
			if s == ch {
				f.subs = append(f.subs[:i], f.subs[i+1:]...)
				return
			}
		}
	}
}
func (f *fakeDriver) WaitSettled(ctx context.Context, t time.Duration) error {
	f.waits.Add(1)
	f.mu.Lock()
	f.waitDur = append(f.waitDur, t)
	on := f.onWait
	f.mu.Unlock()
	if on != nil {
		on()
	}
	return nil
}
func (f *fakeDriver) Settle(ctx context.Context, since int64, n int, t time.Duration) (driver.SettleResult, error) {
	return driver.Changed, nil
}
func (f *fakeDriver) tapAs(kind string, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	f.mu.Lock()
	f.taps = append(f.taps, [2]int{x, y})
	f.tapOpts = append(f.tapOpts, o)
	f.tapKind = append(f.tapKind, kind)
	before := f.clicks.Load() // the agent takes the count as the action starts
	on := f.onTap
	clicked := f.clicked
	err := f.errs["Tap"]
	res := f.tapSettle
	f.mu.Unlock()
	if err != nil {
		return driver.ActionResult{}, err
	}
	var c *bool
	if clicked != nil {
		c = clicked(x, y)
	}
	if on != nil {
		on(x, y)
	}
	if res == "" {
		res = driver.Changed
	}
	return driver.ActionResult{Settle: res, Clicked: c, ClicksBefore: &before}, nil
}
func (f *fakeDriver) Clicks(ctx context.Context) (int64, error) { return f.clicks.Load(), nil }
func (f *fakeDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return f.tapAs("tap", x, y, o)
}
func (f *fakeDriver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return f.tapAs("double", x, y, o)
}
func (f *fakeDriver) LongPress(ctx context.Context, x, y int, h time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	f.mu.Lock()
	f.holds = append(f.holds, h)
	f.mu.Unlock()
	return f.tapAs("long", x, y, o)
}
func (f *fakeDriver) Swipe(ctx context.Context, x1, y1, x2, y2 int, d time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	f.mu.Lock()
	if err := f.errs["Swipe"]; err != nil {
		f.mu.Unlock()
		return driver.ActionResult{}, err
	}
	f.swipes = append(f.swipes, o)
	f.swipeMs = append(f.swipeMs, d)
	f.swipeAt = append(f.swipeAt, [4]int{x1, y1, x2, y2})
	n := len(f.swipes)
	on := f.onSwipe
	f.mu.Unlock()
	if on != nil {
		on(x1, y1, x2, y2)
		return driver.ActionResult{Settle: driver.Changed}, nil
	}
	if n >= 3 {
		f.change(true) // the target scrolls into view after the third drag
	}
	return driver.ActionResult{Settle: driver.Changed}, nil
}
func (f *fakeDriver) TypeText(ctx context.Context, t string, o driver.ActionOpts) (driver.ActionResult, error) {
	err := f.record("TypeText", t)
	f.mu.Lock()
	f.typed = append(f.typed, t)
	f.typeOpts = append(f.typeOpts, o)
	on := f.onType
	f.mu.Unlock()
	if err == nil && on != nil {
		on(t)
	}
	return driver.ActionResult{}, err
}
func (f *fakeDriver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	err := f.record("EraseText", n)
	f.mu.Lock()
	f.erased = append(f.erased, n)
	f.mu.Unlock()
	return driver.ActionResult{}, err
}
func (f *fakeDriver) PressKey(ctx context.Context, k string, o driver.ActionOpts) (driver.ActionResult, error) {
	err := f.record("PressKey", k)
	f.mu.Lock()
	f.keys = append(f.keys, k)
	f.keyOpts = append(f.keyOpts, o)
	res := f.keySettle
	on := f.onPress
	f.mu.Unlock()
	if err != nil {
		return driver.ActionResult{}, err
	}
	if on != nil {
		on(k)
	}
	return driver.ActionResult{Settle: res}, nil
}
func (f *fakeDriver) HideKeyboard(ctx context.Context) error { return f.record("HideKeyboard") }
func (f *fakeDriver) Screenshot(ctx context.Context, s float64, fm string) ([]byte, error) {
	err := f.record("Screenshot", s, fm)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shot, err
}
func (f *fakeDriver) LaunchApp(ctx context.Context, a string, o driver.LaunchOpts) error {
	err := f.record("LaunchApp", a)
	f.mu.Lock()
	f.launches = append(f.launches, launchCall{a, o})
	on := f.onLaunch
	f.mu.Unlock()
	if err == nil && on != nil {
		on(a)
	}
	return err
}
func (f *fakeDriver) StopApp(ctx context.Context, a string) error { return f.record("StopApp", a) }
func (f *fakeDriver) KillApp(ctx context.Context, a string) error { return f.record("KillApp", a) }
func (f *fakeDriver) ClearState(ctx context.Context, a string) error {
	return f.record("ClearState", a)
}
func (f *fakeDriver) ClearKeychain(ctx context.Context) error { return f.record("ClearKeychain") }
func (f *fakeDriver) OpenLink(ctx context.Context, l, a string, v, b bool) error {
	err := f.record("OpenLink", l)
	f.mu.Lock()
	f.links = append(f.links, linkCall{l, a, v, b})
	f.mu.Unlock()
	return err
}
func (f *fakeDriver) SetPermissions(ctx context.Context, a string, p map[string]string) error {
	err := f.record("SetPermissions", a)
	f.mu.Lock()
	f.perms = append(f.perms, permCall{a, p})
	f.mu.Unlock()
	return err
}
func (f *fakeDriver) SetLocation(ctx context.Context, la, lo float64) error {
	err := f.record("SetLocation", la, lo)
	f.mu.Lock()
	f.locs = append(f.locs, [2]float64{la, lo})
	f.mu.Unlock()
	return err
}
func (f *fakeDriver) SetOrientation(ctx context.Context, o string) error {
	err := f.record("SetOrientation", o)
	f.mu.Lock()
	f.orients = append(f.orients, o)
	f.mu.Unlock()
	return err
}
func (f *fakeDriver) SetAirplaneMode(ctx context.Context, on bool) error {
	return f.record("SetAirplaneMode", on)
}
func (f *fakeDriver) ToggleAirplaneMode(ctx context.Context) error {
	return f.record("ToggleAirplaneMode")
}
func (f *fakeDriver) AddMedia(ctx context.Context, p []string) error {
	err := f.record("AddMedia", len(p))
	f.mu.Lock()
	f.media = append(f.media, p)
	f.mu.Unlock()
	return err
}
func (f *fakeDriver) InstallApp(ctx context.Context, p string) error {
	return f.record("InstallApp", p)
}
func (f *fakeDriver) Foreground(ctx context.Context) (string, error) {
	err := f.record("Foreground")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fg != "" {
		return f.fg, err
	}
	return "app", err
}
func (f *fakeDriver) WaitForApp(ctx context.Context, a string, t time.Duration) error {
	return f.record("WaitForApp", a)
}
func (f *fakeDriver) StartRecording(ctx context.Context, p string) error {
	err := f.record("StartRecording", p)
	f.mu.Lock()
	f.recs = append(f.recs, p)
	f.mu.Unlock()
	return err
}
func (f *fakeDriver) StopRecording(ctx context.Context) error { return f.record("StopRecording") }
func (f *fakeDriver) Close() error                            { return nil }

func run(t *testing.T, f *fakeDriver, yaml string) (FlowResult, error) {
	t.Helper()
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n"+yaml))
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, Options{LookupTimeout: 3 * time.Second, OptionalTimeout: time.Second})
	res := r.RunFlow(context.Background(), fl)
	return res, res.Err
}

// An element that appears later is found when the device reports the change, with no
// reads in between (no polling).
func TestFindWaitsOnEvents(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	shown := atomic.Bool{}
	f.screen = func() []*ui.Node {
		if shown.Load() {
			return []*ui.Node{f.node("Welcome", "", 100)}
		}
		return []*ui.Node{f.node("Loading", "", 100)}
	}
	go func() {
		time.Sleep(600 * time.Millisecond)
		shown.Store(true)
		f.change(true)
	}()
	start := time.Now()
	if _, err := run(t, f, `- assertVisible: "Welcome"`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 500*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("found after %s; want right after the change at 600ms", d)
	}
	if n := f.reads.Load(); n > 3 {
		t.Errorf("%d screen reads while waiting: the wait polled", n)
	}
}

// assertNotVisible passes on a screen whose ticker never stops changing.
func TestAbsentOnRestlessScreen(t *testing.T) {
	f := &fakeDriver{}
	var tick atomic.Int64
	f.screen = func() []*ui.Node {
		return []*ui.Node{f.node("Live: "+string(rune('0'+tick.Load()%10)), "ticker", 100)}
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				tick.Add(1)
				f.change(false) // restless: changes keep coming, never "settled"
			}
		}
	}()
	start := time.Now()
	if _, err := run(t, f, `- assertNotVisible: "Spinner"`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("assertNotVisible took %s on a never-still screen", d)
	}
}

// A tap on a never-still screen happens at once, at the element's centre.
func TestTapWhileTicking(t *testing.T) {
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		return []*ui.Node{f.node("Live", "ticker", 0), f.node("Tap me", "tap_me", 200)}
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(30 * time.Millisecond):
				f.change(false)
			}
		}
	}()
	start := time.Now()
	if _, err := run(t, f, `- tapOn: {id: tap_me}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("tap took %s", d)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.taps) != 1 || f.taps[0] != [2]int{200, 225} {
		t.Errorf("taps = %v, want one at (200, 225)", f.taps)
	}
}

// Optional lookups that fail are warnings; the flow continues.
func TestOptionalWarns(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.screen = func() []*ui.Node { return []*ui.Node{f.node("Home", "", 0)} }
	res, err := run(t, f, "- tapOn: {text: Nope, optional: true}\n- assertVisible: Home")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 || res.Steps[0].Status != Warned || res.Steps[1].Status != Passed {
		t.Errorf("steps = %+v", res.Steps)
	}
}

// list is a scrolling list of n rows, 50 px each, moved up by the offset; a drag moves it by
// the drag's length (no fling), up to the list's end.
func (f *fakeDriver) list(n int) *atomic.Int64 {
	var off atomic.Int64
	f.screen = func() []*ui.Node {
		box := &ui.Node{Class: "list", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		nodes := []*ui.Node{box}
		for i := 0; i < n; i++ {
			top := 100 + i*50 - int(off.Load())
			if top+50 <= 0 || top >= 800 {
				continue // off screen: a recycled row
			}
			row := f.node(fmt.Sprintf("Item %d", i), "", top)
			row.Depth = 1
			nodes = append(nodes, row)
		}
		return nodes
	}
	end := int64(max(0, 100+n*50-800))
	f.onSwipe = func(x1, y1, x2, y2 int) {
		o := min(off.Load()+int64(y1-y2), end)
		if o != off.Load() {
			off.Store(o)
			f.change(true)
		}
	}
	return &off
}

// scrollUntilVisible drags without flinging and drags again right after each read, with no
// settle between drags.
func TestScrollDragsWithoutWaiting(t *testing.T) {
	f := &fakeDriver{}
	f.list(60)
	start := time.Now()
	if _, err := run(t, f, `- scrollUntilVisible: {element: "Item 40"}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("scroll took %s", d)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Item 40 starts at 2100 px; each drag (center to 10%) moves 320 px.
	if len(f.swipes) != 5 {
		t.Errorf("%d drags, want 5", len(f.swipes))
	}
	for _, o := range f.swipes {
		if !o.Drag || !o.NoSettle {
			t.Errorf("swipe options %+v: want a drag with no settle", o)
		}
	}
}

// At the list's end a drag moves nothing: the next one waits for the screen to change
// rather than dragging over and over.
func TestScrollStopsDraggingAtListEnd(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	f.list(20)
	_, err := run(t, f, `- scrollUntilVisible: {element: "Item 99", timeout: 2500}`)
	if err == nil || !strings.Contains(err.Error(), "No visible element found") {
		t.Fatalf("err = %v, want not found", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.swipes) > 5 {
		t.Errorf("%d drags at the list's end in 2.5 s: it did not wait for a change", len(f.swipes))
	}
}

// A tap returns once the UI starts reacting. An assertion after it runs at once; a command
// that needs a still screen (typing) waits for the UI to settle first.
func TestTapDefersSettle(t *testing.T) {
	for _, tc := range []struct {
		next  string
		waits int64
	}{
		{"- assertVisible: Done", 1}, // findStable before the tap only
		{"- inputText: hello", 2},    // and the settle the tap left
		{"- tapOn: {text: Done}", 3}, // the next tap settles before it looks, then as always
	} {
		f := &fakeDriver{}
		f.screen = func() []*ui.Node { return []*ui.Node{f.node("Go", "go", 0), f.node("Done", "", 100)} }
		if _, err := run(t, f, "- tapOn: {id: go}\n"+tc.next); err != nil {
			t.Fatal(err)
		}
		if n := f.waits.Load(); n != tc.waits {
			t.Errorf("%s: %d settle waits, want %d", tc.next, n, tc.waits)
		}
		if !f.tapOpts[0].React {
			t.Errorf("tap options %+v: want React", f.tapOpts[0])
		}
	}
}

// A paging list springs back from a drag shorter than a page: after two drags that bring
// nothing new into view, the swipes fling (classic swipes), which turn the page.
func TestScrollFlingsWhenDragsDoNotMove(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var page atomic.Int64
	f.screen = func() []*ui.Node {
		box := &ui.Node{Class: "pager", Scrollable: true, Bounds: ui.Rect{R: 400, B: 800}}
		row := f.node(fmt.Sprintf("Page %d", page.Load()), "", 300)
		row.Depth = 1
		return []*ui.Node{box, row}
	}
	f.onSwipe = func(x1, y1, x2, y2 int) {
		f.mu.Lock()
		o := f.swipes[len(f.swipes)-1]
		f.mu.Unlock()
		if !o.Drag { // only a fling turns the page
			page.Add(1)
			f.change(true)
		}
	}
	if _, err := run(t, f, `- scrollUntilVisible: {element: "Page 2", timeout: 10000}`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	flings := 0
	for _, o := range f.swipes {
		if !o.Drag {
			flings++
		}
	}
	if flings != 2 {
		t.Errorf("%d flings, want 2 (swipes: %d)", flings, len(f.swipes))
	}
}

// A row under a sticky footer is dragged just clear of it, then tapped where it shows.
func TestTapClearsFooter(t *testing.T) {
	f := &fakeDriver{}
	f.list(30)
	rows := f.screen
	f.screen = func() []*ui.Node {
		footer := f.node("Checkout", "footer", 700) // drawn after the list, over its bottom
		footer.Bounds = ui.Rect{R: 400, T: 700, B: 800}
		return append(rows(), footer)
	}
	// Item 12's row starts at 700 (100 + 12*50): its middle (725) is under the footer.
	if _, err := run(t, f, `- tapOn: "Item 12"`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.swipes) != 1 || !f.swipes[0].Drag {
		t.Fatalf("swipes = %+v, want one drag", f.swipes)
	}
	if tap := f.taps[0]; tap[1] >= 700 {
		t.Errorf("tapped at %v: under the footer", tap)
	}
}

// A button the keyboard half covers, on a screen that does not scroll, is tapped on its part
// above the keyboard.
func TestTapAvoidsKeyboard(t *testing.T) {
	f := &fakeDriver{keyboard: &ui.Rect{T: 600, R: 400, B: 800}}
	f.screen = func() []*ui.Node {
		b := f.node("Send", "send", 500)
		b.Bounds = ui.Rect{T: 500, R: 400, B: 700}
		return []*ui.Node{b}
	}
	if _, err := run(t, f, `- tapOn: {id: send}`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taps[0] != [2]int{200, 550} {
		t.Errorf("tapped at %v, want (200, 550) above the keyboard", f.taps[0])
	}
}

// A label drawn over the element takes no touches: no cover, the tap goes to the middle.
func TestTapThroughLabel(t *testing.T) {
	f := &fakeDriver{}
	f.screen = func() []*ui.Node {
		b := f.node("Buy", "buy", 100)
		l := &ui.Node{Text: "50% off", Bounds: ui.Rect{L: 150, T: 110, R: 250, B: 140}} // not clickable
		return []*ui.Node{b, l}
	}
	if _, err := run(t, f, `- tapOn: {id: buy}`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taps[0] != [2]int{200, 125} {
		t.Errorf("tapped at %v, want the middle (200, 125)", f.taps[0])
	}
}

// Android's "isn't responding" dialog is answered Wait the first time, Close app when it comes
// back; then the lookup goes on.
func TestAnswersAndroidErrorDialog(t *testing.T) {
	f := &fakeDriver{}
	var shows atomic.Int64
	shows.Store(2) // shows twice
	f.screen = func() []*ui.Node {
		if shows.Load() == 0 {
			return []*ui.Node{f.node("Home", "", 100)}
		}
		title := f.node("Launcher isn't responding", "android:id/alertTitle", 100)
		wait := f.node("Wait", "android:id/aerr_wait", 200)
		closeApp := f.node("Close app", "android:id/aerr_close", 300)
		return []*ui.Node{title, wait, closeApp}
	}
	f.onTap = func(x, y int) { shows.Add(-1); f.change(true) }
	if _, err := run(t, f, `- assertVisible: Home`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.taps) != 2 || f.taps[0][1] != 225 || f.taps[1][1] != 325 {
		t.Errorf("taps = %v, want Wait (y 225) then Close app (y 325)", f.taps)
	}
}

// iOS's notifications alert is answered as launchApp's permissions say.
func TestAnswersNotificationAlert(t *testing.T) {
	for _, tc := range []struct {
		perms string
		want  int // the y of the button tapped, 0 for none
	}{
		{"", 525}, // default: all allowed
		{":\n    permissions: {notifications: deny}", 425}, // Don't Allow
		{":\n    permissions: {notifications: unset}", 0},  // left to the flow
	} {
		f := &fakeDriver{plat: "ios"}
		var shown atomic.Bool
		shown.Store(true)
		f.screen = func() []*ui.Node {
			nodes := []*ui.Node{f.node("Home", "", 100)}
			if shown.Load() {
				msg := &ui.Node{Desc: "“Bench” Would Like to Send You Notifications", Bounds: ui.Rect{R: 400, T: 300, B: 380}}
				no := f.node("", "", 400)
				no.Class, no.Desc = "Button", "Don’t Allow"
				yes := f.node("", "", 500)
				yes.Class, yes.Desc = "Button", "Allow"
				nodes = append(nodes, msg, no, yes)
			}
			return nodes
		}
		f.onTap = func(x, y int) { shown.Store(false); f.change(true) }
		if _, err := run(t, f, "- launchApp"+tc.perms+"\n- assertVisible: Home"); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		got := 0
		if len(f.taps) > 0 {
			got = f.taps[0][1]
		}
		f.mu.Unlock()
		if got != tc.want {
			t.Errorf("permissions %q: tapped y %d, want %d", tc.perms, got, tc.want)
		}
	}
}

// A disabled button is tapped once it turns on (on its change event), not before.
func TestTapWaitsForEnabled(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{}
	var on atomic.Bool
	f.screen = func() []*ui.Node {
		b := f.node("Save", "save", 100)
		b.Enabled = on.Load()
		return []*ui.Node{b}
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		on.Store(true)
		f.change(true)
	}()
	start := time.Now()
	if _, err := run(t, f, `- tapOn: {id: save}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 250*time.Millisecond || d > 1500*time.Millisecond {
		t.Errorf("tapped after %s; want right after it turned on (300 ms)", d)
	}
	// Asked for as disabled: tapped at once.
	f2 := &fakeDriver{}
	f2.screen = func() []*ui.Node { b := f2.node("Save", "save", 100); b.Enabled = false; return []*ui.Node{b} }
	start = time.Now()
	if _, err := run(t, f2, `- tapOn: {id: save, enabled: false}`); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("tap on a selector asking for disabled took %s", d)
	}
}

// Checks for things that do not show (`runFlow` with `when: visible`) share one optional
// timeout counted from the last command that acted on the app: a runFlow whose commands only
// checked the screen did not act, so the next check does not wait the whole timeout again.
func TestOptionalChecksShareTheTimeout(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 100))}
	check := func(name string) string {
		return "- runFlow:\n    commands:\n      - runFlow:\n          when:\n            visible: " + name +
			"\n          commands:\n            - tapOn: " + name + "\n"
	}
	o := Options{LookupTimeout: time.Second, OptionalTimeout: 400 * time.Millisecond}
	start := time.Now()
	mustPass(t, runWith(t, f, o, check("Promo")+check("Survey")+check("Rate us")))
	if d := time.Since(start); d > 750*time.Millisecond {
		t.Errorf("three checks took %s, want about one optional timeout (400 ms)", d)
	}
	// A command that acts restarts the clock: the check after it waits again.
	start = time.Now()
	mustPass(t, runWith(t, f, o, check("Promo")+"- tapOn: Home\n"+check("Survey")))
	if d := time.Since(start); d < 700*time.Millisecond {
		t.Errorf("took %s: the check after a tap should wait its own timeout", d)
	}
}

// On the web, startRecording is not supported yet: the flow goes on, with a note, and
// stopRecording does nothing.
func TestWebRecordingSkipped(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(btn("Home", "", 100)), plat: "web"}
	var mu sync.Mutex
	var logs []string
	o := quick
	o.ArtifactsDir = t.TempDir()
	o.Reporter = ReporterFunc(func(e Event) {
		if e.Log != "" {
			mu.Lock()
			logs = append(logs, e.Log)
			mu.Unlock()
		}
	})
	mustPass(t, runWith(t, f, o, "- startRecording: demo\n- assertVisible: Home\n- stopRecording\n"))
	if len(f.recs) != 0 {
		t.Errorf("recordings started: %v", f.recs)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 1 || !strings.Contains(logs[0], "not supported on web") || !strings.Contains(logs[0], "demo.mp4") {
		t.Errorf("logs %q", logs)
	}
}
