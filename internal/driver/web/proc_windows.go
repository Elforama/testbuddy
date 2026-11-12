//go:build windows

package web

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func ownGroup() *syscall.SysProcAttr { return nil }

// killGroup kills Chrome and the processes it started (issue #124).
func killGroup(p *os.Process) {
	exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
	p.Kill()
}
