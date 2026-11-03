package device

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain makes the package hermetic: the real adb, xcrun and SDK folders cannot be found
// (PATH, HOME and the SDK variables point at an empty temp folder). Tests that need a tool
// install a fake one with fakeADB / fakeXcrun, which check they are the ones picked.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tb-device-test")
	if err != nil {
		panic(err)
	}
	empty := filepath.Join(dir, "bin")
	os.MkdirAll(empty, 0o755)
	for k, v := range map[string]string{
		"PATH":             empty,
		"HOME":             dir,
		"ANDROID_HOME":     dir,
		"ANDROID_SDK_ROOT": dir,
		"TB_HOME":          filepath.Join(dir, "tbhome"),
	} {
		os.Setenv(k, v)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// script writes an executable shell script that logs its arguments (one call per line) to
// log, then runs body. Only shell builtins and absolute paths are usable inside: PATH is
// empty.
func script(t *testing.T, path, log, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	s := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" + body + "\n"
	if err := os.WriteFile(path, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeADB installs body as adb under a fake ANDROID_HOME and returns its call log.
func fakeADB(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "adb.log")
	adb := filepath.Join(dir, "platform-tools", "adb")
	script(t, adb, log, body)
	t.Setenv("ANDROID_HOME", dir)
	if got := ADB(); got != adb {
		t.Fatalf("ADB() = %q, want the fake %q", got, adb)
	}
	return log
}

// fakeXcrun puts body on PATH as xcrun and returns its call log.
func fakeXcrun(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "xcrun.log")
	script(t, filepath.Join(dir, "bin", "xcrun"), log, body)
	t.Setenv("PATH", filepath.Join(dir, "bin"))
	return log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// heredoc prints text exactly (via /bin/cat, as PATH is empty).
func heredoc(text string) string {
	return "/bin/cat <<'TB_EOF'\n" + text + "\nTB_EOF"
}
