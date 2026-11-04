//go:build !windows

package daemon

import "syscall"

// detached runs the daemon in its own session, so it outlives the command that started it.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
