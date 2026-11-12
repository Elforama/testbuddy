package main

import (
	"context"
	"reflect"
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

// tb release with nothing to release, an unknown device, and a device another tb holds (left
// alone, naming the holder, exit 1). None of these reaches a device.
func TestCmdReleasePaths(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	old := listDevices
	t.Cleanup(func() { listDevices = old })

	listDevices = func(ctx context.Context) []device.Info { return nil }
	var code int
	out, _ := capture(t, func() { code = cmdRelease(nil) })
	if code != 0 || !strings.Contains(out, "no devices to release") {
		t.Errorf("none: exit %d, %q", code, out)
	}

	phone := device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel"}
	web := device.Info{Platform: device.Web, Serial: "chrome", Name: "Chrome"}
	listDevices = func(ctx context.Context) []device.Info { return []device.Info{web, phone} }
	_, errOut := capture(t, func() { code = cmdRelease([]string{"-d", "nope"}) })
	if code != 1 || errOut == "" {
		t.Errorf("unknown device: exit %d, %q", code, errOut)
	}
	_, errOut = capture(t, func() { code = cmdRelease([]string{"--bogus"}) })
	if code != 2 {
		t.Errorf("bad option: exit %d, %q", code, errOut)
	}

	held, err := devlock.TryAcquire("emulator-5554", "tb test flows/")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	out, errOut = capture(t, func() { code = cmdRelease(nil) }) // all: the phone only (browsers have no agent)
	if code != 1 || !strings.Contains(errOut, "Pixel (emulator-5554): in use by tb test flows/") || strings.Contains(out+errOut, "Chrome") {
		t.Errorf("busy: exit %d\nstdout %q\nstderr %q", code, out, errOut)
	}
	out, errOut = capture(t, func() { code = cmdRelease([]string{"-p", "ios"}) })
	if code != 0 || !strings.Contains(out, "no devices to release") {
		t.Errorf("-p ios with only a phone: exit %d, %q %q", code, out, errOut)
	}
}

// issue #184: `tb release -p ios` sends the platform with its `close all`, so the daemon
// closes only the iOS sessions. Without -p it closes them all, as before.
func TestReleaseSendsThePlatformToClose(t *testing.T) {
	f := startFakeDaemon(t)
	old := listDevices
	t.Cleanup(func() { listDevices = old })
	listDevices = func(ctx context.Context) []device.Info { return nil }
	for _, c := range []struct {
		argv []string
		want map[string]any
	}{
		{[]string{"-p", "ios"}, map[string]any{"device": "all", "platform": "ios"}},
		{nil, map[string]any{"device": "all", "platform": ""}},
		{[]string{"-d", "Pixel 7", "-p", "android"}, map[string]any{"device": "Pixel 7", "platform": "android"}},
	} {
		capture(t, func() { cmdRelease(c.argv) })
		cmd, args, _ := f.last(t)
		if cmd != "close" || !reflect.DeepEqual(args, c.want) {
			t.Errorf("issue #184: tb release %q sent %s %v, want close %v", c.argv, cmd, args, c.want)
		}
	}
}
