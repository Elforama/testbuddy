package android

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func stuckAgent(t *testing.T, s *adbStub) *fakeAgent {
	a := newFakeAgent(t)
	a.on("info", func(map[string]any) map[string]any {
		if b, _ := os.ReadFile(s.log); !strings.Contains(string(b), "reboot") {
			return map[string]any{"__noreply": true}
		}
		return map[string]any{"w": 1080, "h": 2400, "sdk": 36}
	})
	return a
}

func ctxLong(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	return ctx
}

var bootRule = stubRule{match: "boot_id", sh: `l=; read -r l < "$TB_STUB_DIR/adb.log"; case "$l" in *reboot*) printf 'new-boot 1'; exit 0;; esac;`, out: "old-boot 1"}

func shortWaits(t *testing.T) {
	oldWait, oldRetry := infoWait, infoRetryWait
	infoWait, infoRetryWait = 200*time.Millisecond, 800*time.Millisecond
	t.Cleanup(func() { infoWait, infoRetryWait = oldWait, oldRetry })
}

func forgetRestarts(t *testing.T) {
	shortWaits(t)
	restartMu.Lock()
	restarted = map[string]bool{}
	restartMu.Unlock()
	t.Cleanup(func() {
		restartMu.Lock()
		restarted = map[string]bool{}
		restartMu.Unlock()
	})
}

func TestStuckEmulatorIsRestarted(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "1")
	forgetRestarts(t)
	s := stubADB(t, nil)
	a := stuckAgent(t, s)
	s.rules([]stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "get-state", out: "device\n"},
		{match: "pidof tb-agent", out: "4321\n"},
		bootRule,
	})
	d := s.driver()
	if err := d.open(ctxLong(t)); err != nil {
		t.Fatalf("issue #217: the stuck emulator was not recovered: %v\nadb calls:\n%s", err, strings.Join(s.joined(), "\n"))
	}
	defer d.Close()
	joined := s.joined()
	if !containsAny(joined, "-s "+testSerial+" reboot") || !containsAny(joined, "-s "+testSerial+" wait-for-device") {
		t.Errorf("no reboot and wait in:\n%s", strings.Join(joined, "\n"))
	}
	if d.sdk != 36 {
		t.Errorf("adopted sdk %d, want the restarted agent's 36", d.sdk)
	}
}

func TestStuckPhoneSaysWhatIsWrong(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "1")
	forgetRestarts(t)
	s := stubADB(t, nil)
	a := stuckAgent(t, s)
	s.rules([]stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "get-state", out: "device\n"},
		{match: "pidof tb-agent", out: "4321\n"},
		{match: "tail -n 20 /data/local/tmp/tb-agent.log", out: "ready\n"},
	})
	d := s.driver()
	d.info.Emulator = false
	err := d.open(ctxLong(t))
	if err == nil {
		d.Close()
		t.Fatal("connected to an agent that never answers")
	}
	if containsAny(s.joined(), " reboot") {
		t.Error("restarted a phone")
	}
	for _, want := range []string{
		`the Test Buddy agent did not start on ` + testSerial + `: agent did not answer "info" within 800ms`,
		`adb sees the device ("device")`,
		"the agent is running (pid 4321) and said hello",
		"restart the device",
		"\nready",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("issue #217: the error lacks %q:\n%s", want, err)
		}
	}
}

func TestStuckEmulatorRestartedOnce(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "1")
	forgetRestarts(t)
	a := newFakeAgent(t)
	a.on("info", func(map[string]any) map[string]any { return map[string]any{"__noreply": true} })
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "get-state", out: "device\n"},
		{match: "pidof tb-agent", out: "4321\n"},
		bootRule,
	})
	d := s.driver()
	err := d.open(ctxLong(t))
	if err == nil || !strings.Contains(err.Error(), "tb restarted the emulator and it is still not answering") {
		t.Fatalf("err = %v", err)
	}
	if err := s.driver().open(ctxLong(t)); err == nil {
		t.Fatal("connected to an agent that never answers")
	}
	n := 0
	for _, c := range s.joined() {
		if strings.HasSuffix(c, " reboot") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("restarted %d times, want once", n)
	}
}

func TestOfflineDeviceNamed(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "1")
	forgetRestarts(t)
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: device offline", code: 1},
		{match: "get-state", out: "error: device offline", code: 1},
	})
	d := s.driver()
	err := d.open(ctxLong(t))
	if err == nil || !strings.Contains(err.Error(), "adb does not see the device as ready") || !strings.Contains(err.Error(), "device offline") {
		t.Errorf("err = %v", err)
	}
	if containsAny(s.joined(), " reboot") {
		t.Error("restarted a device adb cannot reach")
	}
}

func TestStuckEmulatorNotRestartedOutsideCI(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "0")
	forgetRestarts(t)
	s := stubADB(t, nil)
	a := stuckAgent(t, s)
	s.rules([]stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "get-state", out: "device\n"},
		{match: "pidof tb-agent", out: "4321\n"},
		bootRule,
	})
	err := s.driver().open(ctxLong(t))
	if err == nil || !strings.Contains(err.Error(), "restart the emulator (tb restarts it by itself in CI mode)") {
		t.Errorf("err = %v", err)
	}
	if err != nil && (!strings.Contains(err.Error(), fmt.Sprintf(`did not answer "info" within %s (slow or stuck)`, infoWait)) || strings.Contains(err.Error(), "Android itself")) {
		t.Errorf("outside CI mode, after one ask: err = %v", err)
	}
	if containsAny(s.joined(), " reboot") {
		t.Error("restarted an emulator outside CI mode")
	}
}

func TestSlowAgentIsNotStuck(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "1")
	forgetRestarts(t)
	a := newFakeAgent(t)
	slow := infoWait * 4 / 3
	a.on("info", func(map[string]any) map[string]any {
		time.Sleep(slow)
		return map[string]any{"w": 1080, "h": 2400, "sdk": 35}
	})
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "get-state", out: "device\n"},
		{match: "pidof tb-agent", out: "4321\n"},
		bootRule,
	})
	d := s.driver()
	if err := d.open(ctxLong(t)); err != nil {
		t.Fatalf("a slow agent failed the start: %v", err)
	}
	defer d.Close()
	if containsAny(s.joined(), " reboot") {
		t.Error("restarted an emulator whose agent was only slow")
	}
	if d.sdk != 35 {
		t.Errorf("sdk %d", d.sdk)
	}
}

func TestSlowAgentOutsideCIIsNotAskedAgain(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "0")
	forgetRestarts(t)
	a := newFakeAgent(t)
	slow := infoWait * 4 / 3
	a.on("info", func(map[string]any) map[string]any {
		time.Sleep(slow)
		return map[string]any{"w": 1080, "h": 2400, "sdk": 35}
	})
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: fmt.Sprint(a.port())},
		{match: "get-state", out: "device\n"},
		{match: "pidof tb-agent", out: "4321\n"},
		bootRule,
	})
	d := s.driver()
	err := d.open(ctxLong(t))
	if err == nil {
		d.Close()
		t.Error("outside CI mode, an agent that let infoWait pass was asked again and the start passed")
	}
	if n := a.arrived("info"); n != 2 {
		t.Errorf(`"info" asked %d times outside CI mode, want 2 (the fast path's and the start's, not again)`, n)
	}
}

func TestForwardFailureIsNotStuck(t *testing.T) {
	t.Setenv("TB_NO_A11Y_SERVICE", "1")
	t.Setenv("TB_CI", "1")
	forgetRestarts(t)
	s := stubADB(t, []stubRule{
		{match: "forward tcp:0", out: "error: cannot bind listener: Address already in use", code: 1},
		{match: "get-state", out: "device\n"},
		{match: "pidof tb-agent", out: "4321\n"},
		bootRule,
	})
	err := s.driver().open(ctxLong(t))
	if err == nil || !strings.Contains(err.Error(), "cannot bind") || !strings.Contains(err.Error(), "the agent is running (pid 4321)") {
		t.Errorf("err = %v", err)
	}
	if containsAny(s.joined(), " reboot") {
		t.Error("restarted the emulator for a forward that failed")
	}
}

func TestRestartNeedsBootID(t *testing.T) {
	forgetRestarts(t)
	s := stubADB(t, []stubRule{{match: "boot_id", out: "", code: 1}})
	err := s.driver().restartDevice(ctxLong(t))
	if err == nil || !strings.Contains(err.Error(), "could not read its boot id") {
		t.Errorf("err = %v", err)
	}
	if containsAny(s.joined(), " reboot") {
		t.Error("rebooted without the old boot's id")
	}
}
