package android

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// allFilesDevice runs the driver's commands in a host shell, with appops, pidof, am, dumpsys,
// cmd and sleep stubbed to model one app that requests all-files access, as Android handles
// it: taking the access away (a change of the app-op to anything but allow) kills the app's
// process, and its only activity with it, so the launcher comes to the front. Android does
// this from system_server once `appops set` has changed the mode, so the kill can land after
// `appops set` has returned: here, on the killAfter-th pidof after it (1: at once).
type allFilesDevice struct {
	dir string
	a   *fakeAgent
}

func newAllFilesDevice(t *testing.T, mode string, killAfter int) *allFilesDevice {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		"pidof": `if [ -f "$DEV/pending" ]; then n=$(($(cat "$DEV/pending") - 1))
  if [ $n -le 0 ]; then rm -f "$DEV/pending" "$DEV/pid" "$DEV/fg"; else echo $n > "$DEV/pending"; fi
fi
echo pidof >> "$DEV/calls"
[ -f "$DEV/pid" ] && cat "$DEV/pid"; true`,
		"appops": `case "$1" in
get) echo "$4: $(cat "$DEV/mode")" ;;
set) old=$(cat "$DEV/mode"); echo "$5" > "$DEV/mode"
  if [ "$old" != "$5" ] && [ "$5" != allow ] && [ -f "$DEV/pid" ]; then echo ` + strconv.Itoa(killAfter) + ` > "$DEV/pending"; fi ;;
esac`,
		"am":      `[ "$1" = start ] && { echo 5000 > "$DEV/pid"; touch "$DEV/fg"; }; true`,
		"dumpsys": `echo "      android.permission.MANAGE_EXTERNAL_STORAGE"`,
		"cmd":     `echo com.example.example/.MainActivity`,
		"sleep":   `exit 0`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"pid": "4242\n", "fg": "", "mode": mode + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &allFilesDevice{dir: dir, a: newFakeAgent(t)}
	f.a.on("sh", func(req map[string]any) map[string]any {
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
	f.a.on("foreground", func(map[string]any) map[string]any {
		if f.has("fg") {
			return map[string]any{"pkg": "com.example.example"}
		}
		return map[string]any{"pkg": "com.google.android.apps.nexuslauncher"}
	})
	f.a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": f.has("fg")} })
	return f
}

func (f *allFilesDevice) has(name string) bool {
	_, err := os.Stat(filepath.Join(f.dir, name))
	return err == nil
}

func (f *allFilesDevice) pidofCalls() int {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return strings.Count(string(b), "pidof")
}

// Issue #156: setPermissions taking all-files access away from the app in the foreground left
// the device on the home screen. Android kills the app for it from system_server, which can be
// after `appops set` has returned (a slow CI emulator): tb looked at the pid once, right
// away, saw the old process still there and did not bring the app back. It must wait for the
// kill that the change of the app-op brings, and start the app again.
func TestSetPermissionsWaitsForTheAllFilesKill(t *testing.T) {
	const app = "com.example.example"
	deny := map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": "deny"}
	cases := []struct {
		name      string
		mode      string // the app-op's mode before
		killAfter int
		wantStart bool
	}{
		{"kill lands at once", "allow", 1, true},
		{"kill lands after appops returned", "allow", 2, true},
		{"kill lands late", "allow", 8, true},
		{"already denied: no change, no kill", "deny", 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dev := newAllFilesDevice(t, c.mode, c.killAfter)
			d := newDriver(t, dev.a)
			if err := d.SetPermissions(ctxT(t), app, deny); err != nil {
				t.Fatalf("SetPermissions: %v (ran %q)", err, dev.a.shCmds())
			}
			started := false
			for _, cmd := range dev.a.shCmds() {
				started = started || strings.Contains(cmd, "am start -n "+app+"/.MainActivity")
			}
			if started != c.wantStart {
				t.Errorf("started the app again: %v, want %v (ran %q)", started, c.wantStart, dev.a.shCmds())
			}
			if !dev.has("fg") {
				t.Errorf("the app is not in the foreground after setPermissions (ran %q)", dev.a.shCmds())
			}
			// No kill to wait for when the app-op did not change: one look at the pid.
			if !c.wantStart && dev.pidofCalls() > 2 {
				t.Errorf("waited for a kill that does not come: %d pidof calls", dev.pidofCalls())
			}
		})
	}
}
