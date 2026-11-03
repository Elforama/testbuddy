package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/ui"
)

// anrDialog is Android's "isn't responding" dialog: title, Wait, Close app (either optional).
func anrDialog(title string, wait, closeApp bool) []*ui.Node {
	nodes := []*ui.Node{btn(title, "android:id/alertTitle", 100)}
	if wait {
		nodes = append(nodes, btn("Wait", "android:id/aerr_wait", 200))
	}
	if closeApp {
		nodes = append(nodes, btn("Close app", "android:id/aerr_close", 300))
	}
	return nodes
}

// tapY answers one dialog and says which button's row (its y) it tapped, 0 for none.
func tapY(t *testing.T, r *Runner, f *fakeDriver, s *ui.Screen) int {
	t.Helper()
	before := len(f.taps)
	ok, err := r.answerDialog(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		if len(f.taps) != before {
			t.Fatalf("tapped without saying so")
		}
		return 0
	}
	return f.taps[len(f.taps)-1][1]
}

// Android: Wait the first time a dialog shows, Close app when the same one comes back; a
// dialog with one of the buttons gets that one.
func TestAndroidDialogAnswers(t *testing.T) {
	f := &fakeDriver{}
	r := New(f, quick)
	both := linked("android", anrDialog("Launcher isn't responding", true, true)...)
	if y := tapY(t, r, f, both); y != 225 {
		t.Errorf("first: tapped %d, want Wait", y)
	}
	if y := tapY(t, r, f, linked("android", anrDialog("Launcher isn't responding", true, true)...)); y != 325 {
		t.Errorf("again: tapped %d, want Close app", y)
	}
	if y := tapY(t, r, f, linked("android", anrDialog("Maps keeps stopping", true, true)...)); y != 225 {
		t.Errorf("another dialog: tapped %d, want Wait", y)
	}
	if y := tapY(t, r, f, linked("android", anrDialog("Camera keeps stopping", false, true)...)); y != 325 {
		t.Errorf("close only: tapped %d", y)
	}
	waitOnly := anrDialog("Slow isn't responding", true, false)
	tapY(t, r, f, linked("android", waitOnly...))
	if y := tapY(t, r, f, linked("android", anrDialog("Slow isn't responding", true, false)...)); y != 225 {
		t.Errorf("wait only, again: tapped %d, want Wait (there is no Close)", y)
	}
	if y := tapY(t, r, f, linked("android", btn("Home", "", 0), btn("Title", "android:id/alertTitle", 100))); y != 0 {
		t.Errorf("an ordinary alert: tapped %d", y)
	}
}

// iOS notifications alert: allow or deny as launchApp's permissions said; unset leaves it.
func TestIOSNotificationAnswers(t *testing.T) {
	alert := func(labels ...string) *ui.Screen {
		nodes := []*ui.Node{{Text: "“App” Would Like to Send You Notifications", Bounds: ui.Rect{T: 300, R: 400, B: 380}}}
		for i, l := range labels {
			b := btn("", "", 400+i*100)
			b.Class, b.Desc = "Button", l
			nodes = append(nodes, b)
		}
		return linked("ios", nodes...)
	}
	for _, tc := range []struct {
		name, answer string
		s            *ui.Screen
		want         int
	}{
		{"allow", "allow", alert("Don’t Allow", "Allow"), 525},
		{"deny", "deny", alert("Don’t Allow", "Allow"), 425},
		{"deny, straight quote", "deny", alert("Don't Allow", "Allow"), 425},
		{"allow, other order", "allow", alert("Allow", "Don’t Allow"), 425},
		{"allow, localized", "allow", alert("Refuser", "Autoriser"), 525},
		{"deny, localized", "deny", alert("Refuser", "Autoriser"), 425},
		{"allow, one button", "allow", alert("OK"), 425},
		{"unset", "unset", alert("Don’t Allow", "Allow"), 0},
		{"no answer yet", "", alert("Don’t Allow", "Allow"), 0},
		{"no buttons", "allow", alert(), 0},
		{"no alert", "allow", linked("ios", btn("Allow", "", 100)), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeDriver{plat: "ios"}
			r := New(f, quick)
			r.notifications = tc.answer
			if y := tapY(t, r, f, tc.s); y != tc.want {
				t.Errorf("tapped %d, want %d", y, tc.want)
			}
		})
	}
	// Web pages have no system dialogs to answer.
	f := &fakeDriver{plat: "web"}
	r := New(f, quick)
	if y := tapY(t, r, f, linked("web", anrDialog("x", true, true)...)); y != 0 {
		t.Errorf("web: tapped %d", y)
	}
}

// A tap on a dialog button that the device fails fails the lookup.
func TestDialogTapError(t *testing.T) {
	f := &fakeDriver{screen: static(anrDialog("Launcher isn't responding", true, true)...)}
	f.fail("Tap", errors.New("input lost"))
	mustFail(t, runWith(t, f, quick, "- assertVisible: Home"), "input lost")
	mustFail(t, runWith(t, f, quick, "- assertNotVisible: Home"), "input lost")
}

// A dialog that will not go away is answered at most maxDialogAnswers times per lookup, then
// left for the flow to fail on.
func TestDialogThatStays(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{screen: static(anrDialog("Launcher isn't responding", true, true)...)}
	f.onTap = func(x, y int) { f.change(true) }
	mustFail(t, runWith(t, f, quick, "- assertVisible: Home"), "Home")
	if len(f.taps) != maxDialogAnswers {
		t.Errorf("%d taps, want %d", len(f.taps), maxDialogAnswers)
	}
}

// assertNotVisible answers dialogs too, then checks the screen behind.
func TestDialogInAbsent(t *testing.T) {
	f := &fakeDriver{}
	var shown atomic.Bool
	shown.Store(true)
	f.screen = func() []*ui.Node {
		if shown.Load() {
			return anrDialog("Launcher isn't responding", true, true)
		}
		return []*ui.Node{btn("Home", "", 0)}
	}
	f.onTap = func(x, y int) { shown.Store(false); f.change(true) }
	mustPass(t, runWith(t, f, quick, "- assertNotVisible: Wait"))
	if len(f.taps) != 1 {
		t.Errorf("taps %v", f.taps)
	}
}

// iosAlert is a SpringBoard permission alert with Don’t Allow and Allow buttons.
func iosAlert() []*ui.Node {
	alert := &ui.Node{Class: "Alert", Desc: "Allow “Bench” to access your camera?", Bounds: ui.Rect{L: 40, T: 300, R: 360, B: 600}}
	deny := &ui.Node{Class: "Button", Desc: "Don’t Allow", Depth: 1, Bounds: ui.Rect{L: 40, T: 530, R: 200, B: 580}, Enabled: true, Clickable: true}
	allow := &ui.Node{Class: "Button", Desc: "Allow", Depth: 1, Bounds: ui.Rect{L: 200, T: 530, R: 360, B: 580}, Enabled: true, Clickable: true}
	return []*ui.Node{alert, deny, allow}
}

// A tap on a system alert's button that the alert ignores (it was still appearing) is made
// again: the alert stays up until a tap lands.
func TestIOSAlertLostTapIsRetried(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: static(btn("Camera: not asked", "", 100))}
	var taps atomic.Int64
	f.sysAlert = func() []*ui.Node {
		if taps.Load() >= 2 {
			return nil // the second tap landed
		}
		return iosAlert()
	}
	f.onTap = func(x, y int) { taps.Add(1); f.change(true) }
	res := runWith(t, f, quick, `- tapOn: "Allow"`)
	mustPass(t, res)
	if len(f.taps) != 2 {
		t.Fatalf("%d taps, want 2 (the first was lost)", len(f.taps))
	}
	if f.taps[0] != f.taps[1] || f.taps[0][0] < 200 {
		t.Errorf("taps %v, want both on Allow", f.taps)
	}
}

// An alert that closes on the first tap is tapped once, even while it is still listed for a
// moment as it goes (a second tap would land on the app behind it).
func TestIOSAlertClosingIsNotTappedAgain(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: static(btn("Camera: not asked", "", 100))}
	var tappedAt atomic.Int64
	f.sysAlert = func() []*ui.Node {
		at := tappedAt.Load()
		if at == 0 || time.Since(time.Unix(0, at)) < 150*time.Millisecond {
			return iosAlert() // still listed while it animates out
		}
		return nil
	}
	f.onTap = func(x, y int) { tappedAt.CompareAndSwap(0, time.Now().UnixNano()); f.change(true) }
	mustPass(t, runWith(t, f, quick, `- tapOn: "Allow"`))
	if len(f.taps) != 1 {
		t.Errorf("%d taps, want 1", len(f.taps))
	}
}

// An alert that never answers is tapped a bounded number of times, then left (the next step
// fails on it, with the trace saying why).
func TestIOSAlertThatIgnoresEveryTap(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", sysAlert: iosAlert}
	f.onTap = func(x, y int) { f.change(true) }
	mustPass(t, runWith(t, f, quick, `- tapOn: "Allow"`))
	if len(f.taps) != 1+alertRetries {
		t.Errorf("%d taps, want %d", len(f.taps), 1+alertRetries)
	}
}

// The app's own alerts are not retried: one may come back the same on purpose (a retry that
// fails again), and a second tap would answer it twice.
func TestAppAlertIsNotRetried(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: iosAlert}
	f.onTap = func(x, y int) { f.change(true) }
	mustPass(t, runWith(t, f, quick, `- tapOn: "Allow"`))
	if len(f.taps) != 1 {
		t.Errorf("%d taps, want 1", len(f.taps))
	}
}

// A permission alert left up from before is declined before launchApp restarts the app: it
// would take every touch of the new flow.
func TestLaunchClosesStaleIOSAlert(t *testing.T) {
	t.Parallel()
	f := &fakeDriver{plat: "ios", screen: static(btn("Home", "", 100))}
	var closed atomic.Bool
	var launchedFirst atomic.Bool
	f.sysAlert = func() []*ui.Node {
		if closed.Load() {
			return nil
		}
		return iosAlert()
	}
	f.onTap = func(x, y int) {
		if closed.CompareAndSwap(false, true) { // the first tap
			f.mu.Lock()
			launchedFirst.Store(len(f.launches) > 0)
			f.mu.Unlock()
		}
		f.change(true)
	}
	mustPass(t, runWith(t, f, quick, "- launchApp\n- tapOn: Home"))
	if len(f.taps) < 1 || f.taps[0][0] >= 200 {
		t.Fatalf("taps %v, want the first on Don’t Allow (x < 200)", f.taps)
	}
	if launchedFirst.Load() {
		t.Error("the alert was closed after the launch, not before")
	}
	// Without a restart (stopApp: false) an alert may be the running app's own: left alone.
	g := &fakeDriver{plat: "ios", sysAlert: iosAlert}
	mustPass(t, runWith(t, g, quick, "- launchApp: {stopApp: false}"))
	if len(g.taps) != 0 {
		t.Errorf("stopApp false: taps %v", g.taps)
	}
}

// autofillSheet is Android's "Save password?" sheet over the app.
func autofillSheet() []*ui.Node {
	title := btn("Save password to Google Password Manager?", "android:id/autofill_save_title", 500)
	no := btn("Not now", "android:id/autofill_save_no", 700)
	no.Bounds = ui.Rect{L: 0, T: 700, R: 200, B: 750}
	yes := btn("Continue", "android:id/autofill_save_yes", 700)
	yes.Bounds = ui.Rect{L: 200, T: 700, R: 400, B: 750}
	return []*ui.Node{title, no, yes}
}

// The autofill save sheet is declined when a lookup meets it, and before launchApp when one
// was left up.
func TestAndroidAutofillSheetIsDeclined(t *testing.T) {
	t.Parallel()
	for _, flow := range []string{"- tapOn: Remember me", "- launchApp\n- tapOn: Remember me"} {
		f := &fakeDriver{}
		var up atomic.Bool
		up.Store(true)
		f.screen = func() []*ui.Node {
			if up.Load() {
				return autofillSheet()
			}
			return []*ui.Node{btn("Remember me", "", 100)}
		}
		f.onTap = func(x, y int) {
			if y >= 700 && x < 200 {
				up.Store(false) // Not now
			}
			f.change(true)
		}
		mustPass(t, runWith(t, f, quick, flow))
		if len(f.taps) != 2 || f.taps[0][0] >= 200 || f.taps[0][1] < 700 {
			t.Errorf("%q: taps %v, want Not now, then Remember me", flow, f.taps)
		}
	}
}
