package android

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

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

const grantAll = "dp=$(dumpsys package com.example.app); for p in $(printf '%s\\n' \"$dp\" | grep -E '^ +android.permission.[A-Z_]+: granted=false|^ +android.permission.MANAGE_EXTERNAL_STORAGE$' | sed 's/:.*//' | sort -u); do case $p in android.permission.MANAGE_EXTERNAL_STORAGE) appops set --uid com.example.app MANAGE_EXTERNAL_STORAGE allow;; *) pm grant com.example.app $p 2>/dev/null;; esac; done"

const amStart = "am start -n com.example.app/.MainActivity -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f 0x10200000"

const amFresh = "am start -n com.example.app/.MainActivity -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f 0x10008000"

const amResume = "f=0x10200000; case \"$(printf '%s\\n' \"$dp\" | grep -m1 -o ' stopped=[a-z]*')\" in *=true) f=0x10008000;; esac; " +
	"am start -n com.example.app/.MainActivity -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -f $f"

func TestLaunchAppScript(t *testing.T) {
	t.Setenv("TB_CI", "0")
	cases := []struct {
		name string
		o    driver.LaunchOpts
		want string
	}{
		{"plain", driver.LaunchOpts{}, grantAll + "; " + amResume},
		{"stop", driver.LaunchOpts{StopApp: true}, "am force-stop com.example.app; " + grantAll + "; " + amFresh},
		{"clear", driver.LaunchOpts{ClearState: true}, "am force-stop com.example.app; " + clearCmd("com.example.app") + "; " + grantAll + "; " + amFresh},
		{"all allow", driver.LaunchOpts{Permissions: map[string]string{"all": "ALLOW"}}, grantAll + "; " + amResume},
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
	if !strings.HasPrefix(script, grantAll+"; "+amResume+" --") {
		t.Errorf("extras do not follow am start:\n%s", script)
	}
}

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
		amFresh,
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(cmds, "\n"), strings.Join(want, "\n"))
	}
}

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

func TestAppCommands(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	ctx := ctxT(t)
	cases := []struct {
		run  func(string) error
		want string
	}{
		{func(p string) error { return d.StopApp(ctx, p) }, "am force-stop com.example.app; echo TB_USER=$(cmd activity get-current-user 2>/dev/null); cmd activity stack list 2>/dev/null; true"},
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

func TestOpenLink(t *testing.T) {
	a := shAgent(t, nil)
	d := newDriver(t, a)
	ctx := ctxT(t)
	cases := []struct {
		link, app string
		browser   bool
		want      string
	}{
		{"https://example.com/a?b=c&d", "", false, "start -W -a android.intent.action.VIEW -d 'https://example.com/a?b=c&d'"},
		{"myapp://x", "com.example.app", false, "start -W -a android.intent.action.VIEW -d 'myapp://x' com.example.app"},
		{"https://e.com/it's", "com.example.app", true, `start -W -a android.intent.action.VIEW -d 'https://e.com/it'\''s'`},
	}
	for _, c := range cases {
		a.reset()
		if err := d.OpenLink(ctx, c.link, c.app, false, c.browser); err != nil {
			t.Fatal(err)
		}
		if got := linkArgs(a.shCmds()); len(got) != 1 || got[0] != c.want {
			t.Errorf("OpenLink(%q, %q, %v) ran %v, want %q", c.link, c.app, c.browser, got, c.want)
		}
	}
	a.reset()
	if err := d.OpenLink(ctx, "x://", "bad;id", false, false); err == nil || len(a.requests("")) != 0 {
		t.Errorf("bad app id: %v", err)
	}
}

func TestOpenLinkOtherAppsLink(t *testing.T) {
	pinned := "start -W -a android.intent.action.VIEW -d 'https://www.saucedemo.com/' com.example.example"
	a := shAgent(t, map[string]shAnswer{
		pinned: {out: "Starting: Intent { act=android.intent.action.VIEW dat=https://www.saucedemo.com/... pkg=com.example.example }\n" +
			"Error: Activity not started, unable to resolve Intent { act=android.intent.action.VIEW dat=https://www.saucedemo.com/... flg=0x10000000 pkg=com.example.example }", code: 1},
	})
	d := newDriver(t, a)
	if err := d.OpenLink(ctxT(t), "https://www.saucedemo.com/", "com.example.example", false, false); err != nil {
		t.Fatal(err)
	}
	want := []string{pinned, "start -W -a android.intent.action.VIEW -d 'https://www.saucedemo.com/'"}
	if got := linkArgs(a.shCmds()); !reflect.DeepEqual(got, want) {
		t.Errorf("ran %q\nwant %q", got, want)
	}
	b := shAgent(t, map[string]shAnswer{"start -W": {out: "Error: bad URI", code: 1}})
	d = newDriver(t, b)
	if err := d.OpenLink(ctxT(t), "x://", "com.example.example", false, false); err == nil || len(b.shCmds()) != 1 {
		t.Errorf("another failure: %v after %q, want an error after one try", err, b.shCmds())
	}
}

func TestOpenLinkWaitsForTheLinkedScreen(t *testing.T) {
	const drawAfter = 300 * time.Millisecond
	var mu sync.Mutex
	opened := false
	open := func() { mu.Lock(); opened = true; mu.Unlock() }
	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		cmd := fmt.Sprint(req["cmd"])
		if !strings.Contains(cmd, "am start") && !strings.Contains(cmd, "$a start") {
			return map[string]any{"code": 0, "out": ""}
		}
		out := "Starting: Intent { act=android.intent.action.VIEW dat=bench://level/... }\n"
		if !strings.Contains(cmd, "start -W ") {
			time.AfterFunc(drawAfter, open)
			return map[string]any{"code": 0, "out": out}
		}
		time.Sleep(drawAfter)
		open()
		return map[string]any{"code": 0, "out": out + "Status: ok\nLaunchState: WARM\nActivity: dev.bench/.NavLevelActivity\nTotalTime: 300\nComplete\n"}
	})
	a.on("tree", func(map[string]any) map[string]any {
		mu.Lock()
		text := "Scenarios"
		if opened {
			text = "Depth 3"
		}
		mu.Unlock()
		return map[string]any{"seq": 1, "windows": []any{map[string]any{
			"id": 7, "type": 1, "active": true, "focused": true, "pkg": "dev.bench", "b": []int{0, 0, 1080, 2400},
			"nodes": []any{map[string]any{"d": 0, "b": []int{0, 0, 1080, 200}, "c": "android.widget.TextView", "t": text, "f": 4098}},
		}}}
	})
	d := newDriver(t, a)
	ctx := ctxT(t)
	if err := d.OpenLink(ctx, "bench://level/3", "dev.bench", false, false); err != nil {
		t.Fatal(err)
	}
	s, err := d.Screen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Nodes) != 1 || s.Nodes[0].Text != "Depth 3" {
		var texts []string
		for _, n := range s.Nodes {
			texts = append(texts, n.Text)
		}
		t.Errorf("the step after openLink read %q, want the linked screen (\"Depth 3\"); ran %q", texts, a.shCmds())
	}
}

func linkArgs(cmds []string) []string {
	var out []string
	for _, c := range cmds {
		if _, args, ok := strings.Cut(c, "$t $a "); ok {
			c = args
		}
		out = append(out, c)
	}
	return out
}

func TestOpenLinkNotDrawnInTime(t *testing.T) {
	for _, res := range []map[string]any{
		{"code": 124, "out": "Starting: Intent { act=android.intent.action.VIEW pkg=dev.bench }\n"},
		{"code": -1, "out": "Starting: Intent { act=android.intent.action.VIEW pkg=dev.bench }\n", "timedOut": true},
	} {
		a := newFakeAgent(t)
		a.on("sh", func(map[string]any) map[string]any { return res })
		d := newDriver(t, a)
		ctx, cancel := context.WithCancel(context.Background())
		if err := d.OpenLink(ctx, "bench://level/3", "dev.bench", false, false); err != nil {
			t.Fatalf("%v: OpenLink = %v, want nil", res, err)
		}
		cancel()
		sh := a.requests("sh")
		if len(sh) != 1 {
			t.Fatalf("%v: ran %q, want only the pinned attempt", res, a.shCmds())
		}
		if got := sh[0]["timeout"]; got != float64(linkWait.Milliseconds()) {
			t.Errorf("sh timeout %v, want %d", got, linkWait.Milliseconds())
		}
		cmd := fmt.Sprint(sh[0]["cmd"])
		for _, want := range []string{"command -v timeout", "t='timeout 19.5'", "a='cmd activity'", "$t $a start -W ", " dev.bench"} {
			if !strings.Contains(cmd, want) {
				t.Errorf("command %q lacks %q", cmd, want)
			}
		}
	}
}

func TestOpenLinkTimedOutWithError(t *testing.T) {
	a := newFakeAgent(t)
	a.on("sh", func(map[string]any) map[string]any {
		return map[string]any{"code": 124, "out": "Starting: Intent { }\nError: Activity not started, bad\n"}
	})
	d := newDriver(t, a)
	if err := d.OpenLink(ctxT(t), "bench://level/3", "", false, false); err == nil || !strings.Contains(err.Error(), "Activity not started, bad") {
		t.Errorf("OpenLink = %v, want the error", err)
	}
}

func TestOpenLinkStepDeadline(t *testing.T) {
	a := newFakeAgent(t)
	d := newDriver(t, a)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.OpenLink(ctx, "bench://level/3", "", false, false); err != nil {
		t.Fatal(err)
	}
	sh := a.last("sh")
	if ms, _ := sh["timeout"].(float64); ms > 2000 || ms < 1500 {
		t.Errorf("sh timeout %v ms, want the 2 s left", sh["timeout"])
	}
	if cmd := fmt.Sprint(sh["cmd"]); !strings.Contains(cmd, "t='timeout 1.") {
		t.Errorf("command %q: the device timeout should be just under 2 s", cmd)
	}
}

const dumpsysPerms = `    android.permission.CAMERA: granted=false, flags=[ USER_SENSITIVE_WHEN_GRANTED ]
    android.permission.ACCESS_FINE_LOCATION: granted=true, flags=[ USER_SET ]
    android.permission.ACCESS_COARSE_LOCATION: granted=false
    android.permission.RECORD_AUDIO: granted=true
    android.permission.POST_NOTIFICATIONS: granted=false
    com.example.permission.C2D: granted=true
`

func TestSetPermissions(t *testing.T) {
	const app = "com.example.app"
	grant := func(p string) string { return "pm grant " + app + " android.permission." + p + " 2>/dev/null" }
	revoke := func(p string) string { return "pm revoke " + app + " android.permission." + p + " 2>/dev/null" }
	cases := []struct {
		name  string
		perms map[string]string
		want  []string
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
			a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": "com.android.launcher3"} })
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

func TestLaunchAppSendsTheStartAgain(t *testing.T) {
	t.Setenv("TB_CI", "1")
	const amOut = "Starting: Intent { ... }\nWarning: Activity not started, its current task has been brought to the front\n"
	for _, c := range []struct {
		name  string
		found []bool
		err   string
	}{
		{"comes on the second start", []bool{false, true}, ""},
		{"never comes", []bool{false, false}, "did not come to the foreground within 20s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {amOut, 0}})
			var mu sync.Mutex
			n := 0
			a.on("awaitPackage", func(map[string]any) map[string]any {
				mu.Lock()
				defer mu.Unlock()
				f := n < len(c.found) && c.found[n]
				n++
				return map[string]any{"found": f}
			})
			a.on("foreground", func(map[string]any) map[string]any {
				return map[string]any{"pkg": "com.google.android.apps.nexuslauncher"}
			})
			d := newDriver(t, a)
			err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true})
			cmds := startCmds(a)
			if len(cmds) != 3 || cmds[1] != "am force-stop com.example.app; "+grantAll+"; "+amFresh || cmds[2] != amStart {
				t.Errorf("commands:\n%s\nwant the launch script (a start in a clean task), then the am start line alone, which keeps the task", strings.Join(cmds, "\n"))
			}
			waits := a.requests("awaitPackage")
			if len(waits) != 2 || num(t, waits[0], "timeout")+num(t, waits[1], "timeout") != 20000 {
				t.Errorf("awaitPackage requests %v, want two waits of 20 s in all", waits)
			}
			if c.err == "" {
				if err != nil {
					t.Errorf("LaunchApp: %v", err)
				}
				return
			}
			for _, want := range []string{c.err, "its current task has been brought to the front", "in front: com.google.android.apps.nexuslauncher", "sent the start twice"} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to say %q", err, want)
				}
			}
		})
	}
}

func TestLaunchAppOutsideCISaysWhatAmStartSaid(t *testing.T) {
	t.Setenv("TB_CI", "0")
	const amOut = "Starting: Intent { ... }\nWarning: Activity not started, its current task has been brought to the front\n"
	a := shAgent(t, map[string]shAnswer{"resolve-activity": {resolveOut, 0}, "am start": {amOut, 0}})
	a.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": false} })
	a.on("foreground", func(map[string]any) map[string]any {
		return map[string]any{"pkg": "com.google.android.apps.nexuslauncher"}
	})
	d := newDriver(t, a)
	err := d.LaunchApp(ctxT(t), "com.example.app", driver.LaunchOpts{StopApp: true})
	if cmds := startCmds(a); len(cmds) != 2 {
		t.Errorf("commands:\n%s\nwant the launch script once, no second start", strings.Join(cmds, "\n"))
	}
	if waits := a.requests("awaitPackage"); len(waits) != 1 || num(t, waits[0], "timeout") != 20000 {
		t.Errorf("awaitPackage requests %v, want one wait of 20 s", waits)
	}
	for _, want := range []string{"did not come to the foreground within 20s", "its current task has been brought to the front", "in front: com.google.android.apps.nexuslauncher"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to say %q", err, want)
		}
	}
}
