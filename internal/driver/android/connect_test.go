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
		"remove-test-provider $p; done; rm -f /data/local/tmp/tb-location-providers", // issue #72
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
	deadline := time.Now().Add(10 * time.Second)
	for d.conn() == c1 {
		if time.Now().After(deadline) {
			t.Fatal("never reconnected after the drop")
		}
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
	// 3: the reconnect moves the count past everything seen (base 1), then the agent's 2.
	if err != nil || r.Settle != driver.NoChange || r.Seq != 3 {
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
		// 10: the reconnect moves the count past everything seen (base 1), then the agent's 9.
		if err != nil || r.Seq != 10 {
			t.Fatalf("run %d: Tap = %+v, %v", i, r, err)
		}
	}
}

// The new connection drops while adopt enables the accessibility service (issue #67): adopt
// used to send that command through d.call, which on a drop called reconnect again on the same
// goroutine and blocked on connMu forever, past the tap's own deadline. The drop must fail only
// that attempt; the next one reconnects and the tap goes through.
func TestReconnectSurvivesDropDuringAdopt(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "") // the accessibility-service step on, as by default
	a1, a2 := newFakeAgent(t), newFakeAgent(t)
	var shs atomic.Int32
	a2.on("sh", func(map[string]any) map[string]any {
		if shs.Add(1) == 1 {
			a2.dropConns() // the link flaps again, during adopt's command
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
	d.connP.Store(c1) // no pump: only the tap notices the drop
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

// Once reconnecting gives up ("disconnected"), Lost says so, so a run that keeps drivers
// between flows opens the device again instead of reusing this one (issue #54).
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

// recordingAgent answers the recording's shell commands: whether screenrecord can record
// without its cap, and which part files the device holds.
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

// Recording runs screenrecord with no time limit where it allows one, returns once it
// encodes, stops it with SIGINT on the device (after the stop file), pulls the file and
// removes it.
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
	wantSh := []string{"rm -f /sdcard/tb-recording-*.mp4 /sdcard/tb-recording.stop", "screenrecord --help 2>&1",
		"touch /sdcard/tb-recording.stop; pkill -INT screenrecord", "ls /sdcard/tb-recording-*.mp4 2>/dev/null",
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

// Where screenrecord stops at 3 minutes, parts follow one another until the stop file, and
// each is pulled, in order, next to the first.
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

// A screenrecord that ends before it encodes fails the start with what it said; one that
// saved nothing fails the stop; a failed pull is reported. The recording is over each time.
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

// A device-changing request carries a token, the same one when it is sent again after a drop
// (the agent answers a repeat from the first run: a tap is not tapped twice). Reads carry none.
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

// A device that is gone for a few seconds (an emulator offline under load) is waited for:
// reconnecting keeps trying, past the old three quick attempts.
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
		time.Sleep(3500 * time.Millisecond) // longer than the old 3 attempts took
		s.rules([]stubRule{{match: "forward --list", out: forwardLine(a2.port())}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if r, err := d.Tap(ctx, 1, 1, driver.ActionOpts{}); err != nil || r.Seq == 0 {
		t.Fatalf("Tap after the device came back = %+v, %v", r, err)
	}
}

// Release asks the agent to exit, then makes sure: no tb-agent left, and its accessibility
// service taken out of the enabled list.
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
