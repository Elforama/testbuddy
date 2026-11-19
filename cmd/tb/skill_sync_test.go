package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The skill's shared references live in .agents/skills/tb/references; scripts/build-plugin.sh
// copies them into the Claude Code plugin. The two copies must not drift apart.
func TestSkillReferencesInSync(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"cli.md", "flows.md", "multi-device.md", "gotchas.md", "debugging.md"} {
		src, err := os.ReadFile(filepath.Join(root, ".agents", "skills", "tb", "references", name))
		if err != nil {
			t.Fatal(err)
		}
		dst, err := os.ReadFile(filepath.Join(root, "plugin", "skills", "tb", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(src, dst) {
			t.Errorf("plugin/skills/tb/%s differs from .agents/skills/tb/references/%s: edit the .agents copy, then run scripts/build-plugin.sh (or copy it)", name, name)
		}
	}
}
