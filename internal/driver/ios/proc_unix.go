//go:build !windows

package ios

import "syscall"

func ownSession() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func terminate(pid int) { syscall.Kill(pid, syscall.SIGTERM) }

func kill(pid int) { syscall.Kill(pid, syscall.SIGKILL) }

func killSession(pid int) { syscall.Kill(-pid, syscall.SIGKILL) }

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
