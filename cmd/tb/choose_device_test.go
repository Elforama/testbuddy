package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
)

// withFakePhones makes tb see two Android emulators of the same name, each served by its own
// fakeApp.
func withFakePhones(t *testing.T) map[string]*fakeApp {
	t.Helper()
	apps := map[string]*fakeApp{
		"emulator-5594": {serial: "emulator-5594", screen: "home"},
		"emulator-5750": {serial: "emulator-5750", screen: "home"},
	}
	var infos []device.Info
	for _, s := range []string{"emulator-5594", "emulator-5750"} {
		infos = append(infos, device.Info{Platform: device.Android, Serial: s, Name: "sdk gphone64 arm64", Emulator: true, State: "device"})
	}
	oldList := listDevices
	listDevices = func(ctx context.Context) []device.Info { return infos }
	restore := connect.Override(device.Android, func(ctx context.Context, i device.Info) (driver.Driver, error) {
		return apps[i.Serial], nil
	})
	t.Cleanup(func() { listDevices = oldList; restore() })
	return apps
}

// issue #397: tb test with no --device, on a machine with several devices, ran on any free one
// without saying which. An agent explored on emulator-5594 (its session holds it), ran its
// flow, and the run went to emulator-5750 with no word that the device changed. It now says
// which device it took and why not the others.
func TestCmdTestSaysWhichDeviceItChose(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	t.Setenv("TB_HOME", t.TempDir()) // no daemon of the caller's: the session is not taken back
	t.Setenv("TB_DEVICE", "")
	apps := withFakePhones(t)
	l, err := devlock.TryAcquire("emulator-5594", `tb session "alice" (tb open/do/look)`)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(apps["emulator-5750"].launched) != 1 {
		t.Fatalf("the run did not go to the free device: %v", apps["emulator-5750"].launched)
	}
	all := out + errOut
	if !strings.Contains(all, `no device named, so this run uses sdk gphone64 arm64 (emulator-5750); not emulator-5594 (in use by tb session "alice"`) ||
		!strings.Contains(all, "Pin one with --device or TB_DEVICE") {
		t.Errorf("issue #397: the run did not say which device it took, and why not the other:\n%s", all)
	}
	if !strings.Contains(all, "on sdk gphone64 arm64 (emulator-5750)") {
		t.Errorf("the flow's start line does not name the serial:\n%s", all)
	}
}

// issue #397: TB_DEVICE pins a run that names no device, as --device would.
func TestCmdTestUsesTBDevice(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_DEVICE", "emulator-5750")
	apps := withFakePhones(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if len(apps["emulator-5750"].launched) != 1 || len(apps["emulator-5594"].launched) != 0 {
		t.Errorf("TB_DEVICE=emulator-5750: launched on 5594 %v, on 5750 %v", apps["emulator-5594"].launched, apps["emulator-5750"].launched)
	}
	if strings.Contains(out+errOut, "no device named") {
		t.Errorf("a pinned run says it chose:\n%s", out+errOut)
	}
}
