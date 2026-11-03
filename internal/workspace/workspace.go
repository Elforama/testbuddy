// Package workspace finds the flows to run from the paths given to `tb test`, following
// Maestro's workspace rules (config.yaml flows globs, include/exclude tags,
// executionOrder).
package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/legacy"
)

// Config is a workspace config.yaml.
type Config struct {
	Flows          []string
	IncludeTags    []string
	ExcludeTags    []string
	FlowsOrder     []string
	ContinueOnFail bool
	TestOutputDir  string
	Path           string
}

var workspaceKeys = map[string]bool{"flows": true, "includeTags": true, "excludeTags": true, "executionOrder": true,
	"baselineBranch": true, "notifications": true, "disableRetries": true, "platform": true, "testOutputDir": true, "local": true}

func stringList(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		if n.Value == "" {
			return nil
		}
		var out []string
		for _, s := range strings.Split(n.Value, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	var out []string
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

// LoadConfig reads a workspace config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{Flows: []string{"*"}, ContinueOnFail: true, Path: path}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return c, nil
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i].Value, m.Content[i+1]
		switch k {
		case "flows":
			if l := stringList(v); len(l) > 0 {
				c.Flows = l
			}
		case "includeTags":
			c.IncludeTags = stringList(v)
		case "excludeTags":
			c.ExcludeTags = stringList(v)
		case "testOutputDir":
			c.TestOutputDir = v.Value
		case "executionOrder":
			for j := 0; j+1 < len(v.Content); j += 2 {
				switch v.Content[j].Value {
				case "flowsOrder":
					c.FlowsOrder = stringList(v.Content[j+1])
				case "continueOnFailure":
					// any YAML boolean spelling (false, False, FALSE); other values keep the default
					var b bool
					if err := v.Content[j+1].Decode(&b); err == nil {
						c.ContinueOnFail = b
					}
				}
			}
		default:
			if !workspaceKeys[k] {
				fmt.Fprintf(os.Stderr, "warning: %s: unknown workspace key %q\n", path, k)
			}
		}
	}
	return c, nil
}

// Plan is what to run.
type Plan struct {
	Flows          []*flow.Flow // in run order: ordered flows first
	Sequential     int          // the first Sequential flows run in order, one at a time
	ContinueOnFail bool
	Config         *Config
}

// Options are the CLI's selection options.
type Options struct {
	ConfigPath  string
	IncludeTags []string
	ExcludeTags []string
}

// Build turns `tb test` paths into a plan.
func Build(paths []string, o Options) (*Plan, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("give a flow file or a folder of flows")
	}
	var cfg *Config
	if o.ConfigPath != "" {
		c, err := LoadConfig(o.ConfigPath)
		if err != nil {
			return nil, err
		}
		cfg = c
	}
	// A single file runs as is: no globs, no tag filters.
	if len(paths) == 1 {
		if st, err := os.Stat(paths[0]); err == nil && !st.IsDir() {
			f, err := flow.ParseFile(paths[0])
			if err != nil {
				return nil, err
			}
			return &Plan{Flows: []*flow.Flow{f}, Config: cfg, ContinueOnFail: true}, nil
		}
	}
	var files []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			files = append(files, p)
			continue
		}
		if cfg == nil {
			for _, name := range []string{"config.yaml", "config.yml"} {
				cp := filepath.Join(p, name)
				if _, err := os.Stat(cp); err == nil {
					c, err := LoadConfig(cp)
					if err != nil {
						return nil, err
					}
					cfg = c
					break
				}
			}
		}
		globs := []string{"*"}
		if cfg != nil {
			globs = cfg.Flows
		}
		found, err := glob(p, globs, cfg)
		if err != nil {
			return nil, err
		}
		files = append(files, found...)
	}
	include, exclude := o.IncludeTags, o.ExcludeTags
	if cfg != nil {
		include = append(include, cfg.IncludeTags...)
		exclude = append(exclude, cfg.ExcludeTags...)
	}
	var flows []*flow.Flow
	seen := map[string]bool{}
	for _, file := range files {
		abs, _ := filepath.Abs(file)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		f, err := flow.ParseFile(file)
		if err != nil {
			return nil, err
		}
		if !tagsPass(f.Config.Tags, include, exclude) {
			continue
		}
		flows = append(flows, f)
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("no flows to run in %s", strings.Join(paths, ", "))
	}
	plan := &Plan{Flows: flows, Config: cfg, ContinueOnFail: true}
	if cfg != nil && len(cfg.FlowsOrder) > 0 {
		plan.ContinueOnFail = cfg.ContinueOnFail
		byName := map[string]*flow.Flow{}
		for _, f := range flows {
			byName[f.Name()] = f
		}
		var ordered []*flow.Flow
		for _, name := range cfg.FlowsOrder {
			f, ok := byName[name]
			if !ok {
				if len(ordered) == 0 {
					return nil, fmt.Errorf("could not find flow %q needed for executionOrder", name)
				}
				break
			}
			ordered = append(ordered, f)
		}
		used := map[*flow.Flow]bool{}
		for _, f := range ordered {
			used[f] = true
		}
		rest := ordered
		for _, f := range flows {
			if !used[f] {
				rest = append(rest, f)
			}
		}
		plan.Flows = rest
		plan.Sequential = len(ordered)
	}
	return plan, nil
}

func tagsPass(tags, include, exclude []string) bool {
	has := func(list []string) bool {
		for _, t := range tags {
			for _, w := range list {
				if t == w {
					return true
				}
			}
		}
		return false
	}
	if len(include) > 0 && !has(include) {
		return false
	}
	return !has(exclude)
}

// glob matches the workspace globs relative to dir ("*" does not cross folders, "**" does,
// "!pattern" excludes).
func glob(dir string, patterns []string, cfg *Config) ([]string, error) {
	var inc, exc []string
	for _, p := range patterns {
		if strings.HasPrefix(p, "!") {
			exc = append(exc, p[1:])
		} else {
			inc = append(inc, p)
		}
	}
	if len(inc) == 0 {
		return nil, fmt.Errorf("the workspace flows list has only exclusions")
	}
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Hidden folders are skipped, but for the flows folders (.tb, and Maestro's .maestro).
			if path != dir && strings.HasPrefix(d.Name(), ".") && d.Name() != ".tb" && d.Name() != legacy.FlowsFolder() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		ok := false
		for _, p := range inc {
			if globMatch(p, rel) {
				ok = true
				break
			}
		}
		for _, p := range exc {
			if globMatch(p, rel) {
				ok = false
			}
		}
		if ok && isFlowFile(path, cfg) {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// globMatch: "*" matches within one path segment, "**" across segments.
func globMatch(pattern, path string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, _ := filepath.Match(pat[0], segs[0]); !ok {
		// "*" alone at the last level also matches files ("subdir/*")
		return false
	}
	return matchSegs(pat[1:], segs[1:])
}

func isFlowFile(path string, cfg *Config) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".yaml" && ext != ".yml" {
		return false
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if base == "config" {
		return false
	}
	if cfg != nil {
		if a, _ := filepath.Abs(cfg.Path); a != "" {
			if b, _ := filepath.Abs(path); a == b {
				return false
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if !bytes.Contains(data, []byte("\n---")) && !bytes.HasPrefix(data, []byte("---")) {
		// No command section: a workspace config or some other YAML.
		return false
	}
	return true
}
