package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/orchestrate"
	"github.com/elforama/testbuddy/internal/ui"
)

// stuckShotApp is the fake app on a device whose screenshot never comes back (it waits until
// its context ends), and whose screen listing honours its context, as the web driver's does.
type stuckShotApp struct{ *fakeApp }

func (a stuckShotApp) Screenshot(ctx context.Context, s float64, fm string) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (a stuckShotApp) Screen(ctx context.Context) (*ui.Screen, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return a.fakeApp.Screen(ctx)
}

// Issue #163: a failure report's screenshot that never returns must not hide the failure. The
// error is written first; the screenshot gives up after its own bound and the screen listing
// still gets its own time (it used to share the screenshot's, which the stalled call used up).
func TestSaveFailureWithAStuckScreenshot(t *testing.T) {
	old := failureArtifactTimeout
	failureArtifactTimeout = 300 * time.Millisecond
	t.Cleanup(func() { failureArtifactTimeout = old })

	app := stuckShotApp{&fakeApp{serial: "emulator-5554", screen: "home"}}
	info := device.Info{Platform: device.Android, Serial: app.serial, Name: "Fake phone", Emulator: true, State: "device"}

	dir := filepath.Join(t.TempDir(), "debug", "Login")
	r := orchestrate.Result{Err: errors.New("Element not found: \"Go\""), Devices: map[string]device.Info{"": info}}
	start := time.Now()
	saveFailure(dir, r, map[string]driver.Driver{"": app})
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("saveFailure took %s with a stuck screenshot", took)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "error.txt")); err != nil || string(b) != "Element not found: \"Go\"\n" {
		t.Errorf("error.txt = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "screenshot.png")); !os.IsNotExist(err) {
		t.Errorf("screenshot.png written without a screenshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "screen.txt")); err != nil {
		t.Errorf("screen.txt not written after the screenshot stalled: %v", err)
	}
}
