package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/daemon"
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

// issue #184 review: a -p in capitals is lowercased, and one that names no platform is
// refused (exit 2) by tb close and tb release, instead of closing nothing and exiting 0.
func TestClosePlatformIsChecked(t *testing.T) {
	f := startFakeDaemon(t)
	old := listDevices
	t.Cleanup(func() { listDevices = old })
	listDevices = func(ctx context.Context) []device.Info { return nil }

	var code int
	capture(t, func() { code = agentCall("close", []string{"all", "-p", "IOS"}, nil) })
	if _, args, _ := f.last(t); code != 0 || args["platform"] != "ios" {
		t.Errorf("issue #184: tb close all -p IOS: exit %d, sent %v", code, args)
	}
	capture(t, func() { code = cmdRelease([]string{"-p", "IOS"}) })
	if _, args, _ := f.last(t); code != 0 || args["platform"] != "ios" {
		t.Errorf("issue #184: tb release -p IOS: exit %d, sent %v", code, args)
	}

	f.mu.Lock()
	sent := len(f.reqs)
	f.mu.Unlock()
	for name, fn := range map[string]func() int{
		"close":   func() int { return agentCall("close", []string{"all", "-p", "iphone"}, nil) },
		"release": func() int { return cmdRelease([]string{"-p", "iphone"}) },
	} {
		out, errOut := capture(t, func() { code = fn() })
		if code != 2 || !strings.Contains(errOut, `-p: want android, ios or web, not "iphone"`) || strings.Contains(out, "nothing") || strings.Contains(out, "no devices") {
			t.Errorf("issue #184: tb %s -p iphone: exit %d\nstdout %q\nstderr %q", name, code, out, errOut)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != sent {
		t.Errorf("issue #184: a -p naming no platform still reached the daemon: %v", f.reqs[sent:])
	}
}

// issue #184 review: `tb release -d X -p P` whose device is not on P prints the daemon's
// refusal once, not again from its own device lookup.
func TestReleasePlatformMismatchPrintsOnce(t *testing.T) {
	f := startFakeDaemon(t)
	old := listDevices
	t.Cleanup(func() { listDevices = old })
	listDevices = func(ctx context.Context) []device.Info {
		return []device.Info{{Platform: device.IOS, Serial: "UDID-1", Name: "iPhone 17"}}
	}
	msg := `no connected device matches "iPhone 17"`
	f.answer("close", daemon.Response{Exit: 2, Text: msg + "\n"})
	var code int
	out, errOut := capture(t, func() { code = cmdRelease([]string{"-d", "iPhone 17", "-p", "android"}) })
	if n := strings.Count(out+errOut, "no connected device matches"); code != 2 || n != 1 {
		t.Errorf("issue #184 review: exit %d, the refusal printed %d times\nstdout %q\nstderr %q", code, n, out, errOut)
	}
}

// issue #249: `tb release emulator-5700` releases emulator-5700 and no other device. The
// positional serial was ignored, so the command released every device as if `all` had been
// given, and asked the daemon to close its sessions on all of them. Fakes only: the device
// list, the release and the daemon.
func TestReleasePositionalNamesTheDevice(t *testing.T) {
	f := startFakeDaemon(t) // never the real daemon
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	oldList, oldRelease := listDevices, releaseOne
	t.Cleanup(func() { listDevices, releaseOne = oldList, oldRelease })
	listDevices = func(ctx context.Context) []device.Info {
		return []device.Info{
			{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel 7"},
			{Platform: device.Android, Serial: "emulator-5700", Name: "Pixel 9"},
			{Platform: device.IOS, Serial: "UDID-1", Name: "iPhone 17"},
		}
	}
	var released []string
	releaseOne = func(ctx context.Context, d device.Info) error {
		released = append(released, d.Serial)
		return nil
	}
	var code int
	out, errOut := capture(t, func() { code = cmdRelease([]string{"emulator-5700"}) })
	_, closeArgs, _ := f.last(t)
	if code != 0 || !reflect.DeepEqual(released, []string{"emulator-5700"}) || closeArgs["device"] != "emulator-5700" {
		t.Errorf("issue #249: tb release emulator-5700: exit %d, released %v, sent close %v\nstdout %q\nstderr %q",
			code, released, closeArgs, out, errOut)
	}
}

// issue #249: what tb release and the other device commands do with positional arguments.
// `release all` and `close all` still mean every device; two devices, or a positional and a
// -d naming another, are refused (exit 2) before anything is released or sent; tb close
// reads its positional as the device; the commands that take none refuse one.
func TestPositionalDeviceArgs(t *testing.T) {
	f := startFakeDaemon(t)
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	oldList, oldRelease := listDevices, releaseOne
	t.Cleanup(func() { listDevices, releaseOne = oldList, oldRelease })
	listDevices = func(ctx context.Context) []device.Info {
		return []device.Info{
			{Platform: device.Android, Serial: "emulator-5554", Name: "Pixel 7"},
			{Platform: device.Android, Serial: "emulator-5700", Name: "Pixel 9"},
		}
	}
	var released []string
	releaseOne = func(ctx context.Context, d device.Info) error {
		released = append(released, d.Serial)
		return nil
	}
	var code int
	capture(t, func() { code = cmdRelease([]string{"all"}) })
	if _, args, _ := f.last(t); code != 0 || len(released) != 2 || args["device"] != "all" {
		t.Errorf("issue #249: tb release all: exit %d, released %v, sent %v", code, released, args)
	}
	capture(t, func() { code = cmdRelease([]string{"emulator-5700", "-d", "emulator-5700"}) })
	if code != 0 {
		t.Errorf("issue #249: tb release X -d X: exit %d", code)
	}

	f.mu.Lock()
	sent := len(f.reqs)
	f.mu.Unlock()
	released = nil
	for _, argv := range [][]string{
		{"emulator-5700", "emulator-5554"},
		{"emulator-5700", "-d", "emulator-5554"},
	} {
		_, errOut := capture(t, func() { code = cmdRelease(argv) })
		if code != 2 || errOut == "" {
			t.Errorf("issue #249: tb release %q: exit %d, stderr %q", argv, code, errOut)
		}
	}
	refused := map[string][]string{
		"close":   {"a", "b"},
		"look":    {"emulator-5700"},
		"watch":   {"emulator-5700"},
		"log":     {"alice"},
		"clear":   {"alice"},
		"devices": {"emulator-5700"},
		"shot":    {"a.png", "b.png"},
	}
	for cmd, argv := range refused {
		_, errOut := capture(t, func() { code = agentCall(cmd, argv, nil) })
		if code != 2 || !strings.Contains(errOut, "unexpected argument") && !strings.Contains(errOut, "two devices") {
			t.Errorf("issue #249: tb %s %q: exit %d, stderr %q", cmd, argv, code, errOut)
		}
	}
	for name, fn := range map[string]func() int{
		"hierarchy": func() int { return cmdHierarchy([]string{"emulator-5700"}) },
		"devices":   func() int { return cmdDevices([]string{"emulator-5700"}) },
	} {
		_, errOut := capture(t, func() { code = fn() })
		if code != 2 || !strings.Contains(errOut, "unexpected argument") {
			t.Errorf("issue #249: tb %s emulator-5700: exit %d, stderr %q", name, code, errOut)
		}
	}
	f.mu.Lock()
	if len(f.reqs) != sent || len(released) != 0 {
		t.Errorf("issue #249: refused arguments still reached the daemon %v or released %v", f.reqs[sent:], released)
	}
	f.mu.Unlock()

	for _, argv := range [][]string{{"emulator-5700"}, {"all"}} {
		capture(t, func() { code = agentCall("close", argv, nil) })
		if _, args, _ := f.last(t); code != 0 || args["device"] != argv[0] {
			t.Errorf("issue #249: tb close %s: exit %d, sent %v", argv[0], code, args)
		}
	}
}
