package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/elforama/testbuddy/internal/daemon"
	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/devlock"
	"github.com/elforama/testbuddy/internal/driver/android"
	"github.com/elforama/testbuddy/internal/driver/ios"
)

// cmdRelease frees devices for other test tools: it closes tb's sessions on them and stops
// tb's agent (Android's UiAutomation slot, an iOS simulator's XCTest runner), which a run
// with --keep-agents, an open session, or a crashed run leaves behind. A device another tb is
// using (a run, a session in another daemon) is left alone.
func cmdRelease(argv []string) int {
	a, err := parseArgs(argv, flagSpec{[]string{"device", "d"}, textFlag}, flagSpec{[]string{"platform", "p"}, textFlag})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	platform, err := platformFlag(a)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	which := a.str("device")
	if which == "" {
		which = "all"
	}
	// Sessions first: closing one stops its agent and frees its lock. With -p, only that
	// platform's sessions (issue #184).
	closeCode := 0
	if daemon.Running() {
		closeCode = agentCall("close", []string{"-d", which, "-p", platform}, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	all := listDevices(ctx)
	var devs []device.Info
	if which == "all" {
		for _, d := range all {
			if d.Platform == device.Android || d.Platform == device.IOS {
				if p := device.Platform(platform); p == "" || p == d.Platform {
					devs = append(devs, d)
				}
			}
		}
	} else {
		d, err := device.Pick(all, device.Platform(platform), which)
		if err != nil {
			if closeCode == 2 {
				return 2 // tb close refused the device already, and said why
			}
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		devs = append(devs, d)
	}
	if len(devs) == 0 {
		fmt.Println("no devices to release")
		return 0
	}
	code := 0
	for _, d := range devs {
		if err := releaseDevice(ctx, d); err != nil {
			fmt.Fprintf(os.Stderr, "%s (%s): %v\n", d.Name, d.Serial, err)
			code = 1
			continue
		}
		fmt.Printf("released %s (%s)\n", d.Name, d.Serial)
	}
	return code
}

// releaseDevice stops tb's agent on d, holding d's lock meanwhile so no tb run starts on it
// halfway, and refusing a device another tb holds.
func releaseDevice(ctx context.Context, d device.Info) error {
	lock, err := devlock.TryAcquire(d.Serial, "tb release")
	if err != nil {
		if b, ok := devlock.IsBusy(err); ok {
			return fmt.Errorf("in use by %s: left alone", b.Owner)
		}
		return err
	}
	defer lock.Release()
	switch d.Platform {
	case device.Android:
		return android.ReleaseDevice(ctx, d.Serial)
	case device.IOS:
		return ios.ReleaseDevice(ctx, d.Serial)
	}
	return nil
}
