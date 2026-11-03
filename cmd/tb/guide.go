package main

import "fmt"

// agentGuide is what an AI agent needs to drive devices with tb. `tb agent-guide` prints it
// so it can go straight into AGENTS.md / CLAUDE.md; no MCP setup needed.
const agentGuide = `## Mobile UI testing with tb (Test Buddy)

tb drives Android emulators/devices and iOS simulators from the shell. Devices stay
connected between calls (a background daemon), so each call takes milliseconds.

Explore an app:
- tb devices                      list devices
- tb open --name alice [-d DEVICE] [--app APP_ID]   optional; names a device for multi-device work
- tb look [-d alice]              screen as one line per element: @ref Kind "label" #id state;
                                  indented under "row:", "grid 3 cols:", "column scrollable:" etc.
                                  where layout matters (unindented lines stack top to bottom);
                                  scroll areas say what is out of view: "(12 more below)";
                                  *@ref = new since your last look/do. --json: the same as JSON
- tb do [-d alice] 'tapOn: @5' 'inputText: "hi"' 'pressKey: Enter'
                                  runs flow commands (refs allowed); prints ok/FAILED per command,
                                  then what changed: + new, ~ changed, - gone (or the whole new screen)
- tb wait [-d alice] "Welcome" [--gone] [--timeout 10000]   waits on device events, no polling
- tb watch [-d alice] --until "Done"   streams screen changes as they happen
- tb look --shot                  adds a small screenshot path; use only when the listing is not enough
- tb save flows/name.yaml [-d alice]   writes the commands that passed as a flow

Run tests:
- tb test flows/                  run YAML flows; Maestro flows run unchanged (tb test -h)
- tb test flows/ --json           machine-readable results; failures include the screen listing
- tb test flows/ --shard-split 2  spread over devices; multi-device flows declare devices: in their config
- tb a11y [--changed]             check app code (no device) for elements tb cannot detect or tap;
                                  prints ok, or ":line Kind #id ✗ check" per element, then each check's fix once

Tips: refs come from the latest look/do output. Prefer ids (#id) and visible text in saved
flows. Exit codes: 0 ok, 1 failed, 2 bad input. On failure, read the printed screen and
"similar:" line before looking again.
`

func cmdGuide([]string) int {
	fmt.Print(agentGuide)
	return 0
}
