package android

import (
	"fmt"
	"strings"
	"testing"
)

func TestDisableAnimationsAgainRestoresTheOriginals(t *testing.T) {
	forgetAnimations(t)
	a := shAgent(t, map[string]shAnswer{"settings get global": {"1\n0.5\n1\n", 0}})
	a.on("animations", func(map[string]any) map[string]any { return nil })
	if _, err := newDriver(t, a).DisableAnimations(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	b := shAgent(t, map[string]shAnswer{"settings get global": {"0\n0\n0\n", 0}})
	b.on("animations", func(map[string]any) map[string]any { return nil })
	restore, err := newDriver(t, b).DisableAnimations(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	b.reset()
	restore()
	want := "settings put global window_animation_scale 1; settings put global transition_animation_scale 0.5; settings put global animator_duration_scale 1"
	if got := b.shCmds(); len(got) != 1 || got[0] != want {
		t.Errorf("issue #101: the second driver's restore ran %v\nwant %q (the scales before the run)", got, want)
	}
	if on := b.last("animations")["on"]; on != true {
		t.Errorf("issue #101: the live agent was told animations on = %v, want true", on)
	}
	c := shAgent(t, map[string]shAnswer{"settings get global": {"0.25\n0.25\n0.25\n", 0}})
	c.on("animations", func(map[string]any) map[string]any { return nil })
	restore, err = newDriver(t, c).DisableAnimations(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	c.reset()
	restore()
	if got := c.shCmds(); len(got) != 1 || !strings.Contains(got[0], "window_animation_scale 0.25") {
		t.Errorf("after a restore, the next restore ran %v, want the 0.25 it read", got)
	}
}

func forgetAnimations(t *testing.T) {
	animMu.Lock()
	originalScales = map[string][]string{}
	animMu.Unlock()
	t.Cleanup(func() {
		animMu.Lock()
		originalScales = map[string][]string{}
		animMu.Unlock()
	})
}

func TestStuckEmulatorNotRestartedWithoutRestart(t *testing.T) {
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
	err := s.driver().open(WithoutRestart(ctxLong(t)))
	if containsAny(s.joined(), " reboot") {
		t.Error("issue #101: restarted the emulator to read a failure's screen")
	}
	if err == nil {
		t.Error("connected to an agent that never answers")
	}
	if wasRestarted(testSerial) {
		t.Error("issue #101: the run's restart was used up")
	}
}
