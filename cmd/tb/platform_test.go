package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
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
	for _, c := range []struct{ platform, device, env string }{{"ios", "", ""}, {"", "alice", ""}, {"", "", "emulator-5554"}} {
		t.Setenv("TB_DEVICE", c.env)
		if err := explicitDevice(c.platform, c.device); err != nil {
			t.Errorf("explicitDevice(%q, %q) with TB_DEVICE=%q: %v", c.platform, c.device, c.env, err)
		}
	}
}
