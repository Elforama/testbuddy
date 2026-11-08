package engine

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
)

// launchApp: stop first, grant all, no arguments, unless told otherwise.
func TestLaunchAppDefaults(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- launchApp"))
	if len(f.launches) != 1 {
		t.Fatalf("launches %+v", f.launches)
	}
	l := f.launches[0]
	want := driver.LaunchOpts{StopApp: true, Permissions: map[string]string{"all": "allow"}, Arguments: map[string]any{}}
	if l.app != "app" || !reflect.DeepEqual(l.o, want) {
		t.Errorf("launch %q %+v, want app %+v", l.app, l.o, want)
	}
	if len(f.called("ClearKeychain")) != 0 {
		t.Errorf("cleared the keychain unasked")
	}
}

// launchApp's options are evaluated and passed on; clearKeychain runs before the launch.
func TestLaunchAppOptions(t *testing.T) {
	f := &fakeDriver{screen: static()}
	src := `appId: app
env:
  APP: com.other
  V: hello
---
- launchApp:
    appId: ${APP}
    clearState: true
    clearKeychain: true
    stopApp: false
    permissions:
      camera: "${'de' + 'ny'}"
      location: allow
    arguments:
      count: 3
      ratio: 1.5
      on: true
      text: ${V}
      "${'k' + 'ey'}": v
`
	mustPass(t, runSrc(t, f, quick, t.TempDir(), src))
	f.mu.Lock()
	calls := append([]string(nil), f.calls...)
	f.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"ClearKeychain", "LaunchApp com.other"}) {
		t.Errorf("calls %q", calls)
	}
	o := f.launches[0].o
	if !o.ClearState || o.StopApp {
		t.Errorf("options %+v", o)
	}
	if want := map[string]string{"camera": "deny", "location": "allow"}; !reflect.DeepEqual(o.Permissions, want) {
		t.Errorf("permissions %v", o.Permissions)
	}
	want := map[string]any{"count": int64(3), "ratio": 1.5, "on": true, "text": "hello", "key": "v"}
	if !reflect.DeepEqual(o.Arguments, want) {
		t.Errorf("arguments %#v, want %#v", o.Arguments, want)
	}
}

// Two launch arguments that evaluate to one name are an error.
func TestLaunchAppDuplicateArgument(t *testing.T) {
	f := &fakeDriver{screen: static()}
	res := runWith(t, f, quick, "- launchApp:\n    arguments:\n      \"${'a'}\": 1\n      a: 2")
	mustFail(t, res, `duplicate launch argument "a"`)
	if len(f.launches) != 0 {
		t.Errorf("launched anyway")
	}
}

// A launch the device fails is a flow failure (an optional launch warns).
func TestLaunchAppErrors(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("LaunchApp", errors.New("not installed"))
	mustFail(t, runWith(t, f, quick, "- launchApp"), "Unable to launch app app: not installed")
	res := runWith(t, f, quick, "- launchApp: {optional: true}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("optional launch: %s", res.Steps[0].Status)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("ClearKeychain", errors.New("locked"))
	mustFail(t, runWith(t, f2, quick, "- launchApp: {clearKeychain: true}"), "locked")
	if len(f2.launches) != 0 {
		t.Errorf("launched after the keychain failed")
	}
}

// Bad ${...} in launchApp's fields fail it before anything runs.
func TestLaunchAppJSErrors(t *testing.T) {
	for _, cmd := range []string{
		"- launchApp: {appId: '${(}'}",
		"- launchApp: {permissions: {camera: '${(}'}}",
		"- launchApp: {arguments: {'${(}': 1}}",
		"- launchApp: {arguments: {a: '${(}'}}",
	} {
		f := &fakeDriver{screen: static()}
		mustFail(t, runWith(t, f, quick, cmd), "JavaScript error")
		if len(f.launches) != 0 {
			t.Errorf("%s: launched", cmd)
		}
	}
}

// launchApp's permissions decide how the iOS notifications alert is answered.
func TestLaunchAppNotificationsAnswer(t *testing.T) {
	for _, tc := range []struct{ perms, want string }{
		{"", "allow"},
		{"{all: deny}", "deny"},
		{"{all: deny, notifications: Allow}", "allow"},
		{"{camera: allow}", ""},
		{"{notifications: unset}", "unset"},
	} {
		f := &fakeDriver{screen: static(), plat: "ios"}
		cmd := "- launchApp"
		if tc.perms != "" {
			cmd += ": {permissions: " + tc.perms + "}"
		}
		fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n"+cmd))
		if err != nil {
			t.Fatal(err)
		}
		r := New(f, quick)
		mustPass(t, r.RunFlow(context.Background(), fl))
		if r.notifications != tc.want {
			t.Errorf("permissions %s: answer %q, want %q", tc.perms, r.notifications, tc.want)
		}
	}
}

// A flow with an empty appId cannot launch, stop or clear an app.
func TestNoAppID(t *testing.T) {
	for _, cmd := range []string{"launchApp", "stopApp", "killApp", "clearState", "setPermissions: {permissions: {all: allow}}"} {
		f := &fakeDriver{screen: static()}
		res := runSrc(t, f, quick, t.TempDir(), "appId: \"${''}\"\n---\n- "+cmd)
		mustFail(t, res, "no appId")
	}
}

// stopApp, killApp and clearState act on the flow's app or the one named; clearState also
// resets the app's permissions.
func TestAppLifecycle(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- stopApp\n- stopApp: other\n- killApp\n- killApp: {appId: k}\n- clearState\n- clearState: c"))
	f.mu.Lock()
	calls := append([]string(nil), f.calls...)
	f.mu.Unlock()
	want := []string{"StopApp app", "StopApp other", "KillApp app", "KillApp k", "ClearState app", "SetPermissions app", "ClearState c", "SetPermissions c"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %q, want %q", calls, want)
	}
	for _, p := range f.perms {
		if !reflect.DeepEqual(p.perms, map[string]string{"all": "unset"}) {
			t.Errorf("clearState permissions %v", p.perms)
		}
	}
}

// Errors from stopApp, killApp and clearState fail the command; a failed clear sets nothing.
func TestAppLifecycleErrors(t *testing.T) {
	for _, m := range []string{"StopApp", "KillApp", "ClearState"} {
		f := &fakeDriver{screen: static()}
		f.fail(m, errors.New(m+" failed"))
		cmd := map[string]string{"StopApp": "stopApp", "KillApp": "killApp", "ClearState": "clearState"}[m]
		mustFail(t, runWith(t, f, quick, "- "+cmd), m+" failed")
		if len(f.perms) != 0 {
			t.Errorf("%s: set permissions after the failure", m)
		}
	}
	f := &fakeDriver{screen: static()}
	f.fail("SetPermissions", errors.New("denied"))
	mustFail(t, runWith(t, f, quick, "- clearState"), "denied")
}

// clearKeychain and setPermissions go straight to the device.
func TestKeychainAndPermissions(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- clearKeychain\n- setPermissions: {permissions: {camera: \"${'allow'}\", contacts: deny}}\n- setPermissions: {appId: other, permissions: {all: unset}}"))
	if len(f.called("ClearKeychain")) != 1 {
		t.Errorf("calls %q", f.calls)
	}
	want := []permCall{{"app", map[string]string{"camera": "allow", "contacts": "deny"}}, {"other", map[string]string{"all": "unset"}}}
	if !reflect.DeepEqual(f.perms, want) {
		t.Errorf("permissions %+v, want %+v", f.perms, want)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("ClearKeychain", errors.New("nope"))
	mustFail(t, runWith(t, f2, quick, "- clearKeychain"), "nope")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setPermissions: {permissions: {camera: '${(}'}}"), "JavaScript error")
}

// openLink opens the evaluated link for the flow's app, then waits up to 3 s to settle.
func TestOpenLink(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- openLink: https://a.test/x\n- openLink: {link: \"${'app://' + 'y'}\", autoVerify: true, browser: true}"))
	want := []linkCall{{"https://a.test/x", "app", false, false}, {"app://y", "app", true, true}}
	if !reflect.DeepEqual(f.links, want) {
		t.Errorf("links %+v, want %+v", f.links, want)
	}
	if !reflect.DeepEqual(f.waitDur, []time.Duration{3 * time.Second, 3 * time.Second}) {
		t.Errorf("settle waits %v", f.waitDur)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("OpenLink", errors.New("no handler"))
	mustFail(t, runWith(t, f2, quick, "- openLink: x://y"), "no handler")
	if len(f2.waitDur) != 0 {
		t.Errorf("waited after a failed open")
	}
}

// setOrientation accepts any spelling of the four orientations, evaluated, then settles.
func TestSetOrientation(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"PORTRAIT", "PORTRAIT"},
		{"landscapeLeft", "LANDSCAPE_LEFT"},
		{"LANDSCAPE_RIGHT", "LANDSCAPE_RIGHT"},
		{"upside_down", "UPSIDE_DOWN"},
		{"\"${'portrait'}\"", "PORTRAIT"},
		{"{orientation: landscapeRight}", "LANDSCAPE_RIGHT"},
	} {
		f := &fakeDriver{screen: static()}
		mustPass(t, runWith(t, f, quick, "- setOrientation: "+tc.in))
		if !reflect.DeepEqual(f.orients, []string{tc.want}) {
			t.Errorf("%s: orientations %q, want %s", tc.in, f.orients, tc.want)
		}
		if !reflect.DeepEqual(f.waitDur, []time.Duration{3 * time.Second}) {
			t.Errorf("%s: settle waits %v", tc.in, f.waitDur)
		}
	}
}

// An orientation that evaluates to nothing known fails, even an optional one.
func TestSetOrientationInvalid(t *testing.T) {
	f := &fakeDriver{screen: static()}
	res := runWith(t, f, quick, "- setOrientation: {orientation: \"${'sideways'}\", optional: true}")
	mustFail(t, res, `invalid orientation "sideways"`)
	if len(f.orients) != 0 {
		t.Errorf("set %q", f.orients)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("SetOrientation", errors.New("locked"))
	mustFail(t, runWith(t, f2, quick, "- setOrientation: PORTRAIT"), "locked")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setOrientation: '${(}'"), "JavaScript error")
}

// addMedia sends the flow's media files (resolved next to the flow).
func TestAddMedia(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.png", "png")
	b := writeFile(t, dir, "media/b.mp4", "mp4")
	f := &fakeDriver{screen: static()}
	mustPass(t, runSrc(t, f, quick, dir, "appId: app\n---\n- addMedia: [a.png, media/b.mp4]\n- addMedia: {files: [a.png]}"))
	if want := [][]string{{a, b}, {a}}; !reflect.DeepEqual(f.media, want) {
		t.Errorf("media %q, want %q", f.media, want)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("AddMedia", errors.New("full"))
	mustFail(t, runSrc(t, f2, quick, dir, "appId: app\n---\n- addMedia: [a.png]"), "full")
}

// setAirplaneMode and toggleAirplaneMode go to the device.
func TestAirplaneMode(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- setAirplaneMode: enabled\n- setAirplaneMode: {value: disabled}\n- toggleAirplaneMode"))
	f.mu.Lock()
	calls := append([]string(nil), f.calls...)
	f.mu.Unlock()
	if want := []string{"SetAirplaneMode true", "SetAirplaneMode false", "ToggleAirplaneMode"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls %q", calls)
	}
	for _, m := range []string{"SetAirplaneMode", "ToggleAirplaneMode"} {
		f := &fakeDriver{screen: static()}
		f.fail(m, errors.New("radio"))
		cmd := map[string]string{"SetAirplaneMode": "setAirplaneMode: enabled", "ToggleAirplaneMode": "toggleAirplaneMode"}[m]
		mustFail(t, runWith(t, f, quick, "- "+cmd), "radio")
	}
}

// Dark mode commands work on drivers that can read and set it.
func TestDarkMode(t *testing.T) {
	d := &darkFake{fakeDriver: &fakeDriver{screen: static()}}
	mustPass(t, runWith(t, d, quick, "- assertLightMode\n- setDarkMode: enabled\n- assertDarkMode\n- toggleDarkMode\n- assertLightMode\n- toggleDarkMode\n- assertDarkMode\n- setDarkMode: disabled\n- assertLightMode"))
	d.dark = true
	mustFail(t, runWith(t, d, quick, "- assertLightMode"), "Assertion is false: dark mode is on")
	d.dark = false
	mustFail(t, runWith(t, d, quick, "- assertDarkMode"), "Assertion is false: dark mode is off")
	d.err = errors.New("no ui mode")
	for _, cmd := range []string{"setDarkMode: enabled", "toggleDarkMode", "assertDarkMode", "assertLightMode"} {
		mustFail(t, runWith(t, d, quick, "- "+cmd), "no ui mode")
	}
}

// On a driver without dark mode support the commands fail as unsupported (optional: warn).
func TestDarkModeUnsupported(t *testing.T) {
	for _, cmd := range []string{"setDarkMode: enabled", "toggleDarkMode", "assertDarkMode", "assertLightMode"} {
		mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- "+cmd), "is not supported on android")
	}
	res := runWith(t, &fakeDriver{screen: static()}, quick, "- setDarkMode: {value: enabled, optional: true}")
	mustPass(t, res)
	if res.Steps[0].Status != Warned {
		t.Errorf("status %s", res.Steps[0].Status)
	}
}

// The AI commands need a cloud service tb does not have: they are optional by default (a
// warning), and fail when made required.
func TestAICommandsUnsupported(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want Status
	}{
		{"assertWithAI: the screen looks right", Warned},
		{"assertNoDefectsWithAI", Warned},
		{"extractTextWithAI: {query: the price, outputVariable: price}", Warned},
		{"assertWithAI: {assertion: x, optional: false}", Failed},
		{"extractTextWithAI: {query: q, optional: false}", Failed},
	} {
		res := runWith(t, &fakeDriver{screen: static()}, quick, "- "+tc.cmd)
		if len(res.Steps) != 1 || res.Steps[0].Status != tc.want || !strings.Contains(res.Steps[0].Err, "is not supported by Test Buddy") {
			t.Errorf("%s: steps %+v, want %s", tc.cmd, res.Steps, tc.want)
		}
	}
}

// waitForAnimationToEnd waits for the UI to settle (15 s at most unless told) and never fails.
func TestWaitForAnimationToEnd(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want time.Duration
	}{
		{"waitForAnimationToEnd", 15 * time.Second},
		{"waitForAnimationToEnd: {timeout: 500}", 500 * time.Millisecond},
		{"waitForAnimationToEnd: {timeout: \"${'1_000'}\"}", time.Second},
		{"waitForAnimationToEnd: {timeout: soon}", 15 * time.Second},
		{"waitForAnimationToEnd: {timeout: -5}", 15 * time.Second},
		{"waitForAnimationToEnd: {timeout: 0}", 15 * time.Second},
	} {
		f := &fakeDriver{screen: static()}
		mustPass(t, runWith(t, f, quick, "- "+tc.cmd))
		if !reflect.DeepEqual(f.waitDur, []time.Duration{tc.want}) {
			t.Errorf("%s: settle waits %v, want %s", tc.cmd, f.waitDur, tc.want)
		}
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- waitForAnimationToEnd: {timeout: '${(}'}"), "JavaScript error")
}

// startRecording records to the artifacts folder; stopRecording stops it; one left running
// is stopped when the flow ends.
func TestRecording(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	o := quick
	o.ArtifactsDir = dir
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, o, "- stopRecording\n- startRecording: run\n- stopRecording\n- stopRecording"))
	if want := []string{filepath.Join(dir, "startRecording", "run.mp4")}; !reflect.DeepEqual(f.recs, want) {
		t.Errorf("recordings %q, want %q", f.recs, want)
	}
	if n := len(f.called("StopRecording")); n != 1 {
		t.Errorf("%d stops, want 1 (only a running recording is stopped)", n)
	}
	f2 := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f2, o, "- startRecording: {path: left}"))
	if n := len(f2.called("StopRecording")); n != 1 {
		t.Errorf("a recording left running: %d stops at the end, want 1", n)
	}
	// Also when the flow fails after it started.
	f3 := &fakeDriver{screen: static()}
	mustFail(t, runWith(t, f3, o, "- startRecording: r\n- assertVisible: Nope"), "Nope")
	if n := len(f3.called("StopRecording")); n != 1 {
		t.Errorf("failed flow: %d stops, want 1", n)
	}
}

// A recording that fails to start is not stopped later.
func TestRecordingErrors(t *testing.T) {
	o := quick
	o.ArtifactsDir = t.TempDir()
	f := &fakeDriver{screen: static()}
	f.fail("StartRecording", errors.New("busy"))
	mustFail(t, runWith(t, f, o, "- startRecording: r"), "busy")
	if n := len(f.called("StopRecording")); n != 0 {
		t.Errorf("%d stops of a recording that never started", n)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("StopRecording", errors.New("lost"))
	mustFail(t, runWith(t, f2, o, "- startRecording: r\n- stopRecording"), "lost")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, o, "- startRecording: dir/"), "invalid path")
}

// Commands only multi-device flows run fail elsewhere; unknown ones are not implemented.
func TestCommandsOutsideTheirContext(t *testing.T) {
	r := New(&fakeDriver{screen: static()}, quick)
	for _, tc := range []struct {
		c    *flow.Command
		want string
	}{
		{&flow.Command{Name: "on", On: "alice"}, "on blocks run only in multi-device flows"},
		{&flow.Command{Name: "parallel"}, "parallel blocks run only in multi-device flows"},
		{&flow.Command{Name: "teleport"}, "command teleport is not implemented"},
		{&flow.Command{Name: "waitForSignal", Signal: "go"}, `waitForSignal "go": this flow does not run on several devices`},
	} {
		err := r.RunCommands(context.Background(), []*flow.Command{tc.c}, "app", nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want %q", tc.c.Name, err, tc.want)
		}
	}
	// signal without other devices is a no-op.
	if err := r.RunCommands(context.Background(), []*flow.Command{{Name: "signal", Signal: "go"}}, "app", nil); err != nil {
		t.Errorf("signal: %v", err)
	}
}
