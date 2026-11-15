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
//     app's processes, a process started meanwhile for a new start too (doomed). `cmd
//     activity stack remove` removes the task the same way. `pm clear` stops the package.
//   - keepTask: `pm clear` keeps the task (a removal that never ends, or an Android that
//     does not remove tasks with the data). noCmd: `cmd` fails at once (no stack list).
//   - lists: a line per `cmd activity stack list` call. flags: the last start's flags.
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
  echo "$flags" > "$DEV/flags"
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
  rm -f "$DEV/pid"; : > "$DEV/stopped"
  if [ -f "$DEV/keepTask" ]; then :
  elif grep -q gms "$DEV/task" 2>/dev/null; then echo 2 > "$DEV/removing"; else rm -f "$DEV/task"; fi
  echo Success ;;
esac`,
		"cmd": `[ "$1 $2 $3" = "activity stack list" ] && echo >> "$DEV/lists"
[ -f "$DEV/noCmd" ] && exit 1
[ "$1 $2" = "activity get-current-user" ] && { echo 0; exit 0; }
if [ "$1 $2 $3 $4" = "activity stack remove 7" ]; then
  if grep -q gms "$DEV/task" 2>/dev/null; then echo 2 > "$DEV/removing"; else rm -f "$DEV/task"; fi
  exit 0
fi
[ "$1 $2 $3" = "activity stack list" ] || exit 0
echo "RootTask id=1 bounds=[0,0][1080,2400] displayId=0 userId=0"
echo "  taskId=6: com.google.android.apps.nexuslauncher/.NexusLauncherActivity bounds=[0,0][1080,2400] userId=0 visible=false topActivity=ComponentInfo{com.google.android.apps.nexuslauncher/.NexusLauncherActivity}"
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

func (f *taskDevice) touch(name string) {
	if err := os.WriteFile(filepath.Join(f.dir, name), nil, 0o644); err != nil {
		panic(err)
	}
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

func taskDriver(t *testing.T, dev *taskDevice) *Driver { return taskDriverAt(t, dev, 34) }

// taskDriverAt is a driver for the device at API level sdk.
func taskDriverAt(t *testing.T, dev *taskDevice, sdk int) *Driver {
	d := newDriver(t, dev.a)
	d.stateMu.Lock()
	d.sdk = sdk
	d.stateMu.Unlock()
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
			if got := dev.read("flags"); got != cleanTask {
				t.Errorf("the start's flags: %s, want %s (a clean start: the app was stopped)", got, cleanTask)
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
	// Before Android 10 a task's line in the list gives its stack's top activity, which may
	// be another task's: the message names the package in front only.
	t.Run("before Android 10 the message names the package only", func(t *testing.T) {
		dev := newTaskDevice(t, []string{"app:saved", "gms"}, true, false)
		d := taskDriverAt(t, dev, 28)
		err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{})
		if err == nil || !strings.Contains(err.Error(), "in front: com.google.android.gms)") {
			t.Errorf("err = %v, want it to say \"in front: com.google.android.gms)\"", err)
		}
	})
}

// Issue #281: stopApp leaves no task of the app behind. After `am force-stop`, another app's
// activity (the dialog) is left in the app's task, on screen: from Android 10 tb removes that
// task (a root task of its own) and waits for it to go. The next launch, even one that does
// not stop the app, finds no task.
func TestStopAppLeavesNoTaskBehind(t *testing.T) {
	t.Setenv("TB_CI", "0")
	dev := newTaskDevice(t, []string{"app:saved", "gms"}, true, false)
	d := taskDriver(t, dev)
	if err := d.StopApp(ctxT(t), "com.example.app"); err != nil {
		t.Fatalf("StopApp: %v (ran %q)", err, dev.a.shCmds())
	}
	if dev.has("task") || dev.has("removing") {
		t.Errorf("after stopApp the app's task is %q (removal pending: %v), want none (ran %q)", dev.task(), dev.has("removing"), dev.a.shCmds())
	}
	if got := dev.foreground(); got == "com.google.android.gms" {
		t.Errorf("after stopApp %s is in front, want the dialog gone", got)
	}
	// Only the app's root task: not the launcher's.
	for _, c := range dev.a.shCmds() {
		if strings.Contains(c, "stack remove") && !strings.Contains(c, "stack remove 7;") {
			t.Errorf("removed another root task: %q", c)
		}
	}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{}); err != nil {
		t.Errorf("launch after stopApp: %v", err)
	}
	if got := dev.task(); got != "app:new" {
		t.Errorf("app's task after the launch: %q, want app:new", got)
	}
}

// Issue #281: before Android 10 a stack holds other apps' tasks, and removing it would remove
// theirs: stopApp is `am force-stop` alone, the dialog stays, and the next launch clears it.
func TestStopAppBeforeAndroid10KeepsTheTask(t *testing.T) {
	t.Setenv("TB_CI", "0")
	dev := newTaskDevice(t, []string{"app:saved", "gms"}, true, false)
	d := taskDriverAt(t, dev, 28)
	if err := d.StopApp(ctxT(t), "com.example.app"); err != nil {
		t.Fatal(err)
	}
	if got := dev.a.shCmds(); len(got) != 1 || got[0] != "am force-stop com.example.app" {
		t.Errorf("commands %q, want the force-stop alone", got)
	}
	if got := dev.task(); got != "gms" {
		t.Errorf("app's task: %q, want the dialog left in it", got)
	}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{}); err != nil {
		t.Errorf("launch after stopApp: %v", err)
	}
	if got := dev.task(); got != "app:new" {
		t.Errorf("app's task after the launch: %q, want app:new", got)
	}
}

// Issue #281: the wait for the app's task to go after `pm clear` is bounded (100 polls, 5 s):
// where the task stays (a removal that never ends, or an Android that keeps the task), the
// wait runs out, the trace says so, and the clean start clears the task. Without `cmd` the
// wait ends at once.
func TestClearWaitForTheTaskIsBounded(t *testing.T) {
	t.Setenv("TB_CI", "0")
	for _, c := range []struct {
		name  string
		noCmd bool
		lists int  // stack list calls
		noted bool // the trace says the task stayed
	}{
		{"task stays", false, 101, true}, // 100 polls of 50 ms, then the last look
		{"no cmd", true, 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dev := newTaskDevice(t, []string{"app:saved", "gms"}, true, false)
			dev.touch("keepTask")
			if c.noCmd {
				dev.touch("noCmd")
			}
			d := taskDriver(t, dev)
			if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true, ClearState: true}); err != nil {
				t.Errorf("launch: %v", err)
			}
			b, _ := os.ReadFile(filepath.Join(dev.dir, "lists"))
			if got := strings.Count(string(b), "\n"); got != c.lists {
				t.Errorf("%d task listings, want %d", got, c.lists)
			}
			notes := strings.Join(d.Notes(), "\n")
			if strings.Contains(notes, "still listed") != c.noted {
				t.Errorf("notes %q, want the stayed task noted: %v", notes, c.noted)
			}
			if got := dev.task(); got != "app:new" || dev.read("flags") != cleanTask {
				t.Errorf("app's task %q after a start with %s, want the app alone (a clean start)", got, dev.read("flags"))
			}
		})
	}
}

// Issue #281: stopApp finds the root tasks to remove in the task list. Real listings, after
// `am force-stop` with the permission dialog left in the bench app's task (configuration
// lines left out): Android 11 (API 30) says "Stack id=", Android 16 (API 36) "RootTask id=".
// Only a root task that holds the app's tasks alone, for the current user, is removed.
func TestAppRootTasks(t *testing.T) {
	const api30 = `TB_USER=0
Stack id=9 bounds=[0,0][1080,2400] displayId=0 userId=0
  taskId=9: dev.testbuddy.bench/dev.testbuddy.bench.ScenarioListActivity bounds=[0,0][1080,2400] userId=0 visible=true topActivity=ComponentInfo{com.google.android.permissioncontroller/com.android.permissioncontroller.permission.ui.GrantPermissionsActivity}

Stack id=1 bounds=[0,0][1080,2400] displayId=0 userId=0
  taskId=8: com.google.android.apps.nexuslauncher/com.google.android.apps.nexuslauncher.NexusLauncherActivity bounds=[0,0][1080,2400] userId=0 visible=true topActivity=ComponentInfo{com.google.android.apps.nexuslauncher/com.google.android.apps.nexuslauncher.NexusLauncherActivity}

Stack id=5 bounds=[0,0][1080,1155] displayId=0 userId=0
  taskId=5: unknown bounds=[0,0][1080,1155] userId=0 visible=false
`
	const api36 = `TB_USER=0
RootTask id=963 bounds=[0,0][1080,2400] displayId=0 userId=0
  taskId=963: com.example.example/com.example.example.MainActivity bounds=[0,0][1080,2400] userId=0 visible=true topActivity=ComponentInfo{com.google.android.gms/com.google.android.location.settings.LocationSettingsCheckerActivity}

RootTask id=707 bounds=[0,0][1080,2400] displayId=0 userId=0
  taskId=707: dev.testbuddy.bench/dev.testbuddy.bench.MainActivity bounds=[0,0][1080,2400] userId=0 visible=false topActivity=ComponentInfo{dev.testbuddy.bench/dev.testbuddy.bench.MainActivity}
`
	// A stack shared with another app's task (before Android 10), and another user's task.
	const shared = `TB_USER=0
Stack id=1 bounds=[0,0][1080,2400] displayId=0 userId=0
  taskId=12: com.example.example/com.example.example.MainActivity bounds=[0,0][1080,2400] userId=0 visible=true topActivity=ComponentInfo{com.google.android.gms/x.Y}
  taskId=11: org.other/org.other.Main bounds=[0,0][1080,2400] userId=0 visible=false topActivity=ComponentInfo{com.google.android.gms/x.Y}
Stack id=2 bounds=[0,0][1080,2400] displayId=0 userId=10
  taskId=13: com.example.example/com.example.example.MainActivity bounds=[0,0][1080,2400] userId=10 visible=false topActivity=ComponentInfo{com.example.example/com.example.example.MainActivity}
`
	for _, c := range []struct {
		name, out, app string
		want           []string
	}{
		{"API 30", api30, "dev.testbuddy.bench", []string{"9"}},
		{"API 36", api36, "com.example.example", []string{"963"}},
		{"shared stack and other user", shared, "com.example.example", nil},
		{"no task of the app", api36, "com.missing", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := appRootTasks(c.out, c.app); strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("appRootTasks = %v, want %v", got, c.want)
			}
		})
	}
}
