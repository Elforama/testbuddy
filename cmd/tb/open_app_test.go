package main

import (
	"strings"
	"testing"
)

func TestOpenHelpSaysAppLaunches(t *testing.T) {
	for name, text := range map[string]string{"tb help": usage, "tb agent-guide": agentGuide} {
		lower := strings.ToLower(text)
		if !strings.Contains(lower, "without --app") || !strings.Contains(lower, "a launchapp step") || !strings.Contains(lower, "tb clear") && name == "tb agent-guide" {
			t.Errorf("%s does not say that tb open --app launches the app as a launchApp step, and what it does without --app", name)
		}
	}
	for _, tool := range mcpTools {
		if tool.Name == "open" && !strings.Contains(tool.Description, "launches it") {
			t.Errorf("the MCP open tool does not say that app launches: %q", tool.Description)
		}
	}
}
