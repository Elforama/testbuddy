package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// inDir runs fn with the working directory set to dir.
func inDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	fn()
}

func writeFile(t *testing.T, path, src string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

const badPage = "<button><svg></svg></button>\n<button>Save</button>\n"

// Nothing wrong: one "ok" line, exit 0.
func TestA11yOK(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.html"), "<button>Save</button>")
	var code int
	out, _ := capture(t, func() { code = cmdA11y([]string{dir}) })
	if code != 0 || out != "ok: 1 file checked, every element tb can detect and tap\n" {
		t.Errorf("code %d, out %q", code, out)
	}
}

// Problems: grouped by file, one element per line in tb look's notation, exit 1.
func TestA11yListsElements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.html"), badPage)
	var code int
	out, _ := capture(t, func() { code = cmdA11y([]string{dir}) })
	if code != 1 {
		t.Errorf("code %d", code)
	}
	want := filepath.Join(dir, "a.html") + "\n  :1 button ✗ web-no-selector\n\n✗ web-no-selector: no text and no id"
	if !strings.HasPrefix(out, want) {
		t.Errorf("output does not start with %q:\n%s", want, out)
	}
	for _, want := range []string{". Fix: add aria-label=", "\n✗ 1 element tb cannot detect or tap (1 file checked)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// --fail-on none reports but passes.
	capture(t, func() { code = cmdA11y([]string{dir, "--fail-on", "none"}) })
	if code != 0 {
		t.Errorf("--fail-on none: code %d", code)
	}
}

func TestA11yFormats(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.html"), badPage)
	out, _ := capture(t, func() { cmdA11y([]string{dir, "--json"}) })
	var res struct {
		Files, Errors int
		Findings      map[string][]struct {
			Line          int
			Element, Rule string
		}
		Rules map[string]struct{ Severity, Problem, Fix string }
	}
	err := json.Unmarshal([]byte(out), &res)
	items := res.Findings[filepath.Join(dir, "a.html")]
	if err != nil || res.Files != 1 || res.Errors != 1 || len(items) != 1 || items[0].Element != "button" ||
		items[0].Rule != "web-no-selector" || items[0].Line != 1 || len(res.Rules) != 1 || res.Rules["web-no-selector"].Fix == "" {
		t.Errorf("json %q: %+v %v", out, res, err)
	}
	out, _ = capture(t, func() { cmdA11y([]string{dir, "--format", "github"}) })
	if !strings.HasPrefix(out, "::error file=") || !strings.Contains(out, ",line=1,title=tb a11y (web-no-selector)::button: ") {
		t.Errorf("github: %q", out)
	}
	var code int
	capture(t, func() { code = cmdA11y([]string{dir, "--format", "xml"}) })
	if code != 2 {
		t.Errorf("bad format: code %d", code)
	}
	out, _ = capture(t, func() { code = cmdA11y([]string{"--rules"}) })
	if code != 0 || !strings.Contains(out, "compose-testtag-hidden") {
		t.Errorf("--rules: %d %q", code, out)
	}
}

// --changed reports only lines added since the base, plus untracked files.
func TestA11yChanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeFile(t, filepath.Join(dir, "web", "old.html"), "<button><svg></svg></button>\n")
	run("add", ".")
	run("commit", "-q", "-m", "base")
	run("checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(dir, "web", "old.html"), "<button><svg></svg></button>\n<a href=x><img></a>\n")
	run("commit", "-qam", "edit")
	writeFile(t, filepath.Join(dir, "web", "new.html"), "<button><i></i></button>\n")
	writeFile(t, filepath.Join(dir, "node_modules", "x.html"), "<button></button>\n")

	var code int
	var out string
	inDir(t, dir, func() { out, _ = capture(t, func() { code = cmdA11y([]string{"--changed"}) }) })
	if code != 1 {
		t.Errorf("code %d", code)
	}
	// old.html:1 predates the branch (old.html's first listed line is :2); old.html:2 and
	// new.html:1 are new.
	for _, want := range []string{filepath.Join("web", "new.html") + "\n  :1 button ✗", filepath.Join("web", "old.html") + "\n  :2 a ✗", "(2 files checked)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "node_modules") {
		t.Errorf("checked node_modules:\n%s", out)
	}
	// Limited to a path that changed nothing.
	writeFile(t, filepath.Join(dir, "other", "keep.txt"), "")
	inDir(t, dir, func() { out, _ = capture(t, func() { code = cmdA11y([]string{"--changed", "other"}) }) })
	if code != 0 || !strings.HasPrefix(out, "ok: 0 files checked") {
		t.Errorf("code %d, out %q", code, out)
	}
}

// --fix adds the missing selectors, says what it added and what it left, then reports what
// is left (here nothing an exit status counts), and exits from that.
func TestA11yFix(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "a.html")
	writeFile(t, page, "<button onclick=\"save()\"><svg></svg></button>\n<button>Save</button>\n")
	writeFile(t, filepath.Join(dir, "S.swift"), "Image(\"a\").accessibilityHidden(true)\n")
	var code int
	out, _ := capture(t, func() { code = cmdA11y([]string{dir, "--fix"}) })
	if code != 0 {
		t.Errorf("code %d:\n%s", code, out)
	}
	for _, want := range []string{
		"fixed " + page + ":1 button: added data-testid=\"save-button\"\n",
		"left " + filepath.Join(dir, "S.swift") + ":1 view (ios-hidden): a likely problem",
		"\n! 1 to check (2 files checked)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if b, _ := os.ReadFile(page); !strings.HasPrefix(string(b), `<button data-testid="save-button" onclick=`) {
		t.Errorf("page: %s", b)
	}
}

// --dry-run says what --fix would add and changes nothing; the report is the check's.
func TestA11yFixDryRun(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "a.html")
	writeFile(t, page, badPage)
	var code int
	out, _ := capture(t, func() { code = cmdA11y([]string{dir, "--fix", "--dry-run"}) })
	if code != 1 || !strings.HasPrefix(out, "would fix "+page+":1 button: add data-testid=\"button\"\n") ||
		!strings.Contains(out, "✗ 1 element tb cannot detect or tap") {
		t.Errorf("code %d:\n%s", code, out)
	}
	if b, _ := os.ReadFile(page); string(b) != badPage {
		t.Errorf("a dry run changed the page: %s", b)
	}
}

func TestA11yFixJSON(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "a.html")
	writeFile(t, page, badPage)
	out, _ := capture(t, func() { cmdA11y([]string{dir, "--fix", "--json"}) })
	var res struct {
		Errors int
		DryRun bool `json:"dry_run"`
		Fixed  []struct {
			File, Element, Rule, Added string
			Line                       int
		}
		Left []any
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Errors != 0 || res.DryRun || len(res.Fixed) != 1 ||
		res.Fixed[0].File != page || res.Fixed[0].Line != 1 || res.Fixed[0].Added != `data-testid="button"` ||
		res.Fixed[0].Element != "button" || res.Fixed[0].Rule != "web-no-selector" || res.Left == nil {
		t.Errorf("json %s: %+v", out, res)
	}
	// Without --fix there is no fixed list.
	out, _ = capture(t, func() { cmdA11y([]string{dir, "--json"}) })
	if strings.Contains(out, `"fixed"`) {
		t.Errorf("json without --fix: %s", out)
	}
}

func TestA11yFixUsage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.html"), badPage)
	for _, args := range [][]string{{dir, "--dry-run"}, {dir, "--fix", "--format", "github"}} {
		var code int
		_, errOut := capture(t, func() { code = cmdA11y(args) })
		if code != 2 || errOut == "" {
			t.Errorf("%v: code %d, stderr %q", args, code, errOut)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.html")); string(b) != badPage {
		t.Errorf("a usage error changed the page: %s", b)
	}
}
