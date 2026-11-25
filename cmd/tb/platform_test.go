package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
)

func TestTestNeedsAPlatformOrADevice(t *testing.T) {
	withFakePhone(t)
	dir := t.TempDir()
	flow := writeFlow(t, dir, "pass.yaml", passingFlow)
	cases := []struct {
		name     string
		args     []string
		tbDevice string
		refused  bool
	}{
		{"nothing named", nil, "", true},
		{"-p", []string{"-p", "android"}, "", false},
		{"--device", []string{"--device", "emulator-5554"}, "", false},
		{"TB_DEVICE", nil, "emulator-5554", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TB_DEVICE", c.tbDevice)
			var code int
			out, errOut := capture(t, func() {
				code = cmdTest(append([]string{flow, "--debug-output", filepath.Join(dir, "debug")}, c.args...))
			})
			refused := strings.Contains(errOut, noPlatform)
			if refused != c.refused || (c.refused && code != 2) || (!c.refused && code != 0) {
				t.Errorf("exit %d, refused %v, want refused %v\nstdout: %s\nstderr: %s", code, refused, c.refused, out, errOut)
			}
		})
	}
}

func TestTestPlatformComesFromTheNamedDevice(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	t.Setenv("TB_HOME", t.TempDir())
	apps := withFakePhones(t)
	old := listDevices
	listDevices = func(ctx context.Context) []device.Info {
		return append([]device.Info{{Platform: device.IOS, Serial: "SIM-1", Name: "iPhone 16", Emulator: true, State: "booted"}}, old(ctx)...)
	}
	l, err := devlock.TryAcquire("emulator-5750", "another run")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	t.Setenv("TB_DEVICE", "emulator-5750")
	flow := writeFlow(t, t.TempDir(), "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() { code = cmdTest([]string{flow, "--debug-output", t.TempDir()}) })
	if code != 0 || len(apps["emulator-5594"].launched) != 1 {
		t.Errorf("exit %d, launched on 5594 %v: TB_DEVICE's device was busy, so the run should take the other Android device\n%s%s", code, apps["emulator-5594"].launched, out, errOut)
	}
	_, errOut = capture(t, func() { code = cmdTest([]string{flow, "--device", "role=emulator-5594"}) })
	if code != 2 || !strings.Contains(errOut, noPlatform) || strings.Contains(errOut, "names no connected device") {
		t.Errorf("a role pin alone named the device of an ordinary flow: exit %d, %q", code, errOut)
	}
}

func TestTestOfMultiDeviceFlowsNeedsNoPlatform(t *testing.T) {
	t.Setenv("TB_DEVICE", "")
	withFakePhone(t)
	flow := writeFlow(t, t.TempDir(), "chat.yaml", "devices:\n  alice: {platform: android, appId: com.example.app}\n---\n- alice:\n    - launchApp\n")
	_, errOut := capture(t, func() { cmdTest([]string{flow, "--debug-output", t.TempDir()}) })
	if strings.Contains(errOut, noPlatform) {
		t.Errorf("a flow whose roles name their platforms was refused:\n%s", errOut)
	}
}

func TestOpenNeedsAPlatformOrADevice(t *testing.T) {
	t.Setenv("TB_DEVICE", "")
	var code int
	_, errOut := capture(t, func() {
		code = agentCall("open", []string{"--app", "com.example.app"}, []flagSpec{{[]string{"app", "a"}, textFlag}})
	})
	if code != 2 || !strings.Contains(errOut, noPlatform) {
		t.Errorf("tb open with nothing named: exit %d, stderr %q", code, errOut)
	}
	res := mcpCall(context.Background(), "open", map[string]any{"app": "com.example.app"})
	if res["isError"] != true || !strings.Contains(res["content"].([]map[string]any)[0]["text"].(string), "say which platform") {
		t.Errorf("MCP open with nothing named: %v", res)
	}
	for _, c := range []struct{ platform, device, env, app string }{{"ios", "", "", ""}, {"", "alice", "", ""}, {"", "", "emulator-5554", ""}, {"", "", "", "https://example.com"}} {
		t.Setenv("TB_DEVICE", c.env)
		if err := explicitDevice(c.platform, c.device, c.app); err != nil {
			t.Errorf("explicitDevice(%q, %q, %q) with TB_DEVICE=%q: %v", c.platform, c.device, c.app, c.env, err)
		}
	}
}

func TestTestOfWebFlowsNeedsNoPlatform(t *testing.T) {
	t.Setenv("TB_DEVICE", "")
	withFakePhone(t)
	flow := writeFlow(t, t.TempDir(), "web.yaml", "url: https://example.com\n---\n- assertVisible: Home\n")
	_, errOut := capture(t, func() { cmdTest([]string{flow, "--debug-output", t.TempDir()}) })
	if strings.Contains(errOut, noPlatform) {
		t.Errorf("a web flow, which names its platform with url:, was refused:\n%s", errOut)
	}
}

func TestTestTBDeviceNamesASession(t *testing.T) {
	t.Setenv("TB_NO_DEVICE_LOCK", "")
	t.Setenv("TB_LOCK_DIR", t.TempDir())
	t.Setenv("TB_HOME", t.TempDir())
	apps := withFakePhones(t)
	old := sessionDevice
	sessionDevice = func(ctx context.Context, name string) string {
		if name == "alice" {
			return "emulator-5750"
		}
		return ""
	}
	t.Cleanup(func() { sessionDevice = old })
	t.Setenv("TB_DEVICE", "alice")
	flow := writeFlow(t, t.TempDir(), "pass.yaml", passingFlow)
	var code int
	out, errOut := capture(t, func() { code = cmdTest([]string{flow, "--debug-output", t.TempDir()}) })
	if code != 0 || len(apps["emulator-5750"].launched) != 1 {
		t.Errorf("exit %d, launched on alice's device %v; want the run on the session's device\n%s%s", code, apps["emulator-5750"].launched, out, errOut)
	}
}
