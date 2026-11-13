package web

import (
	"reflect"
	"testing"

	"github.com/elforama/testbuddy/internal/driver"
)

// Tabs on a fake browser connection (issue #78): a new tab the browser attaches paused is set
// up, with the active mocks, before it is let run, and calls go to it from then on; another
// tab's events no longer count; when it closes the driver goes back to its opener, read anew.
func TestTabsFollowAndReturn(t *testing.T) {
	page := newFakeCDP(t)
	d := newWebDriver(t, page, nil)
	first := &tab{target: "T0", session: "S0", ready: true}
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
	d.mockMu.Lock()
	d.fetchOn = true
	d.mocks = append(d.mocks, driver.NetworkMock{URL: "*/api/*"})
	d.mockMu.Unlock()

	attach := func(session, target, opener string) {
		d.onEvent("Target.attachedToTarget", raw(map[string]any{"sessionId": session, "waitingForDebugger": true,
			"targetInfo": map[string]any{"targetId": target, "type": "page", "url": "", "openerId": opener}}), rootSession)
	}
	attach("S1", "T1", "T0")
	d.mu.Lock()
	busy := d.loadBusy()
	d.mu.Unlock()
	if !busy {
		t.Error("a tab being set up does not hold the page")
	}
	eventually(t, "the new tab driven", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.cur.session == "S1" && d.opening == 0
	})
	var s1 []string
	for _, m := range page.calls("") {
		if m.SessionID == "S1" {
			s1 = append(s1, m.Method)
		}
	}
	want := []string{"Page.enable", "Page.setLifecycleEventsEnabled", "Runtime.enable", "Network.enable", "Runtime.addBinding",
		"Page.addScriptToEvaluateOnNewDocument", "Target.setAutoAttach", "Fetch.enable", "Runtime.runIfWaitingForDebugger", "Page.bringToFront"}
	if !reflect.DeepEqual(s1, want) {
		t.Errorf("the new tab's setup %v\nwant %v", s1, want)
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
	d.onEvent("Runtime.bindingCalled", notify(0, 1), "S0")
	if s, _, _ := state(d); s != seq {
		t.Error("the tab left behind changed the page")
	}

	// A second tab, from the first: closing it goes back to its opener, not the tab driven last.
	attach("S2", "T2", "T0")
	eventually(t, "the second tab driven", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.cur.session == "S2" && d.opening == 0
	})
	d.onEvent("Target.detachedFromTarget", raw(map[string]any{"sessionId": "S2", "targetId": "T2"}), rootSession)
	eventually(t, "the opener driven again", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.cur.session == "S0" && d.opening == 0
	})
	d.mu.Lock()
	main, url, loading := d.mainFrame, d.pageURL, d.loading
	d.mu.Unlock()
	if main != "F-S0" || url != "https://shop.example.com/S0" || loading {
		t.Errorf("the opener's state: frame %q url %q loading %v", main, url, loading)
	}
	if d.page.tabSession() != "S0" {
		t.Errorf("calls go to %q", d.page.tabSession())
	}
	// launchApp closes the tabs but the first.
	page.reset()
	d.closeOtherTabs(ctxT(t))
	if c := page.calls("Target.closeTarget"); len(c) != 1 || c[0].params()["targetId"] != "T1" {
		t.Errorf("closed %v, want T1", c)
	}

	// A page attached again (the first tab, by the browser's auto-attach) is let run and left.
	page.reset()
	attach("S9", "T0", "")
	eventually(t, "the duplicate let go", func() bool { return len(page.calls("Target.detachFromTarget")) == 1 })
	if d.page.tabSession() != "S0" {
		t.Errorf("a duplicate session became the driven tab: %q", d.page.tabSession())
	}
}
