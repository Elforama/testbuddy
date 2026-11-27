package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/driver"
	"github.com/elforama/testbuddy/internal/engine"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/orchestrate"
)

type crashedApp struct {
	*fakeApp
	log   driver.AppLog
	asked *[]string
}

func (a crashedApp) AppLog(ctx context.Context, appID string, last time.Duration) (driver.AppLog, error) {
	*a.asked = append(*a.asked, appID)
	return a.log, nil
}

func TestFailedFlowKeepsTheAppLogAndSaysItCrashed(t *testing.T) {
	var asked []string
	app := crashedApp{&fakeApp{serial: "emulator-5554", screen: "home"}, driver.AppLog{
		Crash: "java.lang.IllegalStateException: boom at com.app.Main.onClick(Main.kt:12)",
		Trace: []string{"java.lang.IllegalStateException: boom", "at com.app.Main.onClick(Main.kt:12)"},
		Lines: []string{"12:00:01.000 E Net: request failed"}}, &asked}
	info := device.Info{Platform: device.Android, Serial: app.serial, Name: "Pixel", Emulator: true, State: "device"}
	dir := filepath.Join(t.TempDir(), "debug", "Login")
	r := orchestrate.Result{Job: &orchestrate.Job{Flow: &flow.Flow{Config: flow.Config{Name: "Login", AppID: "com.app"}}},
		Status: engine.Failed, Err: errors.New("Element not found: \"Go\""), Devices: map[string]device.Info{"": info},
		Start: time.Now().Add(-3 * time.Second), Duration: 3 * time.Second}
	saveFailure(dir, r, map[string]driver.Driver{"": app})
	if len(asked) != 1 || asked[0] != "com.app" {
		t.Fatalf("log read for %q, want once for com.app", asked)
	}
	want := "crashed: com.app: java.lang.IllegalStateException: boom at com.app.Main.onClick(Main.kt:12)\n" +
		"  java.lang.IllegalStateException: boom\n  at com.app.Main.onClick(Main.kt:12)\n12:00:01.000 E Net: request failed\n"
	if b, err := os.ReadFile(filepath.Join(dir, "app.log")); err != nil || string(b) != want {
		t.Fatalf("app.log = %q, %v\nwant %q", b, err, want)
	}

	con, out := plainConsole(false, false)
	con.flowEnd(r, dir)
	if !strings.Contains(out.String(), "\n  crashed: com.app: java.lang.IllegalStateException: boom at com.app.Main.onClick(Main.kt:12)\n  debug: ") {
		t.Errorf("no crashed line before the debug line:\n%s", out.String())
	}
}

func TestFailedFlowWithAQuietAppWritesNoAppLog(t *testing.T) {
	var asked []string
	app := crashedApp{&fakeApp{serial: "emulator-5554", screen: "home"}, driver.AppLog{}, &asked}
	dir := filepath.Join(t.TempDir(), "debug", "Login")
	r := orchestrate.Result{Job: &orchestrate.Job{Flow: &flow.Flow{Config: flow.Config{Name: "Login", AppID: "com.app"}}},
		Status: engine.Failed, Err: errors.New("x"), Start: time.Now()}
	saveFailure(dir, r, map[string]driver.Driver{"": app})
	if len(asked) != 1 {
		t.Fatalf("%d log reads, want 1", len(asked))
	}
	if _, err := os.Stat(filepath.Join(dir, "app.log")); !os.IsNotExist(err) {
		t.Errorf("app.log written for an app that logged nothing: %v", err)
	}
}
