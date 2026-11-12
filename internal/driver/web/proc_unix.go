//go:build !windows

package web

import (
	"os"
	"syscall"
)

// ownGroup starts Chrome in a process group of its own, which its helper processes (renderers,
// GPU process, crash handler) join: killGroup can then end them all (issue #124). tb closes
// Chrome itself on Ctrl-C or SIGTERM, so it does not need the terminal's signals.
func ownGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// killGroup kills Chrome's process group (its pid), and Chrome itself in case it is not the
// group's leader.
func killGroup(p *os.Process) {
	syscall.Kill(-p.Pid, syscall.SIGKILL)
	p.Kill()
}
