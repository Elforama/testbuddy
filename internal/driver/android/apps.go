package android

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

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
		// Grant only what is not granted yet, deciding on the device (dumpsys is large).
		script = append(script, "for p in $(dumpsys package "+appID+" | grep -E '^ +android.permission.[A-Z_]+: granted=false|^ +android.permission.MANAGE_EXTERNAL_STORAGE$' | sed 's/:.*//' | sort -u); do case $p in android.permission.MANAGE_EXTERNAL_STORAGE) appops set --uid "+appID+" MANAGE_EXTERNAL_STORAGE allow;; *) pm grant "+appID+" $p 2>/dev/null;; esac; done")
	} else {
		if len(script) > 0 {
			if _, err := d.shOK(ctx, strings.Join(script, "; ")); err != nil {
				return err
			}
			script = nil
		}
		if err := d.setPermissions(ctx, appID, o.Permissions, false); err != nil {
			return err
		}
	}
	args := launcherArgs(comp)
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
	script = append(script, strings.Join(args, " "))
	return d.startApp(ctx, appID, strings.Join(script, "; "))
}

func launcherArgs(comp string) []string {
	return []string{"am", "start", "-n", comp, "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LAUNCHER", "-f", "0x10200000"}
}

func (d *Driver) startApp(ctx context.Context, appID, script string) error {
	out, err := d.shOK(ctx, script)
	if err := clearFailed(appID, out); err != nil {
		return err
	}
	if err != nil {
		return err
	}
	if strings.Contains(out, "Error:") {
		return fmt.Errorf("could not start %s: %s", appID, strings.TrimSpace(out))
	}
	// Not `am start -W`: the agent tells us when the app's window is up (an event).
	return d.WaitForApp(ctx, appID, 20*time.Second)
}

func (d *Driver) WaitForApp(ctx context.Context, appID string, timeout time.Duration) error {
	m, err := d.call(ctx, "awaitPackage", map[string]any{"pkg": appID, "timeout": timeout.Milliseconds()}, timeout+5*time.Second)
	if err != nil {
		return err
	}
	var r struct {
		Found bool `json:"found"`
	}
	m.Decode(&r)
	if !r.Found {
		return fmt.Errorf("%s did not come to the foreground within %s", appID, timeout)
	}
	return nil
}

func (d *Driver) StopApp(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	_, err := d.shOK(ctx, "am force-stop "+appID)
	return err
}

func (d *Driver) KillApp(ctx context.Context, appID string) error {
	if err := validPkg(appID); err != nil {
		return err
	}
	// Like the system killing it in the background (issue #6): `am kill` ends the process and
	// keeps its task and saved state, so the next launch restores where the user was. No
	// force-stop, which clears that state (that is stopApp). `am kill` only ends a background
	// app, so one that survives it is sent to the background (Home) and killed again.
	kill := "am kill " + appID + "; ! pidof " + appID + " >/dev/null"
	script := kill + " && exit 0; input keyevent KEYCODE_HOME; " +
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
	return err
}

func (d *Driver) ClearKeychain(ctx context.Context) error { return nil }

// OpenLink starts a VIEW intent for link. It is aimed at the flow's app first (no chooser
// when several apps take the link), unless the browser is asked for; when the app takes no
// such link (a web page, another app's link) it goes out again with no app, and Android
// picks the handler.
func (d *Driver) OpenLink(ctx context.Context, link, appID string, autoVerify, browser bool) error {
	cmd := "am start -a android.intent.action.VIEW -d " + shellQuote(link)
	if appID != "" && !browser {
		if err := validPkg(appID); err != nil {
			return err
		}
		out, err := d.shOK(ctx, cmd+" "+appID)
		if err == nil || !strings.Contains(out, "unable to resolve Intent") {
			return err
		}
	}
	_, err := d.shOK(ctx, cmd)
	return err
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
	var revokesAppOp bool
	apply := func(perm, value string) {
		granted, ok := requested[perm]
		if !ok {
			return
		}
		if op, isOp := appOpPermissions[perm]; isOp {
			mode := map[string]string{"allow": "allow", "deny": "deny", "unset": "default"}[strings.ToLower(value)]
			if mode != "" {
				cmds = append(cmds, "appops set --uid "+appID+" "+op+" "+mode)
				revokesAppOp = revokesAppOp || mode != "allow"
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
			}
		case "unset":
			cmds = append(cmds, "pm revoke "+appID+" "+perm+" 2>/dev/null; pm clear-permission-flags "+appID+" "+perm+" user-set user-fixed 2>/dev/null")
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
	if restoreForeground && revokesAppOp {
		foreground, _ := d.Foreground(ctx)
		if foreground == appID {
			return d.setPermissionsInForeground(ctx, appID, script)
		}
	}
	_, _, err = d.Sh(ctx, script)
	return err
}

// Android may kill the foreground process after revoking an app-op. Bring the task
// back if it was killed, without applying the default launch-time permissions again.
func (d *Driver) setPermissionsInForeground(ctx context.Context, appID, script string) error {
	const killed = "tb_app_process_killed"
	out, _, err := d.Sh(ctx, "pid=$(pidof "+appID+"); "+script+"; "+
		"if [ -n \"$pid\" ] && [ \"$(pidof "+appID+")\" != \"$pid\" ]; then echo "+killed+"; fi")
	if err != nil || !strings.Contains(out, killed) {
		return err
	}
	comp, err := d.launcher(ctx, appID)
	if err != nil {
		return err
	}
	return d.startApp(ctx, appID, strings.Join(launcherArgs(comp), " "))
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
func clearCmd(appID string) string {
	return `r=$(pm clear ` + appID + ` 2>&1); case "$r" in *Success*) ;; *) echo "TB_CLEAR_FAILED: $r"; exit 1;; esac`
}

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
