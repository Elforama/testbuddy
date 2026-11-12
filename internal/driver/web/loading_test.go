package web

// A main-frame load that never ends (issue #79): once the page's DOM is ready and has been
// quiet for loadGrace, the unfinished load stops holding the page unsettled and stops holding
// navigation back. Driven by injected events; the grace is passed by back-dating the last
// change rather than by sleeping, except where the navigation's own wait is measured.

import (
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

func settledNow(d *Driver) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isSettled()
}

func staleNow(d *Driver) (stale, ready bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stale, d.ready
}

// startLoad: the main frame starts loading and its DOM becomes ready; the load never ends.
func startLoad(d *Driver) {
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"id": "main", "url": "https://shop.example.com/"}}), "")
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	d.onEvent("Page.domContentEventFired", raw(map[string]any{}), "")
}

// backdate makes the last change older than the load grace and runs the stale check.
func backdate(d *Driver) {
	locked(d, func() { d.lastEvent = time.Now().Add(-loadGraceNow() - time.Millisecond) })
	d.onStale()
}

// Issue #79: a ready page whose load never ends settles once its DOM has been quiet for
// loadGrace, and the trace says it is still loading.
func TestStalledLoadStopsCounting(t *testing.T) {
	t.Setenv("TB_CI", "0")
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	if settledNow(d) {
		t.Fatal("a page whose DOM just became ready counts as settled")
	}
	d.onStale() // too early: the DOM changed just now
	if stale, _ := staleNow(d); stale || settledNow(d) {
		t.Fatal("the load stopped counting before the grace")
	}
	events, cancel := subscribe(d)
	defer cancel()
	backdate(d)
	waitEvent(t, events, "settled")
	if !settledNow(d) {
		t.Error("a ready, quiet page whose load never ends is not settled")
	}
	if !isLoading(d) {
		t.Error("the load is still running: loading must stay true")
	}
	if notes := strings.Join(d.Notes(), "\n"); !strings.Contains(notes, "https://shop.example.com/ is still loading 2s after") {
		t.Errorf("notes %q", notes)
	}

	// A new load is waited for afresh.
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	if stale, ready := staleNow(d); stale || ready || settledNow(d) {
		t.Error("a new load did not count as busy")
	}
}

// Before the DOM is ready the load counts however long it takes: no DOMContentLoaded, no
// giving up (a page still parsing, or waiting on a script it needs).
func TestLoadBeforeDOMReadyCounts(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	backdate(d)
	d.armStale()
	if stale, _ := staleNow(d); stale || settledNow(d) {
		t.Error("a load whose DOM is not ready yet stopped counting")
	}
	locked(d, func() {
		if d.staleT != nil {
			t.Error("the stale timer was armed before the DOM was ready")
		}
	})
}

// A load that ends normally needs none of this: the stale check that fires later does nothing.
func TestFinishedLoadNotStale(t *testing.T) {
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	d.onEvent("Page.frameStoppedLoading", raw(map[string]string{"frameId": "main"}), "")
	backdate(d)
	if stale, _ := staleNow(d); stale || len(d.Notes()) != 0 {
		t.Error("a finished load was noted as still loading")
	}
}

// The DOM changing pushes the giving up back: the timer re-arms from the latest change.
func TestStaleWaitsForQuietDOM(t *testing.T) {
	t.Setenv("TB_CI", "0")
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	locked(d, func() { d.lastEvent = time.Now().Add(-loadGrace / 2) })
	d.onStale()
	if stale, _ := staleNow(d); stale {
		t.Error("gave up on the load while the DOM changed within the grace")
	}
}

// In CI mode the grace is longer: on a slow runner a page's own scripts arrive later.
func TestLoadGraceCI(t *testing.T) {
	t.Setenv("TB_CI", "1")
	if g := loadGraceNow(); g != 5*time.Second {
		t.Errorf("CI grace %s", g)
	}
	t.Setenv("TB_CI", "0")
	if g := loadGraceNow(); g != loadGrace {
		t.Errorf("grace %s", g)
	}
}

// Issue #79: launchApp on a page whose image never answers returned only at the 30 s load
// timeout. Now it returns once the DOM has been ready and quiet for loadGrace.
func TestNavigateStalledLoad(t *testing.T) {
	t.Setenv("TB_CI", "0")
	page, d := inputPage(t)
	page.on("Page.navigate", func(cdpMsg) any {
		go func() {
			time.Sleep(20 * time.Millisecond)
			page.event("Page.frameNavigated", map[string]any{"frame": map[string]string{"id": "main", "url": "https://shop.example.com/"}}, "")
			page.event("Page.frameStartedLoading", map[string]string{"frameId": "main"}, "")
			page.event("Page.domContentEventFired", map[string]any{"timestamp": 1}, "")
			// no Page.loadEventFired: an image on the page never answers
		}()
		return map[string]any{"frameId": "main", "loaderId": "L1"}
	})
	t0 := time.Now()
	if err := d.LaunchApp(ctxT(t), "https://shop.example.com/", driver.LaunchOpts{}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(t0)
	t.Logf("launchApp: %s", took.Round(time.Millisecond))
	if took < loadGrace || took > loadGrace+time.Second {
		t.Errorf("launchApp took %s, want about the %s grace", took.Round(time.Millisecond), loadGrace)
	}
}
