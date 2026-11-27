package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/daemon"
)

func TestCmdSavePathArgs(t *testing.T) {
	f := startFakeDaemon(t)
	var code int
	capture(t, func() { code = cmdSave([]string{"--path", "checkout", "-d", "alice"}) })
	cmd, args, _ := f.last(t)
	if _, hasFile := args["path"]; code != 0 || cmd != "save" || args["name"] != "checkout" || args["device"] != "alice" || hasFile {
		t.Errorf("code %d cmd %q args %v", code, cmd, args)
	}
	_, errOut := capture(t, func() { code = cmdSave([]string{"--path", "checkout", "flows/x.yaml"}) })
	if code != 2 || !strings.Contains(errOut, "tb save --path <name>") {
		t.Errorf("a file and a path name: code %d, %q", code, errOut)
	}
}

func TestCmdGoArgs(t *testing.T) {
	f := startFakeDaemon(t)
	f.answer("go", daemon.Response{OK: true, Text: "arrived at \"Total\"\n"})
	var code int
	out, _ := capture(t, func() { code = cmdGo([]string{"checkout", "-d", "alice", "-e", "USER=ann", "--no-wait"}) })
	cmd, args, _ := f.last(t)
	want := map[string]any{"device": "alice", "name": "checkout", "env": map[string]any{"USER": "ann"}, "no_wait": true}
	if code != 0 || cmd != "go" || !reflect.DeepEqual(args, want) || out != "arrived at \"Total\"\n" {
		t.Errorf("code %d cmd %q args %v out %q", code, cmd, args, out)
	}
	capture(t, func() { code = cmdGo([]string{"-d", "alice"}) })
	if cmd, args, _ := f.last(t); code != 0 || cmd != "go" || args["name"] != "" {
		t.Errorf("list: code %d cmd %q args %v", code, cmd, args)
	}
	_, errOut := capture(t, func() { code = cmdGo([]string{"a", "b"}) })
	if code != 2 || !strings.Contains(errOut, "usage: tb go") {
		t.Errorf("two names: code %d, %q", code, errOut)
	}
}

func TestMCPPathTools(t *testing.T) {
	f := startFakeDaemon(t)
	mcpCall(t.Context(), "save_flow", map[string]any{"device": "alice", "name": "checkout"})
	if cmd, args, _ := f.last(t); cmd != "save" || args["name"] != "checkout" || args["path"] != "" {
		t.Errorf("save_flow: cmd %q args %v", cmd, args)
	}
	mcpCall(t.Context(), "go", map[string]any{"device": "alice", "name": "checkout"})
	if cmd, args, _ := f.last(t); cmd != "go" || args["name"] != "checkout" || args["device"] != "alice" {
		t.Errorf("go: cmd %q args %v", cmd, args)
	}
	names := map[string]bool{}
	for _, tool := range mcpTools {
		names[tool.Name] = true
	}
	if !names["go"] {
		t.Error("tools/list lacks go")
	}
}

func TestHelpNamesPaths(t *testing.T) {
	for name, text := range map[string]string{"usage": usage, "agent guide": agentGuide} {
		if !strings.Contains(text, "tb save --path") || !strings.Contains(text, "tb go ") {
			t.Errorf("%s does not say how to save and run a path", name)
		}
	}
}

func TestDocsSayWhatAPathDoes(t *testing.T) {
	root := filepath.Join("..", "..")
	texts := map[string]string{"agent guide": agentGuide}
	for _, f := range []string{".agents/skills/tb/SKILL.md", "plugin/skills/tb/SKILL.md", ".agents/skills/tb/references/cli.md"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		texts[f] = strings.Join(strings.Fields(string(b)), " ")
	}
	for name, text := range texts {
		for _, want := range []string{"restarts the app", "edit the failing step"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q", name, want)
			}
		}
		if strings.Contains(text, "save it again") || strings.Contains(text, "save the path again") {
			t.Errorf("%s still advises saving a failed path again", name)
		}
	}
	for _, f := range []string{".agents/skills/tb/SKILL.md", ".agents/skills/tb/references/cli.md"} {
		for _, want := range []string{"session's app", "depends on"} {
			if !strings.Contains(texts[f], want) {
				t.Errorf("%s does not say %q", f, want)
			}
		}
	}
}
