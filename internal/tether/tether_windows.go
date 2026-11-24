//go:build windows

package tether

import "os"

func startWatchdog(argv []string, onGone [][]string) (*Proc, error) { return nil, nil }

func (p *Proc) Interrupt() error { return p.cmd.Process.Signal(os.Interrupt) }

func (p *Proc) Kill() { p.cmd.Process.Kill() }
