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

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/legacy"
	"github.com/elforama/testbuddy/internal/tether"
	"github.com/elforama/testbuddy/internal/wire"
)

//go:embed assets/tb-agent.apk
var agentAPK []byte

const socketName = "localabstract:testbuddy-agent"

func agentHash() string {
	sum := sha256.Sum256(agentAPK)
	return hex.EncodeToString(sum[:])[:12]
}

type Driver struct {
	adb        *device.ADBDevice
	info       device.Info
	connP      atomic.Pointer[wire.Conn]
	stateMu    sync.Mutex
	port       int
	editorMu   sync.Mutex
	editorPort int
	editorOld  atomic.Bool
	w, h       int
	sdk        int
	mu         sync.Mutex
	launch     map[string]string
	webViewApp string

	recordPath  string
	recordCmd   *tether.Proc
	recordParts []string
	recordT0    time.Time
	clockAhead  time.Duration
	clockKnown  bool
	recordAny   *bool

	ridBase string
	ridNext atomic.Int64

	locMu      sync.Mutex
	locAt      atomic.Pointer[locPoint]
	resumeMu   sync.Mutex
	resumeDone chan struct{}

	connMu      sync.Mutex
	closed      atomic.Bool
	closingOnce sync.Once
	closingCh   chan struct{}
	closeOnce   sync.Once
	seqBase     int64
	lastSeq     atomic.Int64
	subMu       sync.Mutex
	subs        map[int]chan driver.Event
	nextSub     int
	gaveUp      atomic.Pointer[lostConn]
	gaveUpOn    *wire.Conn

	notesMu       sync.Mutex
	notes         []string
	windowFixSeen map[string]bool
}

func (d *Driver) note(s string) {
	d.notesMu.Lock()
	d.notes = append(d.notes, s)
	d.notesMu.Unlock()
}

func (d *Driver) Notes() []string {
	d.notesMu.Lock()
	defer d.notesMu.Unlock()
	n := d.notes
	d.notes = nil
	d.windowFixSeen = nil
	return n
}

type lostConn struct {
	conn *wire.Conn
	err  error
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.LostChecker = (*Driver)(nil)
	_ driver.Noter       = (*Driver)(nil)
)

func (d *Driver) Lost() error {
	g := d.gaveUp.Load()
	if g == nil || d.closed.Load() || d.conn() != g.conn {
		return nil
	}
	return fmt.Errorf("the tb agent could not be reached again: %v", g.err)
}

func Open(ctx context.Context, info device.Info) (*Driver, error) {
	d := &Driver{adb: device.NewADB(info.Serial), info: info, launch: map[string]string{}, subs: map[int]chan driver.Event{},
		ridBase: strconv.FormatInt(time.Now().UnixNano(), 36)}
	if err := d.open(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Driver) open(ctx context.Context) error {
	err := d.connect(ctx)
	var ns *notStartedError
	if err == nil || !errors.As(err, &ns) || !ns.stuck || !d.info.Emulator || !ci.On() || ctx.Value(noRestartKey{}) != nil || !takeRestart(d.info.Serial) {
		return err
	}
	fmt.Fprintf(os.Stderr, "tb: the agent on %s runs but Android does not answer it: restarting the emulator (CI mode)\n", d.info.Serial)
	if rerr := d.restartDevice(ctx); rerr != nil {
		return fmt.Errorf("%w\ntb restarted the emulator to recover, but %v", err, rerr)
	}
	if err := d.connect(ctx); err != nil {
		return err
	}
	d.note(fmt.Sprintf("before this flow, tb restarted %s: its agent ran but Android did not answer it (CI mode, issue #217)", d.info.Serial))
	return nil
}

type noRestartKey struct{}

func WithoutRestart(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRestartKey{}, true)
}

var (
	restartMu sync.Mutex
	restarted = map[string]bool{}
)

func takeRestart(serial string) bool {
	restartMu.Lock()
	defer restartMu.Unlock()
	if restarted[serial] {
		return false
	}
	restarted[serial] = true
	return true
}

func wasRestarted(serial string) bool {
	restartMu.Lock()
	defer restartMu.Unlock()
	return restarted[serial]
}

const restartBoot = 3 * time.Minute

const RestartBoot = restartBoot

func (d *Driver) restartDevice(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, restartBoot)
	defer cancel()
	const bootState = "echo $(cat /proc/sys/kernel/random/boot_id) $(getprop sys.boot_completed)"
	before, _ := d.adb.Shell(ctx, bootState)
	oldBoot := strings.Fields(before)
	if len(oldBoot) != 2 {
		return fmt.Errorf("could not read its boot id (%q), so it was not rebooted", strings.TrimSpace(before))
	}
	if _, err := d.adb.Run(ctx, "reboot"); err != nil {
		return err
	}
	for {
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		d.adb.Run(wctx, "wait-for-device")
		out, _ := d.adb.Shell(wctx, bootState)
		wcancel()
		if f := strings.Fields(out); len(f) == 2 && f[0] != oldBoot[0] && f[1] == "1" {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("it did not finish booting within %s", restartBoot)
		case <-time.After(time.Second):
		}
	}
	kctx, kcancel := context.WithTimeout(ctx, 5*time.Second)
	defer kcancel()
	d.adb.Shell(kctx, "input keyevent 82")
	return nil
}

type notStartedError struct {
	msg   string
	stuck bool
	err   error
}

func (e *notStartedError) Error() string { return e.msg }
func (e *notStartedError) Unwrap() error { return e.err }

func (d *Driver) notStarted(ctx context.Context, err error, logTail string, hung bool) error {
	stuck, why := d.diagnose(ctx, hung)
	msg := fmt.Sprintf("the Test Buddy agent did not start on %s: %v; %s", d.info.Serial, err, why)
	if t := strings.TrimSpace(logTail); t != "" {
		msg += "\n" + t
	}
	return &notStartedError{msg: msg, stuck: stuck, err: err}
}

func (d *Driver) diagnose(ctx context.Context, hung bool) (stuck bool, why string) {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := d.adb.Run(qctx, "get-state")
	state := strings.TrimSpace(out)
	if err != nil || state != "device" {
		if err != nil {
			state = err.Error()
		}
		return false, fmt.Sprintf("adb does not see the device as ready (%s): reconnect or restart it", state)
	}
	pid, _ := d.adb.Shell(qctx, "pidof tb-agent")
	if pid = strings.TrimSpace(pid); pid == "" {
		return false, `adb sees the device ("device"), but the agent is not running: it exited (its log, if any, follows)`
	}
	if !hung {
		return false, fmt.Sprintf(`adb sees the device ("device") and the agent is running (pid %s)`, pid)
	}
	if !ci.On() {
		why = fmt.Sprintf(`adb sees the device ("device") and the agent is running (pid %s) and said hello, but it did not answer "info" within %s (slow or stuck)`, pid, infoWait)
		if !d.info.Emulator {
			return true, why + ": if it stays so, restart the device"
		}
		return true, why + ": if it stays so, restart the emulator (tb restarts it by itself in CI mode)"
	}
	why = fmt.Sprintf(`adb sees the device ("device") and the agent is running (pid %s) and said hello, but it did not answer: Android itself (its window or accessibility service) is not answering`, pid)
	switch {
	case !d.info.Emulator:
		return true, why + ": restart the device"
	case wasRestarted(d.info.Serial):
		return true, why + "; tb restarted the emulator and it is still not answering: restart it, or wipe its data"
	}
	return true, why + ": restart the emulator"
}

func (d *Driver) connect(ctx context.Context) error { return d.connectOnce(ctx, false) }

func (d *Driver) connectRetry(ctx context.Context) error { return d.connectOnce(ctx, true) }

func (d *Driver) stopLeftoverDriver(ctx context.Context) bool {
	if out, _ := exec.Command("pgrep", "-f", legacy.HostProcessPattern()).Output(); len(strings.TrimSpace(string(out))) > 0 {
		return false
	}
	pkgs := legacy.AndroidDriverPackages()
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
	port := d.adb.ExistingForward(ctx, socketName)
	if port == 0 {
		port, _ = d.adb.Forward(ctx, socketName)
	}
	if port != 0 {
		if c, hello, _, err := dialAgent(port, 600*time.Millisecond, infoWait); err == nil {
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
			return d.notStarted(ctx, err, "", false)
		}
	}
	c, hello, hung, err := dialAgent(port, 15*time.Second, infoWait)
	if hung && ci.On() {
		c, hello, hung, err = dialAgent(port, 15*time.Second, infoRetryWait)
	}
	if err != nil {
		logTail, _ := d.adb.Shell(ctx, "tail -n 20 /data/local/tmp/tb-agent.log")
		if strings.Contains(logTail, "already registered") && !retried && d.stopLeftoverDriver(ctx) {
			return d.connectRetry(ctx)
		}
		return d.notStarted(ctx, err, logTail, hung)
	}
	return d.adopt(c, port, hello)
}

var infoWait = 3 * time.Second

var infoRetryWait = 12 * time.Second

type hello struct {
	Version    string  `json:"version"`
	SDK        int     `json:"sdk"`
	W          int     `json:"w"`
	H          int     `json:"h"`
	Density    float64 `json:"density"`
	LocationOn bool    `json:"-"`
	Seq        int64   `json:"-"`
}

func agentVersion() string { return "0.1.67" }

func dialHello(port int, timeout time.Duration) (*wire.Conn, hello, error) {
	c, h, _, err := dialAgent(port, timeout, infoWait)
	return c, h, err
}

func dialAgent(port int, timeout, infoWait time.Duration) (_ *wire.Conn, _ hello, hung bool, _ error) {
	c, err := wire.Dial(fmt.Sprintf("127.0.0.1:%d", port), timeout)
	if err != nil {
		return nil, hello{}, false, err
	}
	m, err := c.Call("info", nil, infoWait)
	if err != nil {
		c.Close()
		var rt *wire.ReplyTimeout
		return nil, hello{}, errors.As(err, &rt), err
	}
	var h hello
	m.Decode(&h)
	v, err := c.Call("ping", nil, 2*time.Second)
	if err != nil {
		c.Close()
		return nil, hello{}, false, err
	}
	h.Seq = max(v.Seq, c.Hello().Seq)
	h.Version = c.HelloVersion()
	if g := c.Hello(); g != nil {
		var on struct {
			Location bool `json:"location"`
		}
		g.Decode(&on)
		h.LocationOn = on.Location
	}
	return c, h, false, nil
}

func (d *Driver) adopt(c *wire.Conn, port int, h hello) error {
	if d.closed.Load() {
		c.Close()
		return errDriverClosed
	}
	sub := c.Subscribe()
	if os.Getenv("TB_NO_A11Y_SERVICE") == "" {
		c.Call("sh", map[string]any{"cmd": `cur=$(settings get secure enabled_accessibility_services); case "$cur" in *` + noopService + `*) ;; null|"") settings put secure enabled_accessibility_services ` + noopService + ` ;; *) settings put secure enabled_accessibility_services "$cur:` + noopService + `" ;; esac`, "timeout": 5000}, 5*time.Second)
		select {
		case <-c.Done():
			c.Close()
			return fmt.Errorf("the agent connection dropped while enabling the accessibility service: %v", c.Err())
		default:
		}
	}
	agent := max(h.Seq, c.Seq(), c.SettledSeq())
	d.stateMu.Lock()
	if d.connP.Load() != nil {
		d.seqBase = d.lastSeq.Load() + 1
	}
	d.port, d.w, d.h, d.sdk = port, h.W, h.H, h.SDK
	d.connP.Store(c)
	d.raiseLocked(d.seqBase + agent)
	d.stateMu.Unlock()
	if d.closed.Load() {
		c.Close()
		return errDriverClosed
	}
	d.editorOld.Store(true)
	d.resumeLocation(c, h.LocationOn)
	go d.pump(c, sub)
	return nil
}

func (d *Driver) startAgent(ctx context.Context) error {
	remote := "/data/local/tmp/tb-agent-" + agentHash() + ".apk"
	out, _ := d.adb.Shell(ctx, "ls "+remote+" 2>/dev/null; "+killAgent+removeLeftLocation+"for f in /data/local/tmp/tb-agent-*.apk; do [ \"$f\" != \""+remote+"\" ] && rm -f \"$f\"; done; true")
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
	setup := ""
	if os.Getenv("TB_NO_A11Y_SERVICE") == "" {
		setup = serviceSetup(remote)
	}
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

func (d *Driver) Seq() int64 {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	c := d.connP.Load()
	d.raiseLocked(d.seqBase + max(c.Seq(), c.SettledSeq()))
	return d.lastSeq.Load()
}

func (d *Driver) conn() *wire.Conn { return d.connP.Load() }

func (d *Driver) seq(agent int64) int64 { return d.seqOn(d.conn(), agent) }

func (d *Driver) seqOn(c *wire.Conn, agent int64) int64 {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if c != d.connP.Load() {
		return max(d.seqBase-1, 0)
	}
	return d.raiseLocked(d.seqBase + agent)
}

func (d *Driver) raiseLocked(v int64) int64 {
	if v > d.lastSeq.Load() {
		d.lastSeq.Store(v)
	}
	return v
}

func (d *Driver) closing() chan struct{} {
	d.closingOnce.Do(func() { d.closingCh = make(chan struct{}) })
	return d.closingCh
}

func (d *Driver) markClosed() {
	d.closed.Store(true)
	d.closeOnce.Do(func() { close(d.closing()) })
}

func (d *Driver) Close() error {
	d.markClosed()
	if d.connMu.TryLock() {
		d.gaveUpOn = nil
		d.connMu.Unlock()
	}
	d.endRecording(context.Background())
	d.stopLocation()
	d.waitResume(context.Background())
	d.closeEditorForward()
	return d.conn().Close()
}

func (d *Driver) Conn() *wire.Conn { return d.conn() }

func (d *Driver) call(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, error) {
	m, _, err := d.callOn(ctx, op, p, timeout)
	return m, err
}

func (d *Driver) callOn(ctx context.Context, op string, p map[string]any, timeout time.Duration) (*wire.Msg, *wire.Conn, error) {
	if debug && op != "tap" && op != "swipe" && op != "key" && op != "type" {
		t0 := time.Now()
		defer func() { fmt.Fprintf(os.Stderr, "[tb] %s %dms\n", op, time.Since(t0).Milliseconds()) }()
	}
	if err := ctx.Err(); err != nil {
		return nil, d.conn(), err
	}
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left < timeout {
			timeout = max(left, 10*time.Millisecond)
		}
	}
	if stampedOps[op] {
		q := make(map[string]any, len(p)+1)
		for k, v := range p {
			q[k] = v
		}
		q["rid"] = fmt.Sprintf("%s-%d", d.ridBase, d.ridNext.Add(1))
		p = q
	}
	c := d.conn()
	m, err := c.CallContext(ctx, op, d.sinceOn(c, p), timeout)
	var agentErr *wire.AgentError
	if err == nil || errors.As(err, &agentErr) || ctx.Err() != nil {
		return m, c, err
	}
	select {
	case <-c.Done():
	default:
		return m, c, err
	}
	if rerr := d.reconnect(ctx, c); rerr != nil {
		return nil, c, fmt.Errorf("%w (reconnecting failed: %v)", err, rerr)
	}
	c = d.conn()
	m, err = c.CallContext(ctx, op, d.sinceOn(c, p), timeout)
	return m, c, err
}

type (
	driverSince int64
	agentSince  struct {
		conn *wire.Conn
		seq  int64
	}
)

func (d *Driver) sinceOn(c *wire.Conn, p map[string]any) map[string]any {
	var v int64
	switch s := p["since"].(type) {
	case driverSince:
		d.stateMu.Lock()
		base, current := d.seqBase, c == d.connP.Load()
		d.stateMu.Unlock()
		v = -1
		if current && int64(s) >= base {
			v = int64(s) - base
		}
	case agentSince:
		v = -1
		if s.conn == c {
			v = s.seq
		}
	default:
		return p
	}
	q := make(map[string]any, len(p))
	for k, val := range p {
		q[k] = val
	}
	q["since"] = v
	return q
}

var stampedOps = map[string]bool{
	"tap": true, "doubleTap": true, "press": true, "swipe": true, "key": true, "type": true,
	"erase": true, "clear": true, "sh": true, "rotate": true,
}

func (d *Driver) pump(c *wire.Conn, sub <-chan *wire.Msg) {
	for m := range sub {
		var e struct {
			Pkg string `json:"pkg"`
		}
		m.Decode(&e)
		d.fanout(driver.Event{Kind: m.Ev, Seq: d.seqOn(c, m.Seq), Pkg: e.Pkg})
	}
	if d.conn() != c || d.closed.Load() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reconnectWindow()+15*time.Second)
	defer cancel()
	if err := d.reconnect(ctx, c); err != nil && !d.closed.Load() {
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

func (d *Driver) reconnect(ctx context.Context, dropped *wire.Conn) error {
	d.connMu.Lock()
	defer d.connMu.Unlock()
	if d.conn() != dropped {
		return nil
	}
	if d.closed.Load() {
		return errDriverClosed
	}
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go func() {
		select {
		case <-d.closing():
			stop(errDriverClosed)
		case <-ctx.Done():
		}
	}()
	start := time.Now()
	window := reconnectWindow()
	if d.gaveUpOn == dropped {
		window = 0
	}
	deadline := start.Add(window)
	var err error
	for attempt := 0; ; attempt++ {
		if d.closed.Load() {
			return errDriverClosed
		}
		if err = d.connect(ctx); err == nil {
			d.gaveUpOn = nil
			d.note(fmt.Sprintf("the connection to the tb agent dropped at %s (during this flow, or before it began): tb reconnected in %.1f s",
				start.Format("15:04:05.0"), time.Since(start).Seconds()))
			return nil
		}
		if !time.Now().Before(deadline) {
			if !d.closed.Load() {
				d.gaveUpOn = dropped
			}
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		wait := min(time.Duration(attempt+1)*500*time.Millisecond, 3*time.Second, max(time.Until(deadline), 0))
		wctx, cancel := context.WithTimeout(ctx, wait)
		d.adb.Run(wctx, "wait-for-device")
		cancel()
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

var errDriverClosed = errors.New("the driver is closed")

var reconnectFor = 45 * time.Second

func reconnectWindow() time.Duration {
	if ci.On() {
		return 2 * reconnectFor
	}
	return reconnectFor
}

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
	m, err := d.call(ctx, "settle", map[string]any{"since": driverSince(since), "noChange": noChangeMs, "timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Result string `json:"result"`
	}
	m.Decode(&r)
	return driver.SettleResult(r.Result), nil
}

func (d *Driver) Release(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseWait)
	defer cancel()
	d.markClosed()
	d.endRecording(ctx)
	d.stopLocation()
	d.waitResume(ctx)
	d.closeEditorForward()
	if c := d.conn(); c != nil {
		c.CallUnqueued(ctx, "exit", nil, 2*time.Second)
		c.Close()
	}
	return stopAgent(ctx, d.adb)
}

var releaseWait = 10 * time.Second

func ReleaseDevice(ctx context.Context, serial string) error {
	return stopAgent(ctx, device.NewADB(serial))
}

func stopAgent(ctx context.Context, adb *device.ADBDevice) error {
	_, err := adb.Shell(ctx, `pids=$(pidof tb-agent) && kill $pids; `+removeLeftLocation+
		`cur=$(settings get secure enabled_accessibility_services); `+
		`new=$(echo "$cur" | tr ':' '\n' | grep -v 'dev.testbuddy.agent/' | grep -v '^null$' | paste -sd ':' -); `+
		`if [ "$new" != "$cur" ]; then if [ -z "$new" ]; then settings delete secure enabled_accessibility_services; `+
		`else settings put secure enabled_accessibility_services "$new"; fi; fi; true`)
	return err
}

const removeLeftLocation = `m=` + locationList + `; if [ -f $m ]; then for p in $(cat ` + locationList + `); do cmd location providers remove-test-provider $p; done; rm -f ` + locationList + `; fi; `

const locationList = "/data/local/tmp/tb-location-providers"

const killAgent = `pids=$(pidof tb-agent) && { kill $pids; for i in 1 2 3 4 5; do pidof tb-agent >/dev/null || break; sleep 0.2; done; ` +
	`left=$(pidof tb-agent) && kill -9 $left; }; `

func (d *Driver) Keep(ctx context.Context) error {
	c := d.conn()
	if c == nil {
		return nil
	}
	_, err := c.CallUnqueued(context.WithoutCancel(ctx), "keep", nil, 2*time.Second)
	return err
}
