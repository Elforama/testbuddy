package session

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/device"
)

func openFake(t *testing.T) (*Service, *fakeDriver, *Dev) {
	t.Helper()
	adbLists(t, "emulator-5554 Pixel_7")
	s := New()
	f := newFake("emulator-5554", "Pixel 7", device.Android)
	f.show(loginScreen()...)
	return s, f, addDev(s, f, "")
}

func TestOpenWithAppLaunchesItAndShowsTheScreen(t *testing.T) {
	s, f, _ := openFake(t)
	cwd := t.TempDir()
	r, _ := call(t, s.open, map[string]any{"device": "emulator-5554", "app": "com.chat"}, cwd)
	if !r.OK || !reflect.DeepEqual(f.launched, []string{"com.chat"}) {
		t.Fatalf("tb open --app com.chat launched %v: %+v", f.launched, r)
	}
	connected := strings.Index(r.Text, "connected ")
	launch := strings.Index(r.Text, "ok Launch app")
	screen := strings.Index(r.Text, "screen now:")
	if connected != 0 || launch < 0 || screen < launch || !strings.Contains(r.Text[screen:], "Welcome back") || !strings.Contains(r.Text[screen:], "Sign in") {
		t.Errorf("the reply is not connected, the launch, then the screen:\n%s", r.Text)
	}
}

func TestOpenWithAppRecordsTheLaunchAsTheFirstStep(t *testing.T) {
	s, _, _ := openFake(t)
	cwd := t.TempDir()
	if r, _ := call(t, s.open, map[string]any{"device": "emulator-5554", "app": "com.chat"}, cwd); !r.OK {
		t.Fatalf("open: %+v", r)
	}
	if r, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Sign in\n"}, cwd); !r.OK {
		t.Fatalf("do: %+v", r)
	}
	if r, _ := call(t, s.log, nil, cwd); !strings.HasPrefix(r.Text, "- launchApp\n- tapOn:") {
		t.Errorf("tb log after open --app and a tap: %q", r.Text)
	}
	r, _ := call(t, s.save, map[string]any{"preview": true}, cwd)
	if !r.OK || strings.Contains(r.Text, "launchApp step") || !strings.Contains(r.Text, "appId: com.chat\n---\n- launchApp\n") {
		t.Errorf("tb save --preview after open --app:\n%s", r.Text)
	}
}

func TestOpenWithoutAppLaunchesNothingAndSaysWhatIsInFront(t *testing.T) {
	s, f, d := openFake(t)
	r, _ := call(t, s.open, map[string]any{"device": "emulator-5554"}, t.TempDir())
	if !r.OK || len(f.launched) != 0 || len(d.steps) != 0 {
		t.Fatalf("tb open without --app launched %v, recorded %v: %+v", f.launched, d.steps, r)
	}
	if !strings.HasPrefix(r.Text, "connected ") || !strings.Contains(r.Text, "\nin front: com.app (nothing was launched: --app does that)\n") || strings.Contains(r.Text, "screen now:") {
		t.Errorf("the reply does not name what is in front:\n%s", r.Text)
	}
	f.get(func() { f.fgErr = errBoom })
	if r, _ := call(t, s.open, map[string]any{"device": "emulator-5554"}, t.TempDir()); !r.OK || strings.Contains(r.Text, "in front") {
		t.Errorf("with no foreground app to name: %+v", r)
	}
}

func TestOpenWithoutAppOnWebSaysThePageInFront(t *testing.T) {
	s := New()
	f := newFake("chrome", "Chrome", device.Web)
	f.fg = "http://localhost:7360/settings"
	addDev(s, f, "")
	r, _ := call(t, s.open, map[string]any{"device": "chrome"}, t.TempDir())
	if !r.OK || len(f.launched) != 0 || !strings.Contains(r.Text, "\nin front: http://localhost:7360/settings (") {
		t.Errorf("tb open on an open browser session: launched %v, %+v", f.launched, r)
	}
}

func TestOpenAgainWithAppLaunchesAgain(t *testing.T) {
	s, f, _ := openFake(t)
	cwd := t.TempDir()
	args := map[string]any{"device": "emulator-5554", "app": "com.chat"}
	if r, _ := call(t, s.open, args, cwd); !r.OK {
		t.Fatalf("open: %+v", r)
	}
	if r, _ := call(t, s.do, map[string]any{"commands": "- tapOn: Sign in\n"}, cwd); !r.OK {
		t.Fatalf("do: %+v", r)
	}
	r, _ := call(t, s.open, args, cwd)
	if !r.OK || !reflect.DeepEqual(f.launched, []string{"com.chat", "com.chat"}) {
		t.Fatalf("the second tb open --app launched %v: %+v", f.launched, r)
	}
	if !strings.Contains(r.Text, "ok Launch app") || !strings.Contains(r.Text, "screen now:") || !strings.Contains(r.Text, "Welcome back") {
		t.Errorf("the second open does not show the whole screen:\n%s", r.Text)
	}
	if r, _ := call(t, s.log, nil, cwd); !strings.HasPrefix(r.Text, "- launchApp\n- tapOn:") || !strings.HasSuffix(r.Text, "\n- launchApp\n") {
		t.Errorf("tb log after the second open: %q", r.Text)
	}
}

func TestOpenWithAppThatDoesNotLaunchKeepsTheSession(t *testing.T) {
	s, f, d := openFake(t)
	f.launchErr = errors.New("com.missing is not installed")
	cwd := t.TempDir()
	r, _ := call(t, s.open, map[string]any{"device": "emulator-5554", "app": "com.missing"}, cwd)
	if r.OK || r.Exit != 1 {
		t.Fatalf("a failed launch: exit %d, %+v", r.Exit, r)
	}
	if !strings.HasPrefix(r.Text, "connected ") || !strings.Contains(r.Text, "FAILED Launch app") || !strings.Contains(r.Text, "com.missing is not installed") ||
		!strings.Contains(r.Text, "the session is open, but com.missing did not launch") {
		t.Errorf("the reply does not say it connected and why the launch failed:\n%s", r.Text)
	}
	if s.devs["emulator-5554"] != d || len(d.steps) != 0 {
		t.Errorf("after a failed launch: session kept %v, recorded %v", s.devs["emulator-5554"] == d, d.steps)
	}
	if r, _ := call(t, s.look, nil, cwd); !r.OK || !strings.Contains(r.Text, "Welcome back") {
		t.Errorf("tb look after the failed launch: %+v", r)
	}
}

func TestOpenWithAppWaitsForTheSessionLikeDo(t *testing.T) {
	s, f, d := openFake(t)
	d.mu.Lock()
	d.holder.Store(&running{what: "do tapOn: Pay"})
	r, _ := call(t, s.open, map[string]any{"device": "emulator-5554", "app": "com.chat", "no_wait": true}, t.TempDir())
	d.mu.Unlock()
	if r.OK || r.Exit != 1 || len(f.launched) != 0 || !strings.Contains(r.Text, "is running a command: do tapOn: Pay") {
		t.Errorf("tb open --app --no-wait on a busy session launched %v: %+v", f.launched, r)
	}
}
