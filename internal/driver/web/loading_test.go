package web

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

func startLoad(d *Driver) {
	d.onEvent("Page.frameNavigated", raw(map[string]any{"frame": map[string]string{"id": "main", "url": "https://shop.example.com/"}}), "")
	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	d.onEvent("Page.domContentEventFired", raw(map[string]any{}), "")
}

func backdate(d *Driver) {
	locked(d, func() { d.domAt = time.Now().Add(-loadGraceNow() - time.Millisecond) })
	d.onStale()
}

func TestStalledLoadStopsCounting(t *testing.T) {
	t.Setenv("TB_CI", "0")
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	if settledNow(d) {
		t.Fatal("a page whose DOM just became ready counts as settled")
	}
	d.onStale()
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

	d.onEvent("Page.frameStartedLoading", raw(map[string]string{"frameId": "main"}), "")
	if stale, ready := staleNow(d); stale || ready || settledNow(d) {
		t.Error("a new load did not count as busy")
	}
}

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

func TestStaleWaitsForQuietDOM(t *testing.T) {
	t.Setenv("TB_CI", "0")
	d := newBareDriver()
	defer d.Close()
	startLoad(d)
	locked(d, func() { d.domAt = time.Now().Add(-loadGrace / 2) })
	d.onStale()
	if stale, _ := staleNow(d); stale {
		t.Error("gave up on the load while the DOM changed within the grace")
	}
}

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

func TestNavigateStalledLoad(t *testing.T) {
	t.Setenv("TB_CI", "0")
	page, d := inputPage(t)
	page.on("Page.navigate", func(cdpMsg) any {
		go func() {
			time.Sleep(20 * time.Millisecond)
			page.event("Page.frameNavigated", map[string]any{"frame": map[string]string{"id": "main", "url": "https://shop.example.com/"}}, "")
			page.event("Page.frameStartedLoading", map[string]string{"frameId": "main"}, "")
			page.event("Page.domContentEventFired", map[string]any{"timestamp": 1}, "")
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
