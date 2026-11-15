package android

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/elforama/testbuddy/internal/ci"
	"github.com/elforama/testbuddy/internal/driver"
)

func validPkg(p string) error {
	if p == "" {
		return fmt.Errorf("no app id given")
	}
	for _, r := range p {
		if !(r == '.' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Errorf("invalid app id %q", p)
		}
	}
	return nil
}

func (d *Driver) launcher(ctx context.Context, pkg string) (string, error) {
	d.mu.Lock()
	c := d.launch[pkg]
	d.mu.Unlock()
	if c != "" {
		return c, nil
	}
	out, err := d.shOK(ctx, "cmd package resolve-activity --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER "+pkg)
	if err != nil {
		return "", fmt.Errorf("app %s is not installed or has no launcher activity", pkg)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	comp := strings.TrimSpace(lines[len(lines)-1])
	if !strings.Contains(comp, "/") {
		return "", fmt.Errorf("app %s is not installed or has no launcher activity", pkg)
	}
	d.mu.Lock()
	d.launch[pkg] = comp
	d.mu.Unlock()
	return comp, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (d *Driver) LaunchApp(ctx context.Context, appID string, o driver.LaunchOpts) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	comp, err := d.launcher(ctx, appID)
	if err != nil {
		return err
	}
	// One on-device script: stop, clear, grant and start, so a launch costs one round trip.
	var script []string
	if o.StopApp || o.ClearState {
		script = append(script, "am force-stop "+appID)
	}
	if o.ClearState {
		script = append(script, clearCmd(appID))
	}
	allAllow := len(o.Permissions) == 0 || (len(o.Permissions) == 1 && strings.EqualFold(o.Permissions["all"], "allow"))
	if allAllow {
		// Grant only what is not granted yet, deciding on the device. dumpsys package is
		// large: the script reads it once ($dp), for the grants and the start's stopped state.
		script = append(script, pkgDump(appID), "for p in $("+fromDump+" | grep -E '^ +android.permission.[A-Z_]+: granted=false|^ +android.permission.MANAGE_EXTERNAL_STORAGE$' | sed 's/:.*//' | sort -u); do case $p in android.permission.MANAGE_EXTERNAL_STORAGE) appops set --uid "+appID+" MANAGE_EXTERNAL_STORAGE allow;; *) pm grant "+appID+" $p 2>/dev/null;; esac; done")
	} else {
		if len(script) > 0 {
			out, err := d.shOK(ctx, strings.Join(script, "; "))
			d.noteTaskStayed(appID, out)
			if err != nil {
				return err
			}
			script = nil
		}
		if err := d.setPermissions(ctx, appID, o.Permissions, false); err != nil {
			return err
		}
	}
	var args []string // the intent extras
	for k, v := range o.Arguments {
		switch t := v.(type) {
		case bool:
			args = append(args, "--ez", shellQuote(k), strconv.FormatBool(t))
		case int:
			args = append(args, intExtra(k, int64(t))...)
		case int64:
			args = append(args, "--el", shellQuote(k), strconv.FormatInt(t, 10))
		case float64:
			if t == math.Trunc(t) && math.Abs(t) < 1<<63 {
				args = append(args, intExtra(k, int64(t))...)
			} else {
				args = append(args, "--ef", shellQuote(k), strconv.FormatFloat(t, 'f', -1, 64))
			}
		default:
			args = append(args, "--es", shellQuote(k), shellQuote(fmt.Sprint(v)))
		}
	}
	extras := strings.Join(args, " ")
	withExtras := func(flags string) string {
		return strings.TrimSpace(strings.Join(launcherArgs(comp, flags), " ") + " " + extras)
	}
	// A start after a stop is a start in a clean task (issue #281): the app was stopped, so
	// nothing of its own is in its task, and whatever is there is another app's activity that
	// would cover it. Without a stop, the task is resumed as it is, unless the app is stopped
	// anyway (a stopApp step before): Android says so in the package's stopped state, which
	// any start of the app's components ends. killApp does not stop it: its task keeps the
	// saved state the launch restores.
	var start string
	if o.StopApp || o.ClearState {
		start = withExtras(cleanTask)
	} else {
		// -m1: the first " stopped=" is user 0's (dumpsys lists the users in order), the
		// current user unless the device has switched to another.
		if !allAllow {
			start = pkgDump(appID) + "; "
		}
		start += "f=" + resumeTask + "; case \"$(" + fromDump + " | grep -m1 -o ' stopped=[a-z]*')\" in *=true) f=" + cleanTask + ";; esac; " +
			withExtras("$f")
	}
	script = append(script, start)
	// The CI-mode second start (issue #274) keeps the task: the first start made it, and
	// clearing it again could end that start while it is still coming up.
	return d.startApp(ctx, appID, strings.Join(script, "; "), withExtras(resumeTask))
}

// Launch flags. resumeTask (NEW_TASK|RESET_TASK_IF_NEEDED) brings the app's task to the front
// as it is, as the launcher does; it starts the app only when the task has nothing in it.
// cleanTask (NEW_TASK|CLEAR_TASK) empties the app's task and starts the app alone in it: for a
// start after a stop, it removes what other apps left in the app's task (issue #281: Google
// Play services' Location Accuracy dialog, a chooser, a document picker, started into the
// app's task without NEW_TASK, survive `am force-stop` and covered every later start).
const (
	resumeTask = "0x10200000"
	cleanTask  = "0x10008000"
)

// pkgDump reads the package's dumpsys into $dp, which fromDump prints: a launch script reads
// it once.
func pkgDump(appID string) string { return "dp=$(dumpsys package " + appID + ")" }

const fromDump = `printf '%s\n' "$dp"`

func launcherArgs(comp, flags string) []string {
	return []string{"am", "start", "-n", comp, "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER", "-f", flags}
}

// launchWait is how long a launched app has to come to the foreground, from the first start.
const launchWait = 20 * time.Second

// restartAfter is when tb sends the start again to an app that is not in front yet (issue
// #274). Sending it to an app that is merely slow to start does no harm: its task exists from
// the first start, and Android brings that task to the front without a second activity.
const restartAfter = 5 * time.Second

// startApp runs script, which ends with the activity start (start), and waits for the app to
// come to the foreground.
//
// In CI mode, when the app is not in front restartAfter after the start, it sends start (a start
// that keeps the app's task as it is, issue #281: not the stop, clear and grants before it, and
// not a start that clears the task) and waits for the rest of launchWait.
// Something can come over the app after Android brought it to the front (issue #274: on a CI
// emulator, a Home key that Android handled 1.5 s late put the launcher over the app right
// after the start). The second start brings the app back.
func (d *Driver) startApp(ctx context.Context, appID, script, start string) error {
	out, err := d.shOK(ctx, script)
	if err := clearFailed(appID, out); err != nil {
		return err
	}
	d.noteTaskStayed(appID, out)
	if err != nil {
		return err
	}
	if strings.Contains(out, "Error:") {
		return fmt.Errorf("could not start %s: %s", appID, strings.TrimSpace(out))
	}
	// Not `am start -W`: the agent tells us when the app's window is up (an event).
	if !ci.On() {
		// Outside CI mode one start and one wait, as before; the error says what am start
		// answered and what was in front.
		found, err := d.awaitApp(ctx, appID, launchWait)
		if err != nil || found {
			return err
		}
		return fmt.Errorf("%s did not come to the foreground within %s (am start said: %s; in front: %s)",
			appID, launchWait, amSaid(out), d.front(ctx, appID))
	}
	// CI mode only (a slow-runner allowance, like the tap re-send of #204): a second start.
	// With Arguments, an app whose launcher activity is singleTop/singleTask gets them again in
	// onNewIntent; it is only sent when the app was not in front after restartAfter.
	if found, err := d.awaitApp(ctx, appID, restartAfter); err != nil || found {
		return err
	}
	front := d.front(ctx, appID)
	out2, err := d.shOK(ctx, start)
	if err != nil {
		return err
	}
	if strings.Contains(out2, "Error:") {
		return fmt.Errorf("could not start %s: %s", appID, strings.TrimSpace(out2))
	}
	found, err := d.awaitApp(ctx, appID, launchWait-restartAfter)
	if err != nil {
		return err
	}
	if found {
		d.note(fmt.Sprintf("launchApp: %s was not in the foreground %s after the start (am start said: %s; in front: %s); tb sent the start again and it came",
			appID, restartAfter, amSaid(out), front))
		return nil
	}
	return fmt.Errorf("%s did not come to the foreground within %s (tb sent the start twice; am start said: %s, then: %s; in front: %s)",
		appID, launchWait, amSaid(out), amSaid(out2), d.front(ctx, appID))
}

// amSaid is what `am start` answered, on one line, from the script's output.
func amSaid(out string) string {
	if i := strings.Index(out, "Starting:"); i >= 0 {
		out = out[i:]
	}
	out = strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(out), "\n", " / ")), " ")
	if out == "" {
		return "nothing"
	}
	return strconv.Quote(out)
}

// front names the package in the foreground, for a message about appID, which did not come
// there. When another app's activity is on top of appID's own task, it says so (issue #281):
// "com.google.android.gms (in com.example's task)". Before Android 10 the task list gives the
// top activity of a stack, which can hold other apps' tasks: there it names the package only.
func (d *Driver) front(ctx context.Context, appID string) string {
	pkg, err := d.Foreground(ctx)
	if err != nil {
		pkg = ""
	}
	if d.sdkLevel() >= 29 {
		if top := d.topOfTask(ctx, appID); top != "" && top != appID && (pkg == "" || pkg == top) {
			return top + " (in " + appID + "'s task)"
		}
	}
	if pkg == "" {
		return "unknown"
	}
	return pkg
}

// sdkLevel is the device's API level, 0 when not known yet.
func (d *Driver) sdkLevel() int {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.sdk
}

// topOfTask is the package of the activity on top of appID's visible task, from the task list
// (Android 10 and later), or "".
func (d *Driver) topOfTask(ctx context.Context, appID string) string {
	out, _, err := d.Sh(ctx, stackList)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, ": "+appID+"/") || !strings.Contains(line, "visible=true") {
			continue
		}
		if _, top, ok := strings.Cut(line, "topActivity=ComponentInfo{"); ok {
			if pkg, _, ok := strings.Cut(top, "/"); ok {
				return pkg
			}
		}
	}
	return ""
}

func (d *Driver) WaitForApp(ctx context.Context, appID string, timeout time.Duration) error {
	found, err := d.awaitApp(ctx, appID, timeout)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s did not come to the foreground within %s", appID, timeout)
	}
	return nil
}

// awaitApp waits until the app is in the foreground, or the timeout.
func (d *Driver) awaitApp(ctx context.Context, appID string, timeout time.Duration) (bool, error) {
	m, err := d.call(ctx, "awaitPackage", map[string]any{"pkg": appID, "timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	if err != nil {
		return false, err
	}
	var r struct {
		Found bool `json:"found"`
	}
	m.Decode(&r)
	return r.Found, nil
}

func (d *Driver) StopApp(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	// stopApp leaves no task of the app behind (issue #281). `am force-stop` ends the app's
	// processes and its own activities; another app's activity in the app's task (a
	// permission or Google Play services dialog, a chooser) stays, on screen, in a task that
	// is still the app's. From Android 10 each such task is a root task of its own, and tb
	// removes it (`stack remove`), then waits for it to go: the removal ends when that
	// activity is destroyed, in the other app's process, and kills the app's processes, which
	// are stopped already. Before Android 10 a stack holds other apps' tasks too, and removing
	// it would remove theirs: the task stays until the next launch, which clears it.
	if d.sdkLevel() < 29 {
		_, err := d.shOK(ctx, "am force-stop "+appID)
		return err
	}
	out, err := d.shOK(ctx, "am force-stop "+appID+"; echo TB_USER=$(cmd activity get-current-user 2>/dev/null); "+stackList+" 2>/dev/null; true")
	if err != nil {
		return err
	}
	roots := appRootTasks(out, appID)
	if len(roots) == 0 {
		return nil
	}
	var cmds []string
	for _, id := range roots {
		cmds = append(cmds, "cmd activity stack remove "+id)
	}
	out, err = d.shOK(ctx, strings.Join(cmds, "; ")+"; "+taskGoneCmd(appID))
	d.noteTaskStayed(appID, out)
	return err
}

// appRootTasks are the ids of the root tasks, in a task listing (stackList, after a
// "TB_USER=<current user>" line), that hold only tasks of appID for the current user.
func appRootTasks(out, appID string) []string {
	user := ""
	var roots []string
	root, mine, others := "", 0, 0
	flush := func() {
		if root != "" && mine > 0 && others == 0 {
			roots = append(roots, root)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "TB_USER="):
			user = strings.TrimPrefix(t, "TB_USER=")
		case strings.HasPrefix(t, "RootTask id=") || strings.HasPrefix(t, "Stack id="):
			flush()
			_, rest, _ := strings.Cut(t, " id=")
			root, _, _ = strings.Cut(rest, " ")
			mine, others = 0, 0
		case strings.HasPrefix(t, "taskId="):
			if strings.Contains(t, ": "+appID+"/") && (user == "" || strings.Contains(t, " userId="+user+" ")) {
				mine++
			} else {
				others++
			}
		}
	}
	flush()
	return roots
}

// noteTaskStayed traces a wait for the app's tasks to go that ran out (taskGoneCmd).
func (d *Driver) noteTaskStayed(appID, out string) {
	if strings.Contains(out, taskStayed) {
		d.note(fmt.Sprintf("a task of %s was still listed %s after it was removed (cleared or stopped); the next launch clears it", appID, time.Duration(taskGoneWait)*50*time.Millisecond))
	}
}

func (d *Driver) KillApp(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	// Like the system killing it in the background (issue #6): `am kill` ends the process and
	// keeps its task and saved state, so the next launch restores where the user was. No
	// force-stop, which clears that state (that is stopApp). `am kill` only ends a background
	// app, so one that survives it is sent to the background (Home) and killed again.
	//
	// Home is the home activity's start, not the Home key: Android handles the key after
	// `input` returns, and on a slow CI emulator the key came 1.5 s late, after the next
	// launchApp had brought the app back, and put the launcher over it (issue #274). Android
	// has handled `am start` when it returns.
	kill := "am kill " + appID + "; ! pidof " + appID + " >/dev/null"
	script := kill + " && exit 0; am start -a android.intent.action.MAIN -c android.intent.category.HOME >/dev/null 2>&1; " +
		"for i in 1 2 3 4 5 6 7 8 9 10; do sleep 0.3; " + kill + " && exit 0; done; exit 1"
	_, code, err := d.Sh(ctx, script)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("killApp: %s is still running after am kill, even in the background", appID)
	}
	return nil
}

func (d *Driver) ClearState(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	out, err := d.shOK(ctx, clearCmd(appID))
	if cerr := clearFailed(appID, out); cerr != nil {
		return cerr
	}
	d.noteTaskStayed(appID, out)
	return err
}

func (d *Driver) ClearKeychain(ctx context.Context) error { return nil }

// OpenLink starts a VIEW intent for link. It is aimed at the flow's app first (no chooser
// when several apps take the link), unless the browser is asked for; when the app takes no
// such link (a web page, another app's link) it goes out again with no app, and Android
// picks the handler.
//
// It returns once the link's screen is up, like iOS's openurl (issue #194): `start -W`
// waits for the activity the link launched to draw. A chooser or another app (a browser) is
// the launched activity. Through a trampoline (an activity that starts others and finishes)
// recent Android (seen on API 36) waits for the activity it starts; older versions may return
// once the trampoline is gone, and the runner's settle after openLink covers the rest. Without
// -W, `am start` returns once Android has the intent, and the next step read the screen from
// before the link.
//
// An intent delivered to the activity already on top, or to a task brought to the front,
// makes -W return at once, before the app (handling it in onNewIntent) draws: then OpenLink
// waits up to linkReact for the screen to start changing, and the runner's settle after
// openLink waits for the rest.
func (d *Driver) OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error {
	events, stop := d.Events()
	defer stop()
	before := d.Seq()
	cmd := "start -W -a android.intent.action.VIEW -d " + shellQuote(link)
	var out string
	var err error
	if appID != "" && !browser {
		if err := validPkg(appID); err != nil {
			return err
		}
		out, err = d.startLink(ctx, cmd+" "+appID)
		if err != nil && strings.Contains(out, "unable to resolve Intent") {
			out, err = d.startLink(ctx, cmd)
		}
	} else {
		out, err = d.startLink(ctx, cmd)
	}
	if err != nil {
		return err
	}
	if strings.Contains(out, "delivered to currently running") || strings.Contains(out, "brought to the front") {
		d.awaitChange(ctx, events, before, linkReact())
	}
	return nil
}

// awaitChange waits until the device reports a UI change after seq, or the timeout.
func (d *Driver) awaitChange(ctx context.Context, events <-chan driver.Event, seq int64, timeout time.Duration) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	for d.Seq() <= seq {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			return
		case e, ok := <-events:
			if !ok || e.Kind == "disconnected" {
				return
			}
		}
	}
}

// linkWait bounds `am start -W`: an activity that never draws would hold it.
const linkWait = 20 * time.Second

// linkReact is how long a link delivered to a running activity has to start changing the
// screen: the app handles it in onNewIntent and draws on a later frame. A slow CI runner
// gets longer.
func linkReact() time.Duration {
	if ci.On() {
		return time.Second
	}
	return 300 * time.Millisecond
}

// startLink runs an activity manager command (args: "start -W ..."). When the launched
// activity has not drawn within linkWait, or before the step's deadline, the link was still
// sent: the step goes on, and the next one waits for what it needs.
func (d *Driver) startLink(ctx context.Context, args string) (string, error) {
	wait := linkWait
	if dl, ok := ctx.Deadline(); ok && time.Until(dl) < wait {
		wait = time.Until(dl)
	}
	if wait <= 0 {
		return "", context.DeadlineExceeded
	}
	cmd := boundedAM(wait, args)
	m, err := d.call(ctx, "sh", map[string]any{"cmd": cmd, "timeout": wait.Milliseconds()}, wait+5*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Code     int    `json:"code"`
		Out      string `json:"out"`
		TimedOut bool   `json:"timedOut"`
	}
	m.Decode(&r)
	if (r.TimedOut || r.Code == 124) && !strings.Contains(r.Out, "Error:") {
		return r.Out, nil
	}
	if r.Code != 0 {
		return r.Out, fmt.Errorf("am %s: exit %d: %s", args, r.Code, strings.TrimSpace(r.Out))
	}
	return r.Out, nil
}

// boundedAM is an activity manager command that the device ends itself a little before the
// agent's sh wait (wait) runs out: toybox `timeout` (exit 124). It calls `cmd activity`
// rather than `am`: on Android 8 and later `am` is a script that runs `cmd activity` as a
// child, and `timeout` ending the script leaves that child running. Without `timeout` or
// `cmd` on the device, it runs `am` and the agent's own timeout ends it.
func boundedAM(wait time.Duration, args string) string {
	dev := wait - 500*time.Millisecond
	if wait < time.Second {
		dev = wait / 2
	}
	secs := strconv.FormatFloat(float64(dev.Milliseconds())/1000, 'f', -1, 64)
	return "t=; command -v timeout >/dev/null && t='timeout " + secs + "'; a=am; command -v cmd >/dev/null && a='cmd activity'; $t $a " + args
}

// The flow format's permission names -> Android permissions.
var permissionGroups = map[string][]string{
	"camera":        {"android.permission.CAMERA"},
	"contacts":      {"android.permission.READ_CONTACTS", "android.permission.WRITE_CONTACTS", "android.permission.GET_ACCOUNTS"},
	"location":      {"android.permission.ACCESS_FINE_LOCATION", "android.permission.ACCESS_COARSE_LOCATION", "android.permission.ACCESS_BACKGROUND_LOCATION"},
	"microphone":    {"android.permission.RECORD_AUDIO"},
	"phone":         {"android.permission.READ_PHONE_STATE", "android.permission.CALL_PHONE", "android.permission.READ_CALL_LOG", "android.permission.WRITE_CALL_LOG", "android.permission.ANSWER_PHONE_CALLS", "android.permission.READ_PHONE_NUMBERS"},
	"sms":           {"android.permission.SEND_SMS", "android.permission.RECEIVE_SMS", "android.permission.READ_SMS", "android.permission.RECEIVE_MMS"},
	"storage":       {"android.permission.READ_EXTERNAL_STORAGE", "android.permission.WRITE_EXTERNAL_STORAGE"},
	"medialibrary":  {"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO", "android.permission.READ_MEDIA_AUDIO", "android.permission.READ_EXTERNAL_STORAGE"},
	"photos":        {"android.permission.READ_MEDIA_IMAGES", "android.permission.READ_MEDIA_VIDEO", "android.permission.READ_EXTERNAL_STORAGE"},
	"calendar":      {"android.permission.READ_CALENDAR", "android.permission.WRITE_CALENDAR"},
	"notifications": {"android.permission.POST_NOTIFICATIONS"},
	"bluetooth":     {"android.permission.BLUETOOTH_CONNECT", "android.permission.BLUETOOTH_SCAN", "android.permission.BLUETOOTH_ADVERTISE"},
	"sensors":       {"android.permission.BODY_SENSORS"},
	"activity":      {"android.permission.ACTIVITY_RECOGNITION"},
}

// appOpPermissions are set with appops, not pm grant: dumpsys lists them only as requested
// (no granted= state), and their state is an app-op (all-files access).
var appOpPermissions = map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": "MANAGE_EXTERNAL_STORAGE"}

// SetPermissions grants or revokes runtime permissions. An empty map grants all (the format's default).
func (d *Driver) SetPermissions(ctx context.Context, appID string, perms map[string]string) error {
	return d.setPermissions(ctx, appID, perms, true)
}

func (d *Driver) setPermissions(ctx context.Context, appID string, perms map[string]string, restoreForeground bool) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	if len(perms) == 0 {
		perms = map[string]string{"all": "allow"}
	}
	// The app's requested runtime permissions, filtered on the device (dumpsys is huge).
	out, _, err := d.Sh(ctx, "dumpsys package "+appID+" | grep -E '^ +android.permission.[A-Z_]+: granted=|^ +android.permission.MANAGE_EXTERNAL_STORAGE$' | sort -u")
	if err != nil {
		return err
	}
	requested := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if name, _, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(name, "android.permission.") {
			requested[name] = strings.Contains(line, "granted=true")
		} else if _, op := appOpPermissions[line]; op {
			requested[line] = false // its state is the app-op's, set below whatever it is
		}
	}
	var cmds []string
	// expectKill: Android kills the app's process for one of the changes (see
	// setPermissionsInForeground). opModes: the app-ops changed to anything but allow.
	var expectKill bool
	opModes := map[string]string{}
	apply := func(perm, value string) {
		granted, ok := requested[perm]
		if !ok {
			return
		}
		if op, isOp := appOpPermissions[perm]; isOp {
			mode := map[string]string{"allow": "allow", "deny": "deny", "unset": "default"}[strings.ToLower(value)]
			if mode != "" {
				cmds = append(cmds, "appops set --uid "+appID+" "+op+" "+mode)
				if mode != "allow" {
					opModes[op] = mode
				}
			}
			return
		}
		switch strings.ToLower(value) {
		case "allow":
			if !granted {
				cmds = append(cmds, "pm grant "+appID+" "+perm+" 2>/dev/null")
			}
		case "deny":
			if granted {
				cmds = append(cmds, "pm revoke "+appID+" "+perm+" 2>/dev/null")
				expectKill = true
			}
		case "unset":
			cmds = append(cmds, "pm revoke "+appID+" "+perm+" 2>/dev/null; pm clear-permission-flags "+appID+" "+perm+" user-set user-fixed 2>/dev/null")
			expectKill = expectKill || granted
		}
	}
	if v, ok := perms["all"]; ok {
		for perm := range requested {
			apply(perm, v)
		}
	}
	for name, v := range perms {
		if name == "all" {
			continue
		}
		if strings.Contains(name, ".") {
			apply(name, v)
			continue
		}
		for _, perm := range permissionGroups[strings.ToLower(name)] {
			apply(perm, v)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	script := strings.Join(cmds, "; ")
	if restoreForeground && !expectKill {
		// Only a change of the app-op's mode kills the app: setting the mode it already has
		// does nothing, and must not wait for a kill that never comes.
		for op, mode := range opModes {
			if before, err := d.uidOpMode(ctx, appID, op); err != nil || before != mode {
				expectKill = true
			}
		}
	}
	if restoreForeground && expectKill && d.appInFront(ctx, appID) {
		return d.setPermissionsInForeground(ctx, appID, script)
	}
	_, _, err = d.Sh(ctx, script)
	return err
}

// uidOpMode is the mode `appops set --uid` last gave the app's app-op: allow, deny, ignore,
// or default when none was set. `appops get --uid` prints it as "Uid mode: OP: mode", and can
// also print the package's own entry, with access times that change on every call.
func (d *Driver) uidOpMode(ctx context.Context, appID, op string) (string, error) {
	out, err := d.shOK(ctx, "appops get --uid "+appID+" "+op)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Uid mode: "+op+":"); ok {
			mode, _, _ := strings.Cut(rest, ";")
			return strings.TrimSpace(mode), nil
		}
	}
	return "default", nil
}

// appInFront says whether the app is in the foreground. The agent can miss it for a moment
// while windows change (no active window yet): ask again once, then ask the system.
func (d *Driver) appInFront(ctx context.Context, appID string) bool {
	for range 2 {
		if pkg, err := d.Foreground(ctx); err == nil && pkg != "" {
			return pkg == appID
		}
	}
	out, _, err := d.Sh(ctx, "dumpsys activity activities | grep -m1 -E 'topResumedActivity|mResumedActivity'")
	return err == nil && strings.Contains(out, " "+appID+"/")
}

// permissionKillWait bounds the wait for Android to kill the app after a permission change
// (in tenths of a second). The wait ends as soon as the process is gone.
const permissionKillWait = 50

// Android kills the app's process when it loses a runtime permission it held (`pm revoke`:
// PermissionManagerService, "permissions revoked") or when an app-op such as all-files access
// changes to anything but allow (StorageManagerService, "MANAGE_EXTERNAL_STORAGE changed").
// With the app's only activity gone, the launcher comes to the front. The kill comes from
// system_server and the process can outlive the command that caused it (issue #156: 253 ms on
// a CI emulator), so wait for the old process to go before looking, then bring the task back
// without applying the default launch-time permissions again. When the process outlives the
// wait (a version of Android that does not kill for that change: API 30 may not for a reset
// of all-files access to default), or the app's activity runs in a process of another name
// (android:process=":main", which pidof does not find), decide by what is in front.
func (d *Driver) setPermissionsInForeground(ctx context.Context, appID, script string) error {
	const killed = "tb_app_process_killed"
	out, _, err := d.Sh(ctx, "pid=$(pidof "+appID+"); "+script+"; "+
		"if [ -n \"$pid\" ]; then i=0; while [ \"$(pidof "+appID+")\" = \"$pid\" ] && [ $i -lt "+strconv.Itoa(permissionKillWait)+" ]; do sleep 0.1; i=$((i+1)); done; "+
		"if [ \"$(pidof "+appID+")\" != \"$pid\" ]; then echo "+killed+"; fi; fi")
	if err != nil {
		return err
	}
	if !strings.Contains(out, killed) && d.appInFront(ctx, appID) {
		return nil
	}
	comp, err := d.launcher(ctx, appID)
	if err != nil {
		return err
	}
	start := strings.Join(launcherArgs(comp, resumeTask), " ")
	return d.startApp(ctx, appID, start, start)
}

func (d *Driver) InstallApp(ctx context.Context, path string) error {
	_, err := d.adb.Run(ctx, "install", "-r", "-g", path)
	return err
}

func (d *Driver) Foreground(ctx context.Context) (string, error) {
	m, err := d.call(ctx, "foreground", nil, 5*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Pkg string `json:"pkg"`
	}
	m.Decode(&r)
	return r.Pkg, nil
}

// clearCmd clears an app's data on the device, and stops the script with a marker when
// Android did not (it prints "Success" when it did). Some vendors' Android (Oppo, Realme,
// Vivo, ...) refuse a shell's `pm clear` unless a developer option allows it: going on would
// run the flow on the app's old data.
//
// Android removes the app's tasks with its data, and the removal kills the app's processes as
// it ends. With another app's activity in the task (issue #281: Google Play services' Location
// Accuracy dialog) it ends only once that activity is destroyed, in the other app's process,
// after `pm clear` returned: a start sent before then had its new process killed, and the app
// never came to the front ("in front: unknown"). So the clear ends once no task of the app is
// listed (taskGoneCmd; normally the first look finds none). Where Android keeps the task, the
// wait runs out (traced) and the clean start clears the task instead.
func clearCmd(appID string) string {
	return `r=$(pm clear ` + appID + ` 2>&1); case "$r" in *Success*) ;; *) echo "TB_CLEAR_FAILED: $r"; exit 1;; esac; ` +
		taskGoneCmd(appID)
}

// taskGoneCmd waits until no task of appID for the current user is listed, polling every
// 50 ms, for up to taskGoneWait polls (5 s); when the wait runs out it prints taskStayed.
// Without `cmd` (or its stack list) nothing is listed, and it returns at once.
func taskGoneCmd(appID string) string {
	return `u=$(cmd activity get-current-user 2>/dev/null); i=0; while ` + stackList + ` 2>/dev/null | grep -F ': ` + appID + `/' | grep -qF "${u:+ userId=$u }"; ` +
		`do [ $i -ge ` + strconv.Itoa(taskGoneWait) + ` ] && { echo ` + taskStayed + `; break; }; sleep 0.05; i=$((i+1)); done`
}

const (
	taskGoneWait = 100
	taskStayed   = "TB_TASK_STAYED"
)

// stackList lists the tasks: under each root task ("RootTask id=N", or "Stack id=N" on older
// versions), a line for each task, "taskId=12: <base activity> ... userId=0 visible=true
// topActivity=ComponentInfo{<top activity>}" (Android 8 and later). From Android 10 a
// fullscreen app's task is a root task of its own and topActivity is that task's top; before,
// a stack holds several apps' tasks, and each task's line gives the stack's visibility and top.
const stackList = "cmd activity stack list"

// clearFailed is the error for a clearCmd that did not clear, or nil.
func clearFailed(appID, out string) error {
	i := strings.Index(out, "TB_CLEAR_FAILED:")
	if i < 0 {
		return nil
	}
	why := strings.TrimSpace(out[i+len("TB_CLEAR_FAILED:"):])
	if why == "" {
		why = "no answer"
	}
	return fmt.Errorf("could not clear the data of %s (Android said: %s). On some phones (Oppo, Realme, Vivo, Xiaomi) allow it in Developer options: turn off \"Verify apps over USB\" / turn on \"Disable permission monitoring\"", appID, why)
}
