# Test Buddy (`tb`)

Fast, event-driven UI tests for Android, iOS and the web. `tb` runs YAML UI flows (the
common `appId` / `---` / `tapOn`, `assertVisible`, `runFlow` format), drives several devices
in one test, and is built for AI agents to use straight from a shell.

```sh
tb test flows/                      # your YAML flows, unchanged
tb look                             # the screen, one line per element, with @refs
tb do 'tapOn: @5' 'inputText: hi'   # run commands; see what changed
```

## Why

- **Your flows as they are.** Every command, selector, condition, `runFlow`, `repeat`,
  `retry`, JavaScript (`${}`, `runScript`, `output`, `http`), env rules, `config.yaml`,
  tags, JUnit/HTML reports and the usual command-line options. Flows written for other
  runners keep working: tb also accepts their names for the script object, the injected
  variables and the flows folder (`internal/legacy`).
  Web flows (`url:`) run in Chrome, with CSS selectors and same- and cross-origin iframes.
- **Fast.** A 15-step Android Settings flow, one emulator: 6–9 s. The bench journey (sign
  in, lists, scrolling, never-still screens): Android 10.7 s, iOS 15.4 s, web 7.6 s
  ([bench/README.md](bench/README.md)).
- **No polling.** The on-device agents (and an in-page script on the web) push "the UI
  changed" and "the UI settled" events. `tb` waits on those; a screen is only read again
  after the device says it changed. Screens that never stop moving (spinners, tickers,
  stopwatches) still settle: an element that keeps changing is marked restless and stops
  holding the screen up. (Details and the few timers that remain:
  [docs/architecture.md](docs/architecture.md).)
- **Many devices.** Spread a suite over devices with a work queue, or write one flow that
  drives an iPhone and an Android phone together ([docs/multi-device.md](docs/multi-device.md)).
- **Agent first.** A CLI, not an MCP server you must set up (MCP is there if you want it).
  Screens are a few hundred tokens, every command answers with what changed, and the
  commands that worked save as a flow.

## Install

Build from source (Go 1.27+, Android SDK for the Android agent, Xcode for the iOS agent):

```sh
scripts/build.sh          # builds both agents, then bin/tb (one file, agents embedded)
```

Devices: start an Android emulator (or plug in a phone with USB debugging), boot an iOS
simulator, or have Google Chrome installed for web flows. `tb devices` lists them. Each
flow goes to a device that has its app (a `url:` flow goes to Chrome).

## Run tests

```sh
tb test flows/login.yaml
tb test flows/ -e PASSWORD=secret --include-tags smoke
tb test flows/ --format junit --output report.xml
tb test flows/ --shard-split 3            # three devices pull flows from one queue
tb test flows/ --retries 1                # re-run a failed flow, on another device if one is free
tb test flows/ --json                     # machine-readable results
```

`tb test` takes the usual options (`-e`, `-p`, `--device`, `--config`, `--include-tags`,
`--exclude-tags`, `--shard-split`, `--shard-all`, `--format`, `--output`,
`--test-suite-name`, `--debug-output`, `--flatten-debug-output`). When a step fails it prints
the screen as it was, with elements similar to what it looked for, and saves a screenshot,
the screen listing and the hierarchy in the debug folder.

On Android, `tb test` turns system animations off for the run and restores them after
(`--keep-animations` to leave them). Web: `--headless` and `--screen-size WxH`.

## Drive a device (agents and people)

```sh
tb open --name alice --app com.example.app    # optional: name a device
tb look                                       # what is on screen
tb do 'tapOn: @7' 'inputText: "user@example.com"' 'tapOn: "Next"'
tb wait "Welcome"                             # waits on device events, not a loop
tb watch --until "Done"                       # streams changes as they happen
tb look --shot                                # adds a small screenshot
tb save flows/sign-in.yaml                    # the commands that passed, as a flow
```

```
android 1080x2400 com.android.settings, 11 elements
@1 Button "Search Settings" #search_action_bar
@3 Button "Network & internet, Mobile, Wi‑Fi, hotspot"
@16 Switch #switchWidget unchecked
...
```

`tb do` prints a line per command (`ok` / `FAILED`, with timing), then what changed:
`+` new elements, `~` changed, `-` gone, or the whole new screen when most of it is new.
A background daemon keeps devices connected, so these calls take milliseconds. With several
devices, add `-d alice`. Exit status: 0 ok, 1 failed, 2 bad input.

For an agent's instructions file: `tb agent-guide >> AGENTS.md`.
For MCP clients: `tb mcp` (tools: `devices`, `open`, `look`, `run`, `wait`, `save_flow`, `close`).

## Claude Code plugin

`plugin/` is a Claude Code plugin: the `tb` CLI (macOS, Apple silicon, both device agents
built in, on Claude's PATH while the plugin is on) and a `tb` skill with the commands, flow
format, gotchas and debugging steps. This repository is its marketplace:

```sh
claude plugin marketplace add "/path/to/Test Buddy"
claude plugin install test-buddy@test-buddy
```

Or try it for one session: `claude --plugin-dir plugin`. After changing tb, refresh the
bundled binary with `scripts/build-plugin.sh` (it also validates the plugin).

## Multi-device flows

```yaml
name: Alice messages Bob
devices:
  alice: { platform: ios, appId: com.example.chat }
  bob: { platform: android, appId: com.example.chat }
---
- parallel:
    alice: [launchApp, { runFlow: login.yaml }]
    bob: [launchApp, { runFlow: login.yaml }]
- alice:
    - tapOn: "New message"
    - inputText: "Hi Bob"
    - tapOn: "Send"
    - signal: sent
- bob:
    - waitForSignal: sent
    - assertVisible: "Hi Bob"
```

The full syntax: [docs/multi-device.md](docs/multi-device.md).

## Behaviour worth knowing

- Steps start as soon as the app settles (events), not after screenshot or hierarchy
  comparisons, so flows run faster. A flow that relied on a slow runner to give a slow
  backend time may need an `extendedWaitUntil`.
- `assertNotVisible` passes once the element is absent on a settled screen, instead of
  after a fixed 500 ms absence.
- Android animations are off during `tb test` runs.
- On Android, an accessibility service (part of the agent) is enabled while the agent runs:
  Jetpack Compose reports UI changes only when one is. It is removed when the agent exits.
- AI commands (`assertWithAI`, ...) are not supported.
- Web: the browser runs with its password manager and translate bar off (they put UI over
  the page). `swipe`/`scroll` scroll what is under the start point with a mouse wheel event;
  `scrollUntilVisible` scrolls that element directly.
- Taps wait for a still screen, steer clear of covers (sticky bars, the keyboard) on Android
  and iOS, give disabled buttons up to 3 s, and system dialogs (Android "isn't responding",
  the iOS notifications alert) are answered ([docs/architecture.md](docs/architecture.md)).

## Layout

| Path | What |
|---|---|
| `cmd/tb` | The CLI: test runner, agent commands, MCP server |
| `internal/flow` | Flow YAML parser (+ multi-device extension) |
| `internal/engine` | Runs commands; event-driven element waits |
| `internal/match` | Selector semantics |
| `internal/js` | Flow JavaScript (goja) |
| `internal/legacy` | Names older flows use, accepted as aliases |
| `internal/orchestrate` | Device pool, work queue, multi-device flows |
| `internal/look` | Compact screen listing, refs, diffs |
| `internal/session`, `internal/daemon` | The background daemon agents talk to |
| `internal/driver/android`, `agents/android` | Android driver and on-device agent (Kotlin) |
| `internal/driver/ios`, `agents/ios` | iOS driver and XCUITest agent (Swift) |
| `internal/driver/web` | Chrome driver (DevTools protocol) and its in-page script |
| `bench/` | Buddy Bench: the same test app for Android, iOS and web, its flows, and the benchmark |
| `docs/` | Architecture, protocol, multi-device syntax, research notes |
