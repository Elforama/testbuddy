package ios

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/tether"
	"github.com/elforama/testbuddy/internal/ui"
	"github.com/elforama/testbuddy/internal/wire"
)

//go:embed assets/tb-ios-agent.tar.gz
var agentArchive []byte

func BuildID() string {
	sum := sha256.Sum256(agentArchive)
	return hex.EncodeToString(sum[:])[:12]
}

const (
	basePort     = 22100
	portSpan     = 800
	startTimeout = 4 * time.Minute
	idleSeconds  = 1800
)

const RunnerStartTimeout = startTimeout

type Driver struct {
	info  device.Info
	connP atomic.Pointer[wire.Conn]
	dir   string
	probe string

	mu        sync.Mutex
	port      int
	w, h      int
	seqBase   int64
	runnerPID int

	ready atomic.Pointer[ui.Screen]

	recording      bool
	recordStarting chan struct{}
	recordProc     *tether.Proc
	recordPath     string
	recordExited   chan error
	recordT0       time.Time
	recordMarkHeld time.Duration

	locationSet map[string]string
	notes       []string
	stalls      []stall

	connMu  sync.Mutex
	reconn  *reconnAttempt
	closed  atomic.Bool
	stopped atomic.Bool
	lastSeq atomic.Int64
	subMu   sync.Mutex
	subs    map[int]chan driver.Event
	nextSub int
	gaveUp  atomic.Pointer[lostConn]

	tapMu    sync.Mutex
	lastTap  lastTap
	reads    atomic.Int64
	lastRead atomic.Pointer[screenRead]
}

type reconnAttempt struct {
	done chan struct{}
	err  error
}

type lostConn struct {
	conn *wire.Conn
	err  error
}

var (
	_ driver.Driver      = (*Driver)(nil)
	_ driver.Noter       = (*Driver)(nil)
	_ driver.LostChecker = (*Driver)(nil)
)

func (d *Driver) Lost() error {
	g := d.gaveUp.Load()
	if g == nil || d.closed.Load() || d.stopped.Load() || d.conn() != g.conn {
		return nil
	}
	return fmt.Errorf("the tb runner could not be reached again: %v", g.err)
}

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
	d.endRecording()
	if c := d.conn(); c != nil {
		return c.Close()
	}
	return nil
}

func (d *Driver) conn() *wire.Conn { return d.connP.Load() }

func (d *Driver) Conn() *wire.Conn { return d.conn() }

func (d *Driver) Port() int { d.mu.Lock(); defer d.mu.Unlock(); return d.port }
