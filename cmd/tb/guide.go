package main

import "fmt"

const agentGuide = `## UI testing with tb (Test Buddy)

tb drives Android emulators and devices, iOS simulators, and Chrome from the shell.
Interactive sessions stay connected between calls through a background daemon.

Set up:
- tb doctor [--json]              what is ready and what to fix: "ok ..." or "problem ...: <fix>" per
                                  check (adb, devices, Xcode, simulators, Chrome, the daemon); changes no device
- tb init [--dry-run]             registers tb's MCP server with Claude Code, Codex and Gemini CLI
                                  where installed; --dry-run prints the commands and changes nothing

Explore an app:
- tb devices                      list devices: free, in use (by whom), or your session
- tb open -p android|ios|web [--name alice] [--app APP_ID]   or -d DEVICE instead of -p; --name for multi-device work
- tb look [-d alice]              screen as one line per element: @ref Kind "label" #id state;
                                  indented under "row:", "grid 3 cols:", "column scrollable:" etc.
                                  where layout matters (unindented lines stack top to bottom);
                                  scroll areas say what is out of view: "(12 more below)";
                                  "partly off-screen" / "covered by keyboard|modal|@N": scroll
                                  or close that first; *@ref = new since your last look/do.
                                  A Slider or Progress shows its reading as value=, not as its
                                  label. --json: the same as JSON, plus covered, offscreen,
                                  scroll (vertical/horizontal) and tappable per element
- tb do [-d alice] 'tapOn: @5' 'inputText: "hi"' 'pressKey: Enter'
                                  runs flow commands (@ref, or 'tapOn: #id' as look prints it); prints ok/FAILED per command,
                                  then what changed: + new, ~ changed, - gone (or the whole new screen)
                                  a FAILED step also says if the app crashed, "crashed: <app>:
                                  <exception and the frame that threw it>", and prints the app's
                                  last error log lines ("log: ..."): an app bug, not a test bug
- tb wait [-d alice] "Welcome" [--gone] [--timeout 10000]   waits on device events, no polling
- tb applog [-d alice] [--since 60]   the app's error log on demand (Android, iOS simulator):
                                  the crash with its stack, then the error lines
- tb locate [-d alice] [--json] '{text: "Edit"}'   every match of a selector and the one tb picks
- tb watch [-d alice] --until "Done"   streams screen changes as they happen
- tb look --shot                  adds a small screenshot path; use only when the listing is not enough
- tb save --preview [-d alice] [--json]   shows the recorded YAML and warnings without writing
- tb save flows/name.yaml [-d alice]   writes the commands that passed as a flow
- TB_SESSION_OWNER=agent-1        makes your sessions yours: other agents are refused on them
- TB_DEVICE=emulator-5554        pins an agent to its device: the session commands' default -d
  (not with -p; with several devices connected and none named, tb asks which)
- tb close -d alice               releases the device (tb test/replay take it from your own idle session)
                                  (Ctrl-C that command, or tb daemon stop, to end a stuck one)
- tb replay flows/name.yaml -d DEVICE   verifies a flow once on that device
                                  start with a required launchApp and close the session after saving
                                  use the device serial, simulator UDID or unique name, or -d web --headless

Run tests:
- tb test flows/ -p android       run YAML flows; Maestro flows run unchanged (tb test -h); -p or a device is required
- tb test flows/ -p ios --json    machine-readable results: each flow's status, seconds and error; a
                                  failed flow's debugDir (log.txt: what tb did; screen.txt: the screen;
                                  app.log: a "crashed:" line if the app crashed, and its error log)
- tb test flows/ -p ios --timeline also timeline.html per flow: each step's screens, taps and trace
- tb test flows/ -p ios --record  record each flow's screen, start to finish (in the reports and timeline)
- tb replay f.yaml -d D --record --annotate  a video a person can follow (for a PR): captions, touches, no rush
- tb mark "event {k: v}"  while a --annotate run records: show the event in the video (the app can POST $TB_EVENTS_URL)
- tb test flows/ -p android --shard-split 2  spread over devices; multi-device flows declare devices: in their config
- tb check --lint flows/          syntax, plus fragile steps: coordinates, index selectors, accidental
                                  regex text ("Sign in?"), optional assertions, flows that assert nothing
- tb a11y [--changed]             check app code (no device) for elements tb cannot detect or tap;
                                  prints ok, or ":line Kind #id ✗ check" per element, then each check's fix once
- tb a11y --fix [--dry-run]       add the missing ids/testIDs/data-testids/testTags in place, named after
                                  what each element does; lists what it left for a person

Write flows: assert each action's outcome (assertVisible: {id: status, text: "Saved"}), and assert
the new screen before acting on it. Lookups already wait 17 s: extendedWaitUntil only for longer,
no sleeps. Selectors: id > id+text > exact text (a full-match regex) > relative > index.

Tips: refs come from the latest look/do output. Prefer ids (#id) and visible text in saved
flows. Exit codes: 0 ok, 1 failed, 2 bad input. On a failed do/wait, read the error and the
"similar:" line before looking again: it lists elements close to the text (or, without text,
the id) that matched nothing, one typo in a longer word included, each with its @ref. With --json the failed step has a
"reason": not_found, screen_changed (look again), assertion_failed or other.
`

func cmdGuide([]string) int {
	fmt.Print(agentGuide)
	return 0
}
