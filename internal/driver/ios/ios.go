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
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// RunnerStartTimeout is how long Open waits for a runner xcodebuild starts: the pool's reopen
// of a lost simulator must leave it that long (issue #101).
const RunnerStartTimeout = startTimeout

// Driver is one booted simulator driven through its runner.
type Driver struct {
	info  device.Info
	connP atomic.Pointer[wire.Conn] // the current runner connection
	dir   string                    // the unpacked agent
	probe string                    // libTBProbe.dylib, injected into launched apps

	mu        sync.Mutex // guards port, w, h, seqBase, runnerPID, recording, locationSet and notes
	port      int
	w, h      int
	seqBase   int64 // added to the runner's change counter: a restarted runner counts from 0 again
	runnerPID int   // the runner process the current connection reaches

	ready atomic.Pointer[ui.Screen] // one-shot tree acquired by the launch handshake

	recordCmd    *exec.Cmd
	recordPath   string
	recordExited chan error // the recording's simctl has exited (its Wait's result)

	locationSet map[string]string // app -> location permission applied (simctl privacy takes ~0.1 s)
	notes       []string          // for the run's trace (Notes)

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
	gaveUp  atomic.Pointer[lostConn] // the last drop reconnecting could not mend (Lost)

	tapMu   sync.Mutex // guards lastTap; held while a tap is sent (input.go, issue #212)
	lastTap lastTap
}

// reconnAttempt is one reconnect, shared by everyone who notices the same drop.
type reconnAttempt struct {
	done chan struct{}
	err  error
}

// lostConn is a dropped connection the driver could not replace, and why.
type lostConn struct {
	conn *wire.Conn
	err  error
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.Noter       = (*Driver)(nil)
	_ driver.LostChecker = (*Driver)(nil)
)

// Lost reports the runner out of reach for good (driver.LostChecker): its connection dropped
// and reconnecting, a runner restart included, gave up (the "disconnected" event). A later
// request that reconnects after all clears it.
func (d *Driver) Lost() error {
	g := d.gaveUp.Load()
	if g == nil || d.closed.Load() || d.stopped.Load() || d.conn() != g.conn {
		return nil
	}
	return fmt.Errorf("the tb runner could not be reached again: %v", g.err)
}

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

func (d *Driver) Platform() string { return "ios" }

func (d *Driver) Serial() string { return d.info.Serial }

func (d *Driver) Name() string { return d.info.Name }

func (d *Driver) Size() (int, int) { d.mu.Lock(); defer d.mu.Unlock(); return d.w, d.h }

func (d *Driver) Seq() int64 { return d.seq(d.conn().Seq()) }

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
