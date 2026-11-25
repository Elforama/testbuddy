//go:build !windows

package main

import "syscall"

func emulatorAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
