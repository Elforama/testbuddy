package workspace

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/flow"
)

func flowSrc(name string, tags ...string) string {
	var b strings.Builder
	b.WriteString("appId: com.example\n")
	if name != "" {
		b.WriteString("name: " + name + "\n")
	}
	if len(tags) > 0 {
		b.WriteString("tags: [" + strings.Join(tags, ", ") + "]\n")
	}
	b.WriteString("---\n- back\n")
	return b.String()
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rels(t *testing.T, dir string, p *Plan) []string {
	t.Helper()
	var out []string
	for _, f := range p.Flows {
		r, err := filepath.Rel(dir, f.Path)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func flowNames(p *Plan) []string {
	var out []string
	for _, f := range p.Flows {
		out = append(out, f.Name())
	}
	return out
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func mustBuild(t *testing.T, paths []string, o Options) *Plan {
	t.Helper()
	p, err := Build(paths, o)
	if err != nil {
		t.Fatalf("Build(%v): %v", paths, err)
	}
	return p
}

func writeConfig(t *testing.T, src string) string {
	t.Helper()
	dir := writeTree(t, map[string]string{"config.yaml": src})
	return filepath.Join(dir, "config.yaml")
}

func TestLoadConfigDefaults(t *testing.T) {
	for _, src := range []string{"", "# only a comment\n", "- a\n- b\n", "just text\n"} {
		path := writeConfig(t, src)
		c, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		want := &Config{Flows: []string{"*"}, ContinueOnFail: true, Path: path}
		if !reflect.DeepEqual(c, want) {
			t.Errorf("%q: got %+v", src, c)
		}
	}
}

func TestLoadConfigAllKeys(t *testing.T) {
	path := writeConfig(t, `flows:
  - "*"
  - "auth/**"
  - "!wip/*"
includeTags: [smoke, fast]
excludeTags:
  - slow
testOutputDir: out/reports
executionOrder:
  continueOnFailure: false
  flowsOrder:
    - login
    - checkout
`)
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		Flows:          []string{"*", "auth/**", "!wip/*"},
		IncludeTags:    []string{"smoke", "fast"},
		ExcludeTags:    []string{"slow"},
		FlowsOrder:     []string{"login", "checkout"},
		ContinueOnFail: false,
		TestOutputDir:  "out/reports",
		Path:           path,
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("got  %+v\nwant %+v", c, want)
	}
}

func TestLoadConfigCommaLists(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, `flows: "a/*, b/**"
includeTags: "smoke,  fast ,,"
excludeTags: slow
executionOrder:
  flowsOrder: "login, checkout"
`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Flows, []string{"a/*", "b/**"}) || !reflect.DeepEqual(c.IncludeTags, []string{"smoke", "fast"}) ||
		!reflect.DeepEqual(c.ExcludeTags, []string{"slow"}) || !reflect.DeepEqual(c.FlowsOrder, []string{"login", "checkout"}) {
		t.Errorf("%+v", c)
	}
}

func TestLoadConfigEmptyFlowsKeepsDefault(t *testing.T) {
	for _, src := range []string{"flows: []\n", "flows: \"\"\n", "flows:\n", "flows: \" , \"\n"} {
		c, err := LoadConfig(writeConfig(t, src))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.Flows, []string{"*"}) {
			t.Errorf("%q: %v", src, c.Flows)
		}
	}
}

func TestLoadConfigContinueOnFailure(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want bool
	}{
		{"executionOrder:\n  continueOnFailure: false\n", false},
		{"executionOrder:\n  continueOnFailure: true\n", true},
		{"executionOrder:\n  flowsOrder: [a]\n", true},
		{"executionOrder: {}\n", true},
		{"flows: ['*']\n", true},
	} {
		c, err := LoadConfig(writeConfig(t, tc.src))
		if err != nil {
			t.Fatal(err)
		}
		if c.ContinueOnFail != tc.want {
			t.Errorf("%q: ContinueOnFail %v, want %v", tc.src, c.ContinueOnFail, tc.want)
		}
	}
}

func TestLoadConfigContinueOnFailureYAMLBools(t *testing.T) {
	for _, v := range []string{"False", "FALSE"} {
		c, err := LoadConfig(writeConfig(t, "executionOrder:\n  continueOnFailure: "+v+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if c.ContinueOnFail {
			t.Errorf("%s: ContinueOnFail true", v)
		}
	}
}

func TestLoadConfigUnknownKeyWarns(t *testing.T) {
	path := writeConfig(t, "flows: ['*']\nbogusKey: 1\nflow: x\n")
	var c *Config
	var err error
	out := captureStderr(t, func() { c, err = LoadConfig(path) })
	if err != nil || c == nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bogusKey", "flow"} {
		want := "warning: " + path + `: unknown workspace key "` + k + `"`
		if !strings.Contains(out, want) {
			t.Errorf("stderr %q, want %q", out, want)
		}
	}

	known := writeConfig(t, "baselineBranch: main\nnotifications: {email: {enabled: true}}\ndisableRetries: true\nplatform: {ios: {}}\nlocal: {deterministicOrder: true}\n")
	if out := captureStderr(t, func() { _, err = LoadConfig(known) }); out != "" || err != nil {
		t.Errorf("Maestro keys tb ignores are silent: %q %v", out, err)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	path := writeConfig(t, "flows: [\n")
	_, err = LoadConfig(path)
	if err == nil || !strings.HasPrefix(err.Error(), path+": ") {
		t.Errorf("syntax error names the file: %v", err)
	}
}

func TestBuildNoPaths(t *testing.T) {
	if _, err := Build(nil, Options{}); err == nil || !strings.Contains(err.Error(), "give a flow file or a folder") {
		t.Errorf("%v", err)
	}
}

func TestBuildSingleFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"slow.yaml":   flowSrc("", "slow"),
		"config.yaml": "excludeTags: [slow]\n",
	})
	p := mustBuild(t, []string{filepath.Join(dir, "slow.yaml")}, Options{ExcludeTags: []string{"slow"}, IncludeTags: []string{"smoke"}})
	if len(p.Flows) != 1 || p.Flows[0].Name() != "slow" || p.Sequential != 0 || !p.ContinueOnFail || p.Config != nil {
		t.Errorf("%+v", p)
	}

	p = mustBuild(t, []string{filepath.Join(dir, "slow.yaml")}, Options{ConfigPath: filepath.Join(dir, "config.yaml")})
	if len(p.Flows) != 1 || p.Config == nil || !reflect.DeepEqual(p.Config.ExcludeTags, []string{"slow"}) {
		t.Errorf("with config: %+v", p)
	}
}

func TestBuildSingleFileErrors(t *testing.T) {
	dir := writeTree(t, map[string]string{"bad.yaml": "appId: a\n---\n- tapOnn: X\n", "nocmds.yaml": "appId: a\n"})
	_, err := Build([]string{filepath.Join(dir, "bad.yaml")}, Options{})
	var fe *flow.Error
	if !errors.As(err, &fe) || fe.Line != 3 {
		t.Errorf("parse error: %v", err)
	}
	if _, err := Build([]string{filepath.Join(dir, "nocmds.yaml")}, Options{}); err == nil || !strings.Contains(err.Error(), "Commands Section Required") {
		t.Errorf("no commands: %v", err)
	}
	if _, err := Build([]string{filepath.Join(dir, "missing.yaml")}, Options{}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	if _, err := Build([]string{filepath.Join(dir, "bad.yaml")}, Options{ConfigPath: filepath.Join(dir, "nope.yaml")}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing config: %v", err)
	}
}

func TestBuildFolderDefaults(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"c.yaml":         flowSrc(""),
		"a.yaml":         flowSrc(""),
		"b.yml":          flowSrc(""),
		"D.YAML":         flowSrc(""),
		"notes.txt":      flowSrc(""),
		"data.json":      "{}",
		"sub/deep.yaml":  flowSrc(""),
		"settings.yaml":  "key: value\n",
		"README.md":      "# hi\n",
		"script.js":      "1",
		"start.yaml":     "---\nappId: s\n---\n- back\n",
		"template.yaml.": flowSrc(""),
	})
	p := mustBuild(t, []string{dir}, Options{})
	if got, want := rels(t, dir, p), []string{"D.YAML", "a.yaml", "b.yml", "c.yaml", "start.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if p.Config != nil || p.Sequential != 0 || !p.ContinueOnFail {
		t.Errorf("%+v", p)
	}
}

func TestBuildFolderConfigDiscovery(t *testing.T) {
	for _, name := range []string{"config.yaml", "config.yml"} {
		t.Run(name, func(t *testing.T) {
			dir := writeTree(t, map[string]string{
				name:            "flows: ['**']\n",
				"top.yaml":      flowSrc(""),
				"sub/deep.yaml": flowSrc(""),
			})
			p := mustBuild(t, []string{dir}, Options{})
			if p.Config == nil || p.Config.Path != filepath.Join(dir, name) {
				t.Fatalf("config: %+v", p.Config)
			}
			if got := rels(t, dir, p); !reflect.DeepEqual(got, []string{"sub/deep.yaml", "top.yaml"}) {
				t.Errorf("%v", got)
			}
		})
	}
	t.Run("config.yaml before config.yml", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"config.yaml": "flows: ['a*']\n", "config.yml": "flows: ['b*']\n", "a.yaml": flowSrc(""), "b.yaml": flowSrc("")})
		if got := rels(t, dir, mustBuild(t, []string{dir}, Options{})); !reflect.DeepEqual(got, []string{"a.yaml"}) {
			t.Errorf("%v", got)
		}
	})
	t.Run("explicit config wins", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"config.yaml": "flows: ['a*']\n", "a.yaml": flowSrc(""), "b.yaml": flowSrc(""), "other/ws.yaml": "flows: ['b*']\n"})
		p := mustBuild(t, []string{dir}, Options{ConfigPath: filepath.Join(dir, "other", "ws.yaml")})
		if got := rels(t, dir, p); !reflect.DeepEqual(got, []string{"b.yaml"}) {
			t.Errorf("%v", got)
		}
	})
	t.Run("bad folder config", func(t *testing.T) {
		dir := writeTree(t, map[string]string{"config.yaml": "flows: [\n", "a.yaml": flowSrc("")})
		if _, err := Build([]string{dir}, Options{}); err == nil || !strings.Contains(err.Error(), "config.yaml") {
			t.Errorf("%v", err)
		}
	})
}

func TestBuildNoFlows(t *testing.T) {
	dir := writeTree(t, map[string]string{"notes.txt": "x", "other.yaml": "foo: bar\n"})
	_, err := Build([]string{dir}, Options{})
	if err == nil || err.Error() != "no flows to run in "+dir {
		t.Errorf("%v", err)
	}
	empty := t.TempDir()
	if _, err := Build([]string{dir, empty}, Options{}); err == nil || !strings.Contains(err.Error(), dir+", "+empty) {
		t.Errorf("lists every path: %v", err)
	}
}

func TestBuildFolderParseError(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.yaml": flowSrc(""), "b.yaml": "appId: a\n---\n- frob\n"})
	_, err := Build([]string{dir}, Options{})
	var fe *flow.Error
	if !errors.As(err, &fe) || fe.File != filepath.Join(dir, "b.yaml") {
		t.Errorf("%v", err)
	}
}

func TestBuildMultiplePaths(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.yaml": flowSrc(""), "b.yaml": flowSrc(""), "extra/x.yaml": flowSrc("")})
	x := filepath.Join(dir, "extra", "x.yaml")
	p := mustBuild(t, []string{x, dir, filepath.Join(dir, "a.yaml"), x}, Options{})
	if got := rels(t, dir, p); !reflect.DeepEqual(got, []string{"extra/x.yaml", "a.yaml", "b.yaml"}) {
		t.Errorf("%v", got)
	}
	if _, err := Build([]string{dir, filepath.Join(dir, "nope")}, Options{}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing path: %v", err)
	}
}

func TestBuildDedupesRelativeAndAbsolute(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.yaml": flowSrc(""), "b.yaml": flowSrc("")})
	t.Chdir(dir)
	p := mustBuild(t, []string{"a.yaml", filepath.Join(dir, "a.yaml"), "b.yaml"}, Options{})
	if len(p.Flows) != 2 {
		t.Errorf("%v", flowNames(p))
	}
}

func TestBuildGlobs(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.yaml":          `flows: ["*", "auth/**", "wip/*", "!wip/draft*", "!auth/legacy/*"]` + "\n",
		"home.yaml":            flowSrc(""),
		"auth/login.yaml":      flowSrc(""),
		"auth/sso/okta.yaml":   flowSrc(""),
		"auth/legacy/old.yaml": flowSrc(""),
		"wip/draft.yaml":       flowSrc(""),
		"wip/ready.yaml":       flowSrc(""),
		"other/skip.yaml":      flowSrc(""),
	})
	got := rels(t, dir, mustBuild(t, []string{dir}, Options{}))
	if want := []string{"auth/login.yaml", "auth/sso/okta.yaml", "home.yaml", "wip/ready.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildGlobsExcludeTopLevel(t *testing.T) {
	dir := writeTree(t, map[string]string{"config.yaml": "flows: ['**', '!*_wip.yaml']\n", "a.yaml": flowSrc(""), "b_wip.yaml": flowSrc(""), "s/c_wip.yaml": flowSrc("")})
	if got := rels(t, dir, mustBuild(t, []string{dir}, Options{})); !reflect.DeepEqual(got, []string{"a.yaml", "s/c_wip.yaml"}) {
		t.Errorf("%v", got)
	}
}

func TestBuildOnlyExclusions(t *testing.T) {
	dir := writeTree(t, map[string]string{"config.yaml": "flows: ['!wip/*']\n", "a.yaml": flowSrc("")})
	if _, err := Build([]string{dir}, Options{}); err == nil || !strings.Contains(err.Error(), "only exclusions") {
		t.Errorf("%v", err)
	}
}

func TestBuildHiddenFolders(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.yaml":         "flows: ['**']\n",
		"a.yaml":              flowSrc(""),
		".tb/tb.yaml":         flowSrc(""),
		".maestro/m.yaml":     flowSrc(""),
		".maestro/sub/s.yaml": flowSrc(""),
		".git/g.yaml":         flowSrc(""),
		".hidden/h.yaml":      flowSrc(""),
		"x/.cache/c.yaml":     flowSrc(""),
	})
	got := rels(t, dir, mustBuild(t, []string{dir}, Options{}))
	if want := []string{".maestro/m.yaml", ".maestro/sub/s.yaml", ".tb/tb.yaml", "a.yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildHiddenFolderGivenDirectly(t *testing.T) {
	root := writeTree(t, map[string]string{".hidden/a.yaml": flowSrc(""), ".maestro/b.yaml": flowSrc("")})
	for _, name := range []string{".hidden", ".maestro"} {
		dir := filepath.Join(root, name)
		if p := mustBuild(t, []string{dir}, Options{}); len(p.Flows) != 1 {
			t.Errorf("%s: %v", name, flowNames(p))
		}
	}
}

func TestBuildSkipsConfigFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.yaml":    "flows: ['**']\n---\n- back\n",
		"sub/config.yml": "flows: ['*']\n---\n- back\n",
		"a.yaml":         flowSrc(""),
		"sub/b.yaml":     flowSrc(""),
	})
	p := mustBuild(t, []string{dir}, Options{})
	if got := rels(t, dir, p); !reflect.DeepEqual(got, []string{"a.yaml", "sub/b.yaml"}) {
		t.Errorf("config.yaml files: %v", got)
	}
	tools := writeTree(t, map[string]string{"ws.yaml": "flows: ['**']\n---\n- back\n", "real.yaml": flowSrc("")})
	p = mustBuild(t, []string{tools}, Options{ConfigPath: filepath.Join(tools, "ws.yaml")})
	if got := rels(t, tools, p); !reflect.DeepEqual(got, []string{"real.yaml"}) {
		t.Errorf("explicit config: %v", got)
	}
}

func TestBuildTags(t *testing.T) {
	files := map[string]string{
		"smoke.yaml":      flowSrc("", "smoke"),
		"smoke_slow.yaml": flowSrc("", "smoke", "slow"),
		"slow.yaml":       flowSrc("", "slow"),
		"untagged.yaml":   flowSrc(""),
		"pay.yaml":        flowSrc("", "payments"),
	}
	for _, tc := range []struct {
		name   string
		config string
		o      Options
		want   []string
	}{
		{"no filters", "", Options{}, []string{"pay", "slow", "smoke", "smoke_slow", "untagged"}},
		{"include option", "", Options{IncludeTags: []string{"smoke"}}, []string{"smoke", "smoke_slow"}},
		{"include any of several", "", Options{IncludeTags: []string{"smoke", "payments"}}, []string{"pay", "smoke", "smoke_slow"}},
		{"exclude option", "", Options{ExcludeTags: []string{"slow"}}, []string{"pay", "smoke", "untagged"}},
		{"include and exclude", "", Options{IncludeTags: []string{"smoke"}, ExcludeTags: []string{"slow"}}, []string{"smoke"}},
		{"config include", "includeTags: [payments]\n", Options{}, []string{"pay"}},
		{"config exclude", "excludeTags: slow, payments\n", Options{}, []string{"smoke", "untagged"}},
		{"option and config combine", "includeTags: [payments]\n", Options{IncludeTags: []string{"smoke"}, ExcludeTags: []string{"slow"}}, []string{"pay", "smoke"}},
		{"tags are case-sensitive", "", Options{IncludeTags: []string{"SMOKE"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := map[string]string{}
			for k, v := range files {
				tree[k] = v
			}
			if tc.config != "" {
				tree["config.yaml"] = tc.config
			}
			dir := writeTree(t, tree)
			p, err := Build([]string{dir}, tc.o)
			if tc.want == nil {
				if err == nil || !strings.Contains(err.Error(), "no flows to run") {
					t.Errorf("want no flows, got %v %v", p, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := flowNames(p); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestBuildTagsOnExplicitFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{"a.yaml": flowSrc("", "slow"), "b.yaml": flowSrc("")})
	p := mustBuild(t, []string{filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")}, Options{ExcludeTags: []string{"slow"}})
	if got := flowNames(p); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("%v", got)
	}
}

func TestTagsPass(t *testing.T) {
	for _, tc := range []struct {
		tags, include, exclude []string
		want                   bool
	}{
		{nil, nil, nil, true},
		{[]string{"a"}, nil, nil, true},
		{nil, []string{"a"}, nil, false},
		{[]string{"a", "b"}, []string{"b"}, nil, true},
		{[]string{"a"}, []string{"b"}, nil, false},
		{[]string{"a"}, nil, []string{"a"}, false},
		{[]string{"a", "b"}, []string{"a"}, []string{"b"}, false},
		{nil, nil, []string{"a"}, true},
	} {
		if got := tagsPass(tc.tags, tc.include, tc.exclude); got != tc.want {
			t.Errorf("tagsPass(%v, %v, %v) = %v", tc.tags, tc.include, tc.exclude, got)
		}
	}
}

func TestBuildFlowsOrder(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"config.yaml": "executionOrder:\n  continueOnFailure: false\n  flowsOrder: [login, Pay now]\n",
		"a.yaml":      flowSrc(""),
		"login.yaml":  flowSrc(""),
		"pay.yaml":    flowSrc("Pay now"),
		"z.yaml":      flowSrc(""),
	})
	p := mustBuild(t, []string{dir}, Options{})
	if got := flowNames(p); !reflect.DeepEqual(got, []string{"login", "Pay now", "a", "z"}) {
		t.Errorf("order: %v", got)
	}
	if p.Sequential != 2 || p.ContinueOnFail {
		t.Errorf("Sequential %d ContinueOnFail %v", p.Sequential, p.ContinueOnFail)
	}
}

func TestBuildFlowsOrderDefaultsToContinue(t *testing.T) {
	dir := writeTree(t, map[string]string{"config.yaml": "executionOrder:\n  flowsOrder: [b]\n", "a.yaml": flowSrc(""), "b.yaml": flowSrc("")})
	p := mustBuild(t, []string{dir}, Options{})
	if got := flowNames(p); !reflect.DeepEqual(got, []string{"b", "a"}) || p.Sequential != 1 || !p.ContinueOnFail {
		t.Errorf("%v %+v", got, p)
	}
}

func TestBuildContinueOnFailureNeedsFlowsOrder(t *testing.T) {
	dir := writeTree(t, map[string]string{"config.yaml": "executionOrder:\n  continueOnFailure: false\n", "a.yaml": flowSrc("")})
	p := mustBuild(t, []string{dir}, Options{})
	if !p.ContinueOnFail || p.Sequential != 0 || p.Config.ContinueOnFail {
		t.Errorf("%+v %+v", p, p.Config)
	}
}

func TestBuildFlowsOrderMissing(t *testing.T) {
	dir := writeTree(t, map[string]string{"config.yaml": "executionOrder:\n  flowsOrder: [ghost, a]\n", "a.yaml": flowSrc("")})
	if _, err := Build([]string{dir}, Options{}); err == nil || err.Error() != `could not find flow "ghost" needed for executionOrder` {
		t.Errorf("first missing: %v", err)
	}

	dir = writeTree(t, map[string]string{
		"config.yaml": "executionOrder:\n  flowsOrder: [c, ghost, a]\n",
		"a.yaml":      flowSrc(""), "b.yaml": flowSrc(""), "c.yaml": flowSrc(""),
	})
	p := mustBuild(t, []string{dir}, Options{})
	if got := flowNames(p); !reflect.DeepEqual(got, []string{"c", "a", "b"}) || p.Sequential != 1 {
		t.Errorf("later missing: %v Sequential %d", got, p.Sequential)
	}
}

func TestBuildFlowsOrderFilteredByTags(t *testing.T) {
	dir := writeTree(t, map[string]string{"config.yaml": "excludeTags: [slow]\nexecutionOrder:\n  flowsOrder: [a]\n", "a.yaml": flowSrc("", "slow"), "b.yaml": flowSrc("")})
	if _, err := Build([]string{dir}, Options{}); err == nil || !strings.Contains(err.Error(), `could not find flow "a"`) {
		t.Errorf("%v", err)
	}
}

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"*", "a.yaml", true},
		{"*", "sub/a.yaml", false},
		{"**", "a.yaml", true},
		{"**", "sub/deep/a.yaml", true},
		{"*.yaml", "a.yaml", true},
		{"*.yaml", "a.yml", false},
		{"a*", "abc.yaml", true},
		{"a*", "bc.yaml", false},
		{"[ab].yaml", "a.yaml", true},
		{"?.yaml", "ab.yaml", false},
		{"sub/*", "sub/a.yaml", true},
		{"sub/*", "sub/deep/a.yaml", false},
		{"sub/*", "other/a.yaml", false},
		{"sub/**", "sub/a.yaml", true},
		{"sub/**", "sub/deep/er/a.yaml", true},
		{"sub/**", "other/a.yaml", false},
		{"**/*.yaml", "a.yaml", true},
		{"**/*.yaml", "x/y/a.yaml", true},
		{"**/*.yml", "x/a.yaml", false},
		{"**/login.yaml", "x/login.yaml", true},
		{"**/login.yaml", "x/signup.yaml", false},
		{"x/**/z.yaml", "x/z.yaml", true},
		{"x/**/z.yaml", "x/a/b/z.yaml", true},
		{"x/**/z.yaml", "y/a/z.yaml", false},
		{"a.yaml", "a.yaml", true},
		{"a.yaml", "sub/a.yaml", false},
		{"sub", "sub/a.yaml", false},
	} {
		if got := globMatch(tc.pattern, tc.path); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestGlobWalkError(t *testing.T) {
	if _, err := glob(filepath.Join(t.TempDir(), "missing"), []string{"*"}, nil); err == nil {
		t.Error("want an error for a missing folder")
	}
}

func TestIsFlowFile(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"flow.yaml":       flowSrc(""),
		"flow.yml":        flowSrc(""),
		"UPPER.YAML":      flowSrc(""),
		"starts.yaml":     "---\n- back\n",
		"plain.yaml":      "appId: a\n",
		"dashes.yaml":     "appId: a ---\n",
		"config.yaml":     flowSrc(""),
		"config.yml":      flowSrc(""),
		"flow.json":       flowSrc(""),
		"flow":            flowSrc(""),
		"myconfig.yaml":   flowSrc(""),
		"ws/settings.yml": flowSrc(""),
	})
	cfg := &Config{Path: filepath.Join(dir, "ws", "settings.yml")}
	for _, tc := range []struct {
		name string
		cfg  *Config
		want bool
	}{
		{"flow.yaml", nil, true},
		{"flow.yml", nil, true},
		{"UPPER.YAML", nil, true},
		{"starts.yaml", nil, true},
		{"plain.yaml", nil, false},
		{"dashes.yaml", nil, false},
		{"config.yaml", nil, false},
		{"config.yml", nil, false},
		{"flow.json", nil, false},
		{"flow", nil, false},
		{"myconfig.yaml", nil, true},
		{"ws/settings.yml", nil, true},
		{"ws/settings.yml", cfg, false},
		{"flow.yaml", cfg, true},
		{"missing.yaml", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFlowFile(filepath.Join(dir, filepath.FromSlash(tc.name)), tc.cfg); got != tc.want {
				t.Errorf("isFlowFile = %v, want %v", got, tc.want)
			}
		})
	}
}
