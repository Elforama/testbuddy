//go:build !windows

package daemon

import "syscall"

func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
