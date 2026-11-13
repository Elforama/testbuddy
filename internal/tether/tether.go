// Package tether runs a helper process that must not outlive tb: the screen recorders
// (`xcrun simctl io recordVideo`, `adb shell screenrecord`) a killed tb would otherwise leave
// recording until the simulator shuts down or the device's storage fills (issue #52).
//
// A SIGKILL cannot be caught, so tb cannot stop its recorder itself then. As the browser's
// watchdog does (internal/driver/web, issue #81), tb starts its own executable again as a
// watchdog, which starts the recorder and waits. The watchdog's stdin is a pipe whose other end
// only tb holds, so it reads end of file the moment tb is gone, however it went: it then runs
// the recorder's stop commands (onGone: tell the device to stop), interrupts the recorder (it
// finishes its file on SIGINT), and kills it if it has not exited after a grace period.
//
// The watchdog runs in a process group of its own, which the recorder joins: a signal sent to
// tb's group (the terminal's Ctrl-C, a CI runner ending the job's group) does not end it before
// it did its work, and Kill ends both at once. A SIGINT sent to the watchdog alone is passed on
// to the recorder: that is how tb stops a recording (Interrupt).
//
// An executable that does not answer as a watchdog of this protocol (another tb version at the
// path, one an upgrade removed) gets the recorder started directly, as before, untethered.
package tether

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// How tb and the watchdog recognise each other: a watchdog runs only when started with arg and
// env=proto, so a user who exports env does not turn tb into one, and it answers ready first,
// so tb knows the executable it started is a watchdog of this protocol. A process the watchdog
// then cannot start says why on its output and exits at once, as one started directly would.
const (
	arg   = "__tb_tether"
	env   = "TB_TETHER_WATCHDOG"
	proto = "1"
	ready = "tb-tether-1 started"
)

// SelfPath is this executable, resolved when tb started, for starting it again as a watchdog
// (this package's, and the browser's in internal/driver/web): on Linux /proc/self/exe, which is
// this very binary even once the file at its path is replaced or removed; elsewhere its path
// with symbolic links resolved (a Homebrew tb is a link that an upgrade points elsewhere, while
// the old version's own folder goes: starting it then fails, and the process is started
// directly). "" when it cannot be found.
var SelfPath = func() string {
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/proc/self/exe"); err == nil {
			return "/proc/self/exe"
		}
	}
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(self); err == nil {
		return p
	}
	return self
}()

// Proc is a process started with Start.
type Proc struct {
	cmd *exec.Cmd
	// Out is the process's stdout and stderr. It reads to the end of the process's output
	// (Wait does not cut it short) and closes itself there.
	Out io.Reader
	// Tethered: a watchdog stops the process when tb is gone.
	Tethered bool

	keep   *os.File // tb's end of the watchdog's stdin: tb gone, it closes
	mu     sync.Mutex
	waited bool
}

// Start starts argv with its stdout and stderr in Out. Tethered, when tb is gone the watchdog
// runs each command of onGone (each bounded by onGoneWait), then sends argv SIGINT and kills it
// after grace.
func Start(argv []string, onGone [][]string) (*Proc, error) {
	if len(argv) == 0 {
		return nil, errors.New("tether: no command")
	}
	if p, err := startWatchdog(argv, onGone); p != nil || err != nil {
		return p, err
	}
	return startDirect(argv)
}

// startDirect starts argv untethered.
func startDirect(argv []string) (*Proc, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = w, w
	err = cmd.Start()
	w.Close()
	if err != nil {
		r.Close()
		return nil, err
	}
	return &Proc{cmd: cmd, Out: &closeAtEOF{r: bufio.NewReader(r), f: r}}, nil
}

// Pid is the process's (tethered: the watchdog's) pid.
func (p *Proc) Pid() int { return p.cmd.Process.Pid }

// Wait waits for the process to exit and returns its exit error, as exec.Cmd.Wait does.
func (p *Proc) Wait() error {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.waited = true
	p.mu.Unlock()
	if p.keep != nil {
		p.keep.Close()
	}
	return err
}

// closeAtEOF reads r and closes f once r is at its end (or fails).
type closeAtEOF struct {
	r    io.Reader
	f    *os.File
	once sync.Once
}

func (c *closeAtEOF) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	if err != nil {
		c.once.Do(func() { c.f.Close() })
	}
	return n, err
}
