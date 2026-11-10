package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/elforama/testbuddy/internal/device"
	"github.com/elforama/testbuddy/internal/flow"
	"github.com/elforama/testbuddy/internal/workspace"
)

const replayUsage = `Usage: tb replay <flow.yaml> -d DEVICE [options]

Verifies one saved flow once on a device. The flow must begin with a required
launchApp step. That step controls whether app data is cleared. Close an open
session on the target before replaying on it.

  -d, --device DEVICE       one device serial, UDID or name (web for Chrome)
  -e, --env KEY=VALUE      set a flow variable (repeatable)
  --headless              web: run Chrome without a window
  --debug-output DIR      where to write logs and failure artifacts
  --timeline              also write timeline.html there: each step with its screens
  --json                  print the test result as JSON
`

func cmdReplay(argv []string) int {
	a, err := parseArgs(argv,
		flagSpec{[]string{"device", "d"}, true}, flagSpec{[]string{"env", "e"}, true},
		flagSpec{[]string{"headless"}, false}, flagSpec{[]string{"debug-output"}, true},
		flagSpec{[]string{"json"}, false}, flagSpec{[]string{"timeline"}, false}, flagSpec{[]string{"help", "h"}, false})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if a.flag("help") {
		fmt.Print(replayUsage)
		return 0
	}
	target := strings.TrimSpace(a.str("device"))
	if len(a.pos) != 1 || len(a.vals["device"]) != 1 || target == "" || strings.ContainsAny(target, ",=") {
		fmt.Fprintln(os.Stderr, "replay requires one flow file and one explicit --device")
		return 2
	}
	f, err := flow.ParseFile(a.pos[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if len(f.Config.Devices) != 0 {
		fmt.Fprintln(os.Stderr, "replay supports single-device flows")
		return 2
	}
	launch := f.FreshLaunch()
	if launch == nil {
		fmt.Fprintln(os.Stderr, "replay requires a leading launchApp step that is required and restarts the app")
		return 2
	}

	state := "relaunch using existing app data"
	if launch.ClearState {
		state = "clear app data and launch"
	}
	fmt.Fprintf(os.Stderr, "Replay on %s: %s\n", target, state)
	plan := &workspace.Plan{Flows: []*flow.Flow{f}}
	return runTest(argv, a, plan, true)
}

func pickReplayDevice(devs []device.Info, platform device.Platform, target string) (device.Info, error) {
	matches := 0
	for _, d := range devs {
		if d.Serial == target {
			return d, nil
		}
		if strings.EqualFold(d.Name, target) {
			matches++
		}
	}
	if matches > 1 {
		return device.Info{}, fmt.Errorf("device name %q is ambiguous; use a serial or UDID", target)
	}
	return device.Pick(devs, platform, target)
}
