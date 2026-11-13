package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// TestMain makes the package hermetic: no real adb, xcrun or Chrome can be found, and
// nothing touches ~/.testbuddy. Every test device gets a fake driver; this is the safety
// net if one ever does not.
func TestMain(m *testing.M) {
	// Device locks off: these tests make many pools and sessions for the same fake devices
	// (locks have their own tests, in devlock and in TestPoolLocks / TestSessionLocks).
	os.Setenv("TB_NO_DEVICE_LOCK", "1")
	dir, err := os.MkdirTemp("", "tb-orchestrate-test")
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
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeDriver is a device whose behavior a test scripts. It records what flows did to it.
type fakeDriver struct {
	name, serial, plat string
	emulator           bool // its device.Info says Emulator

	mu       sync.Mutex
	texts    []string // what is on screen
	typed    []string
	launched []string
	closed   int
	released int
	kept     int
	recStart []string
	recStop  int
	launches int

	launchFails int                // fail the first N launches; -1 = every launch
	onLaunch    func(appID string) // runs before a launch (may block)
	onType      func(text string)  // runs before typing (may block)
	screenErr   error              // Screen fails
	dieOnLaunch bool               // the connection dies for good during the next launch
	dead        error              // why the connection is gone for good (Lost); nil = alive
}

func newFake(serial, name, plat string) *fakeDriver {
	return &fakeDriver{serial: serial, name: name, plat: plat}
}

func (f *fakeDriver) info() device.Info {
	return device.Info{Platform: device.Platform(f.plat), Serial: f.serial, Name: f.name, State: "device", Emulator: f.emulator}
}

func (f *fakeDriver) Typed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed...)
}

func (f *fakeDriver) Launched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.launched...)
}

func (f *fakeDriver) Platform() string { return f.plat }
func (f *fakeDriver) Serial() string   { return f.serial }
func (f *fakeDriver) Name() string     { return f.name }
func (f *fakeDriver) Size() (int, int) { return 400, 800 }
func (f *fakeDriver) Seq() int64       { return 0 }
func (f *fakeDriver) Screen(ctx context.Context) (*ui.Screen, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.screenErr != nil {
		return nil, f.screenErr
	}
	var nodes []*ui.Node
	for i, t := range f.texts {
		nodes = append(nodes, &ui.Node{Text: t, Bounds: ui.Rect{L: 0, T: i * 60, R: 400, B: i*60 + 50}, Enabled: true, Clickable: true, Visible: true})
	}
	w := &ui.Window{Type: "app", Package: "com.app", Active: true, Nodes: nodes, Bounds: ui.Rect{R: 400, B: 800}}
	s := &ui.Screen{Platform: f.plat, Width: 400, Height: 800, Windows: []*ui.Window{w}}
	s.Link()
	return s, nil
}
func (f *fakeDriver) Events() (<-chan driver.Event, func()) {
	return make(chan driver.Event), func() {}
}
func (f *fakeDriver) WaitSettled(ctx context.Context, t time.Duration) error { return nil }
func (f *fakeDriver) Settle(ctx context.Context, since int64, n int, t time.Duration) (driver.SettleResult, error) {
	return driver.NoChange, nil
}
func (f *fakeDriver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
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
func (f *fakeDriver) Screenshot(ctx context.Context, s float64, fm string) ([]byte, error) {
	return nil, errors.New("no screenshots")
}
func (f *fakeDriver) LaunchApp(ctx context.Context, a string, o driver.LaunchOpts) error {
	f.mu.Lock()
	on := f.onLaunch
	f.mu.Unlock()
	if on != nil {
		on(a)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead != nil {
		return f.dead // a dead connection fails at once, as the issue's 0 ms failures did
	}
	f.launches++
	f.launched = append(f.launched, a)
	if f.dieOnLaunch {
		f.dieOnLaunch = false
		f.dead = errors.New("browser connection closed: EOF")
		return errors.New("lost the connection to the device agent")
	}
	if f.launchFails < 0 || f.launches <= f.launchFails {
		return fmt.Errorf("%s cannot launch %s", f.name, a)
	}
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
func (f *fakeDriver) Foreground(ctx context.Context) (string, error)        { return "com.app", nil }
func (f *fakeDriver) WaitForApp(ctx context.Context, a string, t time.Duration) error {
	return nil
}
func (f *fakeDriver) StartRecording(ctx context.Context, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recStart = append(f.recStart, p)
	return nil
}
func (f *fakeDriver) StopRecording(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recStop++
	return nil
}
func (f *fakeDriver) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

// Lost reports the connection gone for good (driver.LostChecker).
func (f *fakeDriver) Lost() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dead
}

func (f *fakeDriver) Closed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// Keep asks the agent to stay for later runs.
func (f *fakeDriver) Keep(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kept++
	return nil
}

// Release stops the agent (and closes the connection): counted in released and closed.
func (f *fakeDriver) Release(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	f.released++
	return nil
}

var _ driver.Driver = (*fakeDriver)(nil)

// poolOf builds a pool over the fakes, with their drivers already open (so the pool never
// opens a real one).
func poolOf(drvs ...*fakeDriver) *Pool {
	var devs []device.Info
	for _, d := range drvs {
		devs = append(devs, d.info())
	}
	p := NewPool(devs)
	for _, d := range drvs {
		p.drivers[d.serial] = d
	}
	return p
}

// parseFlow parses flow text as the file <name>.yaml in a temp folder (its Name is name).
func parseFlow(t *testing.T, name, text string) *flow.Flow {
	t.Helper()
	f, err := flow.ParseBytes(filepath.Join(t.TempDir(), name+".yaml"), []byte(text))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return f
}

// typingFlow launches com.app and types its own name, so a test can tell where it ran.
func typingFlow(t *testing.T, name string) *flow.Flow {
	return parseFlow(t, name, "appId: com.app\n---\n- launchApp\n- inputText: "+name+"\n")
}

func job(f *flow.Flow, needs ...Need) *Job {
	if len(needs) == 0 {
		needs = []Need{{}}
	}
	return &Job{Flow: f, Needs: needs}
}

// recorder captures the hooks' calls (they run on Schedule's goroutine, but the lock keeps
// the race detector honest about reads from the test).
type recorder struct {
	t        *testing.T
	mu       sync.Mutex
	started  []string
	finished []Result
	prepared map[string]int
	env      []flow.KV
	onFinish func(Result)
	onStart  func(*Job, map[string]device.Info)
}

func newRecorder(t *testing.T) *recorder { return &recorder{t: t, prepared: map[string]int{}} }

func (r *recorder) hooks() Hooks {
	dir := r.t.TempDir()
	return Hooks{
		Options: func(j *Job, devs map[string]device.Info) engine.Options {
			return engine.Options{Env: r.env, LookupTimeout: 300 * time.Millisecond, OptionalTimeout: 100 * time.Millisecond, ArtifactsDir: dir}
		},
		Started: func(j *Job, devs map[string]device.Info) {
			r.mu.Lock()
			r.started = append(r.started, j.Flow.Name())
			on := r.onStart
			r.mu.Unlock()
			if on != nil {
				on(j, devs)
			}
		},
		Finished: func(res Result) {
			r.mu.Lock()
			r.finished = append(r.finished, res)
			on := r.onFinish
			r.mu.Unlock()
			if on != nil {
				on(res)
			}
		},
		Prepare: func(ctx context.Context, d device.Info, drv driver.Driver) {
			r.mu.Lock()
			r.prepared[d.Serial]++
			r.mu.Unlock()
		},
	}
}

func (r *recorder) Started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.started...)
}

func (r *recorder) Finished() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Result(nil), r.finished...)
}

// gauge tracks how many things run at once.
type gauge struct {
	mu       sync.Mutex
	cur, max int
	changed  chan struct{}
}

func newGauge() *gauge { return &gauge{changed: make(chan struct{}, 1000)} }

func (g *gauge) enter() {
	g.mu.Lock()
	g.cur++
	if g.cur > g.max {
		g.max = g.cur
	}
	g.mu.Unlock()
	g.changed <- struct{}{}
}

func (g *gauge) exit() {
	g.mu.Lock()
	g.cur--
	g.mu.Unlock()
}

func (g *gauge) Max() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

// waitFor blocks until at least n have run at once, or the timeout.
func (g *gauge) waitFor(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		g.mu.Lock()
		ok := g.max >= n
		g.mu.Unlock()
		if ok {
			return true
		}
		select {
		case <-g.changed:
		case <-deadline:
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// byName indexes results by flow name.
func byName(rs []Result) map[string]Result {
	out := map[string]Result{}
	for _, r := range rs {
		out[r.Job.Flow.Name()] = r
	}
	return out
}

// within fails the test if fn does not return in time (a scheduling deadlock).
func within(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("did not finish within %s", d)
	}
}
