package main

import (
	"context"
	"errors"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

type animDriver struct {
	driver.Driver
	restore func()
	err     error
}

func (d animDriver) DisableAnimations(context.Context) (func(), error) { return d.restore, d.err }

func TestDisableAnimationsKeepsRestoreOnError(t *testing.T) {
	restored := false
	restore := disableAnimations(context.Background(), animDriver{restore: func() { restored = true }, err: context.Canceled})
	if restore == nil {
		t.Fatal("issue #202: the restore was dropped on an error")
	}
	restore()
	if !restored {
		t.Error("issue #202: the kept restore is not the driver's")
	}
	if r := disableAnimations(context.Background(), animDriver{err: errors.New("no adb")}); r != nil {
		t.Error("a nil restore was kept")
	}
	type plain struct{ driver.Driver }
	if r := disableAnimations(context.Background(), plain{}); r != nil {
		t.Error("a driver without animations gave a restore")
	}
}
