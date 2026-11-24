//go:build windows

package web

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func ownGroup() *syscall.SysProcAttr { return nil }

func killProcGroup(p *os.Process) {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	cmd.Run()
	p.Kill()
}

func sweepGroup(p *os.Process) {}

func watchdogPath() string { return "" }
