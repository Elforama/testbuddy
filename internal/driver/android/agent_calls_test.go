package android

import (
	"strings"
	"testing"
	"time"
)

// Clicks is the agent's view-click counter (driver.ClickCounter).
func TestClicks(t *testing.T) {
	a := newFakeAgent(t)
	a.on("clicks", func(map[string]any) map[string]any { return map[string]any{"clicks": 7} })
	d := newDriver(t, a)
	if n, err := d.Clicks(ctxT(t)); err != nil || n != 7 {
		t.Errorf("Clicks = %d, %v; want 7", n, err)
	}
	a.on("clicks", func(map[string]any) map[string]any { return map[string]any{"clicks": "many"} })
	if _, err := d.Clicks(ctxT(t)); err == nil {
		t.Error("a malformed count was accepted")
	}
	a.on("clicks", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "no service"} })
	if _, err := d.Clicks(ctxT(t)); err == nil || !strings.Contains(err.Error(), "no service") {
		t.Errorf("agent error: %v", err)
	}
}

// AwaitCoverGone sends the cover's identity and the tap point, waits on the agent for at most
// the timeout (the call itself gets a second more), and returns what the agent saw.
func TestAwaitCoverGone(t *testing.T) {
	for _, c := range []struct {
		observed, gone bool
	}{{true, true}, {true, false}, {false, false}} {
		a := newFakeAgent(t)
		a.on("awaitCoverGone", func(map[string]any) map[string]any {
			return map[string]any{"observed": c.observed, "gone": c.gone}
		})
		d := newDriver(t, a)
		observed, gone, err := d.AwaitCoverGone(ctxT(t), 3, "com.example.app", "spinner", "android.widget.ProgressBar",
			540, 1200, 750*time.Millisecond)
		if err != nil || observed != c.observed || gone != c.gone {
			t.Errorf("AwaitCoverGone = %v, %v, %v; want %v, %v", observed, gone, err, c.observed, c.gone)
		}
		req := a.requests("awaitCoverGone")
		if len(req) != 1 {
			t.Fatalf("requests %v", req)
		}
		r := req[0]
		if num(t, r, "window") != 3 || r["pkg"] != "com.example.app" || r["nodeId"] != "spinner" ||
			r["class"] != "android.widget.ProgressBar" || num(t, r, "x") != 540 || num(t, r, "y") != 1200 ||
			num(t, r, "timeout") != 750 {
			t.Errorf("request %v", r)
		}
	}

	a := newFakeAgent(t)
	d := newDriver(t, a)
	a.on("awaitCoverGone", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "gone away"} })
	if _, _, err := d.AwaitCoverGone(ctxT(t), 1, "p", "", "", 0, 0, time.Second); err == nil {
		t.Error("agent error not returned")
	}
	a.on("awaitCoverGone", func(map[string]any) map[string]any { return map[string]any{"observed": "yes"} })
	if _, _, err := d.AwaitCoverGone(ctxT(t), 1, "p", "", "", 0, 0, time.Second); err == nil {
		t.Error("a malformed answer was accepted")
	}
}

// ScreenFresh asks the agent to skip its node cache; Screen does not.
func TestScreenFreshSkipsTheCache(t *testing.T) {
	a := newFakeAgent(t)
	a.on("tree", func(map[string]any) map[string]any { return map[string]any{"seq": 3, "windows": []any{}} })
	d := newDriver(t, a)
	if _, err := d.Screen(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if s, err := d.ScreenFresh(ctxT(t)); err != nil || s.Seq != 3 {
		t.Fatalf("ScreenFresh = %+v, %v", s, err)
	}
	reqs := a.requests("tree")
	if len(reqs) != 2 {
		t.Fatalf("tree requests %v", reqs)
	}
	if _, set := reqs[0]["fresh"]; set {
		t.Errorf("Screen asked for a fresh read: %v", reqs[0])
	}
	if reqs[1]["fresh"] != true {
		t.Errorf("ScreenFresh did not ask for a fresh read: %v", reqs[1])
	}
}

// Keep asks a connected agent to stay after the connection closes; with no connection there
// is nothing to ask.
func TestKeep(t *testing.T) {
	if err := bareDriver(nil).Keep(ctxT(t)); err != nil {
		t.Errorf("Keep with no connection: %v", err)
	}
	a := newFakeAgent(t)
	a.on("keep", func(map[string]any) map[string]any { return nil })
	d := newDriver(t, a)
	if err := d.Keep(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if len(a.requests("keep")) != 1 {
		t.Errorf("ops %v", a.ops())
	}
	a.on("keep", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "old agent"} })
	if err := d.Keep(ctxT(t)); err == nil {
		t.Error("agent error not returned")
	}
}

// ReleaseDevice stops a left-over agent through adb alone (no connection to it): it kills
// the agent process and takes tb's service, and only tb's, off the enabled accessibility
// services.
func TestReleaseDevice(t *testing.T) {
	s := stubADB(t, nil)
	if err := ReleaseDevice(ctxT(t), testSerial); err != nil {
		t.Fatal(err)
	}
	calls := s.calls()
	if len(calls) != 1 || len(calls) > 0 && (calls[0][0] != "-s" || calls[0][1] != testSerial || calls[0][2] != "shell") {
		t.Fatalf("adb calls %q", calls)
	}
	script := strings.Join(calls[0][3:], " ")
	for _, want := range []string{"pidof tb-agent", "kill $pids", "enabled_accessibility_services",
		"grep -v 'dev.testbuddy.agent/'", "settings delete secure enabled_accessibility_services",
		// The mock location providers an agent killed this way leaves (issue #72): the
		// ones it listed, and the list.
		"for p in $(cat /data/local/tmp/tb-location-providers); do cmd location providers remove-test-provider $p; done",
		"rm -f /data/local/tmp/tb-location-providers"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q:\n%s", want, script)
		}
	}
	s.rules([]stubRule{{match: "shell", out: "error: device offline", code: 1}})
	if err := ReleaseDevice(ctxT(t), testSerial); err == nil {
		t.Error("adb failure not reported")
	}
}

// Revoking an app-op (all-files access) can kill the app's process. When the app was in the
// foreground, setPermissions notices (its pid changed) and starts it again from its launcher;
// otherwise it leaves the app alone. launchApp's own permission step never does this.
func TestSetPermissionsRestartsAKilledForegroundApp(t *testing.T) {
	const app = "com.example.example"
	dumpsys := dumpsysPerms + "      android.permission.MANAGE_EXTERNAL_STORAGE\n"
	const killed = "tb_app_process_killed"
	cases := []struct {
		name       string
		perms      map[string]string
		foreground string
		killed     bool
		wantGuard  bool // ran the change inside the pid check
		wantStart  bool
	}{
		{"revoked, app killed", map[string]string{"all": "deny"}, app, true, true, true},
		{"revoked, app survived", map[string]string{"all": "deny"}, app, false, true, false},
		{"reset to default, app killed", map[string]string{"android.permission.MANAGE_EXTERNAL_STORAGE": "unset"}, app, true, true, true},
		{"revoked, app in the background", map[string]string{"all": "deny"}, "com.android.launcher3", false, false, false},
		{"allowed: nothing revoked", map[string]string{"all": "allow"}, app, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			guardOut := ""
			if c.killed {
				guardOut = killed + "\n"
			}
			a := shAgent(t, map[string]shAnswer{
				"dumpsys package":  {dumpsys, 0},
				"resolve-activity": {"com.example.example/.MainActivity\n", 0},
				"echo " + killed:   {guardOut, 0},
				"appops get":       {"Uid mode: MANAGE_EXTERNAL_STORAGE: allow\n", 0},
			})
			a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": c.foreground} })
			d := newDriver(t, a)
			if err := d.SetPermissions(ctxT(t), app, c.perms); err != nil {
				t.Fatal(err)
			}
			guarded, started := false, false
			for _, cmd := range a.shCmds() {
				if strings.Contains(cmd, "echo "+killed) {
					guarded = true
					if !strings.HasPrefix(cmd, "pid=$(pidof "+app+"); ") || !strings.Contains(cmd, "appops set --uid "+app) {
						t.Errorf("guarded script %q", cmd)
					}
				}
				if strings.Contains(cmd, "am start -n com.example.example/.MainActivity") {
					started = true
				}
			}
			if guarded != c.wantGuard || started != c.wantStart {
				t.Errorf("guarded %v started %v; want %v %v (commands %q)", guarded, started, c.wantGuard, c.wantStart, a.shCmds())
			}
			if c.wantStart && len(a.requests("awaitPackage")) != 1 {
				t.Errorf("did not wait for the restarted app: ops %v", a.ops())
			}
		})
	}
}

// A killed foreground app that cannot be restarted (no launcher activity) is an error.
func TestSetPermissionsRestartFails(t *testing.T) {
	const app = "com.example.example"
	dumpsys := dumpsysPerms + "      android.permission.MANAGE_EXTERNAL_STORAGE\n"
	a := shAgent(t, map[string]shAnswer{
		"dumpsys package":            {dumpsys, 0},
		"resolve-activity":           {"No activity found\n", 0},
		"echo tb_app_process_killed": {"tb_app_process_killed\n", 0},
	})
	a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": app} })
	d := newDriver(t, a)
	err := d.SetPermissions(ctxT(t), app, map[string]string{"all": "deny"})
	if err == nil || !strings.Contains(err.Error(), "no launcher activity") {
		t.Errorf("err = %v", err)
	}
}
