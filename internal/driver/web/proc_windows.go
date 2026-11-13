//go:build windows

package web

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func ownGroup() *syscall.SysProcAttr { return nil }

// killProcGroup kills Chrome and the processes it started (issue #124).
func killProcGroup(p *os.Process) {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	cmd.Run()
	p.Kill()
}

// sweepGroup does nothing on Windows: once Chrome has exited its pid may be reused at once,
// and taskkill /T needs that pid.
func sweepGroup(p *os.Process) {}

// watchdogPath is "": tb starts the browser directly on Windows, without the watchdog the other
// systems have (issue #81).
func watchdogPath() string { return "" }
