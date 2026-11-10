package main

import "fmt"

// agentGuide is a portable CLI reference for agents and project instructions.
const agentGuide = `## UI testing with tb (Test Buddy)

tb drives Android emulators and devices, iOS simulators, and Chrome from the shell.
Interactive sessions stay connected between calls through a background daemon.

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
- tb save --preview [-d alice] [--json]   shows the recorded YAML and warnings without writing
- tb save flows/name.yaml [-d alice]   writes the commands that passed as a flow
- tb close -d alice               releases the device for a test run
- tb replay flows/name.yaml -d DEVICE   verifies a flow once on that device
                                  start with a required launchApp and close the session after saving
                                  use the device serial, simulator UDID or unique name, or -d web --headless

Run tests:
- tb test flows/                  run YAML flows; Maestro flows run unchanged (tb test -h)
- tb test flows/ --json           machine-readable results; failures include the screen listing
- tb test flows/ --timeline       also timeline.html per flow: each step's screens, taps and trace
- tb test flows/ --record         record each flow's screen, start to finish (in the reports and timeline)
- tb test flows/ --shard-split 2  spread over devices; multi-device flows declare devices: in their config
- tb check --lint flows/          syntax, plus fragile steps: coordinates, index selectors, accidental
                                  regex text ("Sign in?"), optional assertions, flows that assert nothing
- tb a11y [--changed]             check app code (no device) for elements tb cannot detect or tap;
                                  prints ok, or ":line Kind #id ✗ check" per element, then each check's fix once
- tb a11y --fix [--dry-run]       add the missing ids/testIDs/data-testids/testTags in place, named after
                                  what each element does; lists what it left for a person

Tips: refs come from the latest look/do output. Prefer ids (#id) and visible text in saved
flows. Exit codes: 0 ok, 1 failed, 2 bad input. On failure, read the printed screen and
"similar:" line before looking again.
`

func cmdGuide([]string) int {
	fmt.Print(agentGuide)
	return 0
}
