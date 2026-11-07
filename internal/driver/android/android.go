// Package android drives an Android device or emulator through the Test Buddy agent.
package android

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/legacy"
	"github.com/elforama/testbuddy/internal/ui"
	"github.com/elforama/testbuddy/internal/wire"
)

//go:embed assets/tb-agent.apk
var agentAPK []byte

const socketName = "localabstract:testbuddy-agent"

func agentHash() string {
	sum := sha256.Sum256(agentAPK)
	return hex.EncodeToString(sum[:])[:12]
}

// Driver is an Android device driven through its agent.
type Driver struct {
	adb        *device.ADBDevice
	info       device.Info
	connP      atomic.Pointer[wire.Conn] // the current agent connection
	stateMu    sync.Mutex                // guards port, w, h, sdk and seqBase: a reconnect rewrites them
	port       int
	editorMu   sync.Mutex
	editorPort int
	w, h       int
	sdk        int
	mu         sync.Mutex
	launch     map[string]string // package -> launcher component

	recordPath string
	recordCmd  *exec.Cmd

	ridBase string       // this driver's request-token prefix (call)
	ridNext atomic.Int64 // its counter

	locMu        sync.Mutex // guards locProviders and locStop
	locProviders []string   // test location providers added by setLocation
	locStop      chan struct{}

	// Reconnection: the adb link can drop (an adb server restart, a USB hiccup). The driver
	// reconnects and keeps its change counter increasing across agent restarts (seqBase), and
	// events reach subscribers through the driver, so waits survive a reconnect.
	connMu  sync.Mutex // serializes reconnects; never held while sending a request
	closed  atomic.Bool
	seqBase int64
	lastSeq atomic.Int64
	subMu   sync.Mutex
	subs    map[int]chan driver.Event
	nextSub int
}

var _ driver.Driver = (*Driver)(nil)

// Open connects to the agent on the device, starting it if it is not already running.
func Open(ctx context.Context, info device.Info) (*Driver, error) {
	d := &Driver{adb: device.NewADB(info.Serial), info: info, launch: map[string]string{}, subs: map[int]chan driver.Event{},
		ridBase: strconv.FormatInt(time.Now().UnixNano(), 36)}
	if err := d.connect(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Driver) connect(ctx context.Context) error { return d.connectOnce(ctx, false) }

func (d *Driver) connectRetry(ctx context.Context) error { return d.connectOnce(ctx, true) }

// stopLeftoverDriver stops Maestro's on-device driver (legacy.AndroidDriverPackages) when
// Maestro is not running on this machine: an emulator snapshot often restores one, and it
// holds the device's only UiAutomation slot.
func (d *Driver) stopLeftoverDriver(ctx context.Context) bool {
	if out, _ := exec.Command("pgrep", "-f", legacy.HostProcessPattern()).Output(); len(strings.TrimSpace(string(out))) > 0 {
		return false
	}
	pkgs := legacy.AndroidDriverPackages() // the test package, then the app
	app := pkgs[len(pkgs)-1]
	out, _ := d.adb.Shell(ctx, "pidof "+app)
	if strings.TrimSpace(out) == "" {
		return false
	}
	var cmd strings.Builder
	for _, p := range pkgs {
		cmd.WriteString("am force-stop " + p + "; ")
	}
	cmd.WriteString("for p in $(pidof app_process); do grep -q " + app + " /proc/$p/cmdline && kill $p; done; true")
	d.adb.Shell(ctx, cmd.String())
	return true
}

func (d *Driver) connectOnce(ctx context.Context, retried bool) error {
	// Fast path: an agent from an earlier run is still serving behind an existing forward,
	// or behind a new one (an adb server restart drops forwards, not the agent).
	port := d.adb.ExistingForward(ctx, socketName)
	if port == 0 {
		port, _ = d.adb.Forward(ctx, socketName)
	}
	if port != 0 {
		if c, hello, err := dialHello(port, 600*time.Millisecond); err == nil {
			if hello.Version == agentVersion() {
				d.adopt(c, port, hello)
				return nil
			}
			c.Close()
		}
	}
	if err := d.startAgent(ctx); err != nil {
		return err
	}
	port = d.adb.ExistingForward(ctx, socketName)
	if port == 0 {
		var err error
		if port, err = d.adb.Forward(ctx, socketName); err != nil {
			return err
		}
	}
	c, hello, err := dialHello(port, 15*time.Second)
	if err != nil {
		logTail, _ := d.adb.Shell(ctx, "tail -n 20 /data/local/tmp/tb-agent.log")
		if strings.Contains(logTail, "already registered") && !retried && d.stopLeftoverDriver(ctx) {
			return d.connectRetry(ctx)
		}
		return fmt.Errorf("the Test Buddy agent did not start on %s: %v\n%s", d.info.Serial, err, strings.TrimSpace(logTail))
	}
	d.adopt(c, port, hello)
	return nil
}

type hello struct {
	Version string  `json:"version"`
	SDK     int     `json:"sdk"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	Density float64 `json:"density"`
}

func agentVersion() string { return "0.1.27" }

func dialHello(port int, timeout time.Duration) (*wire.Conn, hello, error) {
	c, err := wire.Dial(fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return nil, hello{}, err
	}
	m, err := c.Call("info", nil, 3*time.Second)
	if err != nil {
		c.Close()
		return nil, hello{}, err
	}
	var h hello
	m.Decode(&h)
	v, err := c.Call("ping", nil, 2*time.Second)
	if err != nil {
		c.Close()
		return nil, hello{}, err
	}
	_ = v
	h.Version = c.HelloVersion()
	return c, h, nil
}

func (d *Driver) adopt(c *wire.Conn, port int, h hello) {
	d.stateMu.Lock()
	if d.connP.Load() != nil && c.Seq() < d.lastSeq.Load() {
		d.seqBase = d.lastSeq.Load() + 1 // a restarted agent counts from 0 again
	}
	d.port, d.w, d.h, d.sdk = port, h.W, h.H, h.SDK
	d.stateMu.Unlock()
	d.connP.Store(c)
	go d.pump(c)
	if os.Getenv("TB_NO_A11Y_SERVICE") == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.Sh(ctx, `cur=$(settings get secure enabled_accessibility_services); case "$cur" in *`+noopService+`*) ;; null|"") settings put secure enabled_accessibility_services `+noopService+` ;; *) settings put secure enabled_accessibility_services "$cur:`+noopService+`" ;; esac`)
	}
}

// startAgent pushes the agent (only when this build is not on the device) and starts it.
func (d *Driver) startAgent(ctx context.Context) error {
	remote := "/data/local/tmp/tb-agent-" + agentHash() + ".apk"
	out, _ := d.adb.Shell(ctx, "ls "+remote+" 2>/dev/null; pids=$(pidof tb-agent) && kill $pids; for f in /data/local/tmp/tb-agent-*.apk; do [ \"$f\" != \""+remote+"\" ] && rm -f \"$f\"; done; true")
	if !strings.Contains(out, remote) {
		tmp, err := os.CreateTemp("", "tb-agent-*.apk")
		if err != nil {
			return err
		}
		tmp.Write(agentAPK)
		tmp.Close()
		defer os.Remove(tmp.Name())
		if _, err := d.adb.Run(ctx, "push", tmp.Name(), remote); err != nil {
			return err
		}
	}
	// The same APK, installed, provides NoopService: an enabled accessibility service makes
	// Jetpack Compose send accessibility events (it sends none otherwise), so Compose screens
	// are event-driven too. Install only when the installed copy differs; enable it if needed.
	setup := ""
	if os.Getenv("TB_NO_A11Y_SERVICE") == "" {
		setup = serviceSetup(remote)
	}
	// nohup + background: the agent outlives this adb shell and later runs reconnect to it.
	cmd := setup + fmt.Sprintf("CLASSPATH=%s nohup app_process / --nice-name=tb-agent dev.testbuddy.agent.Main > /data/local/tmp/tb-agent.log 2>&1 &", remote)
	if _, err := d.adb.Shell(ctx, cmd); err != nil {
		return err
	}
	return nil
}

func serviceSetup(remote string) string {
	return fmt.Sprintf(`inst=$(pm path %[1]s 2>/dev/null | sed 's/^package://'); `+
		`if [ -z "$inst" ] || ! cmp -s "$inst" %[2]s; then `+
		`res=$(pm install -r -t %[2]s 2>&1); `+
		`case "$res" in *INSTALL_FAILED_UPDATE_INCOMPATIBLE*) `+
		`pm uninstall %[1]s >/dev/null 2>&1 || { echo "Could not replace Test Buddy helper: $res" >&2; exit 1; }; `+
		`res=$(pm install -t %[2]s 2>&1);; esac; `+
		`case "$res" in *"Failure ["*|"") echo "Could not install Test Buddy helper: $res" >&2; exit 1;; `+
		`*Success*) ;; *) echo "Could not install Test Buddy helper: $res" >&2; exit 1;; esac; fi; `+
		`cur=$(settings get secure enabled_accessibility_services); `+
		`case "$cur" in *%[3]s*) ;; null|"") settings put secure enabled_accessibility_services %[3]s ;; *) settings put secure enabled_accessibility_services "$cur:%[3]s" ;; esac; `+
		`settings put secure accessibility_enabled 1; `, agentPackage, remote, noopService)
}

const (
	agentPackage = "dev.testbuddy.agent"
	noopService  = "dev.testbuddy.agent/.NoopService"
)

func (d *Driver) Platform() string { return "android" }
func (d *Driver) Serial() string   { return d.info.Serial }
func (d *Driver) Name() string     { return d.info.Name }
func (d *Driver) Size() (int, int) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.w, d.h
}

func (d *Driver) Seq() int64 { return d.seq(d.conn().Seq()) }

// conn is the current agent connection.
func (d *Driver) conn() *wire.Conn { return d.connP.Load() }

// seq maps an agent change count to the driver's, which never goes backwards.
func (d *Driver) seq(agent int64) int64 {
	d.stateMu.Lock()
	v := d.seqBase + agent
	d.stateMu.Unlock()
	for {
		last := d.lastSeq.Load()
		if v <= last || d.lastSeq.CompareAndSwap(last, v) {
			return v
		}
	}
}
func (d *Driver) Close() error {
	d.closed.Store(true)
	d.stopLocation()
	d.closeEditorForward()
	return d.conn().Close()
}

// Conn exposes the raw agent connection (for diagnostics).
func (d *Driver) Conn() *wire.Conn { return d.conn() }

func (d *Driver) call(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, error) {
	if debug && op != "tap" && op != "swipe" && op != "key" && op != "type" {
		t0 := time.Now()
		defer func() { fmt.Fprintf(os.Stderr, "[tb] %s %dms\n", op, time.Since(t0).Milliseconds()) }()
	}
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left < timeout {
			timeout = max(left, 10*time.Millisecond)
		}
	}
	if stampedOps[op] {
		// A request token: if the connection drops and the request is sent again, the agent
		// answers it from its first run instead of tapping or typing twice.
		q := make(map[string]any, len(p)+1)
		for k, v := range p {
			q[k] = v
		}
		q["rid"] = fmt.Sprintf("%s-%d", d.ridBase, d.ridNext.Add(1))
		p = q
	}
	c := d.conn()
	m, err := c.Call(op, p, timeout)
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
		return nil, fmt.Errorf("%w (reconnecting failed: %v)", err, rerr)
	}
	return d.conn().Call(op, p, timeout)
}

// stampedOps carry a request token (call): every op that changes the device. Reads are sent
// again as they are.
var stampedOps = map[string]bool{
	"tap": true, "doubleTap": true, "press": true, "swipe": true, "key": true, "type": true,
	"erase": true, "clear": true, "sh": true, "rotate": true,
}

// pump forwards one connection's pushed events to the driver's subscribers. When the
// connection drops it reconnects (with back-off, briefly) and tells subscribers only if that
// fails.
func (d *Driver) pump(c *wire.Conn) {
	sub := c.Subscribe()
	for m := range sub {
		var e struct {
			Pkg string `json:"pkg"`
		}
		m.Decode(&e)
		d.fanout(driver.Event{Kind: m.Ev, Seq: d.seq(m.Seq), Pkg: e.Pkg})
	}
	if d.conn() != c || d.closed.Load() {
		return // replaced by a newer connection, or closed on purpose
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.reconnect(ctx, c); err != nil {
		d.fanout(driver.Event{Kind: "disconnected"})
	}
}

func (d *Driver) fanout(e driver.Event) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	for _, ch := range d.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// reconnect replaces a dropped connection (once per drop, whoever notices first).
func (d *Driver) reconnect(ctx context.Context, dropped *wire.Conn) error {
	d.connMu.Lock()
	defer d.connMu.Unlock()
	if d.conn() != dropped {
		return nil // someone else already reconnected
	}
	// The device may be gone for a while (an emulator going offline under load, an adb
	// server restart, a USB hiccup): keep trying for up to reconnectFor, waiting for adb to
	// see the device between attempts. Never restart the adb server: other devices use it.
	deadline := time.Now().Add(reconnectFor)
	var err error
	for attempt := 0; ; attempt++ {
		if err = d.connect(ctx); err == nil {
			return nil
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return err
		}
		wait := min(time.Duration(attempt+1)*500*time.Millisecond, 3*time.Second)
		wctx, cancel := context.WithTimeout(ctx, min(wait, max(time.Until(deadline), 0)))
		d.adb.Run(wctx, "wait-for-device") // returns at once when the device is there
		cancel()
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

// reconnectFor: how long a dropped agent connection is retried before the step fails.
var reconnectFor = 45 * time.Second

func (d *Driver) Events() (<-chan driver.Event, func()) {
	ch := make(chan driver.Event, 64)
	d.subMu.Lock()
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
	E  string `json:"e"`
	SD string `json:"sd"`
	F  int    `json:"f"`
	CI []int  `json:"ci"` // collection: rows, columns (-1 unknown)
	II []int  `json:"ii"` // collection item: row, column
	O  *int   `json:"o"`  // drawing order among its siblings (absent from older agents)
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

func (d *Driver) Screen(ctx context.Context) (*ui.Screen, error) { return d.screen(ctx, nil) }

// ScreenFresh reads the screen without the agent's node cache: a view moved by translation
// (sliding, springing in) sends no event, so a cached read shows it where it was. For reads
// that check whether something still moves.
func (d *Driver) ScreenFresh(ctx context.Context) (*ui.Screen, error) {
	return d.screen(ctx, map[string]any{"fresh": true})
}

func (d *Driver) AwaitCoverGone(ctx context.Context, window int, pkg, id, class string, x, y int, timeout time.Duration) (bool, bool, error) {
	m, err := d.call(ctx, "awaitCoverGone", map[string]any{"window": window, "pkg": pkg, "nodeId": id, "class": class,
		"x": x, "y": y, "timeout": timeout.Milliseconds()}, timeout+time.Second)
	if err != nil {
		return false, false, err
	}
	var result struct{ Observed, Gone bool }
	if err := m.Decode(&result); err != nil {
		return false, false, err
	}
	return result.Observed, result.Gone, nil
}

func (d *Driver) screen(ctx context.Context, p map[string]any) (*ui.Screen, error) {
	m, err := d.call(ctx, "tree", p, 20*time.Second)
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
	s := &ui.Screen{Platform: "android", Width: w, Height: h, Seq: d.seq(raw.Seq), Keyboard: raw.Keyboard}
	for _, rw := range raw.Windows {
		w := &ui.Window{ID: rw.ID, Type: windowType(rw.Type), Package: rw.Pkg, Title: rw.Title,
			Active: rw.Active, Focused: rw.Focused, Bounds: ui.Rect{L: rw.B[0], T: rw.B[1], R: rw.B[2], B: rw.B[3]}}
		for _, rn := range rw.Nodes {
			w.Nodes = append(w.Nodes, convert(rn))
		}
		s.Windows = append(s.Windows, w)
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
	fMoreAfter
	fMoreBefore
)

func convert(r rawNode) *ui.Node {
	n := &ui.Node{
		Depth: r.D, Bounds: ui.Rect{L: r.B[0], T: r.B[1], R: r.B[2], B: r.B[3]},
		Class: r.C, Text: r.T, Desc: r.CD, ID: r.ID, Hint: r.H, Error: r.E, State: r.SD,
		Clickable: r.F&fClickable != 0, Enabled: r.F&fEnabled != 0, Focused: r.F&fFocused != 0,
		Scrollable: r.F&fScrollable != 0, Checked: r.F&fChecked != 0, Selected: r.F&fSelected != 0,
		Checkable: r.F&fCheckable != 0, Password: r.F&fPassword != 0, Editable: r.F&fEditable != 0,
		LongClickable: r.F&fLongClickable != 0, ShowingHint: r.F&fShowingHint != 0, Visible: r.F&fVisible != 0,
		MoreAfter: r.F&fMoreAfter != 0, MoreBefore: r.F&fMoreBefore != 0,
	}
	if len(r.CI) == 2 {
		n.Coll = &ui.Coll{Rows: r.CI[0], Cols: r.CI[1]}
	}
	if len(r.II) == 2 {
		n.Item = &ui.Coll{Rows: r.II[0], Cols: r.II[1]}
	}
	if r.O != nil {
		n.DrawOrder, n.DrawOrderKnown = *r.O, true
	}
	return n
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
		// A tap that opens a screen can take most of a second to start changing it on a
		// busy emulator; when it does change, the wait ends at the first quiet window.
		p["noChange"] = 1000
	}
	if o.Timeout > 0 {
		p["timeout"] = o.Timeout.Milliseconds()
	}
	return p
}

// Clicks is the agent's count of view clicks so far (driver.ClickCounter).
func (d *Driver) Clicks(ctx context.Context) (int64, error) {
	m, err := d.call(ctx, "clicks", nil, 5*time.Second)
	if err != nil {
		return 0, err
	}
	var r struct {
		Clicks int64 `json:"clicks"`
	}
	if err := m.Decode(&r); err != nil {
		return 0, err
	}
	return r.Clicks, nil
}

func (d *Driver) action(ctx context.Context, op string, o driver.ActionOpts, p map[string]any, extra time.Duration) (driver.ActionResult, error) {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	m, err := d.call(ctx, op, actionParams(o, p), timeout+extra+10*time.Second)
	if err != nil {
		return driver.ActionResult{}, err
	}
	var r struct {
		VerifyUnavailable bool   `json:"verifyUnavailable"`
		Result            string `json:"result"`
		InjectMs          int    `json:"injectMs"`
		SettleMs          int    `json:"settleMs"`
		Seq               int64  `json:"seq"`
		Clicked           *bool  `json:"clicked"`
		Before            *int64 `json:"clicksBefore"`
	}
	m.Decode(&r)
	if r.VerifyUnavailable {
		return driver.ActionResult{}, driver.ErrTypingVerificationUnavailable
	}
	if debug {
		fmt.Fprintf(os.Stderr, "[tb] %s %v: inject %dms, settle %s %dms, seq %d\n", op, p, r.InjectMs, r.Result, r.SettleMs, r.Seq)
	}
	return driver.ActionResult{Settle: driver.SettleResult(r.Result), InjectMs: r.InjectMs, SettleMs: r.SettleMs, Seq: d.seq(r.Seq), Clicked: r.Clicked, ClicksBefore: r.Before}, nil
}

var debug = os.Getenv("TB_DEBUG") != ""

func (d *Driver) Tap(ctx context.Context, x, y int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "tap", o, map[string]any{"x": x, "y": y}, 0)
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
	return d.action(ctx, "type", o, map[string]any{"text": text}, time.Duration(len(text))*20*time.Millisecond)
}

func (d *Driver) TypeTextChecked(ctx context.Context, text string, o driver.ActionOpts) (driver.ActionResult, error) {
	if o.NoChangeMs == 0 {
		o.NoChangeMs = 300
	}
	return d.action(ctx, "type", o, map[string]any{"text": text, "verifyFirst": true}, 2*time.Second+time.Duration(len(text))*20*time.Millisecond)
}

func (d *Driver) EraseText(ctx context.Context, n int, o driver.ActionOpts) (driver.ActionResult, error) {
	return d.action(ctx, "erase", o, map[string]any{"n": n}, time.Duration(n)*20*time.Millisecond)
}

var keyCodes = map[string]int{
	"enter": 66, "backspace": 67, "back": 4, "home": 3, "lock": 26, "power": 26,
	"volume up": 24, "volume down": 25, "tab": 61, "escape": 111,
	"remote dpad up": 19, "remote dpad down": 20, "remote dpad left": 21, "remote dpad right": 22,
	"remote dpad center": 23, "remote media play pause": 85, "remote media stop": 86,
	"remote media next": 87, "remote media previous": 88, "remote media rewind": 89,
	"remote media fast forward": 90, "remote system navigation up": 280, "remote system navigation down": 281,
	"remote button a": 96, "remote button b": 97, "remote menu": 82, "remote tv input": 178,
	"remote tv input hdmi 1": 243, "remote tv input hdmi 2": 244, "remote tv input hdmi 3": 245,
}

// KeyCode maps a flow key name to an Android key code.
func KeyCode(key string) (int, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	k = strings.ReplaceAll(k, "_", " ")
	c, ok := keyCodes[k]
	return c, ok
}

func (d *Driver) PressKey(ctx context.Context, key string, o driver.ActionOpts) (driver.ActionResult, error) {
	code, ok := KeyCode(key)
	if !ok {
		return driver.ActionResult{}, fmt.Errorf("unknown key %q", key)
	}
	if code == 67 {
		if result, handled, err := d.editorBackspace(ctx, o); handled || err != nil {
			return result, err
		}
	}
	return d.action(ctx, "key", o, map[string]any{"code": code}, 0)
}

func (d *Driver) HideKeyboard(ctx context.Context) error {
	m, err := d.call(ctx, "hideKeyboard", map[string]any{"timeout": 3000}, 8*time.Second)
	if err != nil {
		return err
	}
	var reply struct {
		Hidden bool `json:"hidden"`
	}
	if err := m.Decode(&reply); err != nil {
		return err
	}
	if !reply.Hidden {
		return fmt.Errorf("the Android keyboard did not close within 3s")
	}
	return nil
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

// Sh runs a shell command on the device through the agent (no adb process per command).
func (d *Driver) Sh(ctx context.Context, cmd string) (string, int, error) {
	m, err := d.call(ctx, "sh", map[string]any{"cmd": cmd, "timeout": 60000}, 65*time.Second)
	if err != nil {
		return "", -1, err
	}
	var r struct {
		Code int    `json:"code"`
		Out  string `json:"out"`
	}
	m.Decode(&r)
	return r.Out, r.Code, nil
}

func (d *Driver) shOK(ctx context.Context, cmd string) (string, error) {
	out, code, err := d.Sh(ctx, cmd)
	if err != nil {
		return out, err
	}
	if code != 0 {
		return out, fmt.Errorf("%s: exit %d: %s", cmd, code, strings.TrimSpace(out))
	}
	return out, nil
}

// ---- apps ----

func validPkg(p string) error {
	if p == "" {
		return fmt.Errorf("no app id given")
	}
	for _, r := range p {
		if !(r == '.' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Errorf("invalid app id %q", p)
		}
	}
	return nil
}

func (d *Driver) launcher(ctx context.Context, pkg string) (string, error) {
	d.mu.Lock()
	c := d.launch[pkg]
	d.mu.Unlock()
	if c != "" {
		return c, nil
	}
	out, err := d.shOK(ctx, "cmd package resolve-activity --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER "+pkg)
	if err != nil {
		return "", fmt.Errorf("app %s is not installed or has no launcher activity", pkg)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	comp := strings.TrimSpace(lines[len(lines)-1])
	if !strings.Contains(comp, "/") {
		return "", fmt.Errorf("app %s is not installed or has no launcher activity", pkg)
	}
	d.mu.Lock()
	d.launch[pkg] = comp
	d.mu.Unlock()
	return comp, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	comp, err := d.launcher(ctx, appID)
	if err != nil {
		return err
	}
	// One on-device script: stop, clear, grant and start, so a launch costs one round trip.
	var script []string
	if o.StopApp || o.ClearState {
		script = append(script, "am force-stop "+appID)
	}
	if o.ClearState {
		script = append(script, clearCmd(appID))
	}
	allAllow := len(o.Permissions) == 0 || (len(o.Permissions) == 1 && strings.EqualFold(o.Permissions["all"], "allow"))
	if allAllow {
		// Grant only what is not granted yet, deciding on the device (dumpsys is large).
		script = append(script, "for p in $(dumpsys package "+appID+" | grep -E '^ +android.permission.[A-Z_]+: granted=false' | sed 's/:.*//' | sort -u); do pm grant "+appID+" $p 2>/dev/null; done")
	} else {
		if len(script) > 0 {
			if _, err := d.shOK(ctx, strings.Join(script, "; ")); err != nil {
				return err
			}
			script = nil
		}
		if err := d.SetPermissions(ctx, appID, o.Permissions); err != nil {
			return err
		}
	}
	args := []string{"am", "start", "-n", comp, "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER", "-f", "0x10200000"}
	for k, v := range o.Arguments {
		switch t := v.(type) {
		case bool:
			args = append(args, "--ez", shellQuote(k), strconv.FormatBool(t))
		case int:
			args = append(args, intExtra(k, int64(t))...)
		case int64:
			args = append(args, "--el", shellQuote(k), strconv.FormatInt(t, 10))
		case float64:
			if t == math.Trunc(t) && math.Abs(t) < 1<<63 {
				args = append(args, intExtra(k, int64(t))...)
			} else {
				args = append(args, "--ef", shellQuote(k), strconv.FormatFloat(t, 'f', -1, 64))
			}
		default:
			args = append(args, "--es", shellQuote(k), shellQuote(fmt.Sprint(v)))
		}
	}
	script = append(script, strings.Join(args, " "))
	out, err := d.shOK(ctx, strings.Join(script, "; "))
	if err := clearFailed(appID, out); err != nil {
		return err
	}
	if err != nil {
		return err
	}
	if strings.Contains(out, "Error:") {
		return fmt.Errorf("could not start %s: %s", appID, strings.TrimSpace(out))
	}
	// Not `am start -W`: the agent tells us when the app's window is up (an event).
	return d.WaitForApp(ctx, appID, 20*time.Second)
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
	if err := validPkg(appID); err != nil {
		return err
	}
	_, err := d.shOK(ctx, "am force-stop "+appID)
	return err
}

func (d *Driver) KillApp(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	// Like the system killing it in the background (issue #6): `am kill` ends the process and
	// keeps its task and saved state, so the next launch restores where the user was. No
	// force-stop, which clears that state (that is stopApp). `am kill` only ends a background
	// app, so one that survives it is sent to the background (Home) and killed again.
	kill := "am kill " + appID + "; ! pidof " + appID + " >/dev/null"
	script := kill + " && exit 0; input keyevent KEYCODE_HOME; " +
		"for i in 1 2 3 4 5 6 7 8 9 10; do sleep 0.3; " + kill + " && exit 0; done; exit 1"
	_, code, err := d.Sh(ctx, script)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("killApp: %s is still running after am kill, even in the background", appID)
	}
	return nil
}

func (d *Driver) ClearState(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	out, err := d.shOK(ctx, clearCmd(appID))
	if cerr := clearFailed(appID, out); cerr != nil {
		return cerr
	}
	return err
}

func (d *Driver) ClearKeychain(ctx context.Context) error { return nil }

// OpenLink starts a VIEW intent for link. It is aimed at the flow's app first (no chooser
// when several apps take the link), unless the browser is asked for; when the app takes no
// such link (a web page, another app's link) it goes out again with no app, and Android
// picks the handler.
func (d *Driver) OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error {
	cmd := "am start -a android.intent.action.VIEW -d " + shellQuote(link)
	if appID != "" && !browser {
		if err := validPkg(appID); err != nil {
			return err
		}
		out, err := d.shOK(ctx, cmd+" "+appID)
		if err == nil || !strings.Contains(out, "unable to resolve Intent") {
			return err
		}
	}
	_, err := d.shOK(ctx, cmd)
	return err
}

// The flow format's permission names -> Android permissions.
var permissionGroups = map[string][]string{
	"camera":        {"android.permission.CAMERA"},
	"contacts":      {"android.permission.READ_CONTACTS", "android.permission.WRITE_CONTACTS", "android.permission.GET_ACCOUNTS"},
	"location":      {"android.permission.ACCESS_FINE_LOCATION", "android.permission.ACCESS_COARSE_LOCATION", "android.permission.ACCESS_BACKGROUND_LOCATION"},
	"microphone":    {"android.permission.RECORD_AUDIO"},
	"phone":         {"android.permission.READ_PHONE_STATE", "android.permission.CALL_PHONE", "android.permission.READ_CALL_LOG", "android.permission.WRITE_CALL_LOG", "android.permission.ANSWER_PHONE_CALLS", "android.permission.READ_PHONE_NUMBERS"},
	"sms":           {"android.permission.SEND_SMS", "android.permission.RECEIVE_SMS", "android.permission.READ_SMS", "android.permission.RECEIVE_MMS"},
	"storage":       {"android.permission.READ_EXTERNAL_STORAGE", "android.permission.WRITE_EXTERNAL_STORAGE"},
	"medialibrary":  {"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO", "android.permission.READ_MEDIA_AUDIO", "android.permission.READ_EXTERNAL_STORAGE"},
	"photos":        {"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO", "android.permission.READ_EXTERNAL_STORAGE"},
	"calendar":      {"android.permission.READ_CALENDAR", "android.permission.WRITE_CALENDAR"},
	"notifications": {"android.permission.POST_NOTIFICATIONS"},
	"bluetooth":     {"android.permission.BLUETOOTH_CONNECT", "android.permission.BLUETOOTH_SCAN", "android.permission.BLUETOOTH_ADVERTISE"},
	"sensors":       {"android.permission.BODY_SENSORS"},
	"activity":      {"android.permission.ACTIVITY_RECOGNITION"},
}

// appOpPermissions are set with appops, not pm grant: dumpsys lists them only as requested
// (no granted= state), and their state is an app-op (all-files access).
var appOpPermissions = map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": "MANAGE_EXTERNAL_STORAGE"}

// SetPermissions grants or revokes runtime permissions. An empty map grants all (the format's default).
func (d *Driver) SetPermissions(ctx context.Context, appID string, perms map[string]string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	if len(perms) == 0 {
		perms = map[string]string{"all": "allow"}
	}
	// The app's requested runtime permissions, filtered on the device (dumpsys is huge).
	out, _, err := d.Sh(ctx, "dumpsys package "+appID+" | grep -E '^ +android.permission.[A-Z_]+: granted=|^ +android.permission.MANAGE_EXTERNAL_STORAGE$' | sort -u")
	if err != nil {
		return err
	}
	requested := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if name, _, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(name, "android.permission.") {
			requested[name] = strings.Contains(line, "granted=true")
		} else if _, op := appOpPermissions[line]; op {
			requested[line] = false // its state is the app-op's, set below whatever it is
		}
	}
	var cmds []string
	apply := func(perm, value string) {
		granted, ok := requested[perm]
		if !ok {
			return
		}
		if op, isOp := appOpPermissions[perm]; isOp {
			mode := map[string]string{"allow": "allow", "deny": "deny", "unset": "default"}[strings.ToLower(value)]
			if mode != "" {
				cmds = append(cmds, "appops set --uid "+appID+" "+op+" "+mode)
			}
			return
		}
		switch strings.ToLower(value) {
		case "allow":
			if !granted {
				cmds = append(cmds, "pm grant "+appID+" "+perm+" 2>/dev/null")
			}
		case "deny":
			if granted {
				cmds = append(cmds, "pm revoke "+appID+" "+perm+" 2>/dev/null")
			}
		case "unset":
			cmds = append(cmds, "pm revoke "+appID+" "+perm+" 2>/dev/null; pm clear-permission-flags "+appID+" "+perm+" user-set user-fixed 2>/dev/null")
		}
	}
	if v, ok := perms["all"]; ok {
		for perm := range requested {
			apply(perm, v)
		}
	}
	for name, v := range perms {
		if name == "all" {
			continue
		}
		if strings.Contains(name, ".") {
			apply(name, v)
			continue
		}
		for _, perm := range permissionGroups[strings.ToLower(name)] {
			apply(perm, v)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	_, _, err = d.Sh(ctx, strings.Join(cmds, "; "))
	return err
}

func (d *Driver) SetLocation(ctx context.Context, lat, lon float64) error {
	d.stateMu.Lock()
	sdk := d.sdk
	d.stateMu.Unlock()
	if sdk < 31 { // 0: not known yet
		// No test providers from the shell before Android 12: the emulator's own GPS.
		if d.info.Emulator {
			_, err := d.adb.Run(ctx, "emu", "geo", "fix", strconv.FormatFloat(lon, 'f', -1, 64), strconv.FormatFloat(lat, 'f', -1, 64))
			return err
		}
		return fmt.Errorf("setLocation needs Android 12 or later, or an emulator")
	}
	return d.mockLocation(ctx, lat, lon)
}

// Location on Android 12+: the shell's test providers (gps, network and fused), which apps read
// through the location service whatever provider they ask for. An emulator's own GPS
// (`emu geo fix`) does not reach apps that use the fused provider. The point is sent again every
// second while the run lasts: a watch that asks for updates at an interval gets them, and the
// fused provider forgets a fix that does not come again. Close removes the providers.
const locationResend = time.Second

func (d *Driver) mockLocation(ctx context.Context, lat, lon float64) error {
	d.locMu.Lock()
	defer d.locMu.Unlock()
	if len(d.locProviders) == 0 {
		_, _, _ = d.Sh(ctx, "appops set com.android.shell android:mock_location allow")
		for _, p := range []string{"gps", "network", "fused"} {
			_, _, _ = d.Sh(ctx, "cmd location providers add-test-provider "+p)
			out, code, err := d.Sh(ctx, "cmd location providers set-test-provider-enabled "+p+" true")
			if err == nil && code == 0 && !strings.Contains(out, "Exception") {
				d.locProviders = append(d.locProviders, p)
			}
		}
		if len(d.locProviders) == 0 {
			return fmt.Errorf("setLocation: could not add test location providers on the device")
		}
	}
	at := strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lon, 'f', -1, 64)
	var cmds []string
	for _, p := range d.locProviders {
		cmds = append(cmds, "cmd location providers set-test-provider-location "+p+" --location "+at+" --accuracy 1")
	}
	send := strings.Join(cmds, "; ")
	if _, err := d.shOK(ctx, send); err != nil {
		return err
	}
	if d.locStop != nil {
		close(d.locStop)
	}
	stop := make(chan struct{})
	d.locStop = stop
	go func() {
		t := time.NewTicker(locationResend)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _, _ = d.Sh(c, send)
				cancel()
			}
		}
	}()
	return nil
}

// stopLocation ends the resends and removes the test providers, so the device reports its
// own location again.
func (d *Driver) stopLocation() {
	d.locMu.Lock()
	defer d.locMu.Unlock()
	if d.locStop != nil {
		close(d.locStop)
		d.locStop = nil
	}
	if len(d.locProviders) == 0 {
		return
	}
	var cmds []string
	for _, p := range d.locProviders {
		cmds = append(cmds, "cmd location providers remove-test-provider "+p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _ = d.Sh(ctx, strings.Join(cmds, "; "))
	d.locProviders = nil
}

// intExtra is an am start integer extra: --ei when it fits an int32, else --el.
func intExtra(k string, v int64) []string {
	if v >= math.MinInt32 && v <= math.MaxInt32 {
		return []string{"--ei", shellQuote(k), strconv.FormatInt(v, 10)}
	}
	return []string{"--el", shellQuote(k), strconv.FormatInt(v, 10)}
}

func (d *Driver) SetOrientation(ctx context.Context, orientation string) error {
	// Flow names (LANDSCAPE_LEFT) and protocol names (landscapeLeft) map alike; others go lower-cased.
	key := strings.ToUpper(strings.ReplaceAll(orientation, "_", ""))
	to := map[string]string{"PORTRAIT": "portrait", "LANDSCAPELEFT": "landscapeLeft", "LANDSCAPERIGHT": "landscapeRight", "UPSIDEDOWN": "upsideDown"}[key]
	if to == "" {
		to = strings.ToLower(orientation)
	}
	_, err := d.call(ctx, "rotate", map[string]any{"to": to}, 5*time.Second)
	if err != nil {
		return err
	}
	// The display turns after the request returns: until it has, the agent reports the old
	// size, and swipes from it would land off a screen that turned. Ask again (every 100 ms,
	// up to rotateWait) until the size has the orientation asked for.
	tall := map[string]bool{"portrait": true, "upsideDown": true}[to]
	wide := strings.HasPrefix(to, "landscape")
	for deadline := time.Now().Add(rotateWait); ; {
		d.refreshSize(ctx)
		w, h := d.Size()
		if (!tall && !wide) || (tall && h >= w) || (wide && w > h) || !time.Now().Before(deadline) || ctx.Err() != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// rotateWait bounds the wait for the display to turn after setOrientation.
var rotateWait = 3 * time.Second

func (d *Driver) refreshSize(ctx context.Context) {
	if m, err := d.call(ctx, "info", nil, 3*time.Second); err == nil {
		var h hello
		m.Decode(&h)
		if h.W > 0 {
			d.stateMu.Lock()
			d.w, d.h = h.W, h.H
			d.stateMu.Unlock()
		}
	}
}

func (d *Driver) SetAirplaneMode(ctx context.Context, on bool) error {
	v := "disable"
	if on {
		v = "enable"
	}
	_, err := d.shOK(ctx, "cmd connectivity airplane-mode "+v)
	return err
}

func (d *Driver) ToggleAirplaneMode(ctx context.Context) error {
	out, err := d.shOK(ctx, "settings get global airplane_mode_on")
	if err != nil {
		return err
	}
	return d.SetAirplaneMode(ctx, strings.TrimSpace(out) != "1")
}

func (d *Driver) AddMedia(ctx context.Context, paths []string) error {
	for _, p := range paths {
		remote := "/sdcard/Pictures/" + filepath.Base(p)
		if _, err := d.adb.Run(ctx, "push", p, remote); err != nil {
			return err
		}
		d.Sh(ctx, "am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE -d "+shellQuote("file://"+remote)+" >/dev/null; content call --uri content://media/external/file --method scan_file --arg "+shellQuote(remote)+" >/dev/null 2>&1")
	}
	return nil
}

func (d *Driver) InstallApp(ctx context.Context, path string) error {
	_, err := d.adb.Run(ctx, "install", "-r", "-g", path)
	return err
}

func (d *Driver) Foreground(ctx context.Context) (string, error) {
	m, err := d.call(ctx, "foreground", nil, 5*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Pkg string `json:"pkg"`
	}
	m.Decode(&r)
	return r.Pkg, nil
}

func (d *Driver) StartRecording(ctx context.Context, path string) error {
	if d.recordCmd != nil {
		return fmt.Errorf("already recording")
	}
	d.recordPath = path
	d.recordCmd = exec.Command(device.ADB(), "-s", d.info.Serial, "shell", "screenrecord", "--bit-rate", "4000000", "/sdcard/tb-recording.mp4")
	return d.recordCmd.Start()
}

func (d *Driver) StopRecording(ctx context.Context) error {
	if d.recordCmd == nil {
		return nil
	}
	d.Sh(ctx, "pkill -INT screenrecord")
	done := make(chan error, 1)
	go func() { done <- d.recordCmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		d.recordCmd.Process.Kill()
	}
	d.recordCmd = nil
	_, err := d.adb.Run(ctx, "pull", "/sdcard/tb-recording.mp4", d.recordPath)
	d.Sh(ctx, "rm -f /sdcard/tb-recording.mp4")
	return err
}

// DisableAnimations turns the system animation scales off and returns a function that restores them.
func (d *Driver) DisableAnimations(ctx context.Context) (restore func(), err error) {
	keys := []string{"window_animation_scale", "transition_animation_scale", "animator_duration_scale"}
	var get []string
	for _, k := range keys {
		get = append(get, "settings get global "+k)
	}
	out, err := d.shOK(ctx, strings.Join(get, "; "))
	if err != nil {
		return func() {}, err
	}
	old := strings.Fields(out)
	var set []string
	for _, k := range keys {
		set = append(set, "settings put global "+k+" 0")
	}
	if _, err := d.shOK(ctx, strings.Join(set, "; ")); err != nil {
		return func() {}, err
	}
	// The agent treats frames drawn on a never-still screen differently with animations off.
	d.call(ctx, "animations", map[string]any{"on": false}, 5*time.Second)
	return func() {
		var cmds []string
		on := false
		for i, k := range keys {
			v := "1"
			if i < len(old) && old[i] != "null" {
				v = old[i]
			}
			if f, err := strconv.ParseFloat(v, 64); k == "animator_duration_scale" && (err != nil || f != 0) {
				on = true
			}
			cmds = append(cmds, "settings put global "+k+" "+v)
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.Sh(c, strings.Join(cmds, "; "))
		d.call(c, "animations", map[string]any{"on": on}, 5*time.Second)
	}, nil
}

// raw JSON helper for diagnostics
func (d *Driver) Raw(ctx context.Context, op string, p map[string]any) (json.RawMessage, error) {
	m, err := d.call(ctx, op, p, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return m.Raw, nil
}

// SetDarkMode switches the system's night mode (`cmd uimode night`), which apps follow.
func (d *Driver) SetDarkMode(ctx context.Context, on bool) error {
	v := "no"
	if on {
		v = "yes"
	}
	_, err := d.shOK(ctx, "cmd uimode night "+v)
	return err
}

// DarkMode reads the system's night mode: "Night mode: yes" when dark.
func (d *Driver) DarkMode(ctx context.Context) (bool, error) {
	out, err := d.shOK(ctx, "cmd uimode night")
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "yes"), nil
}

// clearCmd clears an app's data on the device, and stops the script with a marker when
// Android did not (it prints "Success" when it did). Some vendors' Android (Oppo, Realme,
// Vivo, ...) refuse a shell's `pm clear` unless a developer option allows it: going on would
// run the flow on the app's old data.
func clearCmd(appID string) string {
	return `r=$(pm clear ` + appID + ` 2>&1); case "$r" in *Success*) ;; *) echo "TB_CLEAR_FAILED: $r"; exit 1;; esac`
}

// clearFailed is the error for a clearCmd that did not clear, or nil.
func clearFailed(appID, out string) error {
	i := strings.Index(out, "TB_CLEAR_FAILED:")
	if i < 0 {
		return nil
	}
	why := strings.TrimSpace(out[i+len("TB_CLEAR_FAILED:"):])
	if why == "" {
		why = "no answer"
	}
	return fmt.Errorf("could not clear the data of %s (Android said: %s). On some phones (Oppo, Realme, Vivo, Xiaomi) allow it in Developer options: turn off \"Verify apps over USB\" / turn on \"Disable permission monitoring\"", appID, why)
}

// Release stops tb's agent on the device and frees it for other tools: the agent holds the
// device's only UiAutomation slot (another test tool, uiautomator or an instrumentation cannot
// start while it runs), and the accessibility service it enabled is turned off again. The next
// tb run starts the agent again (about a second).
func (d *Driver) Release(ctx context.Context) error {
	d.closed.Store(true)
	d.stopLocation()
	d.closeEditorForward()
	if c := d.conn(); c != nil {
		c.Call("exit", nil, 2*time.Second)
		c.Close()
	}
	return stopAgent(ctx, d.adb)
}

// ReleaseDevice stops a tb agent left running on a device, with no connection to it (one a
// crashed or killed run left, or one kept with --keep-agents).
func ReleaseDevice(ctx context.Context, serial string) error {
	return stopAgent(ctx, device.NewADB(serial))
}

func stopAgent(ctx context.Context, adb *device.ADBDevice) error {
	_, err := adb.Shell(ctx, `pids=$(pidof tb-agent) && kill $pids; `+
		`cur=$(settings get secure enabled_accessibility_services); `+
		`new=$(echo "$cur" | tr ':' '\n' | grep -v 'dev.testbuddy.agent/' | grep -v '^null$' | paste -sd ':' -); `+
		`if [ "$new" != "$cur" ]; then if [ -z "$new" ]; then settings delete secure enabled_accessibility_services; `+
		`else settings put secure enabled_accessibility_services "$new"; fi; fi; true`)
	return err
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
