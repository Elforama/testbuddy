package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeInit struct {
	env    initEnv
	ran    []string
	fail   map[string]string
	stderr bytes.Buffer
}

func newFakeInit(t *testing.T, installed ...string) *fakeInit {
	t.Helper()
	f := &fakeInit{fail: map[string]string{}}
	f.env = initEnv{
		home:    t.TempDir(),
		tb:      "/usr/local/bin/tb",
		tempDir: "/var/tmp-builds",
		lookPath: func(name string) (string, error) {
			for _, i := range installed {
				if i == name {
					return "/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		},
		run: func(name string, args ...string) (string, error) {
			f.ran = append(f.ran, name+" "+strings.Join(args, " "))
			if out, ok := f.fail[name]; ok {
				return out, errors.New("exit status 1")
			}
			return "", nil
		},
		doctor: fakeDoctorEnv(),
	}
	return f
}

func (f *fakeInit) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(f.env.home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fakeInit) init(dryRun bool) (string, int) {
	var b bytes.Buffer
	code := runInit(f.env, dryRun, &b, &f.stderr)
	return b.String(), code
}

func homeFiles(t *testing.T, home string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(home, func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestInitAddsToEachInstalledClient(t *testing.T) {
	f := newFakeInit(t, "claude", "codex", "gemini")
	out, code := f.init(false)
	wantLines(t, out,
		"added Claude Code: claude mcp add --scope user tb -- /usr/local/bin/tb mcp",
		"added Codex: codex mcp add tb -- /usr/local/bin/tb mcp",
		"added Gemini CLI: gemini mcp add --scope user tb /usr/local/bin/tb mcp")
	want := []string{
		"/bin/claude mcp add --scope user tb -- /usr/local/bin/tb mcp",
		"/bin/codex mcp add tb -- /usr/local/bin/tb mcp",
		"/bin/gemini mcp add --scope user tb /usr/local/bin/tb mcp",
	}
	if strings.Join(f.ran, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran:\n%s\nwant:\n%s", strings.Join(f.ran, "\n"), strings.Join(want, "\n"))
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestInitSkipsClientsNotInstalled(t *testing.T) {
	f := newFakeInit(t, "codex")
	out, code := f.init(false)
	wantLines(t, out,
		"skipped Claude Code: not installed (no claude on PATH)",
		"added Codex: codex mcp add tb -- /usr/local/bin/tb mcp",
		"skipped Gemini CLI: not installed (no gemini on PATH)")
	if len(f.ran) != 1 {
		t.Errorf("ran %v, want only codex", f.ran)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestInitSkipsClientsThatHaveTB(t *testing.T) {
	f := newFakeInit(t, "claude", "codex", "gemini")
	f.write(t, ".claude.json", `{"mcpServers":{"tb":{"command":"tb","args":["mcp"]}}}`)
	f.write(t, ".codex/config.toml", "model = \"x\"\n\n[mcp_servers.tb]\ncommand = \"tb\"\nargs = [\"mcp\"]\n")
	f.write(t, ".gemini/settings.json", `{"mcpServers":{"tb":{"command":"tb","args":["mcp"]}}}`)
	out, code := f.init(false)
	wantLines(t, out,
		"skipped Claude Code: already has tb (MCP server tb in ~/.claude.json)",
		"skipped Codex: already has tb (MCP server tb in ~/.codex/config.toml)",
		"skipped Gemini CLI: already has tb (MCP server tb in ~/.gemini/settings.json)")
	if len(f.ran) != 0 {
		t.Errorf("ran %v, want nothing", f.ran)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestInitSkipsClaudeWithThePlugin(t *testing.T) {
	f := newFakeInit(t, "claude")
	f.write(t, ".claude.json", `{"mcpServers":{"other":{"command":"x"}}}`)
	f.write(t, ".claude/plugins/installed_plugins.json", `{"version":2,"plugins":{"test-buddy@test-buddy":[{"scope":"user"}]}}`)
	out, _ := f.init(false)
	wantLines(t, out, "skipped Claude Code: already has tb (the test-buddy plugin: the tb CLI and skill)")
	if len(f.ran) != 0 {
		t.Errorf("ran %v, want nothing", f.ran)
	}
}

func TestInitOtherServersAreNotTB(t *testing.T) {
	f := newFakeInit(t, "claude", "codex", "gemini")
	f.write(t, ".claude.json", `{"mcpServers":{"tbx":{"command":"x"}}}`)
	f.write(t, ".codex/config.toml", "[mcp_servers.tbx]\ncommand = \"x\"\n")
	f.write(t, ".gemini/settings.json", `not json`)
	f.init(false)
	if len(f.ran) != 3 {
		t.Errorf("ran %v, want all three clients", f.ran)
	}
}

func TestInitDryRunChangesNothing(t *testing.T) {
	f := newFakeInit(t, "claude", "codex")
	f.write(t, ".codex/config.toml", "[mcp_servers.tb]\ncommand = \"tb\"\n")
	before := homeFiles(t, f.env.home)
	out, code := f.init(true)
	wantLines(t, out,
		"would run for Claude Code: claude mcp add --scope user tb -- /usr/local/bin/tb mcp",
		"skipped Codex: already has tb (MCP server tb in ~/.codex/config.toml)",
		"skipped Gemini CLI: not installed (no gemini on PATH)",
		"dry run: nothing was changed (tb init writes no files itself; each client's own command records the server)")
	if len(f.ran) != 0 {
		t.Errorf("a dry run ran %v", f.ran)
	}
	after := homeFiles(t, f.env.home)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("a dry run changed the home directory:\n%v\n%v", before, after)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestInitReportsAFailedCommand(t *testing.T) {
	f := newFakeInit(t, "claude", "codex")
	f.fail["/bin/claude"] = "MCP server tb already exists in user config\n"
	out, code := f.init(false)
	wantLines(t, out,
		"failed Claude Code: claude mcp add --scope user tb -- /usr/local/bin/tb mcp: MCP server tb already exists in user config",
		"added Codex: codex mcp add tb -- /usr/local/bin/tb mcp")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestInitNoClients(t *testing.T) {
	f := newFakeInit(t)
	out, code := f.init(false)
	wantLines(t, out, "no supported coding agent found: tb agent-guide prints instructions any agent can use")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestInitEndsWithTheDoctorChecks(t *testing.T) {
	f := newFakeInit(t, "claude")
	out, _ := f.init(false)
	if !strings.HasSuffix(out, "ok daemon not running (tb starts it when needed)\nready: android, ios, web\n") {
		t.Errorf("no doctor checks at the end:\n%s", out)
	}
}

func TestInitQuotesAPathWithSpaces(t *testing.T) {
	f := newFakeInit(t, "codex")
	f.env.tb = "/Users/a b/tb"
	out, _ := f.init(true)
	wantLines(t, out, `would run for Codex: codex mcp add tb -- "/Users/a b/tb" mcp`)
}

func TestInitRegistersThePathEntryThatIsThisBinary(t *testing.T) {
	f := newFakeInit(t, "codex")
	dir := t.TempDir()
	exe, onPath := filepath.Join(dir, "build", "tb"), filepath.Join(dir, "bin", "tb")
	for _, d := range []string{"build", "bin"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(exe, []byte("tb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, onPath); err != nil {
		t.Skip(err)
	}
	f.env.tb, f.env.tempDir = exe, filepath.Join(dir, "build")
	inner := f.env.lookPath
	f.env.lookPath = func(name string) (string, error) {
		if name == "tb" {
			return onPath, nil
		}
		return inner(name)
	}
	out, code := f.init(false)
	wantLines(t, out, "added Codex: codex mcp add tb -- "+onPath+" mcp")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestInitRefusesATemporaryBuild(t *testing.T) {
	f := newFakeInit(t, "claude", "codex")
	f.env.tb = "/var/tmp-builds/go-build123/b001/exe/tb"
	for _, dryRun := range []bool{false, true} {
		f.stderr.Reset()
		out, code := f.init(dryRun)
		if code != 2 || out != "" || len(f.ran) != 0 {
			t.Errorf("dry run %v: exit %d, stdout %q, ran %v; want 2 and nothing done", dryRun, code, out, f.ran)
		}
		want := "tb init: this tb is a temporary build (/var/tmp-builds/go-build123/b001/exe/tb), which agents could not start later: install tb (README, Install) and run that tb's init\n"
		if f.stderr.String() != want {
			t.Errorf("stderr %q, want %q", f.stderr.String(), want)
		}
	}
}

func TestInitRegistersAnotherPathEntrysBinaryByItsOwnPath(t *testing.T) {
	f := newFakeInit(t, "codex")
	dir := t.TempDir()
	other := filepath.Join(dir, "tb")
	if err := os.WriteFile(other, []byte("another tb"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := f.env.lookPath
	f.env.lookPath = func(name string) (string, error) {
		if name == "tb" {
			return other, nil
		}
		return inner(name)
	}
	out, _ := f.init(false)
	wantLines(t, out, "added Codex: codex mcp add tb -- /usr/local/bin/tb mcp")
}

func TestCmdInitRejectsBadInput(t *testing.T) {
	_, stderr := capture(t, func() {
		if code := cmdInit([]string{"--nope"}); code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "nope") {
		t.Errorf("stderr: %s", stderr)
	}
}

func TestUsageMentionsDoctorAndInit(t *testing.T) {
	for _, want := range []string{"tb doctor", "tb init"} {
		if !strings.Contains(usage, want) {
			t.Errorf("tb help lacks %q", want)
		}
	}
}
