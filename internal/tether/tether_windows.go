//go:build windows

package tether

import "os"

// Windows has no watchdog yet: processes start directly (iOS needs macOS; Android recording on
// a Windows host is not tethered).
func startWatchdog(argv []string, onGone [][]string) (*Proc, error) { return nil, nil }

func (p *Proc) Interrupt() error { return p.cmd.Process.Signal(os.Interrupt) }

func (p *Proc) Kill() { p.cmd.Process.Kill() }
