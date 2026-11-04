package main

import (
	"context"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
)

// tb release leaves a device another tb is using alone, naming who: stopping its agent would
// break that run.
func TestReleaseLeavesBusyDeviceAlone(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	held, err := devlock.TryAcquire("emulator-5554", "tb test flows/")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	err = releaseDevice(context.Background(), device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel"})
	if err == nil || !strings.Contains(err.Error(), "in use by tb test flows/") || !strings.Contains(err.Error(), "left alone") {
		t.Errorf("err = %v", err)
	}
}
