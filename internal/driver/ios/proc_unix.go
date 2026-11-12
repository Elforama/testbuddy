//go:build !windows

package ios

import "syscall"

// ownSession starts a process in its own session, so it is not tied to tb's terminal.
func ownSession() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func terminate(pid int) { syscall.Kill(pid, syscall.SIGTERM) }

func kill(pid int) { syscall.Kill(pid, syscall.SIGKILL) }

// killSession kills a process started with ownSession and what it started: its process group
// has its pid.
func killSession(pid int) { syscall.Kill(-pid, syscall.SIGKILL) }
