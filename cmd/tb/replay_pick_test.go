package main

import (
	"context"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
)

// tb replay runs on exactly the device named: a serial or UDID always wins, a name must name
// one device (two simulators can share "iPhone 17"), and a name matches without regard to case.
func TestPickReplayDevice(t *testing.T) {
	iphoneA := device.Info{Platform: device.IOS, Serial: "AAAA-1111", Name: "iPhone 17"}
	iphoneB := device.Info{Platform: device.IOS, Serial: "BBBB-2222", Name: "iPhone 17"}
	pixel := device.Info{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel 9"}
	// A device whose name is another device's serial: the serial is what was meant.
	odd := device.Info{Platform: device.Android, Serial: "R58M", Name: "emulator-5554"}
	web := device.Info{Platform: device.Web, Serial: "web", Name: "Chrome"}
	devs := []device.Info{iphoneA, iphoneB, pixel, odd, web}

	cases := []struct {
		target  string
		want    string // serial; "" for an error
		errPart string
	}{
		{target: "BBBB-2222", want: "BBBB-2222"},
		{target: "emulator-5554", want: "emulator-5554"},
		{target: "pixel 9", want: "emulator-5554"},
		{target: "Chrome", want: "web"},
		{target: "web", want: "web"},
		{target: "iPhone 17", errPart: `device name "iPhone 17" is ambiguous`},
		{target: "IPHONE 17", errPart: "ambiguous"},
		{target: "Galaxy", errPart: `no connected device matches "Galaxy"`},
	}
	for _, c := range cases {
		got, err := pickReplayDevice(devs, "", c.target)
		switch {
		case c.want != "" && (err != nil || got.Serial != c.want):
			t.Errorf("%q: got %q, %v; want %q", c.target, got.Serial, err, c.want)
		case c.want == "" && (err == nil || !strings.Contains(err.Error(), c.errPart)):
			t.Errorf("%q: got %q, %v; want an error with %q", c.target, got.Serial, err, c.errPart)
		}
	}
	if _, err := pickReplayDevice(nil, "", "emulator-5554"); err == nil {
		t.Error("picked a device from an empty list")
	}
}

// Releasing a browser stops nothing (it has no agent), but still takes and gives back the
// device's lock, so a later run or release can have it.
func TestReleaseDeviceWithoutAgent(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	web := device.Info{Platform: device.Web, Serial: "web", Name: "Chrome"}
	if err := releaseDevice(context.Background(), web); err != nil {
		t.Fatalf("release: %v", err)
	}
	l, err := devlock.TryAcquire("web", "next run")
	if err != nil {
		t.Fatalf("lock not given back: %v", err)
	}
	l.Release()
}
