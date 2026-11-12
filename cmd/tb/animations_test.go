package main

import (
	"context"
	"errors"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// animDriver is a driver whose DisableAnimations returns restore and err as set.
type animDriver struct {
	driver.Driver
	restore func()
	err     error
}

func (d animDriver) DisableAnimations(context.Context) (func(), error) { return d.restore, d.err }

// issue #202 (second review): the Prepare hook keeps DisableAnimations' restore even when it
// returns an error: the scales may have been set though the run was cancelled meanwhile. It
// used to keep it only without an error, so a cancel left the device's animations off.
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
