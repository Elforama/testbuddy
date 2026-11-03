package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/look"
	"github.com/elforama/testbuddy/internal/ui"
)

// TestMain makes the package hermetic: no real adb, xcrun or Chrome can be found (so
// device.List finds nothing unless a test installs a fake adb), and TB_HOME is a temp folder.
func TestMain(m *testing.M) {
	// Device locks off: these tests make many pools and sessions for the same fake devices
	// (locks have their own tests, in devlock and in TestPoolLocks / TestSessionLocks).
	os.Setenv("TB_NO_DEVICE_LOCK", "1")
	dir, err := os.MkdirTemp("", "tb-session-test")
	if err != nil {
		panic(err)
	}
	empty := filepath.Join(dir, "bin")
	os.MkdirAll(empty, 0o755)
	for k, v := range map[string]string{
		"PATH":             empty,
		"HOME":             dir,
		"ANDROID_HOME":     dir,
		"ANDROID_SDK_ROOT": dir,
		"TB_HOME":          filepath.Join(dir, "tbhome"),
		"TB_CHROME":        filepath.Join(dir, "no-chrome"),
	} {
		os.Setenv(k, v)
	}
	device.WebAvailable = func() bool { return false }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// item is one node on the fake screen.
type item struct {
	text, id, class string
	hint            string
	top             int
	disabled        bool
}

func button(text string, top int) item {
	return item{text: text, class: "android.widget.Button", top: top}
}
func label(text string, top int) item {
	return item{text: text, class: "android.widget.TextView", top: top}
}

// fakeDriver is a device whose screen and events the test controls.
type fakeDriver struct {
	info device.Info

	mu         sync.Mutex
	items      []item
	seq        int64
	subs       []chan driver.Event
	w, h       int
	fg         string
	fgErr      error
	screenErr  error
	shotErr    error
	shotW      int // screenshot width in pixels (0 = w)
	shotScales []float64
	taps       [][2]int
	typed      []string
	launched   []string
	waits      int
	closed     int
	onType     func(string)
}

func newFake(serial, name string, plat device.Platform) *fakeDriver {
	return &fakeDriver{info: device.Info{Platform: plat, Serial: serial, Name: name, Emulator: true, State: "device"}, w: 400, h: 800, fg: "com.app"}
}

// show replaces the screen and reports the change.
func (f *fakeDriver) show(items ...item) {
	f.mu.Lock()
	f.items = items
	f.seq++
	f.mu.Unlock()
}

// push delivers an event to every subscriber.
func (f *fakeDriver) push(kind string) {
	f.mu.Lock()
	subs := append([]chan driver.Event(nil), f.subs...)
	seq := f.seq
	f.mu.Unlock()
	for _, s := range subs {
		s <- driver.Event{Kind: kind, Seq: seq}
	}
}

// closeEvents ends every subscription (the connection went away).
func (f *fakeDriver) closeEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		close(s)
	}
	f.subs = nil
}

func (f *fakeDriver) get(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func (f *fakeDriver) Platform() string { return string(f.info.Platform) }
func (f *fakeDriver) Serial() string   { return f.info.Serial }
func (f *fakeDriver) Name() string     { return f.info.Name }
func (f *fakeDriver) Size() (int, int) { f.mu.Lock(); defer f.mu.Unlock(); return f.w, f.h }
func (f *fakeDriver) Seq() int64       { f.mu.Lock(); defer f.mu.Unlock(); return f.seq }
func (f *fakeDriver) Screen(ctx context.Context) (*ui.Screen, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.screenErr != nil {
		return nil, f.screenErr
	}
	root := &ui.Node{Class: "android.widget.FrameLayout", Bounds: ui.Rect{R: f.w, B: f.h}, Enabled: true, Visible: true}
	nodes := []*ui.Node{root}
	for _, it := range f.items {
		cls := it.class
		if cls == "" {
			cls = "android.widget.TextView"
		}
		n := &ui.Node{Text: it.text, ID: it.id, Hint: it.hint, Class: cls, Depth: 1, Enabled: !it.disabled, Visible: true,
			Bounds:    ui.Rect{L: 0, T: it.top, R: f.w, B: it.top + 50},
			Clickable: strings.HasSuffix(cls, "Button"), Editable: strings.HasSuffix(cls, "EditText")}
		nodes = append(nodes, n)
	}
	w := &ui.Window{Type: "app", Package: f.fg, Active: true, Nodes: nodes, Bounds: ui.Rect{R: f.w, B: f.h}}
	s := &ui.Screen{Platform: string(f.info.Platform), Width: f.w, Height: f.h, Seq: f.seq, Windows: []*ui.Window{w}}
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
	f.mu.Lock()
	f.waits++
	f.mu.Unlock()
	return nil
}
func (f *fakeDriver) Settle(ctx context.Context, since int64, n int, t time.Duration) (driver.SettleResult, error) {
	return driver.NoChange, nil
}
func (f *fakeDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	f.mu.Lock()
	f.taps = append(f.taps, [2]int{x, y})
	f.mu.Unlock()
	return driver.ActionResult{Settle: driver.NoChange}, nil
}
func (f *fakeDriver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return f.Tap(ctx, x, y, o)
}
func (f *fakeDriver) LongPress(ctx context.Context, x, y int, h time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return f.Tap(ctx, x, y, o)
}
func (f *fakeDriver) Swipe(ctx context.Context, x1, y1, x2, y2 int, d time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return driver.ActionResult{}, nil
}
func (f *fakeDriver) TypeText(ctx context.Context, t string, o driver.ActionOpts) (driver.ActionResult, error) {
	f.mu.Lock()
	on := f.onType
	f.mu.Unlock()
	if on != nil {
		on(t)
	}
	f.mu.Lock()
	f.typed = append(f.typed, t)
	f.mu.Unlock()
	return driver.ActionResult{Settle: driver.NoChange}, nil
}
func (f *fakeDriver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	return driver.ActionResult{}, nil
}
func (f *fakeDriver) PressKey(ctx context.Context, k string, o driver.ActionOpts) (driver.ActionResult, error) {
	return driver.ActionResult{}, nil
}
func (f *fakeDriver) HideKeyboard(ctx context.Context) error { return nil }
func (f *fakeDriver) Screenshot(ctx context.Context, scale float64, format string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shotScales = append(f.shotScales, scale)
	if f.shotErr != nil {
		return nil, f.shotErr
	}
	w := f.shotW
	if w == 0 {
		w = f.w
	}
	return solidPNG(w, w*2, color.RGBA{10, 20, 30, 255}), nil
}
func (f *fakeDriver) LaunchApp(ctx context.Context, a string, o driver.LaunchOpts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launched = append(f.launched, a)
	return nil
}
func (f *fakeDriver) StopApp(ctx context.Context, a string) error    { return nil }
func (f *fakeDriver) KillApp(ctx context.Context, a string) error    { return nil }
func (f *fakeDriver) ClearState(ctx context.Context, a string) error { return nil }
func (f *fakeDriver) ClearKeychain(ctx context.Context) error        { return nil }
func (f *fakeDriver) OpenLink(ctx context.Context, l, a string, v, b bool) error {
	return nil
}
func (f *fakeDriver) SetPermissions(ctx context.Context, a string, p map[string]string) error {
	return nil
}
func (f *fakeDriver) SetLocation(ctx context.Context, la, lo float64) error { return nil }
func (f *fakeDriver) SetOrientation(ctx context.Context, o string) error    { return nil }
func (f *fakeDriver) SetAirplaneMode(ctx context.Context, on bool) error    { return nil }
func (f *fakeDriver) ToggleAirplaneMode(ctx context.Context) error          { return nil }
func (f *fakeDriver) AddMedia(ctx context.Context, p []string) error        { return nil }
func (f *fakeDriver) InstallApp(ctx context.Context, p string) error        { return nil }
func (f *fakeDriver) Foreground(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fg, f.fgErr
}
func (f *fakeDriver) WaitForApp(ctx context.Context, a string, t time.Duration) error {
	return nil
}
func (f *fakeDriver) StartRecording(ctx context.Context, p string) error { return nil }
func (f *fakeDriver) StopRecording(ctx context.Context) error            { return nil }
func (f *fakeDriver) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

var _ driver.Driver = (*fakeDriver)(nil)

var errBoom = errors.New("boom")

// addDev opens a device in the service the way connect does, without a real driver.
func addDev(s *Service, f *fakeDriver, name string) *Dev {
	d := &Dev{Info: f.info, Name: name, Drv: f, refs: look.NewRefs()}
	d.runner = engine.New(f, engine.Options{ArtifactsDir: filepath.Join(daemon.Dir(), "artifacts"),
		LookupTimeout: 300 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond})
	s.mu.Lock()
	s.devs[f.info.Serial] = d
	if name != "" {
		s.names[name] = f.info.Serial
	}
	s.mu.Unlock()
	return d
}

type handler func(context.Context, daemon.Request, func(daemon.Response)) daemon.Response

// call runs a handler with JSON args; it returns the final response and the streamed parts.
func call(t *testing.T, h handler, args any, cwd string) (daemon.Response, []daemon.Response) {
	t.Helper()
	return callCtx(t, context.Background(), h, args, cwd)
}

func callCtx(t *testing.T, ctx context.Context, h handler, args any, cwd string) (daemon.Response, []daemon.Response) {
	t.Helper()
	var raw json.RawMessage
	switch a := args.(type) {
	case nil:
	case string:
		raw = json.RawMessage(a)
	default:
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	var mu sync.Mutex
	var parts []daemon.Response
	resp := h(ctx, daemon.Request{Args: raw, Cwd: cwd}, func(r daemon.Response) {
		mu.Lock()
		parts = append(parts, r)
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	return resp, parts
}

func solidPNG(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func decodePNG(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	return img
}

// fakeADB installs body as adb under a fake ANDROID_HOME (device.List then uses it).
func fakeADB(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	adb := filepath.Join(dir, "platform-tools", "adb")
	os.MkdirAll(filepath.Dir(adb), 0o755)
	if err := os.WriteFile(adb, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_HOME", dir)
	if got := device.ADB(); got != adb {
		t.Fatalf("device.ADB() = %q, want the fake %q", got, adb)
	}
}

// adbLists makes `adb devices -l` list these "serial model" pairs (models use _ for spaces).
func adbLists(t *testing.T, devs ...string) {
	out := "List of devices attached\n"
	for _, d := range devs {
		serial, model, _ := strings.Cut(d, " ")
		out += serial + " device model:" + model + "\n"
	}
	fakeADB(t, "/bin/cat <<'EOF'\n"+out+"EOF")
}
