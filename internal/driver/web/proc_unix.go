//go:build !windows

package web

import (
	"os"
	"syscall"
)

func ownGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func killProcGroup(p *os.Process) {
	syscall.Kill(-p.Pid, syscall.SIGKILL)
	p.Kill()
}

func sweepGroup(p *os.Process) { syscall.Kill(-p.Pid, syscall.SIGKILL) }
