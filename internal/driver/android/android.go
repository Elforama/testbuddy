// Package android drives an Android device or emulator through the Test Buddy agent.
package android

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/legacy"
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
	webViewApp string            // app with DevTools reads enabled for this flow

	recordPath  string
	recordCmd   *exec.Cmd
	recordParts []string // the files the last recording was saved as (recording.go)
	recordAny   *bool    // screenrecord takes --time-limit 0 (no 3-minute cap); nil: not asked yet

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
	gaveUp  atomic.Pointer[lostConn] // the last drop reconnecting could not mend (Lost)
}

// lostConn is a dropped connection the driver could not replace, and why.
type lostConn struct {
	conn *wire.Conn
	err  error
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.LostChecker = (*Driver)(nil)
)

// Lost reports the agent out of reach for good (driver.LostChecker): its connection dropped
// and reconnecting gave up (the "disconnected" event). A later request that reconnects after
// all clears it.
func (d *Driver) Lost() error {
	g := d.gaveUp.Load()
	if g == nil || d.closed.Load() || d.conn() != g.conn {
		return nil
	}
	return fmt.Errorf("the tb agent could not be reached again: %v", g.err)
}

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
				return d.adopt(c, port, hello)
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
	return d.adopt(c, port, hello)
}

type hello struct {
	Version string  `json:"version"`
	SDK     int     `json:"sdk"`
	W       int     `json:"w"`
	H       int     `json:"h"`
	Density float64 `json:"density"`
	Seq     int64   `json:"-"` // the agent's change count when it answered ping (issue #68)
}

func agentVersion() string { return "0.1.37" }

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
	h.Seq = max(v.Seq, c.Hello().Seq)
	h.Version = c.HelloVersion()
	return c, h, nil
}

// adopt makes c the driver's connection. With the accessibility service on, it first enables
// NoopService over c itself, not through d.call: adopt runs inside reconnect, which holds
// connMu, and a drop seen by d.call would reconnect again and wait on connMu forever (issue
// #67). If c drops there, adopt closes it and fails, and the caller's next attempt tries again.
func (d *Driver) adopt(c *wire.Conn, port int, h hello) error {
	// Subscribe before anything else so events pushed meanwhile are buffered for the pump.
	sub := c.Subscribe()
	if os.Getenv("TB_NO_A11Y_SERVICE") == "" {
		c.Call("sh", map[string]any{"cmd": `cur=$(settings get secure enabled_accessibility_services); case "$cur" in *` + noopService + `*) ;; null|"") settings put secure enabled_accessibility_services ` + noopService + ` ;; *) settings put secure enabled_accessibility_services "$cur:` + noopService + `" ;; esac`, "timeout": 5000}, 5*time.Second)
		select {
		case <-c.Done():
			c.Close()
			return fmt.Errorf("the agent connection dropped while enabling the accessibility service: %v", c.Err())
		default: // the command's own result is not needed, as before
		}
	}
	// The agent's count as of now: from ping, or from an event already pushed on c.
	agent := max(h.Seq, c.Seq(), c.SettledSeq())
	d.stateMu.Lock()
	if d.connP.Load() != nil {
		// A restarted agent counts from 0 again, and nothing tells it from the same agent:
		// move past every seq seen, so no screen read before the reconnect counts as current.
		d.seqBase = d.lastSeq.Load() + 1
	}
	d.port, d.w, d.h, d.sdk = port, h.W, h.H, h.SDK
	// The connection changes with its base, under the same lock (issue #68): a count is never
	// mapped with the base of another connection.
	d.connP.Store(c)
	d.raiseLocked(d.seqBase + agent) // attaching to a running agent starts Seq at its count, not 0
	d.stateMu.Unlock()
	go d.pump(c, sub)
	return nil
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

func (d *Driver) Serial() string { return d.info.Serial }

func (d *Driver) Name() string { return d.info.Name }

func (d *Driver) Size() (int, int) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.w, d.h
}

// Seq is the highest change count seen: from pushed "changed" and "settled" events (the agent
// coalesces a burst's trailing change, so only "settled" carries it), tree and action replies,
// and ping on attach. It never goes backwards (issue #68).
func (d *Driver) Seq() int64 {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	c := d.connP.Load() // read with its base: adopt changes both under stateMu
	d.raiseLocked(d.seqBase + max(c.Seq(), c.SettledSeq()))
	return d.lastSeq.Load()
}

// conn is the current agent connection.
func (d *Driver) conn() *wire.Conn { return d.connP.Load() }

// seq maps an agent change count read on the current connection (seqOn).
func (d *Driver) seq(agent int64) int64 { return d.seqOn(d.conn(), agent) }

// seqOn maps an agent change count read on connection c to the driver's, which never goes
// backwards. The base, the current connection and lastSeq change together under stateMu, so a
// count is never mapped with another connection's base (issue #68). A count from a connection
// that is no longer current (a reply decoded after a reconnect) maps just below the current
// base and raises nothing: whatever it describes reads as stale and is read again.
func (d *Driver) seqOn(c *wire.Conn, agent int64) int64 {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if c != d.connP.Load() {
		return max(d.seqBase-1, 0)
	}
	return d.raiseLocked(d.seqBase + agent)
}

// raiseLocked records v as seen and returns it. stateMu is held: every write to lastSeq is.
func (d *Driver) raiseLocked(v int64) int64 {
	if v > d.lastSeq.Load() {
		d.lastSeq.Store(v)
	}
	return v
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
	m, _, err := d.callOn(ctx, op, p, timeout)
	return m, err
}

// callOn is call, also returning the connection that answered: a seq in the reply maps with
// that connection's base (seqOn).
func (d *Driver) callOn(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, *wire.Conn, error) {
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
		return m, c, err
	}
	select {
	case <-c.Done(): // the connection dropped: reconnect and send it once more
	default:
		return m, c, err
	}
	if rerr := d.reconnect(ctx, c); rerr != nil {
		return nil, c, fmt.Errorf("%w (reconnecting failed: %v)", err, rerr)
	}
	c = d.conn()
	m, err = c.Call(op, p, timeout)
	return m, c, err
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
func (d *Driver) pump(c *wire.Conn, sub <-chan *wire.Msg) {
	for m := range sub {
		var e struct {
			Pkg string `json:"pkg"`
		}
		m.Decode(&e)
		d.fanout(driver.Event{Kind: m.Ev, Seq: d.seqOn(c, m.Seq), Pkg: e.Pkg})
	}
	if d.conn() != c || d.closed.Load() {
		return // replaced by a newer connection, or closed on purpose
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.reconnect(ctx, c); err != nil {
		d.gaveUp.Store(&lostConn{conn: c, err: err})
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
