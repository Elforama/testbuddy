package web

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

// tabsDriver is a driver on a fake browser connection, driving its first tab (session S0).
func tabsDriver(t *testing.T) (*Driver, *fakeCDP) {
	page := newFakeCDP(t)
	d := newWebDriver(t, page, nil)
	first := newTab("T0", "S0", "", 0)
	first.ready = true
	d.mu.Lock()
	d.tabs, d.cur, d.claimed = []*tab{first}, first, map[string]bool{"T0": true}
	d.mu.Unlock()
	d.page.setTab("S0")
	page.on("Page.getFrameTree", func(m cdpMsg) any {
		return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "F-" + m.SessionID, "url": "https://shop.example.com/" + m.SessionID}}}
	})
	page.evaluator(func(expr, session string) any {
		if expr == "document.readyState" {
			return value("complete")
		}
		return nil
	})
	return d, page
}

// attachTab is the browser attaching a new tab, paused.
func attachTab(d *Driver, session, target, opener string) {
	d.onTabEvent("Target.attachedToTarget", raw(map[string]any{"sessionId": session, "waitingForDebugger": true,
		"targetInfo": map[string]any{"targetId": target, "type": "page", "url": "", "openerId": opener}}), rootSession, rootSession)
}

func detachTab(d *Driver, session, target string) {
	d.onTabEvent("Target.detachedFromTarget", raw(map[string]any{"sessionId": session, "targetId": target}), rootSession, rootSession)
}

func driving(t *testing.T, d *Driver, session string) {
	t.Helper()
	eventually(t, "tab "+session+" driven", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.cur.session == session && d.opening == 0
	})
}

// sent lists the methods sent to session, in order.
func sent(page *fakeCDP, session string) []string {
	var out []string
	for _, m := range page.calls("") {
		if m.SessionID == session {
			out = append(out, m.Method)
		}
	}
	return out
}

// Tabs on a fake browser connection (issue #78): a new tab the browser attaches paused is set
// up, with the active mocks, before it is let run, and calls go to it from then on; the tab
// left behind no longer moves the page, but its requests and dialogs are answered in it; a
// recording follows the driven tab; when it closes the driver goes back to its opener, read
// anew, with the mocks as they are then.
func TestTabsFollowAndReturn(t *testing.T) {
	d, page := tabsDriver(t)
	d.mockMu.Lock()
	d.fetchOn = true
	d.mocks = append(d.mocks, driver.NetworkMock{URL: "*/api/*"})
	d.mockMu.Unlock()
	d.recMu.Lock()
	d.rec = &recording{dir: t.TempDir()}
	d.recMu.Unlock()

	attachTab(d, "S1", "T1", "T0")
	d.mu.Lock()
	busy := d.loadBusy()
	d.mu.Unlock()
	if !busy {
		t.Error("a tab being set up does not hold the page")
	}
	driving(t, d, "S1")
	want := []string{"Page.enable", "Page.setLifecycleEventsEnabled", "Runtime.enable", "Network.enable", "Runtime.addBinding",
		"Page.addScriptToEvaluateOnNewDocument", "Target.setAutoAttach", "Fetch.enable", "Runtime.runIfWaitingForDebugger",
		"Page.bringToFront", "Page.startScreencast"}
	if s1 := sent(page, "S1"); !reflect.DeepEqual(s1, want) {
		t.Errorf("the new tab's setup %v\nwant %v", s1, want)
	}
	if s0 := sent(page, "S0"); !reflect.DeepEqual(s0, []string{"Page.stopScreencast"}) {
		t.Errorf("sent to the tab left %v, want its screencast stopped", s0)
	}
	page.reset()
	if _, err := d.Foreground(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	if c := page.calls("Runtime.evaluate"); len(c) != 1 || c[0].SessionID != "S1" {
		t.Errorf("a call after the switch went to %v, want the new tab", c)
	}

	// The tab left behind no longer moves the page.
	seq, _, _ := state(d)
	d.onTabEvent("Runtime.bindingCalled", notify(0, 1), "S0", "S0")
	if s, _, _ := state(d); s != seq {
		t.Error("the tab left behind changed the page")
	}
	// Its paused request is answered in it, though by the time it is handled "" means the new
	// tab (an event read just before the switch).
	page.reset()
	d.onTabEvent("Fetch.requestPaused", raw(map[string]any{"requestId": "R1",
		"request": map[string]any{"url": "https://shop.example.com/other", "method": "GET"}}), "", "S0")
	eventually(t, "the request let go", func() bool { return len(page.calls("Fetch.continueRequest")) == 1 })
	if c := page.calls("Fetch.continueRequest")[0]; c.SessionID != "S0" {
		t.Errorf("the left tab's request was answered in %q", c.SessionID)
	}
	// Its dialog is answered in it, and the trace says it was a background tab's.
	d.Notes()
	d.onTabEvent("Page.javascriptDialogOpening", raw(map[string]any{"type": "alert", "message": "Saved"}), "S0", "S0")
	eventually(t, "the dialog answered", func() bool { return len(page.calls("Page.handleJavaScriptDialog")) == 1 })
	if c := page.calls("Page.handleJavaScriptDialog")[0]; c.SessionID != "S0" {
		t.Errorf("the background dialog was answered in %q", c.SessionID)
	}
	if n := strings.Join(d.Notes(), "\n"); !strings.Contains(n, `a background tab opened a JavaScript alert "Saved"`) {
		t.Errorf("notes %q", n)
	}

	// A second tab, from the first: closing it goes back to its opener, not the tab driven
	// last. The mocks were cleared in it meanwhile: the opener stops intercepting too.
	attachTab(d, "S2", "T2", "T0")
	driving(t, d, "S2")
	if err := d.ClearNetworkMocks(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	page.reset()
	detachTab(d, "S2", "T2")
	driving(t, d, "S0")
	if s0 := sent(page, "S0"); !contains(s0, "Fetch.disable") || contains(s0, "Fetch.enable") || !contains(s0, "Page.startScreencast") {
		t.Errorf("back on the opener, sent %v: want Fetch.disable and the screencast", s0)
	}
	d.mu.Lock()
	main, url, loading := d.mainFrame, d.pageURL, d.loading
	d.mu.Unlock()
	if main != "F-S0" || url != "https://shop.example.com/S0" || loading {
		t.Errorf("the opener's state: frame %q url %q loading %v", main, url, loading)
	}
	if d.page.tabSession() != "S0" {
		t.Errorf("calls go to %q", d.page.tabSession())
	}

	// launchApp closes the tabs but the first, and says so once.
	page.reset()
	d.Notes()
	d.closeOtherTabs(ctxT(t))
	if c := page.calls("Target.closeTarget"); len(c) != 1 || c[0].params()["targetId"] != "T1" {
		t.Errorf("closed %v, want T1", c)
	}
	if n := d.Notes(); !reflect.DeepEqual(n, []string{"web: launchApp closed 1 other tab(s)"}) {
		t.Errorf("notes %q", n)
	}

	// A page attached again (the first tab, by the browser's auto-attach) is let run and left.
	page.reset()
	attachTab(d, "S9", "T0", "")
	eventually(t, "the duplicate let go", func() bool { return len(page.calls("Target.detachFromTarget")) == 1 })
	if d.page.tabSession() != "S0" {
		t.Errorf("a duplicate session became the driven tab: %q", d.page.tabSession())
	}
}

// Of two tabs opened at once, the one opened last is driven, whichever is set up first.
func TestTabsLastOpenedWins(t *testing.T) {
	d, _ := tabsDriver(t)
	a, b := newTab("TA", "SA", "T0", 1), newTab("TB", "SB", "T0", 2)
	d.mu.Lock()
	d.tabs = append(d.tabs, a, b)
	d.tabSeq = 2
	d.opening += 2
	d.mu.Unlock()
	d.setUpTab(b, "https://shop.example.com/b")
	d.setUpTab(a, "https://shop.example.com/a")
	d.mu.Lock()
	cur, opening := d.cur.session, d.opening
	d.mu.Unlock()
	if cur != "SB" || opening != 0 {
		t.Errorf("driving %s (opening %d), want SB, the tab opened last", cur, opening)
	}
	if !a.ready {
		t.Error("the tab opened first was not set up")
	}
}

// A tab that closes while it is set up (it does not answer) does not hold the page for the
// setup's 10 s: its calls end with it.
func TestTabClosedDuringSetup(t *testing.T) {
	d, page := tabsDriver(t)
	page.on("Page.enable", func(m cdpMsg) any {
		if m.SessionID == "S1" {
			return noAnswer
		}
		return map[string]any{}
	})
	attachTab(d, "S1", "T1", "T0")
	eventually(t, "the setup sent", func() bool { return len(page.calls("Page.enable")) == 1 })
	start := time.Now()
	detachTab(d, "S1", "T1")
	eventually(t, "the page no longer busy", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.opening == 0
	})
	if took := time.Since(start); took > time.Second {
		t.Errorf("the page stayed busy %v after the tab closed", took)
	}
	if d.page.tabSession() != "S0" {
		t.Errorf("driving %q, want the first tab", d.page.tabSession())
	}
}
