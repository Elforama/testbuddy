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

const (
	arg   = "__tb_tether"
	env   = "TB_TETHER_WATCHDOG"
	proto = "1"
	ready = "tb-tether-1 started"
)

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

type Proc struct {
	cmd      *exec.Cmd
	Out      io.Reader
	Tethered bool

	keep   *os.File
	mu     sync.Mutex
	waited bool
}

func Start(argv []string, onGone [][]string) (*Proc, error) {
	if len(argv) == 0 {
		return nil, errors.New("tether: no command")
	}
	if p, err := startWatchdog(argv, onGone); p != nil || err != nil {
		return p, err
	}
	return startDirect(argv)
}

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

func (p *Proc) Pid() int { return p.cmd.Process.Pid }

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
