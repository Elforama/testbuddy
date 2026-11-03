package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/ui"
)

// inputText types the evaluated text, waiting 300 ms for the field to react.
func TestInputText(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- inputText: hello\n- inputText: \"${1+1}-x\"\n- inputText: {text: map form}"))
	if want := []string{"hello", "2-x", "map form"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
	if f.typeOpts[0].NoChangeMs != 300 {
		t.Errorf("type options %+v", f.typeOpts[0])
	}
}

// Typing that the device fails fails the command.
func TestInputTextError(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("TypeText", errors.New("no focused field"))
	mustFail(t, runWith(t, f, quick, "- inputText: hello"), "no focused field")
}

// The inputRandom* commands type generated values of the asked kind and length.
func TestInputRandom(t *testing.T) {
	digits := func(s string) bool { return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 }
	letters := func(s string) bool { return strings.IndexFunc(s, func(r rune) bool { return r < 'a' || r > 'z' }) < 0 }
	for _, tc := range []struct {
		cmd string
		ok  func(string) bool
	}{
		{"inputRandomText", func(s string) bool { return len(s) == 8 && letters(s) }},
		{"inputRandomText: {length: 5}", func(s string) bool { return len(s) == 5 && letters(s) }},
		{"inputRandomText: {length: 0}", func(s string) bool { return len(s) == 8 }}, // non-positive: the default
		{"inputRandomNumber: {length: 4}", func(s string) bool { return len(s) == 4 && digits(s) && s[0] != '0' }},
		{"inputRandomNumber", func(s string) bool { return len(s) == 8 && digits(s) }},
		{"inputRandomEmail", func(s string) bool { return strings.Count(s, "@") == 1 && strings.Contains(s, ".") }},
		{"inputRandomPersonName", func(s string) bool { return strings.Count(s, " ") >= 1 }},
		{"inputRandomCityName", func(s string) bool { return s != "" }},
		{"inputRandomCountryName", func(s string) bool { return s != "" }},
		{"inputRandomColorName", func(s string) bool { return s != "" && letters(s) }},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			f := &fakeDriver{screen: static()}
			mustPass(t, runWith(t, f, quick, "- "+tc.cmd))
			if len(f.typed) != 1 || !tc.ok(f.typed[0]) {
				t.Errorf("typed %q", f.typed)
			}
		})
	}
}

// eraseText erases 50 characters unless told how many.
func TestEraseText(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- eraseText\n- eraseText: 5\n- eraseText: {charactersToErase: 3}"))
	if want := []int{50, 5, 3}; !reflect.DeepEqual(f.erased, want) {
		t.Errorf("erased %v, want %v", f.erased, want)
	}
	f2 := &fakeDriver{screen: static()}
	f2.fail("EraseText", errors.New("no field"))
	mustFail(t, runWith(t, f2, quick, "- eraseText: 2"), "no field")
}

// copyTextFrom copies an element's text (else hint, description, error); pasteText types it.
func TestCopyAndPaste(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    *ui.Node
		want string
	}{
		{"text", &ui.Node{Text: "Hello", Hint: "h", Desc: "d"}, "Hello"},
		{"hint", &ui.Node{Hint: "Email", Desc: "d"}, "Email"},
		{"desc", &ui.Node{Desc: "Avatar"}, "Avatar"},
		{"error", &ui.Node{Error: "Required"}, "Required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.n
			n.ID, n.Bounds, n.Enabled = "src", ui.Rect{R: 400, B: 50}, true
			f := &fakeDriver{screen: static(n)}
			mustPass(t, runWith(t, f, quick, "- copyTextFrom: {id: src}\n- pasteText\n- inputText: ${maestro.copiedText}/${tb.copiedText}"))
			if want := []string{tc.want, tc.want + "/" + tc.want}; !reflect.DeepEqual(f.typed, want) {
				t.Errorf("typed %q, want %q", f.typed, want)
			}
		})
	}
}

// An element with nothing to copy fails copyTextFrom.
func TestCopyTextFromEmpty(t *testing.T) {
	n := &ui.Node{ID: "src", Bounds: ui.Rect{R: 400, B: 50}}
	f := &fakeDriver{screen: static(n)}
	mustFail(t, runWith(t, f, quick, "- copyTextFrom: {id: src}"), "it has no text")
}

// copyTextFrom an element that is not there: an optional one warns.
func TestCopyTextFromMissing(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	res := runWith(t, f, quick, "- copyTextFrom: {id: nope, optional: true}\n- pasteText")
	mustPass(t, res)
	if got := stepStatuses(res); !reflect.DeepEqual(got, []Status{Warned, Passed}) {
		t.Errorf("statuses %v", got)
	}
	if len(f.typed) != 0 {
		t.Errorf("pasted %q with nothing copied", f.typed)
	}
}

// setClipboard sets what pasteText types; pasteText with nothing copied types nothing.
func TestSetClipboard(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- pasteText\n- setClipboard: \"${'a' + 'b'}\"\n- pasteText\n- setClipboard: {text: c}\n- pasteText"))
	if want := []string{"ab", "c"}; !reflect.DeepEqual(f.typed, want) {
		t.Errorf("typed %q, want %q", f.typed, want)
	}
}

// pressKey sends the key and returns once the UI starts reacting; if it changed, the next
// command waits for it to settle.
func TestPressKey(t *testing.T) {
	for _, tc := range []struct {
		res      driver.SettleResult
		deferred bool
	}{{driver.Changed, true}, {driver.NoChange, false}, {"", false}} {
		t.Run(string(tc.res), func(t *testing.T) {
			f := &fakeDriver{screen: static(), keySettle: tc.res}
			mustPass(t, runWith(t, f, quick, "- pressKey: Enter\n- pressKey: {key: Home}\n- eraseText: 1"))
			if want := []string{"Enter", "Home"}; !reflect.DeepEqual(f.keys, want) {
				t.Errorf("keys %q", f.keys)
			}
			if !f.keyOpts[0].React {
				t.Errorf("key options %+v: want React", f.keyOpts[0])
			}
			if got := len(f.waitDur) > 0; got != tc.deferred {
				t.Errorf("settle waits %v, want deferred=%v", f.waitDur, tc.deferred)
			}
		})
	}
}

// A key the device rejects fails on Android; on iOS keys it lacks do nothing.
func TestPressKeyError(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("PressKey", errors.New("unknown key"))
	mustFail(t, runWith(t, f, quick, "- pressKey: Volume Up"), "unknown key")
	f2 := &fakeDriver{screen: static(), plat: "ios"}
	f2.fail("PressKey", errors.New("unknown key"))
	mustPass(t, runWith(t, f2, quick, "- pressKey: Volume Up"))
}

// back presses Android's back key; on iOS it does nothing.
func TestBack(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- back\n- action: back"))
	if want := []string{"back", "back"}; !reflect.DeepEqual(f.keys, want) {
		t.Errorf("keys %q", f.keys)
	}
	f2 := &fakeDriver{screen: static(), plat: "ios"}
	f2.fail("PressKey", errors.New("must not be called"))
	mustPass(t, runWith(t, f2, quick, "- back"))
	if len(f2.keys) != 0 {
		t.Errorf("iOS back pressed %q", f2.keys)
	}
	f3 := &fakeDriver{screen: static()}
	f3.fail("PressKey", errors.New("agent gone"))
	mustFail(t, runWith(t, f3, quick, "- back"), "agent gone")
}

// hideKeyboard asks the device to hide it and checks it went; a keyboard that stays fails.
func TestHideKeyboard(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- hideKeyboard\n- hide keyboard"))
	if n := len(f.called("HideKeyboard")); n != 2 {
		t.Errorf("%d HideKeyboard calls", n)
	}
	f2 := &fakeDriver{screen: static(), kbShown: true}
	mustFail(t, runWith(t, f2, quick, "- hideKeyboard"), "Couldn't hide the keyboard")
	f3 := &fakeDriver{screen: static()}
	f3.fail("HideKeyboard", errors.New("no ime"))
	mustFail(t, runWith(t, f3, quick, "- hideKeyboard"), "no ime")
	f4 := &fakeDriver{screen: static()}
	f4.fail("Screen", errors.New("read failed"))
	mustFail(t, runWith(t, f4, quick, "- hideKeyboard"), "read failed")
}

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

// setLocation takes evaluated numbers; anything else is an error.
func TestSetLocation(t *testing.T) {
	f := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f, quick, "- setLocation: {latitude: '52.5', longitude: \"${13 + 0.25}\"}"))
	if want := [][2]float64{{52.5, 13.25}}; !reflect.DeepEqual(f.locs, want) {
		t.Errorf("locations %v", f.locs)
	}
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setLocation: {latitude: north, longitude: '1'}"), `invalid location "north", "1"`)
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setLocation: {latitude: '${(}', longitude: '1'}"), "JavaScript error")
	mustFail(t, runWith(t, &fakeDriver{screen: static()}, quick, "- setLocation: {latitude: '1', longitude: '${(}'}"), "JavaScript error")
	f2 := &fakeDriver{screen: static()}
	f2.fail("SetLocation", errors.New("no gps"))
	mustFail(t, runWith(t, f2, quick, "- setLocation: {latitude: '1', longitude: '2'}"), "no gps")
}

// travel sets the first point, then moves to each next one in 50 steps, timed by the speed.
func TestTravel(t *testing.T) {
	f := &fakeDriver{screen: static()}
	start := time.Now()
	mustPass(t, runWith(t, f, quick, "- travel: {points: ['0,0', '0,0.001', '0.001,0.001'], speed: 100000}"))
	if d := time.Since(start); d > time.Second {
		t.Errorf("travel of ~220 m at 100 km/s took %s", d)
	}
	if len(f.locs) != 101 {
		t.Fatalf("%d locations, want 1 + 2*50", len(f.locs))
	}
	if f.locs[0] != [2]float64{0, 0} || f.locs[50] != [2]float64{0, 0.001} || f.locs[100] != [2]float64{0.001, 0.001} {
		t.Errorf("waypoints %v %v %v", f.locs[0], f.locs[50], f.locs[100])
	}
	if mid := f.locs[25]; mid[0] != 0 || mid[1] < 0.00049 || mid[1] > 0.00051 {
		t.Errorf("halfway point %v", mid)
	}
	// No points: nothing to do.
	f0 := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f0, quick, "- travel: {points: []}"))
	if len(f0.locs) != 0 {
		t.Errorf("locations %v", f0.locs)
	}
	// One point: just set it.
	f2 := &fakeDriver{screen: static()}
	mustPass(t, runWith(t, f2, quick, "- travel: {points: ['1,2']}"))
	if !reflect.DeepEqual(f2.locs, [][2]float64{{1, 2}}) {
		t.Errorf("locations %v", f2.locs)
	}
}

// travel's simulated time is real: a slow walk takes about distance/speed.
func TestTravelTiming(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	start := time.Now()
	// ~11.1 m at 100 m/s: ~111 ms.
	mustPass(t, runWith(t, f, quick, "- travel: {points: ['0,0', '0,0.0001'], speed: 100}"))
	if d := time.Since(start); d < 90*time.Millisecond || d > time.Second {
		t.Errorf("took %s, want ~111 ms", d)
	}
}

// A location the device rejects stops the travel.
func TestTravelErrors(t *testing.T) {
	f := &fakeDriver{screen: static()}
	f.fail("SetLocation", errors.New("no gps"))
	mustFail(t, runWith(t, f, quick, "- travel: {points: ['0,0', '1,1']}"), "no gps")
	if len(f.locs) != 1 {
		t.Errorf("%d locations after the first failed", len(f.locs))
	}
	// Failing on the way, not at the start.
	f2 := &fakeDriver{screen: static()}
	n := 0
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- travel: {points: ['0,0', '0,0.0001'], speed: 100000}"))
	if err != nil {
		t.Fatal(err)
	}
	stepFail := &stepFailDriver{fakeDriver: f2, failAt: 3, n: &n}
	res := New(stepFail, quick).RunFlow(context.Background(), fl)
	mustFail(t, res, "step 3 failed")
}

// stepFailDriver fails the n-th SetLocation.
type stepFailDriver struct {
	*fakeDriver
	failAt int
	n      *int
}

func (d *stepFailDriver) SetLocation(ctx context.Context, la, lo float64) error {
	*d.n++
	if *d.n == d.failAt {
		return fmt.Errorf("step %d failed", *d.n)
	}
	return nil
}

// A travel is cut short when the run is cancelled.
func TestTravelCancelled(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static()}
	fl, err := flow.ParseBytes(t.TempDir()+"/f.yaml", []byte("appId: app\n---\n- travel: {points: ['0,0', '1,1'], speed: 1}"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	res := New(f, quick).RunFlow(ctx, fl)
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Errorf("err %v, want the context's", res.Err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("cancelled travel took %s", d)
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

// takeScreenshot writes the device's PNG under the artifacts folder (or at an absolute path).
func TestTakeScreenshot(t *testing.T) {
	dir := t.TempDir()
	f := &fakeDriver{screen: static(), shot: []byte("PNGDATA")}
	o := quick
	o.ArtifactsDir = dir
	abs := filepath.Join(t.TempDir(), "abs")
	mustPass(t, runWith(t, f, o, "- takeScreenshot: home\n- takeScreenshot: {path: \"sub/${'two'}\"}\n- takeScreenshot: "+abs))
	for _, p := range []string{filepath.Join(dir, "takeScreenshot", "home.png"), filepath.Join(dir, "takeScreenshot", "sub", "two.png"), abs + ".png"} {
		if b, err := os.ReadFile(p); err != nil || string(b) != "PNGDATA" {
			t.Errorf("%s: %q %v", p, b, err)
		}
	}
	if got := f.called("Screenshot"); len(got) != 3 || got[0] != "Screenshot 1 png" {
		t.Errorf("screenshot calls %q", got)
	}
}

// Screenshot paths that are empty, folders, or outside the artifacts folder are refused.
func TestTakeScreenshotBadPaths(t *testing.T) {
	o := quick
	o.ArtifactsDir = t.TempDir()
	for _, tc := range []struct{ path, want string }{
		{"\"${''}\"", "invalid path"},
		{"dir/", "invalid path"},
		{"name.", "invalid path"},
		{"../../escape", "escapes the artifacts folder"},
	} {
		f := &fakeDriver{screen: static(), shot: []byte("x")}
		mustFail(t, runWith(t, f, o, "- takeScreenshot: "+tc.path), tc.want)
	}
	f := &fakeDriver{screen: static()}
	f.fail("Screenshot", errors.New("black screen"))
	mustFail(t, runWith(t, f, o, "- takeScreenshot: x"), "black screen")
}

// A path that climbs out of the command's folder into a sibling folder with the same prefix
// is refused like any other escape.
func TestArtifactPathSiblingPrefix(t *testing.T) {
	o := quick
	o.ArtifactsDir = t.TempDir()
	f := &fakeDriver{screen: static(), shot: []byte("x")}
	mustFail(t, runWith(t, f, o, "- takeScreenshot: ../takeScreenshotEvil/a"), "escapes the artifacts folder")
}

// cropOn cuts the screenshot to the element, scaled from points to the image's pixels.
func TestTakeScreenshotCropOn(t *testing.T) {
	dir := t.TempDir()
	// The device image is 2x the screen's points (800x1600).
	img := pngOf(t, 800, 1600, colorWhite, imageRect(0, 200, 800, 300), colorRed)
	el := btn("Banner", "banner", 100) // points 0,100-400,150 = pixels 0,200-800,300
	f := &fakeDriver{screen: static(el), shot: img}
	o := quick
	o.ArtifactsDir = dir
	mustPass(t, runWith(t, f, o, "- takeScreenshot: {path: crop, cropOn: {id: banner}}"))
	got := decodePNG(t, filepath.Join(dir, "takeScreenshot", "crop.png"))
	if b := got.Bounds(); b.Dx() != 800 || b.Dy() != 100 {
		t.Fatalf("cropped to %v, want 800x100", b)
	}
	if r, g, b, _ := got.At(got.Bounds().Min.X+10, got.Bounds().Min.Y+10).RGBA(); r>>8 != 255 || g != 0 || b != 0 {
		t.Errorf("cropped pixel %d,%d,%d: want the red banner", r>>8, g>>8, b>>8)
	}
}

// cropOn errors: element missing, element without size, a screenshot that is no PNG.
func TestTakeScreenshotCropErrors(t *testing.T) {
	t.Parallel()
	o := quick
	o.ArtifactsDir = t.TempDir()
	thin := &ui.Node{ID: "thin", Bounds: ui.Rect{L: 10, T: 10, R: 10, B: 60}}
	f := &fakeDriver{screen: static(thin), shot: pngOf(t, 400, 800, colorWhite, imageRect(0, 0, 0, 0), colorRed)}
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: a, cropOn: {id: nope}}"), "Element not found")
	mustFail(t, runWith(t, f, o, "- takeScreenshot: {path: a, cropOn: {id: thin}}"), "cropOn element has no size")
	f2 := &fakeDriver{screen: static(btn("B", "b", 0)), shot: []byte("not a png")}
	mustFail(t, runWith(t, f2, o, "- takeScreenshot: {path: a, cropOn: {id: b}}"), "png")
	mustFail(t, runWith(t, f2, o, "- takeScreenshot: {path: a, cropOn: {id: '${(}'}}"), "JavaScript error")
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
