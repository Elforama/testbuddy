package ios

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/wire"
)

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
	// The op the runner is running ("" when none), how long it has run, and when it counts as
	// stuck (issue #48).
	Busy        string `json:"busy"`
	BusyMs      int64  `json:"busyMs"`
	BusyLimitMs int64  `json:"busyLimitMs"`
}

// Ending a runner process, and how often connect asks a busy runner whether it is done; tests
// replace them.
var (
	terminateProc = terminate
	killProc      = kill
	idlePoll      = 200 * time.Millisecond
	// staleExitWait: how long a runner told to exit has before its process is ended.
	staleExitWait = 5 * time.Second
)

// probe asks whoever serves a port what it is (nil when nothing answers quickly). The runner
// answers the hello and this info at once, even while it runs a long op (issue #48): a runner
// that is only busy is found; one that is silent is stuck, and connect replaces it.
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
			if idle, ok := awaitIdle(ctx, c, in); ok {
				d.adopt(c, p, idle)
				return nil
			}
			// Busy past its op's limit, or silent since: stuck. End it and start another here.
			c.Close()
			endRunner(p, in.PID)
			port = p
			break
		}
		if in.UDID == d.info.Serial || in.UDID == "" {
			c.Call("exit", nil, 2*time.Second)
			c.Close()
			// A busy runner gets exit only after its op: end its process if it holds the port.
			if !waitPortFree(p, staleExitWait) {
				endRunner(p, in.PID)
			}
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
	if in.Build != want {
		// The runner of another build was still on the port (it did not exit, and could not be
		// ended): do not drive it with this build's protocol.
		c.Close()
		return fmt.Errorf("port %d on %s is still served by a Test Buddy runner of another build (%q, want %q)", port, d.info.Name, in.Build, want)
	}
	d.adopt(c, port, &in)
	return nil
}

// awaitIdle waits for a runner busy with an op to finish it before connect uses it: an op an
// earlier connection left (a launch a cancelled run gave up on, the request a dropped
// connection was waiting for), so the new connection's first requests are not charged for it
// (issue #48). It returns the runner's info once idle, or false when the runner is stuck: busy
// past the op's limit (busyLimitMs), or no longer answering. When ctx ends first, the runner is
// still within its limit: it is used, its op ahead of the requests.
func awaitIdle(ctx context.Context, c *wire.Conn, in *info) (*info, bool) {
	deadline := time.Now().Add(time.Duration(in.BusyLimitMs-in.BusyMs)*time.Millisecond + time.Second)
	for in.Busy != "" {
		if in.BusyMs > in.BusyLimitMs || time.Now().After(deadline) {
			return in, false
		}
		select {
		case <-ctx.Done():
			return in, true
		case <-time.After(idlePoll): // connection setup only, never during a test
		}
		m, err := c.CallContext(ctx, "info", map[string]any{"keyboard": false}, 3*time.Second)
		if err != nil {
			// A runner past its op's limit answers in turn, after the op: silence is stuck.
			return in, ctx.Err() != nil
		}
		var next info
		m.Decode(&next)
		in = &next
	}
	return in, true
}

// endRunner ends a runner's process (tb started it) and waits for its port to close.
func endRunner(port, pid int) {
	if pid <= 0 {
		return
	}
	terminateProc(pid)
	if !waitPortFree(port, 3*time.Second) {
		killProc(pid)
		waitPortFree(port, 2*time.Second)
	}
}

// waitPortFree waits for nothing to accept connections on port (a runner that stopped
// answering still holds it), and reports whether that happened in time.
func waitPortFree(port int, timeout time.Duration) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err != nil {
			return true
		}
		c.Close()
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond) // shutdown of a stale runner only
	}
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
	// Register before returning: an immediate device callback must have a subscriber.
	go d.pump(c, c.Subscribe())
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
	// Unqueued: a request a cancelled run gave up on does not hold it past its own 3 s; the
	// runner still gets it once that request is done (issue #202).
	_, err := d.conn().CallUnqueued(context.Background(), "exit", nil, 3*time.Second)
	return err
}

// Release stops tb's runner on the simulator and frees it for other tools (another XCTest
// runner, Maestro, Appium). The next tb run starts it again (a few seconds).
func (d *Driver) Release(ctx context.Context) error {
	d.closed.Store(true)
	if c := d.conn(); c != nil {
		// Cleanup: sent on a cancelled run too, and unqueued, so a request the run gave up on
		// (a launch: up to 60 s) does not hold it past its own 2 s; the runner still gets it,
		// and exits, once that request is done (issue #202).
		c.CallUnqueued(context.WithoutCancel(ctx), "exit", nil, 2*time.Second)
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
			if !waitPortFree(p, staleExitWait) {
				// It did not stop (busy or hung): end the runner process, which tb started.
				endRunner(p, in.PID)
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
	// Cleanup: sent on a cancelled run too, and unqueued, so a request the run gave up on does
	// not hold it past its own 2 s; the runner still gets it once that request is done (#202).
	_, err := c.CallUnqueued(context.WithoutCancel(ctx), "keep", nil, 2*time.Second)
	return err
}
