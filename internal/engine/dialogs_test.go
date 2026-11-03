package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

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
