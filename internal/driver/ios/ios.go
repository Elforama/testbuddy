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
	"sync/atomic"
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
	connP atomic.Pointer[wire.Conn] // the current runner connection
	dir   string                    // the unpacked agent
	probe string                    // libTBProbe.dylib, injected into launched apps

	mu        sync.Mutex // guards port, w, h, seqBase, runnerPID, recording and locationSet
	port      int
	w, h      int
	seqBase   int64 // added to the runner's change counter: a restarted runner counts from 0 again
	runnerPID int   // the runner process the current connection reaches

	launched atomic.Bool // an app was launched since the last screen read (it may be slow: Screen)

	recordCmd  *exec.Cmd
	recordPath string

	locationSet map[string]string // app -> location permission applied (simctl privacy takes ~0.1 s)

	// Reconnection (Maestro #880): the runner connection can drop, or the runner process die,
	// mid-run. A request that finds the connection gone reconnects (restarting the runner if
	// it is gone) and is sent once more; device-changing requests carry a token (rid) the
	// runner answers a repeat of from its first run. Events reach subscribers through the
	// driver, so waits survive a new connection, and Seq never goes backwards.
	connMu  sync.Mutex     // guards reconn
	reconn  *reconnAttempt // the reconnect in progress, if any
	closed  atomic.Bool    // Close was called
	stopped atomic.Bool    // StopRunner was called: a dropped connection is expected
	lastSeq atomic.Int64
	subMu   sync.Mutex
	subs    map[int]chan driver.Event
	nextSub int
}

// reconnAttempt is one reconnect, shared by everyone who notices the same drop.
type reconnAttempt struct {
	done chan struct{}
	err  error
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
	disablePasswordAutoFill(ctx, simDataDir(info.Serial))
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
	PID   int    `json:"pid"`
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
	var c *wire.Conn
	for attempt := 0; ; attempt++ {
		proc, err := d.startRunner(ctx, port)
		if err != nil {
			return err
		}
		c, err = dialRunner(ctx, port, proc, startTimeout)
		if err == nil {
			break
		}
		// xcodebuild gave up before the runner served (Maestro #1585): say why at once
		// instead of dialing on for minutes, after one more try (a just-booted simulator,
		// a busy CoreSimulator).
		if errors.Is(err, errRunnerExited) && attempt == 0 && ctx.Err() == nil {
			continue
		}
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

// waitPortFree waits for nothing to serve port, and reports whether that happened in time.
func waitPortFree(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, _ := probePort(port)
		if c == nil {
			return true
		}
		c.Close()
		time.Sleep(100 * time.Millisecond) // shutdown of a stale runner only
	}
	return false
}

// errRunnerExited: xcodebuild ended before its runner answered.
var errRunnerExited = errors.New("xcodebuild exited before the runner was up")

// dialRunner connects to a runner starting on port. It gives up as soon as proc (its
// xcodebuild) exits, when ctx ends, or after timeout.
func dialRunner(ctx context.Context, port int, proc *runnerProc, timeout time.Duration) (*wire.Conn, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	for {
		c, err := wire.Dial(addr, 0) // one attempt
		if err == nil {
			return c, nil
		}
		var exited <-chan struct{}
		if proc != nil {
			exited = proc.done
		}
		select {
		case <-exited:
			// It may have come up just before xcodebuild ended: one last look.
			if c, err := wire.Dial(addr, 0); err == nil {
				return c, nil
			}
			if proc.err != nil {
				return nil, fmt.Errorf("%w (%v)", errRunnerExited, proc.err)
			}
			return nil, errRunnerExited
		case <-ctx.Done():
			return nil, fmt.Errorf("%v (last attempt: %v)", ctx.Err(), err)
		case <-time.After(100 * time.Millisecond): // runner start-up only
		}
		if time.Now().After(deadline) {
			return nil, err
		}
	}
}

// adopt makes c the driver's connection and forwards its events.
func (d *Driver) adopt(c *wire.Conn, port int, in *info) {
	var h struct {
		W, H int
		PID  int `json:"pid"`
	}
	if c.Hello() != nil {
		c.Hello().Decode(&h)
	}
	pid := in.PID
	if pid == 0 {
		pid = h.PID
	}
	d.mu.Lock()
	if d.connP.Load() != nil && (pid == 0 || pid != d.runnerPID) {
		d.seqBase = d.lastSeq.Load() + 1 // another runner process counts from 0 again
	}
	d.runnerPID = pid
	d.port = port
	d.w, d.h = in.W, in.H
	if d.w == 0 {
		d.w, d.h = h.W, h.H
	}
	d.mu.Unlock()
	d.connP.Store(c)
	if d.closed.Load() {
		c.Close()
		return
	}
	go d.pump(c)
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

// runnerProc is a started xcodebuild: done closes when it exits, err is how.
type runnerProc struct {
	done chan struct{}
	err  error
}

// startRunner stops this simulator's other runners and starts one in its own session, so it
// survives this process (and Ctrl-C) for the next run.
func (d *Driver) startRunner(ctx context.Context, port int) (*runnerProc, error) {
	stopRunners(d.info.Serial)
	log, err := os.Create(d.logPath())
	if err != nil {
		return nil, err
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
	cmd.SysProcAttr = ownSession()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting xcodebuild: %w", err)
	}
	proc := &runnerProc{done: make(chan struct{})}
	go func() { // reap it if it ends while we run; it is in its own session otherwise
		err := cmd.Wait()
		if err == nil {
			err = errors.New("exit status 0")
		}
		proc.err = err
		close(proc.done)
	}()
	return proc, nil
}

// stopRunners ends xcodebuild sessions serving this simulator: two runners would fight over it.
func stopRunners(udid string) {
	out, _ := exec.Command("pgrep", "-f", "xcodebuild test-without-building.*tb-agent.xctestrun.*id="+udid).Output()
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil && pid != os.Getpid() {
			terminate(pid)
		}
	}
}

// StopRunner asks the runner to exit (it otherwise stays up for the next run, ~30 min idle).
func (d *Driver) StopRunner() error {
	d.stopped.Store(true)
	_, err := d.conn().Call("exit", nil, 3*time.Second)
	return err
}

// ---- basics ----

func (d *Driver) Platform() string { return "ios" }
func (d *Driver) Serial() string   { return d.info.Serial }
func (d *Driver) Name() string     { return d.info.Name }
func (d *Driver) Size() (int, int) { d.mu.Lock(); defer d.mu.Unlock(); return d.w, d.h }
func (d *Driver) Seq() int64       { return d.seq(d.conn().Seq()) }
func (d *Driver) Close() error {
	d.closed.Store(true)
	return d.conn().Close()
}

// conn is the current runner connection.
func (d *Driver) conn() *wire.Conn { return d.connP.Load() }

// Conn exposes the raw runner connection (diagnostics).
func (d *Driver) Conn() *wire.Conn { return d.conn() }

// Port is the runner's port on 127.0.0.1.
func (d *Driver) Port() int { d.mu.Lock(); defer d.mu.Unlock(); return d.port }

// seq maps a runner change count to the driver's, which never goes backwards.
func (d *Driver) seq(runner int64) int64 {
	d.mu.Lock()
	v := d.seqBase + runner
	d.mu.Unlock()
	for {
		last := d.lastSeq.Load()
		if v <= last || d.lastSeq.CompareAndSwap(last, v) {
			return v
		}
	}
}

// runnerSeq maps a driver change count back to the current runner's (0 when it is older
// than that runner).
func (d *Driver) runnerSeq(v int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return max(v-d.seqBase, 0)
}

// stampedOps carry a request token (rid): every op that changes the device. If the
// connection drops and the request is sent again, the runner answers it from its first run
// instead of tapping or typing twice. Reads are simply sent again.
var stampedOps = map[string]bool{
	"tap": true, "doubleTap": true, "press": true, "swipe": true, "key": true, "type": true,
	"erase": true, "clear": true, "hideKeyboard": true, "rotate": true, "launch": true,
	"terminate": true, "activate": true,
}

var (
	ridBase = strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.Itoa(os.Getpid())
	ridNext atomic.Int64
)

func (d *Driver) call(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, error) {
	limit := func(timeout time.Duration) time.Duration {
		if dl, ok := ctx.Deadline(); ok {
			if left := time.Until(dl); left < timeout {
				timeout = max(left, 10*time.Millisecond)
			}
		}
		return timeout
	}
	if stampedOps[op] {
		q := make(map[string]any, len(p)+1)
		for k, v := range p {
			q[k] = v
		}
		q["rid"] = fmt.Sprintf("%s-%d", ridBase, ridNext.Add(1))
		p = q
	}
	c := d.conn()
	m, err := c.Call(op, d.runnerParams(p), limit(timeout))
	var agentErr *wire.AgentError
	if err == nil || errors.As(err, &agentErr) {
		return m, err
	}
	select {
	case <-c.Done(): // the connection dropped: reconnect and send it once more
	default:
		return m, err
	}
	if rerr := d.reconnect(ctx, c); rerr != nil {
		return nil, fmt.Errorf("%w (reconnecting to the runner failed: %v)", err, rerr)
	}
	return d.conn().Call(op, d.runnerParams(p), limit(timeout))
}

// runnerParams translates a request's driver change count ("since") to the current runner's.
func (d *Driver) runnerParams(p map[string]any) map[string]any {
	since, ok := p["since"].(int64)
	if !ok {
		return p
	}
	q := make(map[string]any, len(p))
	for k, v := range p {
		q[k] = v
	}
	q["since"] = d.runnerSeq(since)
	return q
}

// reconnect replaces a dropped connection: the runner on its port if it is still up, a
// fresh runner otherwise. One attempt runs per drop, with its own time budget
// (reconnectFor), so a caller whose context ends early does not leave a half-started
// runner for the next caller to stop and start again; it is shared by everyone who notices.
func (d *Driver) reconnect(ctx context.Context, dropped *wire.Conn) error {
	d.connMu.Lock()
	if d.conn() != dropped {
		d.connMu.Unlock()
		return nil // someone else already reconnected
	}
	if d.closed.Load() {
		d.connMu.Unlock()
		return errors.New("the driver is closed")
	}
	a := d.reconn
	if a == nil {
		a = &reconnAttempt{done: make(chan struct{})}
		d.reconn = a
		go func() {
			bctx, cancel := context.WithTimeout(context.Background(), reconnectFor)
			a.err = d.redial(bctx)
			cancel()
			d.connMu.Lock()
			d.reconn = nil
			d.connMu.Unlock()
			close(a.done)
		}()
	}
	d.connMu.Unlock()
	select {
	case <-a.done:
		return a.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// redial connects again until ctx ends: connect reuses a runner of this build still serving
// this simulator and starts one otherwise.
func (d *Driver) redial(ctx context.Context) error {
	for {
		err := d.connect(ctx)
		if err == nil {
			return nil
		}
		if d.closed.Load() {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Second): // between reconnect attempts only
		}
	}
}

// reconnectFor bounds one reconnect, a runner restart included.
var reconnectFor = 60 * time.Second

// Raw runs any op and returns its JSON answer (diagnostics, research ops).
func (d *Driver) Raw(ctx context.Context, op string, p map[string]any) (json.RawMessage, error) {
	m, err := d.call(ctx, op, p, 60*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Raw, nil
}

// pump forwards one connection's pushed events to the driver's subscribers. When the
// connection drops unexpectedly it reconnects, and tells subscribers only if that fails.
func (d *Driver) pump(c *wire.Conn) {
	sub := c.Subscribe()
	for m := range sub {
		var e struct {
			Pkg string `json:"pkg"`
		}
		m.Decode(&e)
		d.fanout(driver.Event{Kind: m.Ev, Seq: d.seq(m.Seq), Pkg: e.Pkg})
	}
	if d.conn() != c || d.closed.Load() || d.stopped.Load() {
		return // replaced by a newer connection, or closed on purpose
	}
	if err := d.reconnect(context.Background(), c); err != nil {
		d.fanout(driver.Event{Kind: "disconnected"})
	}
}

func (d *Driver) fanout(e driver.Event) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	for _, ch := range d.subs {
		select { // never block: the consumer may have stopped reading
		case ch <- e:
		default:
		}
	}
}

// Events streams the runner's pushes. The stream outlives a reconnect; "disconnected" means
// the runner could not be reached again.
func (d *Driver) Events() (<-chan driver.Event, func()) {
	ch := make(chan driver.Event, 64)
	d.subMu.Lock()
	if d.subs == nil {
		d.subs = map[int]chan driver.Event{}
	}
	d.nextSub++
	id := d.nextSub
	d.subs[id] = ch
	d.subMu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			d.subMu.Lock()
			delete(d.subs, id)
			d.subMu.Unlock()
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

// readTimeout is how long the runner may take to read the screen. firstReadTimeout is for the
// first read after a launch: XCTest opens its automation session with the new app on that
// read and waits up to 60 s for the app to answer (34 s on a GitHub Actions runner).
var readTimeout, firstReadTimeout = 30 * time.Second, 70 * time.Second

func (d *Driver) Screen(ctx context.Context) (*ui.Screen, error) {
	timeout := readTimeout
	if d.launched.Swap(false) {
		timeout = firstReadTimeout
	}
	m, err := d.call(ctx, "tree", nil, timeout)
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
	s := &ui.Screen{Platform: "ios", Width: w, Height: h, Seq: d.seq(raw.Seq), Keyboard: raw.Keyboard}
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
	return driver.ActionResult{Settle: driver.SettleResult(r.Result), InjectMs: r.InjectMs, SettleMs: r.SettleMs, Seq: d.seq(r.Seq)}, nil
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
	d.launched.Store(true)
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
		Found       bool `json:"found"`
		BehindAlert bool `json:"behindAlert"`
	}
	m.Decode(&r)
	if !r.Found {
		return fmt.Errorf("%s did not come to the foreground within %s", appID, timeout)
	}
	_ = r.BehindAlert // running behind a system alert: launched (the flow answers the alert)
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

// ClearState resets an app to just installed: it stops the app, waits until it has really
// stopped, and reinstalls it from a copy of its own bundle, then resets its privacy decisions.
// Emptying the data container in place (Maestro's old way) leaves UserDefaults cached in
// cfprefsd and the notification authorization behind (Maestro #1601, #1306), so it is only
// done when TB_IOS_FAST_CLEARSTATE=1 (~0.1 s instead of a couple of seconds).
func (d *Driver) ClearState(ctx context.Context, appID string) error {
	if err := validID(appID); err != nil {
		return err
	}
	if os.Getenv("TB_IOS_FAST_CLEARSTATE") == "1" {
		if err := d.emptyContainers(ctx, appID); err != nil {
			return err
		}
	} else if err := d.reinstall(ctx, appID); err != nil {
		return err
	}
	d.resetTCC(ctx, appID)
	d.simctl(ctx, "privacy", d.info.Serial, "reset", "all", appID)
	d.mu.Lock()
	delete(d.locationSet, appID)
	d.mu.Unlock()
	return nil
}

// stopWait bounds how long ClearState waits for a terminated app to be gone.
var stopWait = 10 * time.Second

// reinstall terminates the app, waits for it to stop, and uninstalls and installs it again
// from a copy of its bundle.
func (d *Driver) reinstall(ctx context.Context, appID string) error {
	bundle, err := d.simctl(ctx, "get_app_container", d.info.Serial, appID, "app")
	if err != nil {
		return fmt.Errorf("app %s is not installed: %w", appID, err)
	}
	bundle = strings.TrimSpace(bundle)
	if !filepath.IsAbs(bundle) || !strings.HasSuffix(bundle, ".app") {
		return fmt.Errorf("app %s: unexpected bundle path %q", appID, bundle)
	}
	d.simctl(ctx, "terminate", d.info.Serial, appID) // not running is fine
	if err := d.waitStopped(ctx, appID, stopWait); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "tb-reinstall-")
	if err != nil {
		return err
	}
	cp := filepath.Join(tmp, filepath.Base(bundle))
	if err := copyTree(bundle, cp); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("copying %s to reinstall it: %w", bundle, err)
	}
	if _, err := d.simctl(ctx, "uninstall", d.info.Serial, appID); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	_, err = d.simctl(ctx, "install", d.info.Serial, cp)
	if err != nil {
		_, err = d.simctl(ctx, "install", d.info.Serial, cp) // once more: CoreSimulator can be busy
	}
	if err != nil {
		// Keep the copy: it is the only one left.
		return fmt.Errorf("reinstalling %s for clearState failed (its bundle is kept at %s): %w", appID, cp, err)
	}
	os.RemoveAll(tmp)
	return nil
}

// waitStopped polls launchd until the app's process is gone (a terminate is asynchronous).
func (d *Driver) waitStopped(ctx context.Context, appID string, timeout time.Duration) error {
	label := "UIKitApplication:" + appID + "["
	deadline := time.Now().Add(timeout)
	for {
		out, err := d.simctl(ctx, "spawn", d.info.Serial, "launchctl", "list")
		if err != nil {
			return nil // cannot tell: go on, as before
		}
		running := false
		for _, line := range strings.Split(out, "\n") {
			// "PID\tStatus\tLabel"; a "-" PID is a registered job that is not running.
			f := strings.Fields(line)
			if len(f) >= 3 && strings.HasPrefix(f[2], label) && f[0] != "-" {
				running = true
				break
			}
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s was still running %s after it was terminated", appID, timeout)
		}
		d.simctl(ctx, "terminate", d.info.Serial, appID)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond): // waiting on another process
		}
	}
}

// copyTree copies a directory tree (files, modes, symlinks as links).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, to)
		case fi.IsDir():
			return os.MkdirAll(to, fi.Mode().Perm()|0o700)
		case fi.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm()|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil
	})
}

// emptyContainers empties the app's data container and its app group containers in place
// (TB_IOS_FAST_CLEARSTATE=1).
func (d *Driver) emptyContainers(ctx context.Context, appID string) error {
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

// simDataDir is a simulator's data directory (its "/").
func simDataDir(udid string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Developer/CoreSimulator/Devices", udid, "data")
}

func (d *Driver) tccDB() string {
	return filepath.Join(simDataDir(d.info.Serial), "Library/TCC/TCC.db")
}

// The simulator's configuration-profile settings, where Password AutoFill is allowed or not.
var autoFillPlists = []string{
	"Library/UserConfigurationProfiles/EffectiveUserSettings.plist",
	"Containers/Shared/SystemGroup/systemgroup.com.apple.configurationprofiles/Library/ConfigurationProfiles/UserSettings.plist",
}

// disablePasswordAutoFill turns off Password AutoFill on the simulator (Maestro #1061): its
// Strong Password sheet takes over secure and new-password fields, and typed text goes
// nowhere. TB_IOS_KEEP_AUTOFILL=1 leaves it as it is. Missing files are skipped quietly.
func disablePasswordAutoFill(ctx context.Context, dataDir string) {
	if os.Getenv("TB_IOS_KEEP_AUTOFILL") == "1" {
		return
	}
	const key = "restrictedBool.allowPasswordAutoFill"
	for _, rel := range autoFillPlists {
		f := filepath.Join(dataDir, rel)
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if out, err := exec.CommandContext(ctx, "plutil", "-extract", key+".value", "raw", f).Output(); err == nil &&
			strings.TrimSpace(string(out)) == "false" {
			continue // already off
		}
		// Create the dictionaries if missing (an insert of an existing key fails harmlessly).
		exec.CommandContext(ctx, "plutil", "-insert", "restrictedBool", "-dictionary", f).Run()
		exec.CommandContext(ctx, "plutil", "-insert", key, "-dictionary", f).Run()
		exec.CommandContext(ctx, "plutil", "-replace", key+".value", "-bool", "NO", f).Run()
	}
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

// SetDarkMode switches the simulator's appearance (`simctl ui <udid> appearance dark|light`).
func (d *Driver) SetDarkMode(ctx context.Context, on bool) error {
	v := "light"
	if on {
		v = "dark"
	}
	_, err := d.simctl(ctx, "ui", d.info.Serial, "appearance", v)
	return err
}

// DarkMode reads the simulator's appearance: "dark" or "light".
func (d *Driver) DarkMode(ctx context.Context) (bool, error) {
	out, err := d.simctl(ctx, "ui", d.info.Serial, "appearance")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "dark", nil
}

// Release stops tb's runner on the simulator and frees it for other tools (another XCTest
// runner, Maestro, Appium). The next tb run starts it again (a few seconds).
func (d *Driver) Release(ctx context.Context) error {
	d.closed.Store(true)
	if c := d.conn(); c != nil {
		c.Call("exit", nil, 2*time.Second)
		c.Close()
	}
	return ReleaseDevice(ctx, d.info.Serial)
}

// ReleaseDevice stops a tb runner left serving a simulator, with no connection to it: one a
// crashed or killed run left, or one kept with --keep-agents.
func ReleaseDevice(ctx context.Context, udid string) error {
	port := preferredPort(udid)
	for p := port; p < port+8; p++ {
		c, in := probePort(p)
		if c == nil {
			continue
		}
		if in != nil && in.UDID == udid {
			c.Call("exit", nil, 2*time.Second)
			c.Close()
			if !waitPortFree(p, 5*time.Second) && in.PID > 0 {
				// It did not stop (hung): end the runner process, which tb started.
				terminate(in.PID)
				if !waitPortFree(p, 3*time.Second) {
					kill(in.PID)
				}
			}
			continue
		}
		c.Close()
	}
	return nil
}

// Keep asks the agent to stay after this connection closes (for a later run, --keep-agents):
// by default an agent left without a connection exits within a minute.
func (d *Driver) Keep(ctx context.Context) error {
	c := d.conn()
	if c == nil {
		return nil
	}
	_, err := c.Call("keep", nil, 2*time.Second)
	return err
}
