//go:build windows

package ios

// iOS simulators need macOS: these only let tb build for Windows, where no runner starts.

import (
	"os"
	"syscall"
)

func ownSession() *syscall.SysProcAttr { return nil }

func terminate(pid int) { kill(pid) }

func kill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
}
