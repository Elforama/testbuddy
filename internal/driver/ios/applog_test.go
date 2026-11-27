package ios

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elforama/testbuddy/internal/driver"
)

const benchApp = "dev.testbuddy.bench"

const logShowAbort = `getpwuid_r did not find a match for uid 501
Timestamp               Ty Process[PID:TID]
2026-11-27 01:57:25.791 E  BuddyBench[72147:43a1762] [com.apple.coreanimation:API] cannot add handler to 0 from 0 - dropping
2026-11-27 01:57:25.797 E  BuddyBench[72147:43a1762] [com.apple.BoardServices:XPCErrors] non-launching port is incompatible with service identifier "com.apple.PointerUI.pointeruid.default-service"
2026-11-27 01:57:26.001 E  BuddyBench[72147:43a1762] [dev.testbuddy.bench:net] request failed: timeout
2026-11-27 01:57:26.125 Df BuddyBench[72147:43a1762] [com.apple.Accessibility:AXCommon] Read Per-App on Init: Smart invert = (null)
2026-11-27 01:57:39.410 F  BuddyBench[72147:43a1762] BuddyBench/CartView.swift:41: Fatal error: Index out of range
second line of the same message
2026-11-27 02:04:27.628 Df SpringBoard[981:43a3b10] [com.apple.FrontBoard:Process] [app<dev.testbuddy.bench>:77021] Process exited: <RBSProcessExitContext| specific, status:<RBSProcessExitStatus| domain:signal(2) code:SIGABRT(6)>>.
`

const logShowForceQuit = `Timestamp               Ty Process[PID:TID]
2026-11-27 02:04:43.121 Df SpringBoard[981:43aa258] [com.apple.FrontBoard:Process] [app<dev.testbuddy.bench>:79864] Process exited: <RBSProcessExitContext| specific, status:<RBSProcessExitStatus| domain:frontboard(10) code:force-quit(0xfbfbfbfb)>>.
`

const listApps = `{
    "com.apple.Bridge" =     {
        ApplicationType = System;
        CFBundleDisplayName = Watch;
        CFBundleExecutable = Bridge;
        CFBundleIdentifier = "com.apple.Bridge";
    };
    "dev.testbuddy.bench" =     {
        ApplicationType = User;
        Bundle = "file:///Users/me/Library/Developer/CoreSimulator/Devices/SIM-1/data/Containers/Bundle/Application/3A89F434/dev.testbuddy.bench-1795683396313.app/";
        CFBundleDisplayName = "Buddy Bench";
        CFBundleExecutable = BuddyBench;
        CFBundleIdentifier = "dev.testbuddy.bench";
        GroupContainers =         {
        };
    };
    "com.shop.app" =     {
        ApplicationType = User;
        CFBundleExecutable = "Shop Beta";
        CFBundleIdentifier = "com.shop.app";
    };
}`

func TestExecutableOf(t *testing.T) {
	for id, want := range map[string]string{benchApp: "BuddyBench", "com.shop.app": "Shop Beta", "com.apple.Bridge": "Bridge", "dev.testbuddy": "", "com.none": ""} {
		if got := executableOf(listApps, id); got != want {
			t.Errorf("executableOf(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestParseLogShowCrash(t *testing.T) {
	got := parseLogShow(logShowAbort, benchApp)
	want := driver.AppLog{
		Crash: "exited on signal SIGABRT",
		Lines: []string{
			"01:57:26.001 E BuddyBench: [dev.testbuddy.bench:net] request failed: timeout",
			"01:57:39.410 F BuddyBench: BuddyBench/CartView.swift:41: Fatal error: Index out of range",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLogShow =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseLogShowAStoppedAppDidNotCrash(t *testing.T) {
	if got := parseLogShow(logShowForceQuit, benchApp); got.Crash != "" || got.Lines != nil {
		t.Errorf("parseLogShow = %+v", got)
	}
	other := strings.ReplaceAll(logShowAbort, "app<"+benchApp+">", "app<com.other>")
	if got := parseLogShow(other, benchApp); got.Crash != "" || len(got.Lines) != 2 {
		t.Errorf("another app's exit read as this one's crash: %+v", got)
	}
}

func TestAppLogReadsTheAppsErrors(t *testing.T) {
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "listapps", out: listApps}, {match: "log show", out: logShowAbort}})
	d := newIOSDriver(t, newFakeRunner(t))
	var _ driver.AppLogReader = d
	log, err := d.AppLog(ctxT(t), benchApp, 2500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if log.Crash != "exited on signal SIGABRT" || len(log.Lines) != 2 {
		t.Errorf("log = %+v", log)
	}
	calls := ts.simctl()
	if len(calls) != 2 || calls[0] != "listapps "+testUDID {
		t.Fatalf("simctl calls %q", calls)
	}
	for _, want := range []string{"spawn " + testUDID + " log show --last 4s --style compact --predicate ",
		`(process == "BuddyBench" AND messageType >= 16)`, `"[app<` + benchApp + `>:"`} {
		if !strings.Contains(calls[1], want) {
			t.Errorf("no %q in %q", want, calls[1])
		}
	}
}

func TestParseLogShowLeavesOutSystemFrameworkLines(t *testing.T) {
	for _, l := range parseLogShow(logShowAbort, benchApp).Lines {
		if strings.Contains(l, ": [com.apple.") {
			t.Errorf("a system framework's line among the app's: %q", l)
		}
	}
}

func TestAppLogStopsAtItsDeadline(t *testing.T) {
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "listapps", out: listApps}, {match: "log show", sh: "/bin/sleep 6;"}})
	d := newIOSDriver(t, newFakeRunner(t))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.AppLog(ctx, benchApp, time.Second)
	if took := time.Since(start); err == nil || took > 3500*time.Millisecond {
		t.Errorf("AppLog returned %v after %s, want an error soon after the 500 ms deadline", err, took.Round(100*time.Millisecond))
	}
}

func TestAppLogOfAnAppThatIsNotInstalled(t *testing.T) {
	ts := stubTools(t)
	ts.set("xcrun", []stubRule{{match: "listapps", out: listApps}})
	d := newIOSDriver(t, newFakeRunner(t))
	if _, err := d.AppLog(ctxT(t), "com.none", time.Second); err == nil || !strings.Contains(err.Error(), "com.none is not installed") {
		t.Errorf("err = %v", err)
	}
	if calls := ts.simctl(); len(calls) != 1 {
		t.Errorf("simctl calls %q, want the app list only", calls)
	}
}

func TestAppLogRejectsABadBundleID(t *testing.T) {
	ts := stubTools(t)
	d := newIOSDriver(t, newFakeRunner(t))
	if _, err := d.AppLog(ctxT(t), `x" OR "1" == "1`, time.Second); err == nil {
		t.Error("no error for a bundle id with predicate syntax")
	}
	if calls := ts.simctl(); len(calls) != 0 {
		t.Errorf("simctl ran: %q", calls)
	}
}
