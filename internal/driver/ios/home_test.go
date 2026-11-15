package ios

import (
	"strings"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// homeRunner answers the key op as the agent before issue #286 did when XCTest refused the
// Home press: ok, nothing changed. springboard says whether SpringBoard comes to the front.
func homeRunner(t *testing.T, springboard bool) *fakeRunner {
	r := newFakeRunner(t)
	r.on("key", func(map[string]any) map[string]any {
		return map[string]any{"result": "no_change", "injectMs": 1, "settleMs": 1002, "seq": 5}
	})
	r.on("awaitPackage", func(req map[string]any) map[string]any {
		return map[string]any{"found": springboard && req["pkg"] == "com.apple.springboard"}
	})
	r.on("foreground", func(map[string]any) map[string]any {
		if springboard {
			return map[string]any{"pkg": "com.apple.springboard", "pid": 40}
		}
		return map[string]any{"pkg": "dev.testbuddy.bench", "pid": 41}
	})
	return r
}

// Issue #286: a Home press XCTest refused ("only one gesture can be performed at a time")
// answered ok and the step passed, with the app still in front; the flow failed later, on a
// misleading assertion. After the press, SpringBoard must be in front.
func TestPressHomeFailsWhenTheAppStaysInFront(t *testing.T) {
	r := homeRunner(t, false)
	d := newIOSDriver(t, r)
	_, err := d.PressKey(ctxT(t), "home", driver.ActionOpts{})
	if err == nil {
		t.Fatal("PressKey(home) passed with the app still in front (issue #286)")
	}
	if !strings.Contains(err.Error(), "Home press did not take effect") || !strings.Contains(err.Error(), "dev.testbuddy.bench") {
		t.Errorf("err = %v", err)
	}
	if n := len(r.requests("key")); n != 1 {
		t.Errorf("key sent %d times, want 1 (a press that may have gone is never sent again)", n)
	}
}

// Issue #286: a Home press that took effect passes; other keys do not look at the foreground.
func TestPressHomePassesWithSpringBoardInFront(t *testing.T) {
	r := homeRunner(t, true)
	d := newIOSDriver(t, r)
	if _, err := d.PressKey(ctxT(t), "home", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if n := len(r.requests("awaitPackage")); n != 1 {
		t.Errorf("awaitPackage asked %d times, want 1", n)
	}
	if _, err := d.PressKey(ctxT(t), "enter", driver.ActionOpts{}); err != nil {
		t.Fatal(err)
	}
	if n := len(r.requests("awaitPackage")); n != 1 {
		t.Errorf("enter looked at the foreground: awaitPackage asked %d times, want 1", n)
	}
}
