package android

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// taskDevice runs the driver's shell commands in a host shell, with am, pm, cmd, dumpsys,
// pidof and sleep stubbed to model one app's task on a device (issue #281), each part a file
// in a temp dir:
//
//   - task: the app's task, one activity per line, bottom to top: "app:saved" (the app's own
//     activity, with its saved state), "app:new" (a fresh start of it) or "gms" (another app's
//     activity started into the app's task, like Google Play services' Location Accuracy
//     dialog). No file: no task.
//   - pid: the app's process runs. stopped: the package is stopped (force-stopped, none of its
//     components has run since). front: "task" when the app's task is in front, "none" when
//     nothing is (a start whose process was killed).
//   - removing: `pm clear` removes the app's task, and when another app's activity is in it the
//     removal ends only when that activity is destroyed (asynchronously, in the other app's
//     process): here after that many `cmd activity stack list` calls. Ending, it kills the
//     app's processes, a process started meanwhile for a new start too (doomed).
//
// `am force-stop` ends the process and removes the app's own activities from the task, not the
// others'. `am start` with NEW_TASK|CLEAR_TASK (0x10008000) empties the task and starts the app
// alone in it; with NEW_TASK|RESET_TASK_IF_NEEDED (0x10200000) it brings the task to the front
// as it is, and starts the app only when the task has nothing in it.
type taskDevice struct {
	dir string
	a   *fakeAgent
}

const gmsTop = "com.google.android.gms/com.google.android.location.settings.LocationSettingsCheckerActivity"

func newTaskDevice(t *testing.T, task []string, running, stopped bool) *taskDevice {
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
		"am": `case "$1" in
force-stop)
  rm -f "$DEV/pid"; : > "$DEV/stopped"
  if [ -f "$DEV/task" ]; then grep -v '^app' "$DEV/task" > "$DEV/t"; mv "$DEV/t" "$DEV/task"; [ -s "$DEV/task" ] || rm -f "$DEV/task"; fi ;;
kill) rm -f "$DEV/pid" ;;
start)
  flags=; while [ $# -gt 0 ]; do [ "$1" = -f ] && flags=$2; shift; done
  echo "Starting: Intent { act=android.intent.action.MAIN flg=$flags cmp=com.example.app/.MainActivity }"
  if [ -f "$DEV/removing" ]; then : > "$DEV/doomed"; : > "$DEV/pid"; echo none > "$DEV/front"; exit 0; fi
  rm -f "$DEV/stopped"; echo task > "$DEV/front"
  if [ "$flags" = 0x10008000 ]; then echo app:new > "$DEV/task"; : > "$DEV/pid"
  elif [ ! -s "$DEV/task" ]; then echo app:new > "$DEV/task"; : > "$DEV/pid"
  elif [ "$(tail -n 1 "$DEV/task")" = gms ]; then echo "Warning: Activity not started, intent has been delivered to currently running top-most instance."
  else : > "$DEV/pid"
  fi ;;
esac`,
		"pm": `case "$1" in
clear)
  rm -f "$DEV/pid"
  if grep -q gms "$DEV/task" 2>/dev/null; then echo 2 > "$DEV/removing"; else rm -f "$DEV/task"; fi
  echo Success ;;
esac`,
		"cmd": `[ "$1 $2 $3" = "activity stack list" ] || exit 0
if [ -f "$DEV/removing" ]; then
  n=$(($(cat "$DEV/removing") - 1))
  if [ $n -le 0 ]; then
    rm -f "$DEV/removing" "$DEV/task"
    if [ -f "$DEV/doomed" ]; then rm -f "$DEV/pid" "$DEV/doomed"; fi
  else echo $n > "$DEV/removing"; fi
fi
[ -s "$DEV/task" ] || exit 0
top=com.example.app/com.example.app.MainActivity
[ "$(tail -n 1 "$DEV/task")" = gms ] && top=` + gmsTop + `
echo "RootTask id=7 bounds=[0,0][1080,2400] displayId=0 userId=0"
echo "  taskId=7: com.example.app/com.example.app.MainActivity bounds=[0,0][1080,2400] userId=0 visible=true topActivity=ComponentInfo{$top}"`,
		"dumpsys": `[ "$1 $2" = "package com.example.app" ] || exit 0
s=false; [ -f "$DEV/stopped" ] && s=true
echo "    User 0: ceDataInode=1 installed=true hidden=false suspended=false distractionFlags=0 stopped=$s notLaunched=false enabled=0"`,
		"pidof": `[ -f "$DEV/pid" ] && echo 4242`,
		"sleep": `exit 0`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, body string, on bool) {
		if on {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("task", strings.Join(task, "\n")+"\n", len(task) > 0)
	write("pid", "", running)
	write("stopped", "", stopped)
	write("front", "task\n", true)

	dev := &taskDevice{dir: dir}
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
	a.on("awaitPackage", func(map[string]any) map[string]any {
		return map[string]any{"found": dev.foreground() == "com.example.app" && dev.has("pid")}
	})
	a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": dev.foreground()} })
	dev.a = a
	return dev
}

func (f *taskDevice) has(name string) bool {
	_, err := os.Stat(filepath.Join(f.dir, name))
	return err == nil
}

func (f *taskDevice) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(f.dir, name))
	return strings.TrimSpace(string(b))
}

// task is the app's task, its activities bottom to top, separated by spaces.
func (f *taskDevice) task() string { return strings.Join(strings.Fields(f.read("task")), " ") }

// foreground is the package in front, as the agent reports it.
func (f *taskDevice) foreground() string {
	switch {
	case f.read("front") == "none":
		return ""
	case f.read("front") != "task" || f.task() == "":
		return "com.google.android.apps.nexuslauncher"
	case strings.HasSuffix(f.task(), "gms"):
		return "com.google.android.gms"
	}
	return "com.example.app"
}

// startCmds are the shell commands sent, without the task listings that a failed launch's
// message reads (issue #281).
func startCmds(a *fakeAgent) []string {
	var cmds []string
	for _, c := range a.shCmds() {
		if c != "cmd activity stack list" {
			cmds = append(cmds, c)
		}
	}
	return cmds
}

func taskDriver(t *testing.T, dev *taskDevice) *Driver {
	d := newDriver(t, dev.a)
	d.launch["com.example.app"] = "com.example.app/.MainActivity"
	return d
}

// Issue #281: a flow left another app's activity in the app's task (Google Play services'
// Location Accuracy dialog, which the app started with startResolutionForResult). `am
// force-stop` ends the app's process and removes its own activities, not that one, and the
// start then brought the task back with the dialog on top: every later launchApp failed after
// 20 s, "in front: com.google.android.gms". A launch that stops the app (stopApp, the default,
// or clearState) starts it in a clean task, and so does any launch of an app that is stopped
// (nothing of its own can be in its task).
//
// clearState: `pm clear` removes the app's task, and with another app's activity in it the
// removal ends later, killing the app's processes as it ends: a start sent before it ended had
// its process killed ("in front: unknown" in the issue's run). Clearing ends once the task is gone.
func TestLaunchAfterStopStartsInACleanTask(t *testing.T) {
	t.Setenv("TB_CI", "0")
	for _, c := range []struct {
		name string
		run  func(d *Driver) error
	}{
		{"launchApp", func(d *Driver) error {
			return d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true})
		}},
		{"launchApp clearState", func(d *Driver) error {
			return d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true, ClearState: true})
		}},
		{"stopApp then launchApp stopApp false", func(d *Driver) error {
			if err := d.StopApp(ctxT(t), "com.example.app"); err != nil {
				return err
			}
			return d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{})
		}},
		{"clearState then launchApp", func(d *Driver) error {
			if err := d.ClearState(ctxT(t), "com.example.app"); err != nil {
				return err
			}
			return d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true})
		}},
		{"clearState then launchApp stopApp false", func(d *Driver) error {
			if err := d.ClearState(ctxT(t), "com.example.app"); err != nil {
				return err
			}
			return d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dev := newTaskDevice(t, []string{"app:saved", "gms"}, true, false)
			d := taskDriver(t, dev)
			if err := c.run(d); err != nil {
				t.Errorf("launch: %v (ran %q)", err, dev.a.shCmds())
			}
			if got := dev.task(); got != "app:new" {
				t.Errorf("app's task after the launch: %q, want the app alone in it (ran %q)", got, dev.a.shCmds())
			}
			if got := dev.foreground(); got != "com.example.app" || !dev.has("pid") {
				t.Errorf("in front: %q (process running: %v), want com.example.app", got, dev.has("pid"))
			}
		})
	}
}

// Issue #281: a launch with stopApp: false resumes the app's task as it is when the app has
// something in it (it was killed, not stopped: killApp keeps its saved state; or it runs).
// When it does not come to the front, the error names what is on top of the app's task.
func TestLaunchWithoutStopResumesTheTask(t *testing.T) {
	t.Setenv("TB_CI", "0")
	t.Run("killed app restores its saved state", func(t *testing.T) {
		dev := newTaskDevice(t, []string{"app:saved"}, true, false)
		d := taskDriver(t, dev)
		if err := d.KillApp(ctxT(t), "com.example.app"); err != nil {
			t.Fatal(err)
		}
		if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{}); err != nil {
			t.Errorf("launch: %v (ran %q)", err, dev.a.shCmds())
		}
		if got := dev.task(); got != "app:saved" {
			t.Errorf("app's task: %q, want app:saved (the start cleared the saved state; ran %q)", got, dev.a.shCmds())
		}
	})
	t.Run("running app under another app's dialog", func(t *testing.T) {
		dev := newTaskDevice(t, []string{"app:saved", "gms"}, true, false)
		d := taskDriver(t, dev)
		err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{})
		if got := dev.task(); got != "app:saved gms" {
			t.Errorf("app's task: %q, want it as it was (ran %q)", got, dev.a.shCmds())
		}
		want := "in front: com.google.android.gms (in com.example.app's task)"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to say %q", err, want)
		}
	})
}
