package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestSkillWriteAGoodFlowInSync(t *testing.T) {
	root := filepath.Join("..", "..")
	block := func(path string) string {
		b, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		i := strings.Index(s, "<!-- shared: write-a-good-flow")
		j := strings.Index(s, "<!-- /shared -->")
		if i < 0 || j < i {
			t.Fatalf("%s lacks the shared write-a-good-flow block", path)
		}
		return s[i:j]
	}
	if a, p := block(".agents/skills/tb/SKILL.md"), block("plugin/skills/tb/SKILL.md"); a != p {
		t.Errorf("the write-a-good-flow blocks of the two SKILL.md copies differ")
	}
}
