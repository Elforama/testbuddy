package android

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeDevice struct {
	dir string
	a   *fakeAgent
}

func newFakeDevice(t *testing.T, running, foreground, stuck bool) *fakeDevice {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	home := `rm -f "$DEV/fg"`
	if stuck {
		home = `:`
	}
	stubs := map[string]string{
		"am": `case "$1" in
kill) [ -f "$DEV/fg" ] || rm -f "$DEV/pid" ;;
force-stop) rm -f "$DEV/pid" "$DEV/fg" "$DEV/task" ;;
start) case "$*" in *android.intent.category.HOME*) ` + home + ` ;; esac ;;
esac`,
		"pidof": `[ -f "$DEV/pid" ] && echo 4242`,
		"input": `[ "$1 $2" = "keyevent KEYCODE_HOME" ] && { touch "$DEV/homeKey"; ` + home + `; }; exit 0`,
		"sleep": `exit 0`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(name string, on bool) {
		if on {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	touch("pid", running)
	touch("task", true)
	touch("fg", foreground)

	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		c := exec.Command("/bin/sh", "-c", req["cmd"].(string))
		c.Env = append(os.Environ(), "DEV="+dir, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := c.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Errorf("sh: %v", err)
		}
		return map[string]any{"code": code, "out": string(out)}
	})
	return &fakeDevice{dir: dir, a: a}
}

func (f *fakeDevice) has(name string) bool {
	_, err := os.Stat(filepath.Join(f.dir, name))
	return err == nil
}

func TestKillAppKeepsSavedState(t *testing.T) {
	cases := []struct {
		name                string
		running, foreground bool
	}{
		{"background", true, false},
		{"foreground", true, true},
		{"not running", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dev := newFakeDevice(t, c.running, c.foreground, false)
			d := newDriver(t, dev.a)
			if err := d.KillApp(ctxT(t), "com.example.app"); err != nil {
				t.Fatalf("KillApp: %v (ran %q)", err, dev.a.shCmds())
			}
			if dev.has("pid") {
				t.Errorf("process still running after killApp (ran %q)", dev.a.shCmds())
			}
			if !dev.has("task") {
				t.Errorf("killApp cleared the app's task and saved state, like stopApp (ran %q)", dev.a.shCmds())
			}
		})
	}
}

func TestKillAppLeavesNoHomeKeyBehind(t *testing.T) {
	dev := newFakeDevice(t, true, true, false)
	d := newDriver(t, dev.a)
	if err := d.KillApp(ctxT(t), "com.example.app"); err != nil {
		t.Fatalf("KillApp: %v (ran %q)", err, dev.a.shCmds())
	}
	if dev.has("pid") || !dev.has("task") {
		t.Errorf("want the process ended and the task kept (ran %q)", dev.a.shCmds())
	}
	if dev.has("homeKey") {
		t.Errorf("killApp sent the Home key, which Android may handle after the next launch (ran %q)", dev.a.shCmds())
	}
}

func TestKillAppReportsSurvivingProcess(t *testing.T) {
	dev := newFakeDevice(t, true, true, true)
	d := newDriver(t, dev.a)
	err := d.KillApp(ctxT(t), "com.example.app")
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("KillApp = %v, want a still-running error", err)
	}
	if !dev.has("task") {
		t.Errorf("killApp cleared the app's task and saved state (ran %q)", dev.a.shCmds())
	}
}
