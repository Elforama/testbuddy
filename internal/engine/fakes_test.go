package engine

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
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
	mu          sync.Mutex
	screen      func() []*ui.Node
	seq         int64
	settled     bool
	reads       atomic.Int64
	taps        []([2]int)
	swipes      []driver.ActionOpts
	swipeMs     []time.Duration
	subs        []chan driver.Event
	onSwipe     func(x1, y1, x2, y2 int) // what a swipe does to the screen (default: shows the target after 3)
	waits       atomic.Int64             // WaitSettled calls
	tapOpts     []driver.ActionOpts
	keyboard    *ui.Rect   // a keyboard window over the app, if set
	kbNodes     []*ui.Node // the keyboard window's own nodes (its keys, its header), if set
	plat        string     // "android" unless set
	onTap       func(x, y int)
	clicked     func(x, y int) *bool // what a tap reports about a view taking its click (nil: unknown)
	clicks      atomic.Int64         // the agent's click count (Clicks); taps report it as it was before them
	notInjected func(n int) string   // what the n-th tap (from 1) reports of its touch not injected ("": injected)
	sysAlert    func() []*ui.Node    // nodes of a system window over the app (an iOS alert), if set
	size        [2]int               // what Size reports (default 400x800)

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
	draw      func() image.Image  // what is drawn, at 400x800 (Screenshot scales it), if set
	kbShown   bool                // Screen reports the keyboard as shown
	onWait    func()              // runs inside WaitSettled (a change while settling)
	fg        string              // Foreground ("app" unless set)
	onType    func(text string)   // what typing does to the screen
	onPress   func(key string)    // what a key press does to the screen
	onLaunch  func(app string)    // what launching does
	tracked   bool                // Screen reports every later change as bumping Seq (web, issue #80)
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

func (f *fakeDriver) Serial() string { return "fake" }

func (f *fakeDriver) Name() string { return "fake" }

func (f *fakeDriver) Size() (int, int) {
	if f.size != [2]int{} {
		return f.size[0], f.size[1]
	}
	return 400, 800
}

func (f *fakeDriver) Seq() int64 { f.mu.Lock(); defer f.mu.Unlock(); return f.seq }

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
	tracked := f.tracked
	f.mu.Unlock()
	w := &ui.Window{Type: "app", Package: "app", Active: true, Nodes: nodes, Bounds: ui.Rect{R: 400, B: 800}}
	s := &ui.Screen{Platform: f.Platform(), Width: 400, Height: 800, Seq: seq, Windows: []*ui.Window{w}, Keyboard: kbShown, Tracked: tracked}
	if f.keyboard != nil {
		kb := &ui.Window{Type: "keyboard", Package: "ime", Bounds: *f.keyboard, Nodes: f.kbNodes}
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
	var missed string
	if f.notInjected != nil {
		missed = f.notInjected(len(f.taps))
	}
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
	if missed != "" {
		no := false
		return driver.ActionResult{Settle: driver.NoChange, Clicked: &no, ClicksBefore: &before, NotInjected: missed}, nil
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
	draw := f.draw
	shot := f.shot
	f.mu.Unlock()
	if draw == nil || err != nil {
		return shot, err
	}
	return scaledPNG(draw(), s), nil
}

// scaledPNG is img scaled by s (nearest pixel), as a PNG: what a device's downscaled
// screenshot gives.
func scaledPNG(img image.Image, s float64) []byte {
	b := img.Bounds()
	w, h := max(1, int(float64(b.Dx())*s)), max(1, int(float64(b.Dy())*s))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Set(x, y, img.At(b.Min.X+int(float64(x)/s), b.Min.Y+int(float64(y)/s)))
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, out)
	return buf.Bytes()
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

func (f *fakeDriver) Close() error { return nil }

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
