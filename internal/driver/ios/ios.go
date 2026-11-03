// Package ios drives a booted iOS simulator through the Test Buddy XCUITest runner
// (agents/ios), which serves docs/protocol.md on 127.0.0.1:<port>.
//
// The runner takes seconds to start (xcodebuild test-without-building), so it is started in
// its own session and outlives this process: a later run that finds it serving this build on
// this simulator reuses it (~0.1 s). Each simulator gets its own port, so several run in
// parallel. Apps launched through LaunchApp get the TBProbe dylib injected, which pushes every
// UI change to the runner (docs/research/ios-events.md).
package ios

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/ui"
	"github.com/elforama/testbuddy/internal/wire"
)

//go:embed assets/tb-ios-agent.tar.gz
var agentArchive []byte

// BuildID identifies the embedded agent build: a runner reporting another one is replaced.
func BuildID() string {
	sum := sha256.Sum256(agentArchive)
	return hex.EncodeToString(sum[:])[:12]
}

const (
	basePort     = 22100 // + a per-simulator offset
	portSpan     = 800
	startTimeout = 4 * time.Minute // a just-booted simulator can take a minute to start a runner
	idleSeconds  = 1800
)

// Driver is one booted simulator driven through its runner.
type Driver struct {
	info  device.Info
	conn  *wire.Conn
	port  int
	dir   string // the unpacked agent
	probe string // libTBProbe.dylib, injected into launched apps

	mu   sync.Mutex
	w, h int

	recordCmd  *exec.Cmd
	recordPath string

	locationSet map[string]string // app -> location permission applied (simctl privacy takes ~0.1 s)
}

var _ driver.Driver = (*Driver)(nil)

// Open connects to the runner serving this simulator, starting one if needed.
func Open(ctx context.Context, info device.Info) (*Driver, error) {
	if info.Platform != device.IOS {
		return nil, fmt.Errorf("%s is not an iOS simulator", info.Serial)
	}
	dir, err := unpack()
	if err != nil {
		return nil, err
	}
	d := &Driver{info: info, dir: dir, probe: filepath.Join(dir, "Release-iphonesimulator", "libTBProbe.dylib")}
	if os.Getenv("TB_IOS_PROBE") == "0" {
		d.probe = ""
	}
	if err := d.connect(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

// ---- runner lifecycle ----

// unpack extracts the embedded agent into the user's cache once per build (atomically).
func unpack() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "testbuddy", "ios-agent-"+BuildID())
	if _, err := os.Stat(filepath.Join(dir, "tb-agent.xctestrun")); err == nil {
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".ios-agent-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	zr, err := gzip.NewReader(bytes.NewReader(agentArchive))
	if err != nil {
		return "", fmt.Errorf("the embedded iOS agent is unreadable (rebuild it with agents/ios/build.sh): %w", err)
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := filepath.Clean(h.Name)
		if name == "." || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		p := filepath.Join(tmp, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", err
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o755|0o600)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.Symlink(h.Linkname, p); err != nil {
				return "", err
			}
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		if _, statErr := os.Stat(filepath.Join(dir, "tb-agent.xctestrun")); statErr == nil {
			return dir, nil // another process unpacked it first
		}
		return "", err
	}
	return dir, nil
}

// preferredPort is this simulator's port: stable per UDID, so a later run finds its runner.
func preferredPort(udid string) int {
	h := fnv.New32a()
	h.Write([]byte(udid))
	return basePort + int(h.Sum32()%portSpan)
}

type info struct {
	W     int    `json:"w"`
	H     int    `json:"h"`
	UDID  string `json:"udid"`
	Build string `json:"build"`
	Agent string `json:"agent"`
}

// probe asks whoever serves a port what it is (nil when nothing answers quickly).
func probePort(port int) (*wire.Conn, *info) {
	c, err := wire.Dial(fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return nil, nil
	}
	m, err := c.Call("info", map[string]any{"keyboard": false}, 3*time.Second)
	if err != nil {
		c.Close()
		return nil, nil
	}
	var in info
	m.Decode(&in)
	return c, &in
}

func (d *Driver) connect(ctx context.Context) error {
	want := BuildID()
	port := preferredPort(d.info.Serial)
	// A runner of this build serving this simulator: reuse it. Another simulator's runner on
	// the port (a hash collision): move on. A stale build: stop it and start this one.
	for p := port; p < port+8; p++ {
		c, in := probePort(p)
		if c == nil {
			port = p
			break
		}
		if in.UDID == d.info.Serial && in.Build == want {
			d.adopt(c, p, in)
			return nil
		}
		if in.UDID == d.info.Serial || in.UDID == "" {
			c.Call("exit", nil, 2*time.Second)
			c.Close()
			waitPortFree(p, 5*time.Second)
			port = p
			break
		}
		c.Close()
		port = p + 1
	}
	if err := d.startRunner(ctx, port); err != nil {
		return err
	}
	c, err := wire.Dial(fmt.Sprintf("127.0.0.1:%d", port), startTimeout)
	if err != nil {
		return fmt.Errorf("the Test Buddy runner did not start on %s: %v\n%s", d.info.Name, err, d.logTail())
	}
	m, err := c.Call("info", map[string]any{"keyboard": false}, 10*time.Second)
	if err != nil {
		c.Close()
		return err
	}
	var in info
	m.Decode(&in)
	d.adopt(c, port, &in)
	return nil
}

func waitPortFree(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, _ := probePort(port)
		if c == nil {
			return
		}
		c.Close()
		time.Sleep(100 * time.Millisecond) // shutdown of a stale runner only
	}
}

func (d *Driver) adopt(c *wire.Conn, port int, in *info) {
	d.conn, d.port = c, port
	d.w, d.h = in.W, in.H
	if d.w == 0 {
		var h struct{ W, H int }
		c.Hello().Decode(&h)
		d.w, d.h = h.W, h.H
	}
}

func (d *Driver) logPath() string {
	return filepath.Join(d.dir, "runner-"+d.info.Serial+".log")
}

func (d *Driver) logTail() string {
	b, _ := os.ReadFile(d.logPath())
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	return strings.Join(lines, "\n")
}

// startRunner stops this simulator's other runners and starts one in its own session, so it
// survives this process (and Ctrl-C) for the next run.
func (d *Driver) startRunner(ctx context.Context, port int) error {
	stopRunners(d.info.Serial)
	log, err := os.Create(d.logPath())
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command("xcrun", "xcodebuild", "test-without-building",
		"-xctestrun", filepath.Join(d.dir, "tb-agent.xctestrun"),
		"-destination", "id="+d.info.Serial)
	cmd.Env = append(os.Environ(),
		"TEST_RUNNER_TB_PORT="+strconv.Itoa(port),
		"TEST_RUNNER_TB_BUILD="+BuildID(),
		"TEST_RUNNER_TB_IDLE_S="+strconv.Itoa(idleSeconds))
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Dir = d.dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting xcodebuild: %w", err)
	}
	go cmd.Wait() // reap it if it ends while we run; it is in its own session otherwise
	return nil
}

// stopRunners ends xcodebuild sessions serving this simulator: two runners would fight over it.
func stopRunners(udid string) {
	out, _ := exec.Command("pgrep", "-f", "xcodebuild test-without-building.*tb-agent.xctestrun.*id="+udid).Output()
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil && pid != os.Getpid() {
			syscall.Kill(pid, syscall.SIGTERM)
		}
	}
}

// StopRunner asks the runner to exit (it otherwise stays up for the next run, ~30 min idle).
func (d *Driver) StopRunner() error {
	_, err := d.conn.Call("exit", nil, 3*time.Second)
	return err
}

// ---- basics ----

func (d *Driver) Platform() string { return "ios" }
func (d *Driver) Serial() string   { return d.info.Serial }
func (d *Driver) Name() string     { return d.info.Name }
func (d *Driver) Size() (int, int) { d.mu.Lock(); defer d.mu.Unlock(); return d.w, d.h }
func (d *Driver) Seq() int64       { return d.conn.Seq() }
func (d *Driver) Close() error     { return d.conn.Close() }

// Conn exposes the raw runner connection (diagnostics).
func (d *Driver) Conn() *wire.Conn { return d.conn }

// Port is the runner's port on 127.0.0.1.
func (d *Driver) Port() int { return d.port }

func (d *Driver) call(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, error) {
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left < timeout {
			timeout = max(left, 10*time.Millisecond)
		}
	}
	return d.conn.Call(op, p, timeout)
}

// Raw runs any op and returns its JSON answer (diagnostics, research ops).
func (d *Driver) Raw(ctx context.Context, op string, p map[string]any) (json.RawMessage, error) {
	m, err := d.call(ctx, op, p, 60*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Raw, nil
}

func (d *Driver) Events() (<-chan driver.Event, func()) {
	sub := d.conn.Subscribe()
	out := make(chan driver.Event, 64)
	stop := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case m, ok := <-sub:
				if !ok {
					select { // never block: the consumer may have stopped reading
					case out <- driver.Event{Kind: "disconnected"}:
					default:
					}
					return
				}
				var e struct {
					Pkg string `json:"pkg"`
				}
				m.Decode(&e)
				select {
				case out <- driver.Event{Kind: m.Ev, Seq: m.Seq, Pkg: e.Pkg}:
				default:
				}
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return out, func() {
		once.Do(func() {
			d.conn.Unsubscribe(sub)
			close(stop)
		})
	}
}

func (d *Driver) WaitSettled(ctx context.Context, timeout time.Duration) error {
	_, err := d.call(ctx, "quiet", map[string]any{"timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	return err
}

func (d *Driver) Settle(ctx context.Context, since int64, noChangeMs int, timeout time.Duration) (driver.SettleResult, error) {
	m, err := d.call(ctx, "settle", map[string]any{"since": since, "noChange": noChangeMs, "timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Result string `json:"result"`
	}
	m.Decode(&r)
	return driver.SettleResult(r.Result), nil
}

// ---- screen ----

type rawNode struct {
	D  int    `json:"d"`
	B  [4]int `json:"b"`
	C  string `json:"c"`
	T  string `json:"t"`
	CD string `json:"cd"`
	ID string `json:"id"`
	H  string `json:"h"`
	V  string `json:"v"`
	F  int    `json:"f"`
}

type rawWindow struct {
	ID      int       `json:"id"`
	Type    int       `json:"type"`
	Active  bool      `json:"active"`
	Focused bool      `json:"focused"`
	Pkg     string    `json:"pkg"`
	Title   string    `json:"title"`
	B       [4]int    `json:"b"`
	Nodes   []rawNode `json:"nodes"`
}

func (d *Driver) Screen(ctx context.Context) (*ui.Screen, error) {
	m, err := d.call(ctx, "tree", nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Seq      int64       `json:"seq"`
		Keyboard bool        `json:"keyboard"`
		Windows  []rawWindow `json:"windows"`
	}
	if err := m.Decode(&raw); err != nil {
		return nil, err
	}
	w, h := d.Size()
	for _, rw := range raw.Windows {
		if rw.Type == 1 && rw.B[2] > rw.B[0] {
			w, h = rw.B[2]-rw.B[0], rw.B[3]-rw.B[1]
			d.mu.Lock()
			d.w, d.h = w, h
			d.mu.Unlock()
			break
		}
	}
	s := &ui.Screen{Platform: "ios", Width: w, Height: h, Seq: raw.Seq, Keyboard: raw.Keyboard}
	for _, rw := range raw.Windows {
		win := &ui.Window{ID: rw.ID, Type: windowType(rw.Type), Package: rw.Pkg, Title: rw.Title,
			Active: rw.Active, Focused: rw.Focused, Bounds: ui.Rect{L: rw.B[0], T: rw.B[1], R: rw.B[2], B: rw.B[3]}}
		for _, rn := range rw.Nodes {
			win.Nodes = append(win.Nodes, convert(rn))
		}
		s.Windows = append(s.Windows, win)
	}
	s.Link()
	return s, nil
}

func windowType(t int) string {
	switch t {
	case 1:
		return "app"
	case 2:
		return "keyboard"
	case 3:
		return "system"
	case 4:
		return "overlay"
	default:
		return "other"
	}
}

const (
	fClickable = 1 << iota
	fEnabled
	fFocused
	fScrollable
	fChecked
	fSelected
	fCheckable
	fPassword
	fEditable
	fLongClickable
	fFocusable
	fShowingHint
	fVisible
)

func convert(r rawNode) *ui.Node {
	return &ui.Node{
		Depth: r.D, Bounds: ui.Rect{L: r.B[0], T: r.B[1], R: r.B[2], B: r.B[3]},
		Class: r.C, Text: r.T, Desc: r.CD, ID: r.ID, Hint: r.H, Value: r.V,
		Clickable: r.F&fClickable != 0, Enabled: r.F&fEnabled != 0, Focused: r.F&fFocused != 0,
		Scrollable: r.F&fScrollable != 0, Checked: r.F&fChecked != 0, Selected: r.F&fSelected != 0,
		Checkable: r.F&fCheckable != 0, Password: r.F&fPassword != 0, Editable: r.F&fEditable != 0,
		LongClickable: r.F&fLongClickable != 0, ShowingHint: r.F&fShowingHint != 0, Visible: r.F&fVisible != 0,
	}
}

// ---- input ----

func actionParams(o driver.ActionOpts, p map[string]any) map[string]any {
	if o.NoSettle {
		p["settle"] = false
	}
	if o.React {
		p["react"] = true
	}
	if o.NoChangeMs > 0 {
		p["noChange"] = o.NoChangeMs
	} else {
		// As on Android: a tap that opens a screen can take most of a second to start changing
		// it (React Native runs a press on its JavaScript thread while the main run loop is
		// idle). Taken for "nothing happened" sooner, the next command could read the screen
		// the tap was leaving. When it does change, the wait ends at once.
		p["noChange"] = 1000
	}
	if o.Timeout > 0 {
		p["timeout"] = o.Timeout.Milliseconds()
	}
	return p
}

func (d *Driver) action(ctx context.Context, op string, o driver.ActionOpts, p map[string]any, extra time.Duration) (driver.ActionResult, error) {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	m, err := d.call(ctx, op, actionParams(o, p), timeout+extra+15*time.Second)
	if err != nil {
		return driver.ActionResult{}, err
	}
	var r struct {
		Result   string `json:"result"`
		InjectMs int    `json:"injectMs"`
		SettleMs int    `json:"settleMs"`
		Seq      int64  `json:"seq"`
	}
	m.Decode(&r)
	return driver.ActionResult{Settle: driver.SettleResult(r.Result), InjectMs: r.InjectMs, SettleMs: r.SettleMs, Seq: r.Seq}, nil
}

func (d *Driver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	p := map[string]any{"x": x, "y": y}
	if o.Hold > 0 {
		p["hold"] = o.Hold.Milliseconds()
	}
	return d.action(ctx, "tap", o, p, 0)
}

func (d *Driver) DoubleTap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "doubleTap", o, map[string]any{"x": x, "y": y}, 0)
}

func (d *Driver) LongPress(ctx context.Context, x, y int, hold time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "press", o, map[string]any{"x": x, "y": y, "ms": hold.Milliseconds()}, hold)
}

func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2 int, dur time.Duration, o driver.ActionOpts) (driver.ActionResult, error) {
	p := map[string]any{"x1": x1, "y1": y1, "x2": x2, "y2": y2, "ms": dur.Milliseconds()}
	if o.Drag {
		p["drag"] = true
	}
	return d.action(ctx, "swipe", o, p, dur)
}

func (d *Driver) TypeText(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.action(ctx, "type", o, map[string]any{"text": text}, time.Duration(len(text))*40*time.Millisecond)
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "erase", o, map[string]any{"n": n}, time.Duration(n)*20*time.Millisecond)
}

// The flow format's key names → the Android key codes the protocol carries; the runner maps them to iOS.
var keyCodes = map[string]int{
	"enter": 66, "backspace": 67, "delete": 67, "home": 3, "lock": 26, "power": 26,
	"volume up": 24, "volume down": 25, "tab": 61, "escape": 111, "back": 4,
}

// KeyCode maps a flow key name to the code the runner takes.
func KeyCode(key string) (int, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.ReplaceAll(k, "_", " ")
	c, ok := keyCodes[k]
	return c, ok
}

func (d *Driver) PressKey(ctx context.Context, key string, o driver.ActionOpts) (driver.ActionResult, error) {
	code, ok := KeyCode(key)
	if !ok {
		return driver.ActionResult{}, fmt.Errorf("key %q is not supported on iOS", key)
	}
	return d.action(ctx, "key", o, map[string]any{"code": code}, 0)
}

func (d *Driver) HideKeyboard(ctx context.Context) error {
	_, err := d.action(ctx, "hideKeyboard", driver.ActionOpts{}, map[string]any{}, 5*time.Second)
	return err
}

func (d *Driver) Screenshot(ctx context.Context, scale float64, format string) ([]byte, error) {
	if format == "" {
		format = "png"
	}
	m, err := d.call(ctx, "screenshot", map[string]any{"scale": scale, "format": format}, 20*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Data, nil
}

// ---- simctl ----

func (d *Driver) simctl(ctx context.Context, args ...string) (string, error) {
	return d.simctlEnv(ctx, nil, args...)
}

func (d *Driver) simctlEnv(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	if env != nil {
		cmd.Env = env
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, fmt.Errorf("simctl %s: %s", strings.Join(args, " "), s)
	}
	return s, nil
}

func validID(id string) error {
	if id == "" {
		return errors.New("no app id given")
	}
	for _, r := range id {
		if !(r == '.' || r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Errorf("invalid bundle id %q", id)
		}
	}
	return nil
}

// ---- apps ----

// launchEnv is simctl launch's environment: the probe injected and UIKit's animation speed
// preference enabled (SIMCTL_CHILD_X reaches the app as X), appended to any values the
// caller's environment already sets.
func (d *Driver) launchEnv() []string {
	add := map[string]string{}
	if d.probe != "" {
		add["SIMCTL_CHILD_DYLD_INSERT_LIBRARIES"] = d.probe
	}
	if animationSpeedup() {
		add["SIMCTL_CHILD_FEATUREFLAGS_ENABLED"] = "UIKit/UIPreferences"
	}
	env := os.Environ()
	for i, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if a, ok := add[k]; ok {
			if v == "" {
				env[i] = k + "=" + a
			} else if !strings.Contains(v, a) {
				env[i] = k + "=" + v + ":" + a
			}
			delete(add, k)
		}
	}
	keys := make([]string, 0, len(add))
	for k := range add {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+add[k])
	}
	return env
}

// animationSpeedup: UIKit animations 10x faster (-UIAnimationDragCoefficient 0.1), as the
// simulator's "Slow Animations" setting does the opposite. TB_IOS_FAST_ANIMATIONS=0 turns it off.
func animationSpeedup() bool { return os.Getenv("TB_IOS_FAST_ANIMATIONS") != "0" }

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if err := validID(appID); err != nil {
		return err
	}
	if o.ClearState {
		if err := d.ClearState(ctx, appID); err != nil { // terminates it too
			return err
		}
	} else if o.StopApp {
		d.simctl(ctx, "terminate", d.info.Serial, appID) // not running is fine
	}
	if o.ClearKeychain {
		if err := d.ClearKeychain(ctx); err != nil {
			return err
		}
	}
	if err := d.SetPermissions(ctx, appID, o.Permissions); err != nil {
		return err
	}
	args := []string{"launch", d.info.Serial, appID}
	user := map[string]bool{}
	keys := make([]string, 0, len(o.Arguments))
	for k := range o.Arguments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Launch arguments go in as NSUserDefaults' argument domain: -key value.
		name := strings.TrimLeft(k, "-")
		user[name] = true
		args = append(args, "-"+name, argString(o.Arguments[k]))
	}
	if animationSpeedup() && !user["UIAnimationDragCoefficient"] {
		args = append(args, "-UIAnimationDragCoefficient", "0.1")
	}
	if _, err := d.simctlEnv(ctx, d.launchEnv(), args...); err != nil {
		return err
	}
	return d.WaitForApp(ctx, appID, 30*time.Second)
}

func argString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

func (d *Driver) WaitForApp(ctx context.Context, appID string, timeout time.Duration) error {
	m, err := d.call(ctx, "awaitPackage", map[string]any{"pkg": appID, "timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	if err != nil {
		return err
	}
	var r struct {
		Found bool `json:"found"`
	}
	m.Decode(&r)
	if !r.Found {
		return fmt.Errorf("%s did not come to the foreground within %s", appID, timeout)
	}
	return nil
}

func (d *Driver) StopApp(ctx context.Context, appID string) error {
	if err := validID(appID); err != nil {
		return err
	}
	if _, err := d.simctl(ctx, "terminate", d.info.Serial, appID); err != nil && !strings.Contains(err.Error(), "found nothing to terminate") {
		return err
	}
	return nil
}

// KillApp: iOS has no "killed in the background" short of terminating the process.
func (d *Driver) KillApp(ctx context.Context, appID string) error { return d.StopApp(ctx, appID) }

// ClearState empties the app's data container and its app group containers (instead of a
// reinstall: ~0.1 s) and resets its privacy decisions. The app should not be running.
func (d *Driver) ClearState(ctx context.Context, appID string) error {
	if err := validID(appID); err != nil {
		return err
	}
	// The app's folders are looked up while it terminates (each simctl call is ~90 ms).
	var wg sync.WaitGroup
	var groups string
	var groupsErr error
	wg.Add(2)
	go func() { defer wg.Done(); d.simctl(ctx, "terminate", d.info.Serial, appID) }()
	go func() {
		defer wg.Done()
		groups, groupsErr = d.simctl(ctx, "get_app_container", d.info.Serial, appID, "groups")
	}()
	data, err := d.simctl(ctx, "get_app_container", d.info.Serial, appID, "data")
	wg.Wait()
	if err != nil {
		return fmt.Errorf("app %s is not installed: %w", appID, err)
	}
	if err := emptyDir(data); err != nil {
		return err
	}
	for _, sub := range []string{"Documents", "Library", "Library/Caches", "Library/Preferences", "tmp"} {
		os.MkdirAll(filepath.Join(data, sub), 0o755)
	}
	if groupsErr == nil {
		for _, line := range strings.Split(groups, "\n") {
			if _, path, ok := strings.Cut(line, "\t"); ok && strings.Contains(path, "/Shared/AppGroup/") {
				emptyDir(strings.TrimSpace(path))
			}
		}
	}
	d.resetTCC(ctx, appID)
	d.simctl(ctx, "privacy", d.info.Serial, "reset", "all", appID)
	d.mu.Lock()
	delete(d.locationSet, appID)
	d.mu.Unlock()
	return nil
}

func emptyDir(dir string) error {
	if dir == "" || !filepath.IsAbs(dir) || !strings.Contains(dir, "/CoreSimulator/Devices/") {
		return fmt.Errorf("refusing to empty %q", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) ClearKeychain(ctx context.Context) error {
	_, err := d.simctl(ctx, "keychain", d.info.Serial, "reset")
	return err
}

func (d *Driver) OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error {
	_, err := d.simctl(ctx, "openurl", d.info.Serial, link)
	return err
}

// The flow format's permission names → TCC services (written to the simulator's TCC database, as
// applesimutils does); location goes through simctl privacy. Notifications have no TCC entry:
// their alert shows, and the flow answers it.
var tccServices = map[string]string{
	"calendar": "kTCCServiceCalendar", "camera": "kTCCServiceCamera", "contacts": "kTCCServiceAddressBook",
	"faceid": "kTCCServiceFaceID", "homekit": "kTCCServiceWillow", "medialibrary": "kTCCServiceMediaLibrary",
	"microphone": "kTCCServiceMicrophone", "motion": "kTCCServiceMotion", "photos": "kTCCServicePhotos",
	"reminders": "kTCCServiceReminders", "siri": "kTCCServiceSiri", "speech": "kTCCServiceSpeechRecognition",
	"usertracking": "kTCCServiceUserTracking", "bluetooth": "kTCCServiceBluetoothAlways",
}

// SetPermissions applies a flow's permissions. An empty map grants all (the format's default).
func (d *Driver) SetPermissions(ctx context.Context, appID string, perms map[string]string) error {
	if err := validID(appID); err != nil {
		return err
	}
	if len(perms) == 0 {
		perms = map[string]string{"all": "allow"}
	}
	tcc := map[string]string{}
	location := ""
	if v, ok := perms["all"]; ok {
		v = strings.ToLower(v)
		for _, s := range tccServices {
			tcc[s] = v
		}
		location = map[string]string{"allow": "always", "deny": "never", "unset": "unset"}[v]
	}
	for name, v := range perms {
		name, v = strings.ToLower(name), strings.ToLower(v)
		if name == "all" {
			continue
		}
		if name == "location" {
			location = v
			if v == "allow" {
				location = "always"
			} else if v == "deny" {
				location = "never"
			}
			continue
		}
		if s, ok := tccServices[name]; ok {
			tcc[s] = v
		}
	}
	if len(tcc) > 0 {
		if err := d.writeTCC(ctx, appID, tcc); err != nil {
			return err
		}
	}
	if location == "" {
		return nil
	}
	d.mu.Lock()
	same := d.locationSet[appID] == location
	d.mu.Unlock()
	if same {
		return nil
	}
	var args []string
	switch location {
	case "always":
		args = []string{"grant", "location-always"}
	case "inuse", "whileinuse":
		args = []string{"grant", "location"}
	case "never":
		args = []string{"revoke", "location-always"}
	case "unset":
		args = []string{"reset", "location-always"}
	default:
		return fmt.Errorf("unknown location permission %q", location)
	}
	if _, err := d.simctl(ctx, append(append([]string{"privacy", d.info.Serial}, args...), appID)...); err != nil {
		return err
	}
	// Remember it only once applied, so a failed grant is tried again next time.
	d.mu.Lock()
	if d.locationSet == nil {
		d.locationSet = map[string]string{}
	}
	d.locationSet[appID] = location
	d.mu.Unlock()
	return nil
}

func (d *Driver) tccDB() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Developer/CoreSimulator/Devices", d.info.Serial, "data/Library/TCC/TCC.db")
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (d *Driver) writeTCC(ctx context.Context, appID string, tcc map[string]string) error {
	var sql strings.Builder
	sql.WriteString("BEGIN;")
	services := make([]string, 0, len(tcc))
	for s := range tcc {
		services = append(services, s)
	}
	sort.Strings(services)
	for _, service := range services {
		auth := -1
		switch tcc[service] {
		case "allow":
			auth = 2
		case "deny":
			auth = 0
		case "limited":
			auth = 3
		case "unset":
		default:
			continue
		}
		fmt.Fprintf(&sql, "DELETE FROM access WHERE service=%s AND client=%s AND client_type=0;", sqlQuote(service), sqlQuote(appID))
		if auth >= 0 {
			fmt.Fprintf(&sql, "INSERT INTO access (service, client, client_type, auth_value, auth_reason, auth_version, flags) VALUES (%s, %s, 0, %d, 2, 1, 0);", sqlQuote(service), sqlQuote(appID), auth)
		}
	}
	sql.WriteString("COMMIT;")
	// tccd can hold the database for a while after boot: wait for its lock.
	out, err := exec.CommandContext(ctx, "sqlite3", "-cmd", ".timeout 5000", d.tccDB(), sql.String()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("setting permissions in %s: %v: %s", d.tccDB(), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *Driver) resetTCC(ctx context.Context, appID string) {
	exec.CommandContext(ctx, "sqlite3", "-cmd", ".timeout 5000", d.tccDB(),
		"DELETE FROM access WHERE client="+sqlQuote(appID)+" AND client_type=0;").Run()
}

func (d *Driver) SetLocation(ctx context.Context, lat, lon float64) error {
	_, err := d.simctl(ctx, "location", d.info.Serial, "set",
		strconv.FormatFloat(lat, 'f', -1, 64)+","+strconv.FormatFloat(lon, 'f', -1, 64))
	return err
}

func (d *Driver) SetOrientation(ctx context.Context, orientation string) error {
	to := map[string]string{"PORTRAIT": "portrait", "LANDSCAPE_LEFT": "landscapeLeft", "LANDSCAPE_RIGHT": "landscapeRight", "UPSIDE_DOWN": "upsideDown"}[strings.ToUpper(orientation)]
	if to == "" {
		to = orientation
	}
	_, err := d.call(ctx, "rotate", map[string]any{"to": to}, 10*time.Second)
	return err
}

var errAirplane = errors.New("airplane mode is not supported on iOS simulators (they share the Mac's network)")

func (d *Driver) SetAirplaneMode(ctx context.Context, on bool) error { return errAirplane }
func (d *Driver) ToggleAirplaneMode(ctx context.Context) error       { return errAirplane }

func (d *Driver) AddMedia(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		abs[i] = p
		if a, err := filepath.Abs(p); err == nil {
			abs[i] = a
		}
	}
	_, err := d.simctl(ctx, append([]string{"addmedia", d.info.Serial}, abs...)...)
	return err
}

func (d *Driver) InstallApp(ctx context.Context, path string) error {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	_, err := d.simctl(ctx, "install", d.info.Serial, path)
	return err
}

func (d *Driver) Foreground(ctx context.Context) (string, error) {
	m, err := d.call(ctx, "foreground", nil, 10*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Pkg string `json:"pkg"`
	}
	m.Decode(&r)
	return r.Pkg, nil
}

// ---- recording ----

func (d *Driver) StartRecording(ctx context.Context, path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.recordCmd != nil {
		return errors.New("already recording")
	}
	cmd := exec.Command("xcrun", "simctl", "io", d.info.Serial, "recordVideo", "--codec=h264", "--force", path)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return err
	}
	d.recordCmd, d.recordPath = cmd, path
	return nil
}

func (d *Driver) StopRecording(ctx context.Context) error {
	d.mu.Lock()
	cmd := d.recordCmd
	d.recordCmd = nil
	d.mu.Unlock()
	if cmd == nil {
		return nil
	}
	cmd.Process.Signal(os.Interrupt) // simctl finishes the file on SIGINT
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		return errors.New("simctl recordVideo did not stop")
	}
	if _, err := os.Stat(d.recordPath); err != nil {
		return fmt.Errorf("no recording at %s: %w", d.recordPath, err)
	}
	return nil
}

// ---- iOS-only helpers ----

// LaunchWithXCTest launches (or relaunches) an app through XCTest instead of simctl: no
// probe, XCTest's own launch wait.
func (d *Driver) LaunchWithXCTest(ctx context.Context, bundleID string, args []string, env map[string]string) error {
	_, err := d.call(ctx, "launch", map[string]any{"bundleId": bundleID, "args": args, "env": env}, 60*time.Second)
	return err
}

// AppState is XCUIApplication.State (1 not running, 2 suspended, 3 background, 4 foreground).
func (d *Driver) AppState(ctx context.Context, bundleID string) (int, error) {
	m, err := d.call(ctx, "appState", map[string]any{"bundleId": bundleID}, 10*time.Second)
	if err != nil {
		return 0, err
	}
	var r struct {
		State int `json:"state"`
	}
	m.Decode(&r)
	return r.State, nil
}
