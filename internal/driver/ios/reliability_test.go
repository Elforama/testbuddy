package ios

// Reliability: reconnecting to a runner whose connection dropped or that died (Maestro #880),
// giving up at once when xcodebuild exits early (#1585), clearState by reinstalling (#1601,
// #1306), and Password AutoFill turned off (#1061).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
)

// openAtPreferredPort opens a driver on a fake runner serving the simulator's preferred port.
func openAtPreferredPort(t *testing.T) (*Driver, *fakeRunner, *tools, string, int) {
	t.Helper()
	ts := stubTools(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	r := newFakeRunnerAt(t, fmt.Sprintf("127.0.0.1:%d", port), udid, BuildID())
	d, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d, r, ts, udid, port
}

func tapAnswer(map[string]any) map[string]any {
	return map[string]any{"result": "changed", "seq": 3}
}

// A connection that drops while the runner stays up is replaced by a new one to the same
// runner, and the request goes through: no xcodebuild is started.
func TestReconnectSameRunner(t *testing.T) {
	d, r, ts, _, _ := openAtPreferredPort(t)
	r.on("tap", tapAnswer)
	old := d.Conn()
	r.dropConns()
	if _, err := d.Tap(ctxT(t), 10, 20, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if d.Conn() == old {
		t.Error("still on the dropped connection")
	}
	if xc := ts.calls("xcrun"); len(xc) != 0 {
		t.Errorf("xcrun ran: %q", xc)
	}
}

// A tap whose connection drops before its answer is sent again with the same token, so the
// runner can answer it from its first run instead of tapping twice.
func TestReconnectResendsWithSameToken(t *testing.T) {
	d, r, _, _, _ := openAtPreferredPort(t)
	var n atomic.Int32
	r.on("tap", func(req map[string]any) map[string]any {
		if n.Add(1) == 1 {
			go r.dropConns() // the answer never comes
			return noReply
		}
		return tapAnswer(req)
	})
	if _, err := d.Tap(ctxT(t), 10, 20, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	taps := r.requests("tap")
	if len(taps) != 2 {
		t.Fatalf("%d taps sent, want 2", len(taps))
	}
	rid, _ := taps[0]["rid"].(string)
	if rid == "" || taps[1]["rid"] != rid {
		t.Errorf("tokens %v and %v", taps[0]["rid"], taps[1]["rid"])
	}
	// Reads carry no token; each device-changing request gets its own.
	d.Screen(ctxT(t))
	if q := r.last("tree"); q["rid"] != nil {
		t.Errorf("tree carries a token: %v", q)
	}
	d.Tap(ctxT(t), 1, 1, driver.ActionOpts{})
	if q := r.last("tap"); q["rid"] == rid {
		t.Error("a new tap reused the token")
	}
}

// A runner that died is started again; event subscribers keep receiving events from the new
// one, and the change counter keeps increasing although the new runner counts from 0.
func TestReconnectRestartsDeadRunner(t *testing.T) {
	d, r, ts, udid, port := openAtPreferredPort(t)
	events, cancel := d.Events()
	defer cancel()
	r.push(map[string]any{"ev": "changed", "seq": 50})
	if e := waitEvent(t, events, "changed"); e.Seq != 50 {
		t.Fatalf("event %+v", e)
	}
	started := startsRunnerAt(t, ts, port, udid, func(fr *fakeRunner) { fr.on("tap", tapAnswer) })
	r.close() // the runner process is gone
	res, err := d.Tap(ctxT(t), 10, 20, driver.ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	fresh := <-started
	if fresh == nil {
		t.Fatal("no new runner")
	}
	if res.Seq <= 50 {
		t.Errorf("action seq %d went backwards", res.Seq)
	}
	fresh.push(map[string]any{"ev": "changed", "seq": 5})
	if e := waitEvent(t, events, "changed"); e.Seq <= res.Seq {
		t.Errorf("event seq %d after %d", e.Seq, res.Seq)
	}
	if d.Seq() <= 50 {
		t.Errorf("Seq %d went backwards", d.Seq())
	}
	// A settle since a driver count is sent as the new runner's count.
	fresh.on("settle", func(map[string]any) map[string]any { return map[string]any{"result": "changed"} })
	d.Settle(ctxT(t), d.Seq(), 100, time.Second)
	if q := fresh.last("settle"); num(t, q, "since") > 5 {
		t.Errorf("settle since %v, want the runner's count", q["since"])
	}
}

// Issue #101: a runner connection mended mid-flow left no trace. On a real simulator shut
// down mid-run, xcodebuild booted it again and the reconnect started a new runner: the next
// step found the home screen and blamed a slow simulator, and nothing in the flow's log said
// the runner had been lost. The log must say the connection dropped and tb reconnected,
// whether to the same runner or to a new one.
func TestReconnectIsNoted(t *testing.T) {
	t.Run("same runner", func(t *testing.T) {
		d, r, _, _, _ := openAtPreferredPort(t)
		r.on("tap", tapAnswer)
		d.Notes()
		r.dropConns()
		if _, err := d.Tap(ctxT(t), 10, 20, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
		if n := strings.Join(d.Notes(), "\n"); !strings.Contains(n, "the connection to the tb runner dropped") || !strings.Contains(n, "reconnected") {
			t.Errorf("issue #101: notes after a reconnect = %q, want one saying the connection dropped and tb reconnected", n)
		}
	})
	t.Run("new runner", func(t *testing.T) {
		d, r, ts, udid, port := openAtPreferredPort(t)
		started := startsRunnerAt(t, ts, port, udid, func(fr *fakeRunner) { fr.on("tap", tapAnswer) })
		d.Notes()
		r.close() // the runner process is gone
		if _, err := d.Tap(ctxT(t), 10, 20, driver.ActionOpts{}); err != nil {
			t.Fatal(err)
		}
		<-started
		if n := strings.Join(d.Notes(), "\n"); !strings.Contains(n, "the connection to the tb runner dropped") || !strings.Contains(n, "reconnected") {
			t.Errorf("issue #101: notes after a runner restart = %q, want one saying the connection dropped and tb reconnected", n)
		}
	})
}

// After StopRunner the dropped connection is expected: no restart, no "disconnected".
func TestStopRunnerDoesNotReconnect(t *testing.T) {
	d, r, ts, _, _ := openAtPreferredPort(t)
	r.on("exit", func(map[string]any) map[string]any {
		go func() { time.Sleep(20 * time.Millisecond); r.close() }()
		return map[string]any{"bye": true}
	})
	events, cancel := d.Events()
	defer cancel()
	if err := d.StopRunner(); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		t.Errorf("event %+v", e)
	case <-time.After(500 * time.Millisecond):
	}
	if xc := ts.calls("xcrun"); len(xc) != 0 {
		t.Errorf("xcrun ran: %q", xc)
	}
}

// An xcodebuild that exits before its runner serves ends the wait at once (not after
// minutes), is tried once more, and the error carries the end of its log.
func TestConnectXcodebuildExitsEarly(t *testing.T) {
	ts := stubTools(t)
	port := freePorts(t, 1)
	udid := udidFor(t, port)
	ts.set("xcrun", []stubRule{{match: "xcodebuild", out: "Testing failed:\n\tUnable to boot the Simulator.\n", code: 65}})
	t0 := time.Now()
	_, err := Open(ctxT(t), device.Info{Platform: device.IOS, Serial: udid, Name: "iPhone"})
	if err == nil {
		t.Fatal("no error")
	}
	if took := time.Since(t0); took > 8*time.Second {
		t.Errorf("took %s", took)
	}
	for _, want := range []string{"did not start on iPhone", "xcodebuild exited before the runner was up", "exit status 65", "Unable to boot the Simulator."} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q lacks %q", err, want)
		}
	}
	n := 0
	for _, c := range ts.calls("xcrun") {
		if len(c) > 0 && c[0] == "xcodebuild" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("xcodebuild started %d times, want 2", n)
	}
}

// makeBundle writes a small .app (a file, a nested file, a symlink) and returns its path.
func makeBundle(t *testing.T, home string) string {
	t.Helper()
	b := filepath.Join(home, "Library/Developer/CoreSimulator/Devices", testUDID, "data/Containers/Bundle/Application/B1/App.app")
	os.MkdirAll(filepath.Join(b, "Frameworks/X.framework"), 0o755)
	os.WriteFile(filepath.Join(b, "Info.plist"), []byte("plist"), 0o644)
	os.WriteFile(filepath.Join(b, "App"), []byte("binary"), 0o755)
	os.WriteFile(filepath.Join(b, "Frameworks/X.framework/X"), []byte("x"), 0o755)
	os.Symlink("X", filepath.Join(b, "Frameworks/X.framework/Current"))
	return b
}

// ClearState terminates the app, waits until launchd no longer runs it, and reinstalls it
// from a copy of its bundle (removed afterwards), then resets its privacy decisions.
func TestClearStateReinstalls(t *testing.T) {
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	once := filepath.Join(t.TempDir(), "listed")
	running := `printf '123\t0\tUIKitApplication:` + app + `[ab12][rb-legacy]\n'`
	ts.set("xcrun", []stubRule{
		{match: app + " app", out: bundle + "\n"},
		// Running at the first look, gone at the second.
		{match: "launchctl list", sh: "if [ ! -e " + sq(once) + " ]; then : > " + sq(once) + "; " + running + "; exit 0; fi; printf '-\t0\tUIKitApplication:" + app + "[ab12][rb-legacy]\n'; exit 0;"},
		// The copy is complete when it is installed.
		{match: "simctl install " + testUDID, sh: `[ -f "$4/Info.plist" ] && [ -x "$4/App" ] && [ -f "$4/Frameworks/X.framework/X" ] && [ -L "$4/Frameworks/X.framework/Current" ] || exit 3;`},
	})
	d := &Driver{info: testInfo(), locationSet: map[string]string{app: "always"}}
	if err := d.ClearState(ctxT(t), app); err != nil {
		t.Fatal(err)
	}
	got := ts.simctl()
	var installed string
	var order []string
	for _, c := range got {
		f := strings.Fields(c)
		order = append(order, f[0])
		if f[0] == "install" {
			installed = f[2]
		}
	}
	want := "get_app_container terminate spawn terminate spawn uninstall install privacy"
	if strings.Join(order, " ") != want {
		t.Errorf("simctl %q\nwant %s", got, want)
	}
	if filepath.Base(installed) != "App.app" || strings.HasPrefix(installed, filepath.Dir(bundle)) {
		t.Errorf("installed %q, want a copy", installed)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Errorf("the copy was not removed: %v", err)
	}
	if _, ok := d.locationSet[app]; ok {
		t.Error("location cache kept")
	}
	if sql := ts.sql(); len(sql) != 1 {
		t.Errorf("TCC reset %q", sql)
	}
}

// ClearState's reinstall failures: an app still running after the wait, an install that fails
// twice (the copy is kept and named), an app that is not installed.
func TestClearStateReinstallErrors(t *testing.T) {
	defer func(v time.Duration) { stopWait = v }(stopWait)
	stopWait = 300 * time.Millisecond
	ts := stubTools(t)
	bundle := makeBundle(t, ts.home)
	d := &Driver{info: testInfo()}

	ts.set("xcrun", []stubRule{
		{match: app + " app", out: bundle + "\n"},
		{match: "launchctl list", out: "123\t0\tUIKitApplication:" + app + "[ab12][rb-legacy]\n"},
	})
	if err := d.ClearState(ctxT(t), app); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("err = %v", err)
	}

	ts.set("xcrun", []stubRule{
		{match: app + " app", out: bundle + "\n"},
		{match: "simctl install " + testUDID, out: "install failed", code: 1},
	})
	err := d.ClearState(ctxT(t), app)
	if err == nil || !strings.Contains(err.Error(), "its bundle is kept at ") {
		t.Fatalf("err = %v", err)
	}
	kept := strings.Fields(strings.SplitN(err.Error(), "kept at ", 2)[1])[0]
	kept = strings.TrimSuffix(kept, "):")
	if _, err := os.Stat(filepath.Join(kept, "Info.plist")); err != nil {
		t.Errorf("copy not kept at %q: %v", kept, err)
	} else {
		os.RemoveAll(filepath.Dir(kept))
	}
	n := 0
	for _, c := range ts.simctl() {
		if strings.HasPrefix(c, "install ") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("install tried %d times, want 2", n)
	}

	ts.set("xcrun", []stubRule{{match: " app", out: "No such app", code: 2}})
	if err := d.ClearState(ctxT(t), app); err == nil || !strings.Contains(err.Error(), "is not installed") {
		t.Errorf("err = %v", err)
	}
}

// Password AutoFill is turned off in both settings files (keys created when missing), files
// that do not exist are skipped, and TB_IOS_KEEP_AUTOFILL=1 leaves them alone.
func TestDisablePasswordAutoFill(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("no plutil")
	}
	bin := t.TempDir()
	os.Symlink(plutil, filepath.Join(bin, "plutil"))
	t.Setenv("PATH", bin)

	write := func(dir, rel, body string) string {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>`+body+`</dict></plist>`), 0o644)
		return p
	}
	value := func(p string) string {
		out, err := exec.Command(plutil, "-extract", "restrictedBool.allowPasswordAutoFill.value", "raw", p).CombinedOutput()
		if err != nil {
			return "missing"
		}
		return strings.TrimSpace(string(out))
	}

	data := t.TempDir()
	a := write(data, autoFillPlists[0], `<key>restrictedBool</key><dict><key>allowPasswordAutoFill</key><dict><key>value</key><true/></dict><key>other</key><dict><key>value</key><true/></dict></dict>`)
	b := write(data, autoFillPlists[1], `<key>somethingElse</key><string>x</string>`)
	disablePasswordAutoFill(ctxT(t), data)
	if value(a) != "false" || value(b) != "false" {
		t.Errorf("values %s, %s", value(a), value(b))
	}
	if out, _ := exec.Command(plutil, "-extract", "restrictedBool.other.value", "raw", a).Output(); strings.TrimSpace(string(out)) != "true" {
		t.Error("another setting changed")
	}
	if out, _ := exec.Command(plutil, "-extract", "somethingElse", "raw", b).Output(); strings.TrimSpace(string(out)) != "x" {
		t.Error("existing keys lost")
	}

	// Only one file there: the other is skipped quietly.
	only := t.TempDir()
	c := write(only, autoFillPlists[1], ``)
	disablePasswordAutoFill(ctxT(t), only)
	if value(c) != "false" {
		t.Errorf("value %s", value(c))
	}
	if _, err := os.Stat(filepath.Join(only, autoFillPlists[0])); !os.IsNotExist(err) {
		t.Error("a missing file was created")
	}

	t.Setenv("TB_IOS_KEEP_AUTOFILL", "1")
	keep := t.TempDir()
	k := write(keep, autoFillPlists[0], `<key>restrictedBool</key><dict><key>allowPasswordAutoFill</key><dict><key>value</key><true/></dict></dict>`)
	disablePasswordAutoFill(ctxT(t), keep)
	if value(k) != "true" {
		t.Errorf("TB_IOS_KEEP_AUTOFILL=1: value %s", value(k))
	}
}
