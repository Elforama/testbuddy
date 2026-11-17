package ios

// The simctl, TCC and recording paths, against stub xcrun/sqlite3/pgrep scripts (fakes_test.go)
// and, where the runner is involved, the fake runner.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const app = "com.example.app"

func TestLaunchReadinessRecoversDelayedSnapshot(t *testing.T) {
	r := launchRunner(t)
	var reads atomic.Int32
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		if reads.Add(1) == 1 {
			time.Sleep(100 * time.Millisecond)
		}
		return map[string]any{"found": true, "windows": []any{map[string]any{"id": q["pid"], "type": 1, "pkg": q["pkg"], "active": true}}}
	})
	d := newIOSDriver(t, r)
	if err := d.awaitLaunch(ctxT(t), app, 4242, 2*time.Second, 40*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 || num(t, r.last("awaitPackage"), "pid") != 4242 {
		t.Fatal("expected one additional readiness read for the original launch")
	}
}

func launchRunner(t *testing.T) *fakeRunner {
	r := newFakeRunner(t)
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		return map[string]any{"found": true, "windows": []any{map[string]any{"id": q["pid"], "type": 1, "pkg": q["pkg"], "active": true}}}
	})
	return r
}

func tccInsert(service string, auth int) string {
	return fmt.Sprintf("DELETE FROM access WHERE service='%s' AND client='%s' AND client_type=0;"+
		"INSERT INTO access (service, client, client_type, auth_value, auth_reason, auth_version, flags) VALUES ('%s', '%s', 0, %d, 2, 1, 0);",
		service, app, service, app, auth)
}

func tccDelete(service string) string {
	return fmt.Sprintf("DELETE FROM access WHERE service='%s' AND client='%s' AND client_type=0;", service, app)
}

// LaunchApp grants everything (TCC + location), launches through simctl with the probe and fast
// animations, stopping the running app in the same call, then waits for the app's window.
func TestLaunchApp(t *testing.T) {
	t.Setenv("TB_CI", "0")
	ts := stubTools(t)
	t.Setenv("TB_IOS_FAST_ANIMATIONS", "")
	envFile := filepath.Join(t.TempDir(), "env")
	ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242", sh: "/usr/bin/env > " + sq(envFile) + ";"}})
	r := launchRunner(t)
	d := newIOSDriver(t, r)
	args := map[string]any{"b": true, "-n": 2.5, "s": "x y", "count": 3}
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{StopApp: true, Arguments: args}); err != nil {
		t.Fatal(err)
	}
	calls := ts.calls("xcrun")
	if len(calls) != 2 {
		t.Fatalf("xcrun calls %v", calls)
	}
	if got := strings.Join(calls[0], " "); got != "simctl privacy "+testUDID+" grant location-always "+app {
		t.Errorf("first call %q", got)
	}
	// stopApp: the launch itself stops the running copy (issue #50), no separate terminate.
	want := []string{"simctl", "launch", "--terminate-running-process", testUDID, app, "-n", "2.5", "-b", "true", "-count", "3", "-s", "x y", "-UIAnimationDragCoefficient", "0.1"}
	if !reflect.DeepEqual(calls[1], want) {
		t.Errorf("launch %q\nwant   %q", calls[1], want)
	}
	env, _ := os.ReadFile(envFile)
	for _, want := range []string{"SIMCTL_CHILD_DYLD_INSERT_LIBRARIES=" + d.probe + "\n", "SIMCTL_CHILD_FEATUREFLAGS_ENABLED=UIKit/UIPreferences\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("launch environment lacks %q", strings.TrimSpace(want))
		}
	}
	sql := ts.sql()
	if len(sql) != 1 {
		t.Fatalf("sqlite3 calls %v", sql)
	}
	var services []string
	for _, s := range tccServices {
		services = append(services, s)
	}
	sort.Strings(services)
	wantSQL := "BEGIN;"
	for _, s := range services {
		wantSQL += tccInsert(s, 2)
	}
	wantSQL += "COMMIT;"
	if sql[0] != wantSQL {
		t.Errorf("TCC SQL:\n%s\nwant:\n%s", sql[0], wantSQL)
	}
	if c := ts.calls("sqlite3")[0]; c[0] != "-cmd" || c[1] != ".timeout 5000" || c[2] != d.tccDB() {
		t.Errorf("sqlite3 args %q", c[:3])
	}
	if q := r.last("awaitPackage"); q["pkg"] != app || num(t, q, "timeout") != 30000 {
		t.Errorf("awaitPackage %v", q)
	}

	// A second launch does not re-apply the location permission (simctl privacy costs ~0.1 s).
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := ts.simctl(); len(got) != 3 || !strings.HasPrefix(got[2], "launch ") {
		t.Errorf("second launch ran %v", got[2:])
	}
}

// TB_IOS_FAST_ANIMATIONS=0 launches without the drag coefficient or UIKit's preference flag; no probe without one.
func TestLaunchAppWithoutSpeedups(t *testing.T) {
	ts := stubTools(t)
	t.Setenv("TB_IOS_FAST_ANIMATIONS", "0")
	t.Setenv("SIMCTL_CHILD_DYLD_INSERT_LIBRARIES", "")
	envFile := filepath.Join(t.TempDir(), "env")
	ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242", sh: "/usr/bin/env > " + sq(envFile) + ";"}})
	r := launchRunner(t)
	d := newIOSDriver(t, r)
	d.probe = ""
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{Permissions: map[string]string{"camera": "allow"}}); err != nil {
		t.Fatal(err)
	}
	got := ts.simctl()
	if len(got) != 1 || got[0] != "launch "+testUDID+" "+app {
		t.Errorf("simctl calls %v", got)
	}
	env, _ := os.ReadFile(envFile)
	if strings.Contains(string(env), "FEATUREFLAGS") || strings.Contains(string(env), "DYLD_INSERT_LIBRARIES="+"/") {
		t.Errorf("launch environment has a speedup:\n%s", env)
	}
}

// A drag coefficient the flow passes itself wins over the default one.
func TestLaunchAppUserCoefficient(t *testing.T) {
	ts := stubTools(t)
	t.Setenv("TB_IOS_FAST_ANIMATIONS", "1")
	r := launchRunner(t)
	d := newIOSDriver(t, r)
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{Permissions: map[string]string{"photos": "deny"},
		Arguments: map[string]any{"--UIAnimationDragCoefficient": 1.0}}); err != nil {
		t.Fatal(err)
	}
	calls := ts.calls("xcrun")
	last := calls[len(calls)-1]
	if want := []string{"simctl", "launch", testUDID, app, "-UIAnimationDragCoefficient", "1"}; !reflect.DeepEqual(last, want) {
		t.Errorf("launch %q, want %q", last, want)
	}
}

// ClearState and ClearKeychain run before the launch when asked for.
func TestLaunchAppClearsStateAndKeychain(t *testing.T) {
	ts := stubTools(t)
	bundle := filepath.Join(ts.home, "Library/Developer/CoreSimulator/Devices", testUDID, "data/Containers/Bundle/Application/A1/App.app")
	os.MkdirAll(bundle, 0o755)
	ts.set("xcrun", []stubRule{{match: app + " app", out: bundle + "\n"}})
	r := launchRunner(t)
	d := newIOSDriver(t, r)
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{ClearState: true, ClearKeychain: true, StopApp: true,
		Permissions: map[string]string{"camera": "allow"}}); err != nil {
		t.Fatal(err)
	}
	got := ts.simctl()
	n := 0
	for _, c := range got {
		if strings.HasPrefix(c, "terminate ") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("terminated %d times, want once (ClearState terminates)", n)
	}
	for _, want := range []string{"uninstall " + testUDID + " " + app, "install " + testUDID + " ", "keychain " + testUDID + " reset",
		"privacy " + testUDID + " reset all " + app, "launch --terminate-running-process " + testUDID + " " + app} {
		found := false
		for _, c := range got {
			found = found || strings.HasPrefix(c, want)
		}
		if !found {
			t.Errorf("no %q in %v", want, got)
		}
	}
}

// LaunchApp stops at the first failing step.
func TestLaunchAppFailures(t *testing.T) {
	t.Setenv("TB_CI", "0")
	cases := []struct {
		name  string
		id    string
		o     driver.LaunchOpts
		xcrun []stubRule
		sqlOK bool
		found bool
		want  string
	}{
		{"bad id", "com.app;x", driver.LaunchOpts{}, nil, true, true, "invalid bundle id"},
		{"not installed", app, driver.LaunchOpts{ClearState: true}, []stubRule{{match: "get_app_container", out: "No such app", code: 2}}, true, true, "is not installed"},
		{"keychain", app, driver.LaunchOpts{ClearKeychain: true}, []stubRule{{match: "keychain", out: "boom", code: 1}}, true, true, "simctl keychain " + testUDID + " reset: boom"},
		{"tcc", app, driver.LaunchOpts{}, nil, false, true, "setting permissions in "},
		{"location", app, driver.LaunchOpts{Permissions: map[string]string{"location": "allow"}}, []stubRule{{match: "privacy", out: "denied", code: 1}}, true, true, "simctl privacy"},
		{"launch", app, driver.LaunchOpts{Permissions: map[string]string{"camera": "allow"}}, []stubRule{{match: "simctl launch", out: "FBSOpenApplicationServiceErrorDomain", code: 4}}, true, true, "FBSOpenApplicationServiceErrorDomain"},
		{"window", app, driver.LaunchOpts{Permissions: map[string]string{"camera": "allow"}}, nil, true, false, "did not come to the foreground within 30s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := stubTools(t)
			ts.set("xcrun", c.xcrun)
			if !c.sqlOK {
				ts.set("sqlite3", []stubRule{{match: "-cmd", out: "Error: unable to open database file", code: 1}})
			}
			r := newFakeRunner(t)
			r.on("awaitPackage", func(map[string]any) map[string]any { return map[string]any{"found": c.found} })
			d := newIOSDriver(t, r)
			err := d.LaunchApp(ctxT(t), c.id, c.o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// The TCC rows written per value: allow 2, deny 0, limited 3, unset only deletes, anything else is skipped.
func TestSetPermissionsTCC(t *testing.T) {
	cases := []struct {
		name  string
		perms map[string]string
		sql   string
	}{
		{"allow", map[string]string{"camera": "allow"}, "BEGIN;" + tccInsert("kTCCServiceCamera", 2) + "COMMIT;"},
		{"deny", map[string]string{"microphone": "deny"}, "BEGIN;" + tccInsert("kTCCServiceMicrophone", 0) + "COMMIT;"},
		{"limited", map[string]string{"photos": "limited"}, "BEGIN;" + tccInsert("kTCCServicePhotos", 3) + "COMMIT;"},
		{"unset", map[string]string{"contacts": "unset"}, "BEGIN;" + tccDelete("kTCCServiceAddressBook") + "COMMIT;"},
		{"unknown value", map[string]string{"camera": "sometimes"}, "BEGIN;COMMIT;"},
		{"case and unknown names", map[string]string{"Camera": "ALLOW", "notifications": "allow", "telepathy": "deny"}, "BEGIN;" + tccInsert("kTCCServiceCamera", 2) + "COMMIT;"},
		{"sorted", map[string]string{"siri": "deny", "camera": "allow", "bluetooth": "allow"},
			"BEGIN;" + tccInsert("kTCCServiceBluetoothAlways", 2) + tccInsert("kTCCServiceCamera", 2) + tccInsert("kTCCServiceSiri", 0) + "COMMIT;"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := stubTools(t)
			d := &Driver{info: testInfo()}
			if err := d.SetPermissions(ctxT(t), app, c.perms); err != nil {
				t.Fatal(err)
			}
			if sql := ts.sql(); len(sql) != 1 || sql[0] != c.sql {
				t.Errorf("SQL %q\nwant %q", sql, c.sql)
			}
			if got := ts.simctl(); len(got) != 0 {
				t.Errorf("simctl calls %v without a location permission", got)
			}
		})
	}
}

// "all" applies to every TCC service and location; a named permission overrides it.
func TestSetPermissionsAll(t *testing.T) {
	ts := stubTools(t)
	d := &Driver{info: testInfo()}
	if err := d.SetPermissions(ctxT(t), app, map[string]string{"all": "DENY", "camera": "allow"}); err != nil {
		t.Fatal(err)
	}
	sql := ts.sql()[0]
	if strings.Count(sql, "INSERT") != len(tccServices) || !strings.Contains(sql, tccInsert("kTCCServiceCamera", 2)) ||
		!strings.Contains(sql, tccInsert("kTCCServiceMicrophone", 0)) {
		t.Errorf("SQL %s", sql)
	}
	if got := ts.simctl(); len(got) != 1 || got[0] != "privacy "+testUDID+" revoke location-always "+app {
		t.Errorf("simctl %v", got)
	}
	if err := d.SetPermissions(ctxT(t), "bad id!", nil); err == nil {
		t.Error("bad id accepted")
	}
}

// Location permission values map to simctl privacy grant/revoke/reset.
func TestSetPermissionsLocation(t *testing.T) {
	cases := []struct {
		perms map[string]string
		want  string
	}{
		{map[string]string{"location": "allow"}, "grant location-always"},
		{map[string]string{"location": "always"}, "grant location-always"},
		{map[string]string{"Location": "Deny"}, "revoke location-always"},
		{map[string]string{"location": "never"}, "revoke location-always"},
		{map[string]string{"location": "inuse"}, "grant location"},
		{map[string]string{"location": "whileinuse"}, "grant location"},
		{map[string]string{"location": "unset"}, "reset location-always"},
		{map[string]string{"all": "unset"}, "reset location-always"},
		{map[string]string{"all": "limited"}, ""},
	}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.perms), func(t *testing.T) {
			ts := stubTools(t)
			d := &Driver{info: testInfo()}
			if err := d.SetPermissions(ctxT(t), app, c.perms); err != nil {
				t.Fatal(err)
			}
			got := ts.simctl()
			if c.want == "" {
				if len(got) != 0 {
					t.Errorf("simctl %v", got)
				}
				return
			}
			parts := strings.SplitN(c.want, " ", 2)
			if want := "privacy " + testUDID + " " + parts[0] + " " + parts[1] + " " + app; len(got) != 1 || got[0] != want {
				t.Errorf("simctl %v, want %q", got, want)
			}
			if _, ok := c.perms["all"]; !ok && len(ts.sql()) != 0 {
				t.Errorf("location alone wrote TCC: %v", ts.sql())
			}
		})
	}
}

// An unknown location value is an error; the same value twice in a row is applied once.
func TestSetPermissionsLocationErrorsAndCache(t *testing.T) {
	ts := stubTools(t)
	d := &Driver{info: testInfo()}
	err := d.SetPermissions(ctxT(t), app, map[string]string{"location": "sideways"})
	if err == nil || err.Error() != `unknown location permission "sideways"` {
		t.Errorf("err = %v", err)
	}
	d2 := &Driver{info: testInfo()}
	for i := 0; i < 2; i++ {
		d2.SetPermissions(ctxT(t), app, map[string]string{"location": "allow"})
	}
	d2.SetPermissions(ctxT(t), app, map[string]string{"location": "deny"})
	if got := ts.simctl(); len(got) != 2 || !strings.Contains(got[0], "grant") || !strings.Contains(got[1], "revoke") {
		t.Errorf("simctl %v, want one grant then one revoke", got)
	}
}

// A location permission that failed to apply should be retried by the next call.
func TestSetPermissionsLocationRetriesAfterFailure(t *testing.T) {
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "privacy", out: "busy", code: 1}})
	d := &Driver{info: testInfo()}
	if err := d.SetPermissions(ctxT(t), app, map[string]string{"location": "allow"}); err == nil {
		t.Fatal("failure not reported")
	}
	ts.set("xcrun", nil)
	d.SetPermissions(ctxT(t), app, map[string]string{"location": "allow"})
	if got := ts.simctl(); len(got) != 2 {
		t.Errorf("simctl %v, want the grant tried again", got)
	}
	if err := d.SetPermissions(ctxT(t), app, map[string]string{"location": "sideways"}); err == nil {
		t.Error("unknown value accepted")
	}
	if err := d.SetPermissions(ctxT(t), app, map[string]string{"location": "sideways"}); err == nil {
		t.Error("unknown value accepted the second time")
	}
}

// The TCC SQL really runs: rows land in a TCC-shaped database, unset rows are removed.
func TestWriteTCCWithSQLite(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	ts := stubTools(t)
	ts.realSQLite(sqlite)
	d := &Driver{info: testInfo()}
	db := d.tccDB()
	os.MkdirAll(filepath.Dir(db), 0o755)
	schema := "CREATE TABLE access (service TEXT NOT NULL, client TEXT NOT NULL, client_type INTEGER NOT NULL, auth_value INTEGER NOT NULL, " +
		"auth_reason INTEGER NOT NULL, auth_version INTEGER NOT NULL, csreq BLOB, policy_id INTEGER, flags INTEGER, " +
		"PRIMARY KEY (service, client, client_type));" +
		"INSERT INTO access VALUES ('kTCCServiceAddressBook', 'com.example.app', 0, 2, 2, 1, NULL, NULL, 0);" +
		"INSERT INTO access VALUES ('kTCCServiceCamera', 'com.other', 0, 0, 2, 1, NULL, NULL, 0);"
	if out, err := exec.Command(sqlite, db, schema).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	rows := func() string {
		out, err := exec.Command(sqlite, db, "SELECT service, client, auth_value FROM access ORDER BY client, service;").CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	perms := map[string]string{"camera": "allow", "photos": "limited", "microphone": "deny", "contacts": "unset"}
	for i := 0; i < 2; i++ { // twice: re-applying replaces the rows rather than failing on the key
		if err := d.SetPermissions(ctxT(t), app, perms); err != nil {
			t.Fatal(err)
		}
	}
	want := "kTCCServiceCamera|com.example.app|2\nkTCCServiceMicrophone|com.example.app|0\nkTCCServicePhotos|com.example.app|3\nkTCCServiceCamera|com.other|0"
	if got := rows(); got != want {
		t.Errorf("rows:\n%s\nwant:\n%s", got, want)
	}
	d.resetTCC(ctxT(t), app)
	if got := rows(); got != "kTCCServiceCamera|com.other|0" {
		t.Errorf("after reset:\n%s", got)
	}
}

// With TB_IOS_FAST_CLEARSTATE=1, ClearState empties the data and app group containers in place,
// recreates the standard folders, and resets privacy.
func TestClearStateFastInPlace(t *testing.T) {
	ts := stubTools(t)
	t.Setenv("TB_IOS_FAST_CLEARSTATE", "1")
	devices := filepath.Join(ts.home, "Library/Developer/CoreSimulator/Devices", testUDID, "data/Containers")
	data := filepath.Join(devices, "Data/Application/APP-UUID")
	group := filepath.Join(devices, "Shared/AppGroup/GROUP-UUID")
	other := filepath.Join(ts.home, "not-a-simulator")
	for _, f := range []string{filepath.Join(data, "Documents/notes.txt"), filepath.Join(data, "Library/Preferences/com.example.app.plist"),
		filepath.Join(data, "junk.bin"), filepath.Join(group, "shared.db"), filepath.Join(other, "keep.txt")} {
		os.MkdirAll(filepath.Dir(f), 0o755)
		os.WriteFile(f, []byte("x"), 0o644)
	}
	ts.set("xcrun", []stubRule{
		{match: app + " data", out: data + "\n"},
		{match: app + " groups", out: "group.com.example\t" + group + "\nbad line\ngroup.other\t" + other + "\n"},
	})
	d := &Driver{info: testInfo(), locationSet: map[string]string{app: "always", "other.app": "never"}}
	if err := d.ClearState(ctxT(t), app); err != nil {
		t.Fatal(err)
	}
	var left []string
	filepath.Walk(data, func(p string, info os.FileInfo, err error) error {
		if p != data {
			rel, _ := filepath.Rel(data, p)
			left = append(left, rel)
		}
		return nil
	})
	sort.Strings(left)
	if want := []string{"Documents", "Library", "Library/Caches", "Library/Preferences", "tmp"}; !reflect.DeepEqual(left, want) {
		t.Errorf("data container holds %v, want %v", left, want)
	}
	if entries, _ := os.ReadDir(group); len(entries) != 0 {
		t.Errorf("app group not emptied: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(other, "keep.txt")); err != nil {
		t.Error("a group outside /Shared/AppGroup/ was emptied")
	}
	got := ts.simctl()
	sort.Strings(got)
	want := []string{"get_app_container " + testUDID + " " + app + " data", "get_app_container " + testUDID + " " + app + " groups",
		"privacy " + testUDID + " reset all " + app, "terminate " + testUDID + " " + app}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("simctl %v\nwant %v", got, want)
	}
	if sql := ts.sql(); len(sql) != 1 || sql[0] != "DELETE FROM access WHERE client='"+app+"' AND client_type=0;" {
		t.Errorf("TCC reset %q", sql)
	}
	if _, ok := d.locationSet[app]; ok || d.locationSet["other.app"] != "never" {
		t.Errorf("location cache %v", d.locationSet)
	}
}

// ClearState refuses a missing app, a bad id, and a container outside the simulator's data.
func TestClearStateFastErrors(t *testing.T) {
	ts := stubTools(t)
	t.Setenv("TB_IOS_FAST_CLEARSTATE", "1")
	d := &Driver{info: testInfo()}
	if err := d.ClearState(ctxT(t), ""); err == nil {
		t.Error("empty id accepted")
	}
	ts.set("xcrun", []stubRule{{match: " data", out: "No such file or directory", code: 2}})
	if err := d.ClearState(ctxT(t), app); err == nil || !strings.Contains(err.Error(), "app "+app+" is not installed: simctl get_app_container") {
		t.Errorf("err = %v", err)
	}
	ts.set("xcrun", []stubRule{{match: " data", out: ts.home + "\n"}})
	if err := d.ClearState(ctxT(t), app); err == nil || !strings.Contains(err.Error(), "refusing to empty") {
		t.Errorf("err = %v", err)
	}
}

// StopApp treats "found nothing to terminate" as success; KillApp is the same on iOS.
func TestStopApp(t *testing.T) {
	ts := stubTools(t)
	d := &Driver{info: testInfo()}
	if err := d.StopApp(ctxT(t), app); err != nil {
		t.Fatal(err)
	}
	ts.set("xcrun", []stubRule{{match: "terminate", out: "An error was encountered processing the command (domain=com.apple.CoreSimulator.SimError, code=164):\nfound nothing to terminate", code: 164}})
	if err := d.KillApp(ctxT(t), app); err != nil {
		t.Errorf("not running: %v", err)
	}
	ts.set("xcrun", []stubRule{{match: "terminate", out: "Invalid device: " + testUDID, code: 148}})
	if err := d.StopApp(ctxT(t), app); err == nil || !strings.Contains(err.Error(), "Invalid device") {
		t.Errorf("err = %v", err)
	}
	if err := d.StopApp(ctxT(t), "a b"); err == nil {
		t.Error("bad id accepted")
	}
	if got := ts.simctl(); len(got) != 3 || got[0] != "terminate "+testUDID+" "+app {
		t.Errorf("simctl %v", got)
	}
}

// The plain simctl commands, with relative paths made absolute.
func TestSimctlCommands(t *testing.T) {
	ts := stubTools(t)
	d := &Driver{info: testInfo()}
	ctx := ctxT(t)
	cwd, _ := os.Getwd()
	steps := []struct {
		run  func() error
		want string
	}{
		{func() error { return d.ClearKeychain(ctx) }, "keychain " + testUDID + " reset"},
		{func() error { return d.OpenLink(ctx, "myapp://item/7?x=1", app, true, false) }, "openurl " + testUDID + " myapp://item/7?x=1"},
		{func() error { return d.SetLocation(ctx, 37.3349, -122.009) }, "location " + testUDID + " set 37.3349,-122.009"},
		{func() error { return d.AddMedia(ctx, []string{"pics/cat.png", "/abs/dog.jpg"}) },
			"addmedia " + testUDID + " " + filepath.Join(cwd, "pics/cat.png") + " /abs/dog.jpg"},
		{func() error { return d.InstallApp(ctx, "build/App.app") }, "install " + testUDID + " " + filepath.Join(cwd, "build/App.app")},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatal(err)
		}
		got := ts.simctl()
		if got[len(got)-1] != s.want {
			t.Errorf("ran %q, want %q", got[len(got)-1], s.want)
		}
	}
}

// simctl errors carry simctl's output, else the exit status.
func TestSimctlErrors(t *testing.T) {
	ts := stubTools(t)
	d := &Driver{info: testInfo()}
	ts.set("xcrun", []stubRule{{match: "openurl", out: "  Unable to open URL  \n", code: 1}, {match: "keychain", code: 3}})
	if err := d.OpenLink(ctxT(t), "x://", "", false, false); err == nil || err.Error() != "simctl openurl "+testUDID+" x://: Unable to open URL" {
		t.Errorf("err = %v", err)
	}
	if err := d.ClearKeychain(ctxT(t)); err == nil || err.Error() != "simctl keychain "+testUDID+" reset: exit status 3" {
		t.Errorf("err = %v", err)
	}
	out, err := d.simctl(ctxT(t), "openurl", testUDID, "x://")
	if out != "Unable to open URL" || err == nil {
		t.Errorf("simctl = %q, %v", out, err)
	}
}

// Recording runs simctl recordVideo, stops it with SIGINT and checks the file exists.
func TestRecording(t *testing.T) {
	ts := stubTools(t)
	ready := filepath.Join(t.TempDir(), "ready")
	ts.set("xcrun", []stubRule{{match: "recordVideo", sh: `trap ': > "$7"; exit 0' INT; : > ` + sq(ready) + `; while :; do /bin/sleep 0.02; done;`}})
	d := &Driver{info: testInfo()}
	out := filepath.Join(t.TempDir(), "run.mp4")
	if err := d.StartRecording(ctxT(t), out); err != nil {
		t.Fatal(err)
	}
	if err := d.StartRecording(ctxT(t), out); err == nil || err.Error() != "already recording" {
		t.Errorf("second start: %v", err)
	}
	waitFile(t, ready)
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("no recording: %v", err)
	}
	want := []string{"simctl", "io", testUDID, "recordVideo", "--codec=h264", "--force", out}
	if got := ts.calls("xcrun"); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("xcrun %q, want %q", got, want)
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Errorf("second stop: %v", err)
	}
}

// A recording that left no file is an error; a missing xcrun fails the start.
func TestRecordingErrors(t *testing.T) {
	ts := stubTools(t)
	ready := filepath.Join(t.TempDir(), "ready")
	ts.set("xcrun", []stubRule{{match: "recordVideo", sh: `trap 'exit 0' INT; : > ` + sq(ready) + `; while :; do /bin/sleep 0.02; done;`}})
	d := &Driver{info: testInfo()}
	out := filepath.Join(t.TempDir(), "none.mp4")
	if err := d.StartRecording(ctxT(t), out); err != nil {
		t.Fatal(err)
	}
	waitFile(t, ready)
	if err := d.StopRecording(ctxT(t)); err == nil || !strings.Contains(err.Error(), "no recording at "+out) {
		t.Errorf("err = %v", err)
	}
	ts.hide()
	if err := d.StartRecording(ctxT(t), out); err == nil {
		t.Error("started without xcrun")
	}
}

func waitFile(t *testing.T, p string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(p); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", p)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stopRunners sends SIGTERM to this simulator's xcodebuild sessions (never to tb itself).
func TestStopRunners(t *testing.T) {
	ts := stubTools(t)
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Skip(err)
	}
	defer child.Process.Kill()
	ts.set("pgrep", []stubRule{{match: "-f", out: fmt.Sprintf("%d\n%d\nnot-a-pid\n", child.Process.Pid, os.Getpid())}})
	stopRunners(testUDID)
	err := child.Wait()
	if ee, ok := err.(*exec.ExitError); !ok || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Errorf("runner ended with %v, want SIGTERM", err)
	}
	want := []string{"-f", "xcodebuild test-without-building.*tb-agent.xctestrun.*id=" + testUDID}
	if got := ts.calls("pgrep"); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("pgrep %q, want %q", got, want)
	}
}

// startRunner starts xcodebuild in its own session with the port, build and idle time, logging to the runner log.
func TestStartRunner(t *testing.T) {
	ts := stubTools(t)
	envFile := filepath.Join(t.TempDir(), "env")
	ts.set("xcrun", []stubRule{{match: "xcodebuild", out: "Testing started\n", sh: "/usr/bin/env > " + sq(envFile) + ".tmp; /bin/mv " + sq(envFile) + ".tmp " + sq(envFile) + ";"}})
	d := &Driver{info: testInfo(), dir: t.TempDir()}
	if _, err := d.startRunner(ctxT(t), 22345); err != nil {
		t.Fatal(err)
	}
	waitFile(t, envFile)
	want := []string{"xcodebuild", "test-without-building", "-xctestrun", filepath.Join(d.dir, "tb-agent.xctestrun"), "-destination", "id=" + testUDID}
	if got := ts.calls("xcrun"); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("xcrun %q\nwant %q", got, want)
	}
	env, _ := os.ReadFile(envFile)
	for _, want := range []string{"TEST_RUNNER_TB_PORT=22345\n", "TEST_RUNNER_TB_BUILD=" + BuildID() + "\n", "TEST_RUNNER_TB_IDLE_S=1800\n"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("runner environment lacks %q", strings.TrimSpace(want))
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for d.logTail() != "Testing started" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := d.logTail(); got != "Testing started" {
		t.Errorf("runner log %q", got)
	}
}

// startRunner reports a log it cannot create or an xcrun it cannot start.
func TestStartRunnerErrors(t *testing.T) {
	ts := stubTools(t)
	d := &Driver{info: testInfo(), dir: filepath.Join(t.TempDir(), "missing")}
	if _, err := d.startRunner(ctxT(t), 1); err == nil {
		t.Error("started without a log")
	}
	ts.hide()
	d.dir = t.TempDir()
	if _, err := d.startRunner(ctxT(t), 1); err == nil || !strings.HasPrefix(err.Error(), "starting xcodebuild: ") {
		t.Errorf("err = %v", err)
	}
}

// Dark mode is the simulator's appearance (`simctl ui <udid> appearance`).
func TestDarkMode(t *testing.T) {
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "appearance dark", out: ""}, {match: "appearance light", out: ""}, {match: "appearance", out: "dark\n"}})
	d := &Driver{info: testInfo(), dir: t.TempDir()}
	if err := d.SetDarkMode(ctxT(t), true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetDarkMode(ctxT(t), false); err != nil {
		t.Fatal(err)
	}
	on, err := d.DarkMode(ctxT(t))
	if err != nil || !on {
		t.Errorf("DarkMode = %v, %v", on, err)
	}
	calls := ts.calls("xcrun")
	want := [][]string{
		{"simctl", "ui", testUDID, "appearance", "dark"},
		{"simctl", "ui", testUDID, "appearance", "light"},
		{"simctl", "ui", testUDID, "appearance"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("xcrun calls %q\nwant %q", calls, want)
	}
	ts.set("xcrun", []stubRule{{match: "appearance", out: "light\n"}})
	if on, err := d.DarkMode(ctxT(t)); err != nil || on {
		t.Errorf("light: DarkMode = %v, %v", on, err)
	}
}

// In CI, the first screen read after a launch may take longer: XCTest opens its automation
// session with the new app on it and waits up to 60 s for the app to answer. On a CI runner the
// bench app took 34 s, past the 30 s a read had ("agent did not answer "tree" within 30s",
// bench/flows/scenarios on GitHub Actions' iPhone 17 Pro simulator). Later reads keep the
// usual limit, so a hung runner is still caught as soon; outside CI every read keeps it.
func TestScreenAfterLaunchWaitsForTheAppInCI(t *testing.T) {
	defer func(r, f, m time.Duration) { readTimeout, ciFirstReadTimeout, agentMargin = r, f, m }(readTimeout, ciFirstReadTimeout, agentMargin)
	// A later read's limit is readTimeout plus the agent's margin (issue #130).
	readTimeout, ciFirstReadTimeout, agentMargin = 300*time.Millisecond, 3*time.Second, 200*time.Millisecond
	for _, inCI := range []bool{true, false} {
		t.Run(map[bool]string{true: "CI", false: "not CI"}[inCI], func(t *testing.T) {
			t.Setenv("TB_CI", map[bool]string{true: "1", false: "0"}[inCI])
			ts := stubTools(t)
			ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242"}})
			r := launchRunner(t)
			tree := treeHandler("false", "[0, 0, 393, 852]")
			r.on("tree", func(req map[string]any) map[string]any {
				time.Sleep(time.Second) // longer than a read, shorter than CI's first read after a launch
				return tree(req)
			})
			d := newIOSDriver(t, r)
			if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{}); err != nil {
				t.Fatal(err)
			}
			wantBudget := int64(30000)
			if inCI {
				wantBudget = ciFirstReadTimeout.Milliseconds()
			}
			if got := num(t, r.last("awaitPackage"), "timeout"); int64(got) != wantBudget {
				t.Errorf("launch budget %v, want %v", got, wantBudget)
			}
			if _, err := d.Screen(ctxT(t)); err != nil {
				t.Fatalf("cached launch tree: %v", err)
			}
			if _, err := d.Screen(ctxT(t)); err == nil {
				t.Error("a later read that slow passed: want the usual limit")
			}
		})
	}
}

// A foreground bundle alone can be a stale instance after clearState. Consume the
// first readable tree returned by the launch handshake without asking XCTest again.
func TestLaunchReadinessUsesLaunchedPIDAndReturnsTree(t *testing.T) {
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "simctl launch", out: app + ": 4242"}})
	r := launchRunner(t)
	r.on("awaitPackage", func(q map[string]any) map[string]any {
		if q["pid"] != float64(4242) || q["readTree"] != true {
			return map[string]any{"found": false}
		}
		return map[string]any{"found": true, "seq": 0, "windows": []any{map[string]any{"id": 4242, "type": 1, "pkg": app, "active": true, "b": []int{0, 0, 393, 852}}}}
	})
	d := newIOSDriver(t, r)
	if err := d.LaunchApp(ctxT(t), app, driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	if s, err := d.Screen(ctxT(t)); err != nil || s.Foreground() != app {
		t.Fatalf("ready screen: %v, %v", s, err)
	}
	if len(r.requests("tree")) != 0 {
		t.Fatal("duplicate first tree read")
	}
}
