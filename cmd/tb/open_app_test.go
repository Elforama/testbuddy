package main

import (
	"context"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/daemon"
)

func TestOpenHelpSaysAppLaunches(t *testing.T) {
	for name, text := range map[string]string{"tb help": usage, "tb agent-guide": agentGuide} {
		lower := strings.ToLower(text)
		if !strings.Contains(lower, "without --app") || !strings.Contains(lower, "a launchapp step") {
			t.Errorf("%s does not say that tb open --app launches the app as a launchApp step, and what it does without --app", name)
		}
	}
	for _, tool := range mcpTools {
		if tool.Name == "open" && !strings.Contains(tool.Description, "launches it") {
			t.Errorf("the MCP open tool does not say that app launches: %q", tool.Description)
		}
	}
}

func TestOpenReportsAFailedLaunch(t *testing.T) {
	f := startFakeDaemon(t)
	f.answer("open", daemon.Response{Exit: 1, Text: "connected android emulator-5554 in 3ms\nFAILED Launch app \"com.missing\"  40ms\n  Unable to launch app com.missing: not installed\n"})
	var code int
	out, errOut := capture(t, func() {
		code = agentCall("open", []string{"-d", "emulator-5554", "--app", "com.missing"}, []flagSpec{{[]string{"app", "a"}, textFlag}})
	})
	if code != 1 || !strings.Contains(out+errOut, "connected android emulator-5554") || !strings.Contains(out+errOut, "Unable to launch app com.missing") {
		t.Errorf("tb open --app with a failed launch: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	res := mcpCall(context.Background(), "open", map[string]any{"device": "emulator-5554", "app": "com.missing"})
	if res["isError"] != true || !strings.Contains(res["content"].([]map[string]any)[0]["text"].(string), "Unable to launch app com.missing") {
		t.Errorf("MCP open with a failed launch: %v", res)
	}
}
