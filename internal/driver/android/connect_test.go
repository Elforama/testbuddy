package android

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
	for _, want := range []string{"ls " + remoteAPK() + " 2>/dev/null", killAgent, "kill -9 $left",
		"remove-test-provider $p; done; rm -f /data/local/tmp/tb-location-providers",
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
		`if [ -z "$inst" ] || ! cmp -s "$inst" ` + remoteAPK() + `; then `,
		`res=$(pm install -r -t ` + remoteAPK() + ` 2>&1); `,
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

func TestConnectFailureQuotesLog(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	a.on("info", func(map[string]any) map[string]any { return map[string]any{"ok": false, "error": "not ready"} })
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "tail -n 20 /data/local/tmp/tb-agent.log", out: "  Exception in main\n  at Main.java:1\n"},
		{match: "get-state", out: "device\n"},
	})
	d := s.driver()
	err := d.connect(ctxT(t))
	if err == nil {
		d.Close()
		t.Fatal("connected to an agent that never answers info")
	}
	want := "the Test Buddy agent did not start on " + testSerial + ": info: not ready; " +
		`adb sees the device ("device"), but the agent is not running: it exited (its log, if any, follows)` +
		"\nException in main\n  at Main.java:1"
	if err.Error() != want {
		t.Errorf("error:\n%s\nwant:\n%s", err, want)
	}
	if containsAny(s.joined(), "pidof dev.mobile") {
		t.Error("looked for a leftover driver without an \"already registered\" log")
	}
}

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

func TestForwardWithoutPort(t *testing.T) {
	s := stubADB(t, []stubRule{{match: "forward tcp:0", out: "garbage"}, {match: " push ", out: "fail", code: 1}})
	d := s.driver()
	if err := d.connect(ctxT(t)); err == nil || !strings.Contains(err.Error(), "adb push") {
		t.Errorf("err = %v (a failed push ends the start)", err)
	}
}

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
	if err := d.Lost(); err != nil {
		t.Errorf("Lost = %v after a drop the driver recovered from", err)
	}
	select {
	case e := <-events:
		if e.Kind == "disconnected" {
			t.Error("subscribers were told about a drop the driver recovered from")
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestReconnectSizeIsSynchronized(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	c1, h, _ := dialHello(a1.port(), time.Second)
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	a1.dropConns()
	deadline := time.Now().Add(10 * time.Second)
	for d.conn() == c1 {
		if time.Now().After(deadline) {
			t.Fatal("never reconnected after the drop")
		}
		d.Size()
	}
	d.Size()
}

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
	d.connP.Store(c1)
	defer d.Close()
	a1.dropConns()
	<-c1.Done()
	r, err := d.Tap(ctxT(t), 3, 4, driver.ActionOpts{})
	if err != nil || r.Settle != driver.NoChange || r.Seq != 3 {
		t.Fatalf("Tap = %+v, %v", r, err)
	}
	if d.conn() == c1 || len(a1.requests("tap")) != 0 || len(a2.requests("tap")) != 1 {
		t.Errorf("tap went to the old agent %d, new %d times", len(a1.requests("tap")), len(a2.requests("tap")))
	}
}

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
		if err != nil || r.Seq != 10 {
			t.Fatalf("run %d: Tap = %+v, %v", i, r, err)
		}
	}
}

func TestReconnectSurvivesDropDuringAdopt(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	var shs atomic.Int32
	a2.on("sh", func(map[string]any) map[string]any {
		if shs.Add(1) == 1 {
			a2.dropConns()
			return noReply
		}
		return map[string]any{"code": 0, "out": ""}
	})
	a2.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 4} })
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	c1, _, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.connP.Store(c1)
	defer d.Close()
	a1.dropConns()
	<-c1.Done()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type result struct {
		r   driver.ActionResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := d.Tap(ctx, 1, 1, driver.ActionOpts{})
		done <- result{r, err}
	}()
	select {
	case res := <-done:
		if res.err != nil || res.r.Seq == 0 {
			t.Fatalf("Tap = %+v, %v; want it sent on the next connection", res.r, res.err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Tap with a 3s context still blocked after 6s: reconnect deadlocked when the new connection dropped during adopt (issue #67)")
	}
	if n := len(a2.requests("tap")); n != 1 {
		t.Errorf("the new agent got %d taps, want 1", n)
	}
	if n := a2.connCount(); n != 2 {
		t.Errorf("connections to the new agent: %d, want 2 (the one that dropped, then the one that held)", n)
	}
}

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

func TestLostAfterReconnectGivesUp(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	defer func(v time.Duration) { reconnectFor = v }(reconnectFor)
	reconnectFor = 500 * time.Millisecond
	a1 := newFakeAgent(t)
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: device offline", code: 1},
		{match: " push ", out: "error: device offline", code: 1},
	})
	d := s.driver()
	c1, h, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	if err := d.Lost(); err != nil {
		t.Fatalf("Lost = %v on a working connection", err)
	}
	events, cancel := d.Events()
	defer cancel()
	a1.dropConns()
	waitEvent(t, events, "disconnected")
	if err := d.Lost(); err == nil || !strings.Contains(err.Error(), "could not be reached again") {
		t.Errorf("Lost = %v after reconnecting gave up", err)
	}
}

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

func recordingAgent(t *testing.T, unlimited bool, files string) *fakeAgent {
	a := newFakeAgent(t)
	a.on("sh", func(r map[string]any) map[string]any {
		cmd := fmt.Sprint(r["cmd"])
		switch {
		case strings.HasPrefix(cmd, "screenrecord --help") && unlimited:
			return map[string]any{"code": 0, "out": "--time-limit TIME\n    Default is 180. Set to 0\n    to remove the time limit."}
		case strings.HasPrefix(cmd, "screenrecord --help"):
			return map[string]any{"code": 0, "out": "--time-limit TIME\n    Default / maximum is 180."}
		case strings.HasPrefix(cmd, "ls "):
			return map[string]any{"code": 0, "out": files}
		}
		return map[string]any{"code": 0, "out": ""}
	})
	return a
}

func TestRecording(t *testing.T) {
	a := recordingAgent(t, true, "/sdcard/tb-recording-1.mp4\n")
	s := stubADB(t, []stubRule{{match: "screenrecord --verbose", out: "Configuring recorder\nContent area is 1080x2400 at offset x=0 y=0"}})
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
		"-s " + testSerial + " shell screenrecord --verbose --time-limit 0 --bit-rate 4000000 /sdcard/tb-recording-1.mp4",
		"-s " + testSerial + " pull /sdcard/tb-recording-1.mp4 " + out,
	}
	if got := s.joined(); !reflect.DeepEqual(got, want) {
		t.Errorf("adb calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	wantSh := []string{recordLeftoversSh, "screenrecord --help 2>&1",
		"touch /sdcard/tb-recording.stop; pkill -INT -f '^[^ ]*screenrecord .*/sdcard/tb-recordin[g]'", "ls /sdcard/tb-recording-*.mp4 2>/dev/null",
		"rm -f /sdcard/tb-recording-*.mp4 /sdcard/tb-recording.stop"}
	if got := a.shCmds(); !reflect.DeepEqual(got, wantSh) {
		t.Errorf("agent commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantSh, "\n"))
	}
	if d.recordCmd != nil || !reflect.DeepEqual(d.RecordingParts(), []string{out}) {
		t.Errorf("after stop: recording %v, parts %v", d.recordCmd != nil, d.RecordingParts())
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Errorf("second stop: %v", err)
	}
}

func TestRecordingInParts(t *testing.T) {
	a := recordingAgent(t, false, "/sdcard/tb-recording-10.mp4 /sdcard/tb-recording-2.mp4\n/sdcard/tb-recording-1.mp4\n")
	s := stubADB(t, []stubRule{{match: "screenrecord --verbose", out: "Content area is 1080x2400 at offset x=0 y=0"}})
	d := stubbedDriver(t, s, a)
	out := filepath.Join(t.TempDir(), "run.mp4")
	if err := d.StartRecording(ctxT(t), out); err != nil {
		t.Fatal(err)
	}
	if err := d.StopRecording(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	got := s.joined()
	if len(got) != 4 || !strings.Contains(got[0], "while [ ! -f /sdcard/tb-recording.stop ]; do screenrecord --verbose --bit-rate 4000000 /sdcard/tb-recording-$i.mp4") {
		t.Fatalf("adb calls %q", got)
	}
	base := strings.TrimSuffix(out, ".mp4")
	wantPulls := []string{"pull /sdcard/tb-recording-1.mp4 " + out, "pull /sdcard/tb-recording-2.mp4 " + base + "-part2.mp4",
		"pull /sdcard/tb-recording-10.mp4 " + base + "-part3.mp4"}
	for i, w := range wantPulls {
		if got[i+1] != "-s "+testSerial+" "+w {
			t.Errorf("pull %d = %q, want %q", i+1, got[i+1], w)
		}
	}
	if parts := d.RecordingParts(); !reflect.DeepEqual(parts, []string{out, base + "-part2.mp4", base + "-part3.mp4"}) {
		t.Errorf("parts %v", parts)
	}
}

func TestRecordingFailures(t *testing.T) {
	s := stubADB(t, []stubRule{{match: "screenrecord --verbose", out: "ERROR: unable to get output buffers", code: 1}})
	d := stubbedDriver(t, s, recordingAgent(t, true, ""))
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "x.mp4")); err == nil || !strings.Contains(err.Error(), "unable to get output buffers") {
		t.Errorf("start = %v", err)
	}
	if d.recordCmd != nil {
		t.Error("recording after a failed start")
	}

	s = stubADB(t, []stubRule{{match: "screenrecord --verbose", out: "Content area is 1x1"}})
	d = stubbedDriver(t, s, recordingAgent(t, true, ""))
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "x.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := d.StopRecording(ctxT(t)); err == nil || !strings.Contains(err.Error(), "saved no recording") {
		t.Errorf("stop with no file = %v", err)
	}

	s = stubADB(t, []stubRule{{match: "screenrecord --verbose", out: "Content area is 1x1"},
		{match: " pull ", out: "remote object does not exist", code: 1}})
	d = stubbedDriver(t, s, recordingAgent(t, true, "/sdcard/tb-recording-1.mp4"))
	if err := d.StartRecording(ctxT(t), filepath.Join(t.TempDir(), "x.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := d.StopRecording(ctxT(t)); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("failed pull = %v", err)
	}
	if d.recordCmd != nil {
		t.Error("still recording")
	}
}

func TestOpenFails(t *testing.T) {
	stubADB(t, []stubRule{{match: "forward tcp:0", out: "error: no devices", code: 1}, {match: " push ", out: "error: no devices", code: 1}})
	d, err := Open(ctxT(t), device.Info{Platform: device.Android, Serial: testSerial})
	if err == nil || d != nil {
		t.Errorf("Open = %v, %v", d, err)
	}
}

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

func TestStartAgentStartFails(t *testing.T) {
	s := stubADB(t, []stubRule{{match: "app_process", out: "error: closed", code: 1}})
	if err := s.driver().startAgent(ctxT(t)); err == nil || !strings.Contains(err.Error(), "error: closed") {
		t.Errorf("err = %v", err)
	}
}

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

func TestRequestTokens(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	a1.on("tap", func(map[string]any) map[string]any { a1.dropConns(); return noReply })
	a2.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 9} })
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	d.ridBase = "run"
	c1, _, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.connP.Store(c1)
	defer d.Close()
	if _, err := d.Tap(ctxT(t), 3, 4, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	first, again := a1.last("tap"), a2.last("tap")
	if first == nil || again == nil || first["rid"] == nil || first["rid"] != again["rid"] {
		t.Errorf("tokens: first %v, again %v", first["rid"], again["rid"])
	}
	if _, err := d.Tap(ctxT(t), 5, 6, driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if next := a2.last("tap"); next["rid"] == first["rid"] {
		t.Errorf("a new tap reused the token %v", next["rid"])
	}
	d.Sh(ctxT(t), "echo hi")
	if r := a2.last("sh"); r == nil || r["rid"] == nil {
		t.Errorf("sh without a token: %v", r)
	}
	d.call(ctxT(t), "ping", nil, time.Second)
	if r := a2.last("ping"); r == nil || r["rid"] != nil {
		t.Errorf("a read carries a token: %v", r)
	}
}

func TestReconnectWaitsForTheDevice(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	a2.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 3} })
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: device offline", code: 1},
		{match: "forward --list", out: "", code: 1},
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
	go func() {
		time.Sleep(3500 * time.Millisecond)
		s.rules([]stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if r, err := d.Tap(ctx, 1, 1, driver.ActionOpts{}); err != nil || r.Seq == 0 {
		t.Fatalf("Tap after the device came back = %+v, %v", r, err)
	}
}

func TestReleaseStopsTheAgent(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a := newFakeAgent(t)
	s := stubADB(t, nil)
	d := s.driver()
	c, h, err := dialHello(a.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c, a.port(), h)
	if err := d.Release(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if len(a.requests("exit")) != 1 {
		t.Errorf("exit sent %d times", len(a.requests("exit")))
	}
	found := false
	for _, call := range s.calls() {
		cmd := strings.Join(call, " ")
		if strings.Contains(cmd, "pidof tb-agent") && strings.Contains(cmd, "dev.testbuddy.agent/") {
			found = true
		}
	}
	if !found {
		t.Errorf("no stop command in %v", s.calls())
	}
	select {
	case <-time.After(300 * time.Millisecond):
	case e := <-func() <-chan driver.Event { ch, _ := d.Events(); return ch }():
		if e.Kind == "disconnected" {
			t.Error("a released driver reported a disconnect (it reconnected?)")
		}
	}
}

func TestReconnectDoesNotStartOverAfterGivingUp(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	defer func(v time.Duration) { reconnectFor = v }(reconnectFor)
	reconnectFor = 2 * time.Second
	a1 := newFakeAgent(t)
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: device 'emulator-5644' not found", code: 1},
		{match: " push ", out: "error: device 'emulator-5644' not found", code: 1},
		{match: "wait-for-device", out: "", code: 1},
	})
	d := s.driver()
	c1, h, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	events, cancel := d.Events()
	defer cancel()
	t0 := time.Now()
	a1.dropConns()
	<-c1.Done()
	if _, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{}); err == nil {
		t.Fatal("Tap succeeded on a device that is gone")
	}
	if el := time.Since(t0); el > reconnectWindow()+time.Second {
		t.Errorf("issue #101: the request made as the connection dropped failed after %s, want about the reconnect window (%s): it reconnected again after the attempt it waited for gave up",
			el.Round(100*time.Millisecond), reconnectWindow())
	}
	waitEvent(t, events, "disconnected")
	t1 := time.Now()
	if _, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{}); err == nil {
		t.Fatal("Tap succeeded on a device that is gone")
	}
	if el := time.Since(t1); el > time.Second {
		t.Errorf("issue #101: a request after reconnecting gave up failed after %s, want at once (one attempt, not reconnectFor again)",
			el.Round(100*time.Millisecond))
	}
}

func TestReconnectAfterGivingUpReachesAnAgentThatIsBack(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	defer func(v time.Duration) { reconnectFor = v }(reconnectFor)
	reconnectFor = 500 * time.Millisecond
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	a2.on("tap", func(map[string]any) map[string]any { return map[string]any{"result": "changed", "seq": 3} })
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: device offline", code: 1},
		{match: " push ", out: "error: device offline", code: 1},
		{match: "forward --list", out: "", code: 1},
	})
	d := s.driver()
	c1, h, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	events, cancel := d.Events()
	defer cancel()
	a1.dropConns()
	waitEvent(t, events, "disconnected")
	s.rules([]stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	if r, err := d.Tap(ctxT(t), 1, 1, driver.ActionOpts{}); err != nil || r.Seq == 0 {
		t.Fatalf("issue #101: Tap after the agent came back = %+v, %v, want it to reach the agent in one attempt", r, err)
	}
	if err := d.Lost(); err != nil {
		t.Errorf("Lost = %v after a request reconnected after all", err)
	}
}

func TestReconnectWindowInCI(t *testing.T) {
	for _, tc := range []struct {
		ci       string
		min, max time.Duration
	}{
		{"1", 1900 * time.Millisecond, 8 * time.Second},
		{"0", 0, 1900 * time.Millisecond},
	} {
		t.Run("TB_CI="+tc.ci, func(t *testing.T) {
			t.Setenv("TB_CI", tc.ci)
			t.Setenv("TB_NO_A11Y_SERVICE", "1")
			defer func(v time.Duration) { reconnectFor = v }(reconnectFor)
			reconnectFor = time.Second
			a1 := newFakeAgent(t)
			s := stubADB(t, []stubRule{
				{match: "forward tcp:0", out: "error: device offline", code: 1},
				{match: " push ", out: "error: device offline", code: 1},
				{match: "wait-for-device", out: "", code: 1},
			})
			d := s.driver()
			c1, h, err := dialHello(a1.port(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			d.adopt(c1, a1.port(), h)
			defer d.Close()
			events, cancel := d.Events()
			defer cancel()
			t0 := time.Now()
			a1.dropConns()
			timeout := time.After(10 * time.Second)
			for gaveUp := false; !gaveUp; {
				select {
				case e := <-events:
					gaveUp = e.Kind == "disconnected"
				case <-timeout:
					t.Fatal("no disconnected event")
				}
			}
			if el := time.Since(t0); el < tc.min || el > tc.max {
				t.Errorf("issue #101: with TB_CI=%s reconnecting gave up after %s, want %s to %s",
					tc.ci, el.Round(100*time.Millisecond), tc.min, tc.max)
			}
		})
	}
}

func TestReconnectIsNoted(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	s := stubADB(t, []stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	d := s.driver()
	c1, h, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	d.Notes()
	a1.dropConns()
	eventually(t, "the reconnect", func() bool { return d.conn() != c1 })
	notes := strings.Join(d.Notes(), "\n")
	if !strings.Contains(notes, "the connection to the tb agent dropped") || !strings.Contains(notes, "reconnected") {
		t.Errorf("issue #101: notes after a reconnect = %q, want one saying the connection dropped and tb reconnected", notes)
	}
}

func TestCloseEndsAReconnectUnderWay(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "1")
	a1 := newFakeAgent(t)
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: device offline", code: 1},
		{match: " push ", out: "error: device offline", code: 1},
	})
	d := s.driver()
	c1, h, err := dialHello(a1.port(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d.adopt(c1, a1.port(), h)
	defer d.Close()
	a1.dropConns()
	eventually(t, "the reconnect's first attempt", func() bool { return containsAny(s.joined(), "forward --list") })
	d.Close()
	eventually(t, "the reconnect to end after Close", func() bool {
		if d.connMu.TryLock() {
			d.connMu.Unlock()
			return true
		}
		return false
	})
	n := len(s.calls())
	time.Sleep(700 * time.Millisecond)
	if calls := s.joined(); len(calls) != n {
		t.Errorf("issue #335: a closed driver still ran adb: %q", calls[n:])
	}
}
