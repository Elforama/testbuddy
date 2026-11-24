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
	var script []string
	if o.StopApp || o.ClearState {
		script = append(script, "am force-stop "+appID)
	}
	if o.ClearState {
		script = append(script, clearCmd(appID))
	}
	allAllow := len(o.Permissions) == 0 || (len(o.Permissions) == 1 && strings.EqualFold(o.Permissions["all"], "allow"))
	if allAllow {
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
	var args []string
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
	var start string
	if o.StopApp || o.ClearState {
		start = withExtras(cleanTask)
	} else {
		if !allAllow {
			start = pkgDump(appID) + "; "
		}
		start += "f=" + resumeTask + "; case \"$(" + fromDump + " | grep -m1 -o ' stopped=[a-z]*')\" in *=true) f=" + cleanTask + ";; esac; " +
			withExtras("$f")
	}
	script = append(script, start)
	return d.startApp(ctx, appID, strings.Join(script, "; "), withExtras(resumeTask))
}

const (
	resumeTask = "0x10200000"
	cleanTask  = "0x10008000"
)

func pkgDump(appID string) string { return "dp=$(dumpsys package " + appID + ")" }

const fromDump = `printf '%s\n' "$dp"`

func launcherArgs(comp, flags string) []string {
	return []string{"am", "start", "-n", comp, "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER", "-f", flags}
}

const launchWait = 20 * time.Second

const restartAfter = 5 * time.Second

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
	if !ci.On() {
		found, err := d.awaitApp(ctx, appID, launchWait)
		if err != nil || found {
			return err
		}
		return fmt.Errorf("%s did not come to the foreground within %s (am start said: %s; in front: %s)",
			appID, launchWait, amSaid(out), d.front(ctx, appID))
	}
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

func (d *Driver) sdkLevel() int {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.sdk
}

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
			if user != "" && strings.Contains(t, ": "+appID+"/") && strings.Contains(t, " userId="+user+" ") {
				mine++
			} else {
				others++
			}
		}
	}
	flush()
	return roots
}

func (d *Driver) noteTaskStayed(appID, out string) {
	i := strings.Index(out, taskStayed)
	if i < 0 {
		return
	}
	waited := fmt.Sprintf("%d polls after", taskGoneWait)
	rest, _, _ := strings.Cut(out[i+len(taskStayed):], "\n")
	if ms, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(rest), "ms")); err == nil {
		waited = (time.Duration(ms) * time.Millisecond).String() + " after"
	}
	d.note(fmt.Sprintf("a task of %s was still listed %s it was removed (cleared or stopped); the next launch clears it", appID, waited))
}

func (d *Driver) KillApp(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
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

const linkWait = 20 * time.Second

func linkReact() time.Duration {
	if ci.On() {
		return time.Second
	}
	return 300 * time.Millisecond
}

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

func boundedAM(wait time.Duration, args string) string {
	dev := wait - 500*time.Millisecond
	if wait < time.Second {
		dev = wait / 2
	}
	secs := strconv.FormatFloat(float64(dev.Milliseconds())/1000, 'f', -1, 64)
	return "t=; command -v timeout >/dev/null && t='timeout " + secs + "'; a=am; command -v cmd >/dev/null && a='cmd activity'; $t $a " + args
}

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

var appOpPermissions = map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": "MANAGE_EXTERNAL_STORAGE"}

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
			requested[line] = false
		}
	}
	var cmds []string
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

func (d *Driver) appInFront(ctx context.Context, appID string) bool {
	for range 2 {
		if pkg, err := d.Foreground(ctx); err == nil && pkg != "" {
			return pkg == appID
		}
	}
	out, _, err := d.Sh(ctx, "dumpsys activity activities | grep -m1 -E 'topResumedActivity|mResumedActivity'")
	return err == nil && strings.Contains(out, " "+appID+"/")
}

const permissionKillWait = 50

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

func clearCmd(appID string) string {
	return `r=$(pm clear ` + appID + ` 2>&1); case "$r" in *Success*) ;; *) echo "TB_CLEAR_FAILED: $r"; exit 1;; esac; ` +
		taskGoneCmd(appID)
}

func taskGoneCmd(appID string) string {
	return `u=$(cmd activity get-current-user 2>/dev/null); t0=; read t0 x 2>/dev/null </proc/uptime; i=0; ` +
		`while ` + stackList + ` 2>/dev/null | grep -F ': ` + appID + `/' | grep -qF "${u:+ userId=$u }"; do ` +
		`t=; read t x 2>/dev/null </proc/uptime; e=; [ -n "$t0" ] && [ -n "$t" ] && e=$(( ${t%.*}${t#*.} - ${t0%.*}${t0#*.} )); ` +
		`if [ $i -ge ` + strconv.Itoa(taskGoneWait) + ` ] || [ "${e:-0}" -ge ` + strconv.Itoa(taskGoneMs/10) + ` ]; then echo "` + taskStayed + `${e:+ ${e}0ms}"; break; fi; ` +
		`sleep 0.05; i=$((i+1)); done`
}

const (
	taskGoneMs   = 5000
	taskGoneWait = 100
	taskStayed   = "TB_TASK_STAYED"
)

const stackList = "cmd activity stack list"

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
