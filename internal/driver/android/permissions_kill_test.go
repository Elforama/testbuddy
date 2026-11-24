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

type permDevice struct {
	dir string
	a   *fakeAgent
}

type permDeviceOpts struct {
	mode         string
	camera       bool
	killAfter    int
	activityOnly bool
}

func newPermDevice(t *testing.T, o permDeviceOpts) *permDevice {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := `rm -f "$DEV/pending" "$DEV/pid" "$DEV/fg"`
	if o.activityOnly {
		gone = `rm -f "$DEV/pending" "$DEV/fg"`
	}
	kill := `[ -f "$DEV/pid" ] && echo ` + strconv.Itoa(o.killAfter) + ` > "$DEV/pending"`
	stubs := map[string]string{
		"pidof": `if [ -f "$DEV/pending" ]; then n=$(($(cat "$DEV/pending") - 1))
  if [ $n -le 0 ]; then ` + gone + `; else echo $n > "$DEV/pending"; fi
fi
echo pidof >> "$DEV/calls"
[ -f "$DEV/pid" ] && cat "$DEV/pid"; true`,
		"appops": `case "$1" in
get) m=$(cat "$DEV/mode")
  if [ "$m" = default ]; then echo "No operations."; echo "Default mode: default"
  else echo "Uid mode: $4: $m"; fi
  echo "$4: allow; time=+$(od -An -N2 -tu2 /dev/urandom | tr -d ' ')ms ago" ;;
set) old=$(cat "$DEV/mode"); echo "$5" > "$DEV/mode"
  if [ "$old" != "$5" ] && [ "$5" != allow ]; then ` + kill + `; fi ;;
esac`,
		"pm": `case "$1" in
grant) touch "$DEV/camera" ;;
revoke) if [ -f "$DEV/camera" ]; then rm -f "$DEV/camera"; ` + kill + `; fi ;;
esac; true`,
		"am": `[ "$1" = start ] && { echo 5000 > "$DEV/pid"; touch "$DEV/fg"; }; true`,
		"dumpsys": `case "$1" in
package) echo "      android.permission.MANAGE_EXTERNAL_STORAGE"
  if [ -f "$DEV/camera" ]; then g=true; else g=false; fi
  echo "      android.permission.CAMERA: granted=$g, flags=[ USER_SENSITIVE_WHEN_GRANTED]" ;;
esac`,
		"cmd":   `echo com.example.example/.MainActivity`,
		"sleep": `exit 0`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"pid": "4242\n", "fg": "", "mode": o.mode + "\n"}
	if o.camera {
		files["camera"] = ""
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &permDevice{dir: dir, a: newFakeAgent(t)}
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

func (f *permDevice) has(name string) bool {
	_, err := os.Stat(filepath.Join(f.dir, name))
	return err == nil
}

func (f *permDevice) pidofCalls() int {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return strings.Count(string(b), "pidof")
}

func TestSetPermissionsWaitsForTheKill(t *testing.T) {
	const app = "com.example.example"
	allFiles := func(v string) map[string]string {
		return map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": v}
	}
	camera := func(v string) map[string]string { return map[string]string{"camera": v} }
	cases := []struct {
		name      string
		dev       permDeviceOpts
		perms     map[string]string
		wantStart bool
		wantWait  bool
	}{
		{"all files: kill lands at once", permDeviceOpts{mode: "allow", killAfter: 1}, allFiles("deny"), true, false},
		{"all files: kill lands after appops returned", permDeviceOpts{mode: "allow", killAfter: 2}, allFiles("deny"), true, true},
		{"all files: kill lands late", permDeviceOpts{mode: "allow", killAfter: 8}, allFiles("deny"), true, true},
		{"all files: reset to default", permDeviceOpts{mode: "allow", killAfter: 3}, allFiles("unset"), true, true},
		{"all files: already denied, no kill", permDeviceOpts{mode: "deny", killAfter: 1}, allFiles("deny"), false, false},
		{"all files: default to allow, no kill", permDeviceOpts{mode: "default", killAfter: 1}, allFiles("allow"), false, false},
		{"camera: revoked, kill lands late", permDeviceOpts{mode: "allow", camera: true, killAfter: 4}, camera("deny"), true, true},
		{"camera: unset while granted", permDeviceOpts{mode: "allow", camera: true, killAfter: 3}, camera("unset"), true, true},
		{"camera: not granted, no kill", permDeviceOpts{mode: "allow", killAfter: 1}, camera("deny"), false, false},
		{"camera: granted, no change", permDeviceOpts{mode: "allow", camera: true, killAfter: 1}, camera("allow"), false, false},
		{"process outlives the wait, activity gone", permDeviceOpts{mode: "allow", killAfter: 1, activityOnly: true}, allFiles("deny"), true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dev := newPermDevice(t, c.dev)
			d := newDriver(t, dev.a)
			if err := d.SetPermissions(ctxT(t), app, c.perms); err != nil {
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
			if waited := dev.pidofCalls() > 3; waited != c.wantWait {
				t.Errorf("waited for a kill: %v (%d pidof calls), want %v (ran %q)", waited, dev.pidofCalls(), c.wantWait, dev.a.shCmds())
			}
		})
	}
}
