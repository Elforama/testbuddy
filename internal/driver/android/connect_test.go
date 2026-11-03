package android

// Agent start-up and reconnection, against the fake agent and a stub adb (see fakes_test.go):
// the port forwards, pushing and starting the agent, clearing a leftover driver, and the
// reconnect after the adb link drops.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/legacy"
	"github.com/elforama/testbuddy/internal/wire"
)

func remoteAPK() string { return "/data/local/tmp/tb-agent-" + agentHash() + ".apk" }

func forwardLine(port int) string {
	return fmt.Sprintf("other-serial tcp:1 localabstract:testbuddy-agent\n%s tcp:%d localabstract:testbuddy-agent\n%s tcp:2 tcp:8080\n", testSerial, port, testSerial)
}

// An agent of this build behind an existing forward is reused at once: no forward, push or start.
func TestConnectReusesExistingForward(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a.port())}})
	d := s.driver()
	if err := d.connect(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if got := s.joined(); !reflect.DeepEqual(got, []string{"forward --list"}) {
		t.Errorf("adb calls %v, want only the forward list", got)
	}
	if d.port != a.port() || d.sdk != 34 {
		t.Errorf("port %d sdk %d", d.port, d.sdk)
	}
}

// Open connects through a new forward when none exists, and the driver is ready to use.
func TestOpenWithNewForward(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	a.on("foreground", func(map[string]any) map[string]any { return map[string]any{"pkg": "com.x"} })
	s := stubADB(t, []stubRule{{match: "forward tcp:0 " + socketName, out: fmt.Sprintf("%d\n", a.port())}})
	d, err := Open(ctxT(t), device.Info{Platform: device.Android, Serial: testSerial, Name: "Pixel"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want := []string{"forward --list", "-s " + testSerial + " forward tcp:0 " + socketName}
	if got := s.joined(); !reflect.DeepEqual(got, want) {
		t.Errorf("adb calls %v, want %v", got, want)
	}
	if p, err := d.Foreground(ctxT(t)); err != nil || p != "com.x" {
		t.Errorf("Foreground = %q, %v", p, err)
	}
	if d.Name() != "Pixel" {
		t.Errorf("Name = %q", d.Name())
	}
}

// An agent of another build is replaced: the APK is pushed under its hash and started with app_process.
func TestConnectStartsAgentOfThisBuild(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	a.greet(func(n int) map[string]any {
		v := agentVersion()
		if n == 1 {
			v = "0.0.1-old"
		}
		return map[string]any{"ev": "hello", "platform": "android", "version": v}
	})
	s := stubADB(t, nil)
	pushed := filepath.Join(t.TempDir(), "pushed.apk")
	s.rules([]stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: " push ", sh: "/bin/cp \"$4\" " + sq(pushed) + ";"},
	})
	d := s.driver()
	if err := d.connect(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	calls := s.calls()
	var shells, pushes [][]string
	for _, c := range calls {
		if len(c) > 2 && c[2] == "shell" {
			shells = append(shells, c)
		}
		if len(c) > 2 && c[2] == "push" {
			pushes = append(pushes, c)
		}
	}
	if len(shells) != 2 || len(pushes) != 1 {
		t.Fatalf("adb calls:\n%s", strings.Join(s.joined(), "\n"))
	}
	prep := shells[0][3]
	for _, want := range []string{"ls " + remoteAPK() + " 2>/dev/null", "pids=$(pidof tb-agent) && kill $pids",
		`for f in /data/local/tmp/tb-agent-*.apk; do [ "$f" != "` + remoteAPK() + `" ] && rm -f "$f"; done; true`} {
		if !strings.Contains(prep, want) {
			t.Errorf("prepare command lacks %q:\n%s", want, prep)
		}
	}
	if pushes[0][4] != remoteAPK() || !strings.HasPrefix(filepath.Base(pushes[0][3]), "tb-agent-") {
		t.Errorf("push %v", pushes[0])
	}
	if b, err := os.ReadFile(pushed); err != nil || !bytes.Equal(b, agentAPK) {
		t.Errorf("pushed %d bytes (%v), want the embedded APK's %d", len(b), err, len(agentAPK))
	}
	if _, err := os.Stat(pushes[0][3]); !os.IsNotExist(err) {
		t.Errorf("the temporary APK %s was left behind", pushes[0][3])
	}
	start := "CLASSPATH=" + remoteAPK() + " nohup app_process / --nice-name=tb-agent dev.testbuddy.agent.Main > /data/local/tmp/tb-agent.log 2>&1 &"
	if shells[1][3] != start {
		t.Errorf("start command:\n%s\nwant:\n%s", shells[1][3], start)
	}
	if a.connCount() != 2 {
		t.Errorf("%d connections, want 2 (the old agent, then the new one)", a.connCount())
	}
}

// With this build already on the device nothing is pushed; NoopService is installed and enabled before the start.
func TestConnectStartWithoutPushAndWithNoopService(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "")
	a := newFakeAgent(t)
	a.greet(func(n int) map[string]any {
		return map[string]any{"ev": "hello", "version": map[bool]string{true: "old", false: agentVersion()}[n == 1]}
	})
	s := stubADB(t, nil)
	s.rules([]stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "shell ls ", out: remoteAPK() + "\n"},
	})
	d := s.driver()
	if err := d.connect(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	joined := s.joined()
	if containsAny(joined, " push ") {
		t.Errorf("pushed although the device has the APK:\n%s", strings.Join(joined, "\n"))
	}
	var start string
	for _, c := range s.calls() {
		if len(c) > 3 && c[2] == "shell" && strings.Contains(c[3], "app_process") {
			start = c[3]
		}
	}
	for _, want := range []string{
		"inst=$(pm path " + agentPackage + " 2>/dev/null | sed 's/^package://'); ",
		`if [ -z "$inst" ] || ! cmp -s "$inst" ` + remoteAPK() + `; then pm install -r -t ` + remoteAPK() + ` >/dev/null 2>&1; sleep 1; fi; `,
		"settings put secure enabled_accessibility_services " + noopService + " ;;",
		"settings put secure accessibility_enabled 1; CLASSPATH=" + remoteAPK(),
	} {
		if !strings.Contains(start, want) {
			t.Errorf("start command lacks %q:\n%s", want, start)
		}
	}
	if len(a.shCmds()) != 1 {
		t.Errorf("adopt sent %d service commands, want 1", len(a.shCmds()))
	}
}

// When the agent does not come up, the error names the device and quotes the agent's log.
func TestConnectFailureQuotesLog(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	a.on("info", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "not ready"} })
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "tail -n 20 /data/local/tmp/tb-agent.log", out: "  Exception in main\n  at Main.java:1\n"},
	})
	d := s.driver()
	err := d.connect(ctxT(t))
	if err == nil {
		d.Close()
		t.Fatal("connected to an agent that never answers info")
	}
	want := "the Test Buddy agent did not start on " + testSerial + ": info: not ready\nException in main\n  at Main.java:1"
	if err.Error() != want {
		t.Errorf("error:\n%s\nwant:\n%s", err, want)
	}
	if containsAny(s.joined(), "pidof dev.mobile") {
		t.Error("looked for a leftover driver without an \"already registered\" log")
	}
}

// Without a forward the start-up fails with adb's error.
func TestConnectForwardFails(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	s := stubADB(t, []stubRule{{match: "forward tcp:0", out: "error: device offline", code: 1}})
	d := s.driver()
	err := d.connect(ctxT(t))
	if err == nil || !strings.Contains(err.Error(), "adb forward tcp:0 "+socketName) || !strings.Contains(err.Error(), "device offline") {
		t.Errorf("err = %v", err)
	}
	if containsAny(s.joined(), "app_process") != true {
		t.Error("the agent was not started before the forward was retried")
	}
}

// A forward that prints no port is an error too.
func TestForwardWithoutPort(t *testing.T) {
	s := stubADB(t, []stubRule{{match: "forward tcp:0", out: "garbage"}, {match: " push ", out: "fail", code: 1}})
	d := s.driver()
	if err := d.connect(ctxT(t)); err == nil || !strings.Contains(err.Error(), "adb push") {
		t.Errorf("err = %v (a failed push ends the start)", err)
	}
}

// "already registered" in the log means another UiAutomation client (Maestro's driver) holds the
// slot: it is stopped and the connection retried once.
func TestConnectStopsLeftoverDriverAndRetries(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	var infos atomic.Int32
	a.on("info", func(map[string]any) map[string]any {
		if infos.Add(1) <= 2 {
			return map[string]any{"ok": false, "error": "UiAutomation not connected"}
		}
		return map[string]any{"w": 720, "h": 1280, "sdk": 30}
	})
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "tail -n 20", out: "java.lang.IllegalStateException: UiAutomationService android.accessibilityservice.IAccessibilityServiceClient@1 already registered!"},
		{match: "pidof " + legacy.AndroidDriverPackages()[1], out: "4242\n"},
	})
	pgrep := s.tool("pgrep", []stubRule{{match: "-f", code: 1}})
	d := s.driver()
	if err := d.connect(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.w != 720 || d.sdk != 30 {
		t.Errorf("adopted size %d sdk %d", d.w, d.sdk)
	}
	if got := readStubLog(t, pgrep); len(got) != 1 || !reflect.DeepEqual(got[0], []string{"-f", legacy.HostProcessPattern()}) {
		t.Errorf("pgrep calls %v", got)
	}
	cleanup := "am force-stop dev.mobile.maestro.test; am force-stop dev.mobile.maestro; " +
		"for p in $(pidof app_process); do grep -q dev.mobile.maestro /proc/$p/cmdline && kill $p; done; true"
	if !containsAny(s.joined(), "shell "+cleanup) {
		t.Errorf("no cleanup command %q in:\n%s", cleanup, strings.Join(s.joined(), "\n"))
	}
}

// The retry after a cleanup happens once: a second failure is reported.
func TestConnectRetriesOnlyOnce(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	a.on("info", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "busy"} })
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "tail -n 20", out: "already registered"},
		{match: "pidof dev.mobile.maestro", out: "77"},
	})
	d := s.driver()
	err := d.connect(ctxT(t))
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("err = %v", err)
	}
	n := 0
	for _, c := range s.joined() {
		if strings.Contains(c, "pidof dev.mobile.maestro") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("looked for the leftover driver %d times, want 1", n)
	}
}

// stopLeftoverDriver leaves Maestro's driver alone while Maestro runs here, and does nothing when none runs on the device.
func TestStopLeftoverDriver(t *testing.T) {
	cases := []struct {
		name   string
		pgrep  string
		pidof  string
		want   bool
		stopCd bool
	}{
		{"maestro running on this machine", "1234\n", "4242", false, false},
		{"no driver on the device", "", "", false, false},
		{"leftover driver", "", "4242 4243\n", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := stubADB(t, []stubRule{{match: "pidof dev.mobile.maestro", out: c.pidof}})
			code := 0
			if c.pgrep == "" {
				code = 1
			}
			s.tool("pgrep", []stubRule{{match: "-f", out: c.pgrep, code: code}})
			d := s.driver()
			if got := d.stopLeftoverDriver(ctxT(t)); got != c.want {
				t.Errorf("stopLeftoverDriver = %v, want %v", got, c.want)
			}
			calls := s.joined()
			if c.pgrep != "" && len(calls) != 0 {
				t.Errorf("touched the device while Maestro runs: %v", calls)
			}
			if containsAny(calls, "force-stop") != c.stopCd {
				t.Errorf("adb calls %v", calls)
			}
		})
	}
}

// When the adb link drops, the pump reconnects through the forward to the still-running agent; the
// counter carries on past the old one and no "disconnected" reaches subscribers.
func TestReconnectAfterDrop(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	a2.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 5} })
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	c1, h, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	events, cancel := d.Events()
	defer cancel()
	a1.push(map[string]any{"ev": "changed", "seq": 100})
	waitEvent(t, events, "changed")
	a1.dropConns()
	eventually(t, "the reconnect", func() bool { return d.conn() != c1 })
	r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Seq != 106 {
		t.Errorf("seq after reconnect = %d, want 106 (101 + 5)", r.Seq)
	}
	if len(a2.requests("tap")) != 1 {
		t.Errorf("the new agent got %d taps", len(a2.requests("tap")))
	}
	select {
	case e := <-events:
		if e.Kind == "disconnected" {
			t.Error("subscribers were told about a drop the driver recovered from")
		}
	case <-time.After(50 * time.Millisecond):
	}
}

// A reconnect (adopt, on the pump goroutine) rewrites the port and size while other
// goroutines read them (Size): they are locked. Run with -race to check.
func TestReconnectSizeIsSynchronized(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	c1, h, _ := dialHello(a1.port(), time.Second)
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	a1.dropConns()
	for d.conn() == c1 {
		d.Size()
	}
	d.Size()
}

// A request whose connection already dropped reconnects and is sent once more on the new one.
func TestCallRetriesAfterDrop(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	a2.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "no_change", "seq": 2} })
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	c1, _, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.connP.Store(c1) // no pump: only the request notices the drop
	defer d.Close()
	a1.dropConns()
	<-c1.Done()
	r, err := d.Tap(ctxT(t), 3, 4, driver.ActionOpts{})
	if err != nil || r.Settle != driver.NoChange || r.Seq != 2 {
		t.Fatalf("Tap = %+v, %v", r, err)
	}
	if d.conn() == c1 || len(a1.requests("tap")) != 0 || len(a2.requests("tap")) != 1 {
		t.Errorf("tap went to the old agent %d, new %d times", len(a1.requests("tap")), len(a2.requests("tap")))
	}
}

// A request in flight when the link drops is retried on the new connection.
func TestCallRetriesWhenDroppedInFlight(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	for i := 0; i < 3; i++ {
		a1, a2 := newFakeAgent(t), newFakeAgent(t)
		a1.on("tap", func(map[string]any) map[string]any { a1.dropConns(); return noReply })
		a2.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 9} })
		s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
		d := s.driver()
		c1, _, err := dialHello(a1.port(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		d.connP.Store(c1)
		r, err := d.Tap(ctxT(t), 3, 4, driver.ActionOpts{})
		d.Close()
		if err != nil || r.Seq != 9 {
			t.Fatalf("run %d: Tap = %+v, %v", i, r, err)
		}
	}
}

// A failed reconnect is reported with the request's error.
func TestCallReportsFailedReconnect(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1 := newFakeAgent(t)
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: device offline", code: 1},
		{match: " push ", out: "error: device offline", code: 1},
	})
	d := s.driver()
	c1, _, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.connP.Store(c1)
	defer d.Close()
	a1.dropConns()
	<-c1.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, err = d.Tap(ctx, 1, 1, driver.ActionOpts{})
	if err == nil || !strings.Contains(err.Error(), "agent connection closed (reconnecting failed: adb push") {
		t.Errorf("err = %v", err)
	}
	if el := time.Since(t0); el > 2*time.Second {
		t.Errorf("gave up after %s, want about the context's 300 ms", el)
	}
}

// SetLocation on an emulator sends a geo fix (longitude first).
func TestSetLocationEmulator(t *testing.T) {
	s := stubADB(t, nil)
	d := s.driver()
	if err := d.SetLocation(ctxT(t), 52.52, -13.405); err != nil {
		t.Fatal(err)
	}
	if got := s.joined(); len(got) != 1 || got[0] != "-s "+testSerial+" emu geo fix -13.405 52.52" {
		t.Errorf("adb calls %v", got)
	}
	s.rules([]stubRule{{match: "emu", out: "KO: no console", code: 1}})
	if err := d.SetLocation(ctxT(t), 1, 2); err == nil || !strings.Contains(err.Error(), "KO: no console") {
		t.Errorf("err = %v", err)
	}
}

// stubbedDriver connects to the fake agent with the stub adb behind the driver.
func stubbedDriver(t *testing.T, s *adbStub, a *fakeAgent) *Driver {
	t.Helper()
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	d := s.driver()
	c, h, err := dialHello(a.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c, a.port(), h)
	t.Cleanup(func() { d.Close() })
	return d
}

// AddMedia pushes each file to /sdcard/Pictures and asks the media scanner to index it.
func TestAddMedia(t *testing.T) {
	a := newFakeAgent(t)
	s := stubADB(t, nil)
	d := stubbedDriver(t, s, a)
	if err := d.AddMedia(ctxT(t), []string{"/photos/cat.png", "dog's.jpg"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"-s " + testSerial + " push /photos/cat.png /sdcard/Pictures/cat.png", "-s " + testSerial + " push dog's.jpg /sdcard/Pictures/dog's.jpg"}
	if got := s.joined(); !reflect.DeepEqual(got, want) {
		t.Errorf("adb calls %v", got)
	}
	cmds := a.shCmds()
	if len(cmds) != 2 || !strings.Contains(cmds[1], `-d 'file:///sdcard/Pictures/dog'\''s.jpg'`) ||
		!strings.Contains(cmds[1], `--method scan_file --arg '/sdcard/Pictures/dog'\''s.jpg'`) ||
		!strings.HasPrefix(cmds[0], "am broadcast -a android.intent.action.MEDIA_SCANNER_SCAN_FILE") {
		t.Errorf("scan commands %v", cmds)
	}
	s.rules([]stubRule{{match: "push", out: "no such file", code: 1}})
	a.reset()
	if err := d.AddMedia(ctxT(t), []string{"a.png", "b.png"}); err == nil || len(a.shCmds()) != 0 {
		t.Errorf("failed push: %v, scanned %v", err, a.shCmds())
	}
}

// InstallApp installs with -r (replace) and -g (grant runtime permissions).
func TestInstallApp(t *testing.T) {
	s := stubADB(t, nil)
	d := s.driver()
	if err := d.InstallApp(ctxT(t), "/builds/app-debug.apk"); err != nil {
		t.Fatal(err)
	}
	if got := s.joined(); len(got) != 1 || got[0] != "-s "+testSerial+" install -r -g /builds/app-debug.apk" {
		t.Errorf("adb calls %v", got)
	}
	s.rules([]stubRule{{match: "install", out: "INSTALL_FAILED_OLDER_SDK", code: 1}})
	if err := d.InstallApp(ctxT(t), "x.apk"); err == nil || !strings.Contains(err.Error(), "INSTALL_FAILED_OLDER_SDK") {
		t.Errorf("err = %v", err)
	}
}

// Recording runs screenrecord through adb, stops it with SIGINT on the device, pulls the file and removes it.
func TestRecording(t *testing.T) {
	a := newFakeAgent(t)
	s := stubADB(t, nil)
	d := stubbedDriver(t, s, a)
	out := filepath.Join(t.TempDir(), "run.mp4")
	if err := d.StartRecording(ctxT(t), out); err != nil {
		t.Fatal(err)
	}
	if err := d.StartRecording(ctxT(t), out); err == nil || err.Error() != "already recording" {
		t.Errorf("second start: %v", err)
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-s " + testSerial + " shell screenrecord --bit-rate 4000000 /sdcard/tb-recording.mp4",
		"-s " + testSerial + " pull /sdcard/tb-recording.mp4 " + out,
	}
	if got := s.joined(); !reflect.DeepEqual(got, want) {
		t.Errorf("adb calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := a.shCmds(); !reflect.DeepEqual(got, []string{"pkill -INT screenrecord", "rm -f /sdcard/tb-recording.mp4"}) {
		t.Errorf("agent commands %v", got)
	}
	if d.recordCmd != nil {
		t.Error("still recording after stop")
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Errorf("second stop: %v", err)
	}
}

// A failed pull is reported (and the recording is still over).
func TestRecordingPullFails(t *testing.T) {
	a := newFakeAgent(t)
	s := stubADB(t, []stubRule{{match: " pull ", out: "remote object does not exist", code: 1}})
	d := stubbedDriver(t, s, a)
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "x.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := d.StopRecording(ctxT(t)); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v", err)
	}
	if d.recordCmd != nil {
		t.Error("still recording")
	}
}

// Open reports a device whose agent cannot be started.
func TestOpenFails(t *testing.T) {
	stubADB(t, []stubRule{{match: "forward tcp:0", out: "error: no devices", code: 1}, {match: " push ", out: "error: no devices", code: 1}})
	d, err := Open(ctxT(t), device.Info{Platform: device.Android, Serial: testSerial})
	if err == nil || d != nil {
		t.Errorf("Open = %v, %v", d, err)
	}
}

// startAgent fails cleanly when the APK cannot be written to a temporary file.
func TestStartAgentWithoutTempDir(t *testing.T) {
	s := stubADB(t, nil)
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if err := s.driver().startAgent(ctxT(t)); err == nil {
		t.Error("startAgent succeeded without a temporary directory")
	}
	if containsAny(s.joined(), " push ") {
		t.Error("pushed without an APK file")
	}
}

// A failing start command is reported.
func TestStartAgentStartFails(t *testing.T) {
	s := stubADB(t, []stubRule{{match: "app_process", out: "error: closed", code: 1}})
	if err := s.driver().startAgent(ctxT(t)); err == nil || !strings.Contains(err.Error(), "error: closed") {
		t.Errorf("err = %v", err)
	}
}

// An agent that greets the moment it accepts must still be seen by wire.Dial.
func TestDialSeesImmediateHello(t *testing.T) {
	a := newFakeAgent(t)
	for i := 0; i < 3000; i++ {
		c, err := wire.Dial(a.ln.Addr().String(), 300*time.Millisecond)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		c.Close()
	}
}

// SetLocation on Android 12+ adds the shell's test providers once, sets the point on each,
// and Close removes them again.
func TestSetLocationTestProviders(t *testing.T) {
	a := newFakeAgent(t) // reports sdk 34
	s := stubADB(t, nil)
	d := stubbedDriver(t, s, a)
	if err := d.SetLocation(ctxT(t), 45.5017, -73.5673); err != nil {
		t.Fatal(err)
	}
	cmds := a.shCmds()
	joined := strings.Join(cmds, "\n")
	for _, p := range []string{"gps", "network", "fused"} {
		for _, want := range []string{
			"cmd location providers add-test-provider " + p,
			"cmd location providers set-test-provider-enabled " + p + " true",
			"cmd location providers set-test-provider-location " + p + " --location 45.5017,-73.5673 --accuracy 1",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in %v", want, cmds)
			}
		}
	}
	if len(s.joined()) != 0 {
		t.Errorf("no geo fix wanted, adb calls %v", s.joined())
	}
	// A second point: the providers are added once.
	a.reset()
	if err := d.SetLocation(ctxT(t), 1, 2); err != nil {
		t.Fatal(err)
	}
	if c := a.shCmds(); len(c) != 1 || !strings.Contains(c[0], "--location 1,2") || strings.Contains(c[0], "add-test-provider") {
		t.Errorf("second point: %v", c)
	}
	a.reset()
	d.Close()
	if c := strings.Join(a.shCmds(), "\n"); !strings.Contains(c, "remove-test-provider gps") || !strings.Contains(c, "remove-test-provider fused") {
		t.Errorf("Close: %v", a.shCmds())
	}
}

// Dark mode is the system's night mode.
func TestDarkMode(t *testing.T) {
	a := newFakeAgent(t)
	a.on("sh", func(req map[string]any) map[string]any {
		if req["cmd"] == "cmd uimode night" {
			return map[string]any{"code": 0, "out": "Night mode: yes\n"}
		}
		return map[string]any{"code": 0, "out": ""}
	})
	d := stubbedDriver(t, stubADB(t, nil), a)
	if err := d.SetDarkMode(ctxT(t), true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetDarkMode(ctxT(t), false); err != nil {
		t.Fatal(err)
	}
	if c := a.shCmds(); len(c) != 2 || c[0] != "cmd uimode night yes" || c[1] != "cmd uimode night no" {
		t.Errorf("commands %v", c)
	}
	if on, err := d.DarkMode(ctxT(t)); err != nil || !on {
		t.Errorf("DarkMode = %v, %v", on, err)
	}
}
