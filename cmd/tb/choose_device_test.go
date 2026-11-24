package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/connect"
	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/session"
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

// Review of issue #397: TB_DEVICE set to a session's name (which tb test cannot see) or to a
// device that is gone does not break the run: it says so and chooses, saying which.
func TestCmdTestTBDeviceThatNamesNoDevice(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_DEVICE", "alice")
	withFakePhones(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--debug-output", filepath.Join(dir, "debug")})
	})
	all := out + errOut
	if code != 0 || !strings.Contains(all, "TB_DEVICE=alice names no connected device") || !strings.Contains(all, "no device named, so this run uses") {
		t.Errorf("exit %d:\n%s", code, all)
	}
}

// Review of issue #397: a device that cannot run the flow (no app) is named for that, never "not
// tried"; with only one device that can run it, the run had no choice and says nothing.
func TestCmdTestNoteOnlyWhenThereWasAChoice(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	t.Setenv("TB_HOME", t.TempDir())
	t.Setenv("TB_DEVICE", "")
	withFakePhones(t)
	old := orchestrate.AppInstalled
	orchestrate.AppInstalled = func(_ context.Context, d device.Info, app string) bool { return d.Serial == "emulator-5750" }
	t.Cleanup(func() { orchestrate.AppInstalled = old })
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--debug-output", filepath.Join(dir, "debug")})
	})
	all := out + errOut
	if code != 0 || strings.Contains(all, "no device named") || strings.Contains(all, "not tried") {
		t.Errorf("one device with the app: exit %d:\n%s", code, all)
	}
}

// Review of issue #397: TB_DEVICE's device, held by the caller's own idle session, is taken back
// for the run rather than the run going to another free device.
func TestCmdTestTBDeviceTakesBackTheOwnSession(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	t.Setenv("TB_DEVICE", "emulator-5594")
	apps := withFakePhones(t)
	l, err := devlock.TryAcquire("emulator-5594", `tb session "alice" (tb open/do/look)`)
	if err != nil {
		t.Fatal(err)
	}
	f := startFakeDaemon(t) // the caller's own daemon
	f.hook = func(ctx context.Context, cmd string) {
		if cmd == "yield" {
			l.Release()
		}
	}
	b, _ := json.Marshal(session.Yielded{Yielded: true, Label: "alice (sdk gphone64 arm64)"})
	f.answer("yield", daemon.Response{OK: true, JSON: b})
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() {
		code = cmdTest([]string{flow, "--debug-output", filepath.Join(dir, "debug")})
	})
	if code != 0 || len(apps["emulator-5594"].launched) != 1 || len(apps["emulator-5750"].launched) != 0 {
		t.Errorf("exit %d, on 5594 %v, on 5750 %v:\n%s", code, apps["emulator-5594"].launched, apps["emulator-5750"].launched, out+errOut)
	}
}

// Second review of issue #397: TB_DEVICE's device held by a session that is not the caller's
// (no daemon of the caller's runs), or by the caller's own session busy with a command: the run
// takes the free device, and says why not TB_DEVICE's, instead of failing.
func TestCmdTestTBDeviceHeldGoesElsewhere(t *testing.T) {
	for _, c := range []struct {
		name   string
		daemon *session.Yielded // nil: no daemon of the caller's
		why    string
	}{
		{"another daemon's session", nil, `not emulator-5594 (in use by tb session "bob"`},
		{"own session, busy", &session.Yielded{Busy: true, Label: "bob (sdk gphone64 arm64)"}, "not emulator-5594 (held by your own tb session bob (sdk gphone64 arm64), busy)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TB_NO_DEVICE_LOCK", "")
			t.Setenv("TB_LOCK_DIR", t.TempDir())
			t.Setenv("TB_HOME", t.TempDir())
			t.Setenv("TB_DEVICE", "emulator-5594")
			apps := withFakePhones(t)
			l, err := devlock.TryAcquire("emulator-5594", `tb session "bob" (tb open/do/look)`)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Release()
			if c.daemon != nil {
				f := startFakeDaemon(t)
				b, _ := json.Marshal(*c.daemon)
				f.answer("yield", daemon.Response{OK: true, JSON: b})
			}
			dir := t.TempDir()
			flows := []string{writeFlow(t, dir, "a.yaml", passingFlow), writeFlow(t, dir, "b.yaml", strings.Replace(passingFlow, "name: Go to", "name: Again, go to", 1))}
			var code int
			out, errOut := capture(t, func() {
				code = cmdTest(append(flows, "--debug-output", filepath.Join(dir, "debug")))
			})
			all := out + errOut
			if code != 0 || len(apps["emulator-5750"].launched) != 2 || !strings.Contains(all, c.why) {
				t.Errorf("exit %d, on 5750 %v:\n%s", code, apps["emulator-5750"].launched, all)
			}
		})
	}
}
