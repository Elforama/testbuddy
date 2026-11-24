package device

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func script(t *testing.T, path, log, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	s := "#!/bin/sh\necho \"$*\" >> '" + log + "'\n" + body + "\n"
	if err := os.WriteFile(path, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
}

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

func heredoc(text string) string {
	return "/bin/cat <<'TB_EOF'\n" + text + "\nTB_EOF"
}
