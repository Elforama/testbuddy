package android

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// shAgent answers sh requests from a table of (substring → out, code), recording the commands.
func shAgent(t *testing.T, answers map[string]struct {
	out  string
	code int
}) *fakeAgent {
	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		cmd := fmt.Sprint(req["cmd"])
		best := ""
		for sub := range answers {
			if strings.Contains(cmd, sub) && len(sub) > len(best) {
				best = sub
			}
		}
		if best == "" {
			return map[string]any{"code": 0, "out": ""}
		}
		return map[string]any{"code": answers[best].code, "out": answers[best].out}
	})
	a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": true} })
	return a
}

type shAnswer = struct {
	out  string
	code int
}

const resolveOut = "priority=0 preferredOrder=0 match=0x108000 specificIndex=-1 isDefault=true\ncom.example.app/.MainActivity\n"

// The launcher activity is the last line of resolve-activity, cached per package.
func TestLauncher(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	for i := 0; i < 2; i++ {
		c, err := d.launcher(ctxT(t), "com.example.app")
		if err != nil || c != "com.example.app/.MainActivity" {
			t.Fatalf("launcher = %q, %v", c, err)
		}
	}
	if n := len(a.requests("sh")); n != 1 {
		t.Errorf("%d resolve calls, want 1 (cached)", n)
	}
	want := "cmd package resolve-activity --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER com.example.app"
	if got := a.shCmds()[0]; got != want {
		t.Errorf("command %q", got)
	}
}

// A package with no launcher (no component in the output, or a failed command) is an error.
func TestLauncherMissing(t *testing.T) {
	for name, ans := range map[string]shAnswer{
		"no activity": {"No activity found\n", 0},
		"failed":      {"", 1},
	} {
		t.Run(name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"resolve-activity": ans})
			d := newDriver(t, a)
			_, err := d.launcher(ctxT(t), "com.missing")
			if err == nil || !strings.Contains(err.Error(), "not installed or has no launcher activity") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

const grantAll = "for p in $(dumpsys package com.example.app | grep -E '^ +android.permission.[A-Z_]+: granted=false' | sed 's/:.*//' | sort -u); do pm grant com.example.app $p 2>/dev/null; done"

const amStart = "am start -n com.example.app/.MainActivity -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f 0x10200000"

// LaunchApp runs one script (stop, clear, grant missing permissions, start), then waits for the app's window.
func TestLaunchAppScript(t *testing.T) {
	cases := []struct {
		name string
		o    driver.LaunchOpts
		want string
	}{
		{"plain", driver.LaunchOpts{}, grantAll + "; " + amStart},
		{"stop", driver.LaunchOpts{StopApp: true}, "am force-stop com.example.app; " + grantAll + "; " + amStart},
		{"clear", driver.LaunchOpts{ClearState: true}, "am force-stop com.example.app; " + clearCmd("com.example.app") + "; " + grantAll + "; " + amStart},
		{"all allow", driver.LaunchOpts{Permissions: map[string]string{"all": "ALLOW"}}, grantAll + "; " + amStart},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {"Starting: Intent { ... }\n", 0}})
			d := newDriver(t, a)
			if err := d.LaunchApp(ctxT(t), "com.example.app", c.o); err != nil {
				t.Fatal(err)
			}
			cmds := a.shCmds()
			if len(cmds) != 2 || cmds[1] != c.want {
				t.Errorf("commands:\n%s\nwant launch script:\n%s", strings.Join(cmds, "\n"), c.want)
			}
			w := a.last("awaitPackage")
			if w["pkg"] != "com.example.app" || num(t, w, "timeout") != 20000 {
				t.Errorf("awaitPackage %v", w)
			}
		})
	}
}

// Launch arguments become typed intent extras, keys and string values shell-quoted.
func TestLaunchAppArguments(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	args := map[string]any{"flag": true, "count": 3, "big": int64(1) << 40, "whole": 2.0, "ratio": 0.25,
		"name": "it's me", "list": []int{1, 2}}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{Arguments: args}); err != nil {
		t.Fatal(err)
	}
	script := a.shCmds()[1]
	for _, want := range []string{
		"--ez 'flag' true", "--ei 'count' 3", "--el 'big' 1099511627776", "--ei 'whole' 2", "--ef 'ratio' 0.25",
		`--es 'name' 'it'\''s me'`, "--es 'list' '[1 2]'",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	if !strings.HasPrefix(script, grantAll+"; "+amStart+" --") {
		t.Errorf("extras do not follow am start:\n%s", script)
	}
}

// Whole numbers that do not fit an int32 (am's --ei) should go as --el.
func TestLaunchAppLargeIntegerArgument(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{Arguments: map[string]any{"ts": 1.7e12, "n": 1 << 40}}); err != nil {
		t.Fatal(err)
	}
	script := a.shCmds()[1]
	for _, want := range []string{"--el 'ts' 1700000000000", "--el 'n' 1099511627776"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
}

// Explicit permissions run stop/clear first, then SetPermissions, then a script that only starts the app.
func TestLaunchAppWithPermissions(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{
		"resolve-activity": {resolveOut, 0},
		"dumpsys package":  {"    android.permission.CAMERA: granted=false, flags=[ USER_SET ]\n", 0},
	})
	d := newDriver(t, a)
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true, Permissions: map[string]string{"camera": "allow"}}); err != nil {
		t.Fatal(err)
	}
	cmds := a.shCmds()
	want := []string{
		"cmd package resolve-activity --brief -a android.intent.action.MAIN -c android.intent.category.LAUNCHER com.example.app",
		"am force-stop com.example.app",
		"dumpsys package com.example.app | grep -E '^ +android.permission.[A-Z_]+: granted=|^ +android.permission.MANAGE_EXTERNAL_STORAGE$' | sort -u",
		"pm grant com.example.app android.permission.CAMERA 2>/dev/null",
		amStart,
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(cmds, "\n"), strings.Join(want, "\n"))
	}
}

// LaunchApp fails on a bad id, a missing app, am's "Error:" output, a failing script, or no window.
func TestLaunchAppFailures(t *testing.T) {
	cases := []struct {
		name    string
		app     string
		answers map[string]shAnswer
		found   bool
		want    string
	}{
		{"bad id", "com.app; reboot", nil, true, "invalid app id"},
		{"not installed", "com.example.app", map[string]shAnswer{"resolve-activity": {"No activity found", 0}}, true, "not installed"},
		{"am error", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {"Error: Activity class does not exist.\n", 0}}, true, "could not start com.example.app: Error: Activity class"},
		{"script fails", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {"boom", 255}}, true, "exit 255: boom"},
		{"never shows", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}}, false, "did not come to the foreground within 20s"},
		{"stop fails before permissions", "com.example.app", map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "force-stop": {"no", 1}}, true, "exit 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, c.answers)
			a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": c.found} })
			d := newDriver(t, a)
			o := driver.LaunchOpts{}
			if c.name == "stop fails before permissions" {
				o = driver.LaunchOpts{StopApp: true, Permissions: map[string]string{"camera": "deny"}}
			}
			err := d.LaunchApp(ctxT(t), c.app, o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// WaitForApp asks the agent to wait for the package's window, with the timeout in ms.
func TestWaitForApp(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	if err := d.WaitForApp(ctxT(t), "com.example.app", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if r := a.last("awaitPackage"); r["pkg"] != "com.example.app" || num(t, r, "timeout") != 1500 {
		t.Errorf("request %v", r)
	}
	a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "x"} })
	if err := d.WaitForApp(ctxT(t), "com.example.app", time.Second); err == nil {
		t.Error("agent error not reported")
	}
}

// Stop and clear run their am/pm commands, and validate the id first (kill: kill_test.go).
func TestAppCommands(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	ctx := ctxT(t)
	cases := []struct {
		run  func(string) error
		want string
	}{
		{func(p string) error { return d.StopApp(ctx, p) }, "am force-stop com.example.app"},
		{func(p string) error { return d.ClearState(ctx, p) }, clearCmd("com.example.app")},
	}
	for _, c := range cases {
		a.reset()
		if err := c.run("com.example.app"); err != nil {
			t.Fatal(err)
		}
		if got := a.shCmds(); len(got) != 1 || got[0] != c.want {
			t.Errorf("commands %v, want %q", got, c.want)
		}
		a.reset()
		if err := c.run("bad id"); err == nil || len(a.requests("")) != 0 {
			t.Errorf("bad id: %v, sent %v", err, a.ops())
		}
	}
}

// OpenLink starts a VIEW intent, aimed at the app unless the browser is asked for.
func TestOpenLink(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	ctx := ctxT(t)
	cases := []struct {
		link, app string
		browser   bool
		want      string
	}{
		{"https://example.com/a?b=c&d", "", false, "am start -a android.intent.action.VIEW -d 'https://example.com/a?b=c&d'"},
		{"myapp://x", "com.example.app", false, "am start -a android.intent.action.VIEW -d 'myapp://x' com.example.app"},
		{"https://e.com/it's", "com.example.app", true, `am start -a android.intent.action.VIEW -d 'https://e.com/it'\''s'`},
	}
	for _, c := range cases {
		a.reset()
		if err := d.OpenLink(ctx, c.link, c.app, false, c.browser); err != nil {
			t.Fatal(err)
		}
		if got := a.shCmds(); len(got) != 1 || got[0] != c.want {
			t.Errorf("OpenLink(%q, %q, %v) ran %v, want %q", c.link, c.app, c.browser, got, c.want)
		}
	}
	a.reset()
	if err := d.OpenLink(ctx, "x://", "bad;id", false, false); err == nil || len(a.requests("")) != 0 {
		t.Errorf("bad app id: %v", err)
	}
}

// A link the flow's app does not handle (a web page, another app's link: Maestro's e2e
// demo_app openLink.yaml opens https://www.saucedemo.com/) is not refused: when the intent
// aimed at the app resolves to nothing, it goes out again with no app, and Android picks the
// handler, as Maestro's runner does. Another failure (a bad link) is not retried.
func TestOpenLinkOtherAppsLink(t *testing.T) {
	pinned := "am start -a android.intent.action.VIEW -d 'https://www.saucedemo.com/' com.example.example"
	a := shAgent(t, map[string]shAnswer{
		pinned: {out: "Starting: Intent { act=android.intent.action.VIEW dat=https://www.saucedemo.com/... pkg=com.example.example }\n" +
			"Error: Activity not started, unable to resolve Intent { act=android.intent.action.VIEW dat=https://www.saucedemo.com/... flg=0x10000000 pkg=com.example.example }", code: 1},
	})
	d := newDriver(t, a)
	if err := d.OpenLink(ctxT(t), "https://www.saucedemo.com/", "com.example.example", false, false); err != nil {
		t.Fatal(err)
	}
	want := []string{pinned, "am start -a android.intent.action.VIEW -d 'https://www.saucedemo.com/'"}
	if got := a.shCmds(); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q\nwant %q", got, want)
	}
	b := shAgent(t, map[string]shAnswer{"am start": {out: "Error: bad URI", code: 1}})
	d = newDriver(t, b)
	if err := d.OpenLink(ctxT(t), "x://", "com.example.example", false, false); err == nil || len(b.shCmds()) != 1 {
		t.Errorf("another failure: %v after %q, want an error after one try", err, b.shCmds())
	}
}

const dumpsysPerms = `    android.permission.CAMERA: granted=false, flags=[ USER_SENSITIVE_WHEN_GRANTED ]
    android.permission.ACCESS_FINE_LOCATION: granted=true, flags=[ USER_SET ]
    android.permission.ACCESS_COARSE_LOCATION: granted=false
    android.permission.RECORD_AUDIO: granted=true
    android.permission.POST_NOTIFICATIONS: granted=false
    com.example.permission.C2D: granted=true
`

// SetPermissions grants, revokes or resets only permissions the app requests, and only what changes.
func TestSetPermissions(t *testing.T) {
	const app = "com.example.app"
	grant := func(p string) string { return "pm grant " + app + " android.permission." + p + " 2>/dev/null" }
	revoke := func(p string) string { return "pm revoke " + app + " android.permission." + p + " 2>/dev/null" }
	cases := []struct {
		name  string
		perms map[string]string
		want  []string // commands of the second sh, in any order
	}{
		{"empty grants all", nil, []string{grant("CAMERA"), grant("ACCESS_COARSE_LOCATION"), grant("POST_NOTIFICATIONS")}},
		{"all deny", map[string]string{"all": "deny"}, []string{revoke("ACCESS_FINE_LOCATION"), revoke("RECORD_AUDIO")}},
		{"group allow", map[string]string{"camera": "allow"}, []string{grant("CAMERA")}},
		{"group case-insensitive", map[string]string{"Camera": "ALLOW"}, []string{grant("CAMERA")}},
		{"group deny", map[string]string{"location": "deny"}, []string{revoke("ACCESS_FINE_LOCATION")}},
		{"already granted", map[string]string{"microphone": "allow"}, nil},
		{"not requested", map[string]string{"contacts": "allow", "sms": "deny"}, nil},
		{"qualified name", map[string]string{"android.permission.RECORD_AUDIO": "deny"}, []string{revoke("RECORD_AUDIO")}},
		{"unset", map[string]string{"camera": "unset"}, []string{revoke("CAMERA") + "; pm clear-permission-flags " + app + " android.permission.CAMERA user-set user-fixed 2>/dev/null"}},
		{"unknown value", map[string]string{"camera": "sometimes"}, nil},
		{"unknown group", map[string]string{"telepathy": "allow"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"dumpsys package": {dumpsysPerms, 0}})
			d := newDriver(t, a)
			if err := d.SetPermissions(ctxT(t), app, c.perms); err != nil {
				t.Fatal(err)
			}
			cmds := a.shCmds()
			if cmds[0] != "dumpsys package "+app+" | grep -E '^ +android.permission.[A-Z_]+: granted=|^ +android.permission.MANAGE_EXTERNAL_STORAGE$' | sort -u" {
				t.Errorf("first command %q", cmds[0])
			}
			if len(c.want) == 0 {
				if len(cmds) != 1 {
					t.Errorf("ran %v, want only the dumpsys", cmds)
				}
				return
			}
			if len(cmds) != 2 {
				t.Fatalf("ran %v", cmds)
			}
			got := strings.Split(cmds[1], "; ")
			want := strings.Split(strings.Join(c.want, "; "), "; ")
			if !sameSet(got, want) {
				t.Errorf("commands %v\nwant %v", got, want)
			}
		})
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

// SetPermissions validates the id and reports a failed dumpsys.
// All-files access (MANAGE_EXTERNAL_STORAGE) is an app-op permission: dumpsys lists it only
// as requested (no granted= state), and pm grant does not apply it. It is set with appops,
// as Maestro's runner does (Maestro's e2e demo_app all_files_permission.yaml: launchApp's
// default grant, all: allow, an explicit deny).
func TestSetPermissionsAppOp(t *testing.T) {
	const app = "com.example.example"
	dumpsys := dumpsysPerms + "      android.permission.MANAGE_EXTERNAL_STORAGE\n"
	op := func(v string) string { return "appops set --uid " + app + " MANAGE_EXTERNAL_STORAGE " + v }
	for _, c := range []struct {
		perms map[string]string
		want  string
	}{
		{nil, op("allow")},
		{map[string]string{"all": "deny"}, op("deny")},
		{map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": "allow"}, op("allow")},
		{map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": "unset"}, op("default")},
	} {
		a := shAgent(t, map[string]shAnswer{"dumpsys package": {dumpsys, 0}})
		d := newDriver(t, a)
		if err := d.SetPermissions(ctxT(t), app, c.perms); err != nil {
			t.Fatal(err)
		}
		cmds := a.shCmds()
		if len(cmds) < 2 || !strings.Contains(cmds[len(cmds)-1], c.want) {
			t.Errorf("%v: ran %q, want %q among the changes", c.perms, cmds, c.want)
		}
	}
}

func TestSetPermissionsErrors(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	if err := d.SetPermissions(ctxT(t), "", nil); err == nil {
		t.Error("empty id accepted")
	}
	a.on("sh", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "dead"} })
	if err := d.SetPermissions(ctxT(t), "com.example.app", nil); err == nil {
		t.Error("sh failure not reported")
	}
}

// App commands report a failed shell request; LaunchApp reports a failed permission step.
func TestAppCommandErrors(t *testing.T) {
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}})
	d := newDriver(t, a)
	if _, err := d.launcher(ctxT(t), "com.example.app"); err != nil {
		t.Fatal(err)
	}
	a.on("sh", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "dead"} })
	if err := d.StopApp(ctxT(t), "com.example.app"); err == nil {
		t.Error("StopApp: error not reported")
	}
	if err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{Permissions: map[string]string{"camera": "deny"}}); err == nil {
		t.Error("LaunchApp: permission failure not reported")
	}
}

// A clear Android refuses (some vendors' builds) fails launchApp and clearState with the reason,
// instead of running the flow on the app's old data.
func TestClearStateRefused(t *testing.T) {
	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		cmd, _ := req["cmd"].(string)
		if strings.Contains(cmd, "pm clear") {
			return map[string]any{"code": 1, "out": "TB_CLEAR_FAILED: java.lang.SecurityException: PID 123 does not have permission\n"}
		}
		return map[string]any{"code": 0, "out": ""}
	})
	d := stubbedDriver(t, stubADB(t, nil), a)
	d.launch["com.example.app"] = "com.example.app/.Main"
	err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{ClearState: true})
	if err == nil || !strings.Contains(err.Error(), "could not clear the data of com.example.app") || !strings.Contains(err.Error(), "SecurityException") {
		t.Errorf("LaunchApp = %v", err)
	}
	if err := d.ClearState(ctxT(t), "com.example.app"); err == nil || !strings.Contains(err.Error(), "Developer options") {
		t.Errorf("ClearState = %v", err)
	}
	if clearFailed("x", "Success\nStarting: Intent") != nil {
		t.Error("a successful clear reported as failed")
	}
}
