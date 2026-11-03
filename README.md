# Test Buddy (`tb`)

Fast, event-driven UI tests for Android, iOS and the web. `tb` runs
[Maestro](https://maestro.dev)-compatible YAML flows (your existing Maestro flows run
unchanged), drives several devices in one test, and is built for AI agents to use straight
from a shell.

```sh
tb test flows/                      # your Maestro flows, unchanged
tb look                             # the screen, one line per element, with @refs and layout
tb do 'tapOn: @5' 'inputText: hi'   # run commands; see what changed
```

## Why

- **Maestro-compatible.** Maestro's YAML format: every command, selector, condition,
  `runFlow`, `repeat`, `retry`, JavaScript (`${}`, `runScript`, `output`, `http`), env
  rules, `config.yaml`, tags, JUnit/HTML reports and the same command-line options. Maestro
  flows run unchanged, including `maestro.copiedText` / `maestro.platform` in scripts,
  `MAESTRO_*` variables and `.maestro` flow folders (tb's own names, `tb.copiedText`,
  `TB_FILENAME` and `.tb`, work too).
  Web flows (`url:`) run in Chrome, with CSS selectors and same- and cross-origin iframes.
- **Fast.** The same journey takes tb 10.7 s on Android and 15.4 s on iOS, against
  Maestro's 4–6 minutes ([Speed](#speed)).
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

## Speed

![Bench journey, whole run in seconds, lower is better. Android: tb 10.7, WebdriverIO 18.7, Maestro 371.9. iOS: tb 15.4, WebdriverIO 44.6, Maestro 281.4. Web: tb 7.6, WebdriverIO 4.8, Maestro 274.6.](docs/img/bench-times.svg)

| Platform | tb | WebdriverIO | Maestro |
|---|---|---|---|
| Android (emulator, Android 16) | **10.7 s** (10.3–11.1) | 18.7 s (17.0–25.4) | 371.9 s (one run) |
| iOS (iPhone 17 simulator) | **15.4 s** (15.4–15.5) | 44.6 s (43.6–48.1) | 281.4 s (277.5–286.1) |
| Web (headless Chrome) | 7.6 s (7.6–7.8) | **4.8 s** (4.7–5.2) | 274.6 s (264.6–308.1) |

The bench journey ([bench/](bench/README.md)): sign in through a spinner, open and edit an
item, scroll a list that loads more, and act on screens that never stop moving (a ticker, a
per-frame stopwatch). The same app is built for Android, iOS and the web. Each number is
the mean (min–max) of 5 timed runs of the whole command after a warm-up, start-up included,
on one Mac on 2026-11-02, with each tool's defaults and the same 17 s element timeout.
Versions: Maestro 2.10.0, WebdriverIO 9.32.0 (through Appium 3.9.0 on Android and iOS).

- **Maestro** waits for the screen to stop changing after each step; the bench's screens
  never do, which most likely accounts for most of its time. Its Android figure is a single
  run (each takes about 6 minutes), on the emulator used earlier that day.
- **WebdriverIO** is faster on the web because it does not wait for the page to settle: it
  clicks the moment an element is displayed, even while a screen is still sliding in. With
  a click instrument on the bench page, 7 of its 120 runs clicked the wrong element, 5 of
  them still reporting a pass. tb waits for a still screen before each tap and never tapped
  during a slide-in; the same instrument did catch a scrolling race in tb (12 of 52 runs
  tapped one row off), fixed since: 0 wrong taps in the 15 runs after the fix
  ([bench/README.md](bench/README.md#tap-reliability-web)).
  On iOS its journey needs a 1 s pause after scrolling, without which the tap only stops
  the still-bouncing list.

## How it compares

Checked on 2026-11-02 against Maestro 2.12.1 and WebdriverIO 10.1.0 (with Appium 3 for
mobile), from their docs and release notes ([sources](#sources)).

| | **tb** | Maestro | WebdriverIO |
|---|---|---|---|
| **Dependencies** | | | |
| Install | One 18 MB binary, both device agents built in | Java 17+ and the CLI (~350 MB download) | Node 22+, an `npm init wdio` project (Mocha, Jasmine or Cucumber); for mobile, Appium 3 and a driver per platform |
| Android also needs | `adb` | `adb` (Android SDK) | Android SDK (`ANDROID_HOME`), a JDK (`JAVA_HOME`), the UiAutomator2 driver |
| iOS also needs | Xcode, a booted simulator | Xcode and its command line tools | Xcode, the XCUITest driver; WebDriverAgent is built with `xcodebuild` per session by default |
| Web also needs | Chrome | Nothing (downloads its own Chromium) | Nothing for Chrome and Firefox (downloads them and their drivers); Safari's driver comes with macOS |
| Put on the device | An agent (Android: its accessibility service is on only while tb runs; iOS: an XCUITest runner) | Driver APKs (Android), an XCTest runner (iOS) | UiAutomator2 server, its test APK and Appium Settings (Android); WebDriverAgent (iOS) |
| Tests are written as | YAML flows (Maestro's format) | YAML flows | JavaScript / TypeScript |
| **Platforms** | | | |
| Android emulator / device | ✅ / ✅ | ✅ / ✅ | ✅ / ✅ |
| iOS simulator / real iPhone | ✅ / ❌ | ✅ / ❌ | ✅ / ✅ (signing and Developer Mode needed) |
| Web | Chrome | Chromium (beta) | Chrome, Firefox, Edge, Safari |
| **Multi-device** | | | |
| One test drives several devices at once | ✅ One YAML file with device roles; iOS, Android and web can mix | ❌ Not supported (open feature request); run separate processes from a script | ✅ Multiremote, in code |
| Sync between devices | ✅ `signal` / `waitForSignal`, shared `output` | ❌ | In code (one side waits for the other's result) |
| Spread a suite over devices | ✅ `--shard-split`, `--shard-all` (one work queue) | ✅ `--shard-split`, `--shard-all` | ✅ `maxInstances`, `--shard` |
| **AI agents** | | | |
| Shell session for agents (screen as text, refs, what changed) | ✅ `tb open`, `look` (one line per element, `@refs`), `do` (prints what changed), `wait`, `watch`, `save` (as a flow) | One-shot `maestro hierarchy` (JSON, or CSV with `--compact`) | ✅ `wdio session` (v10): element refs, export to a spec |
| MCP server | ✅ `tb mcp` (optional; the CLI is enough) | ✅ `maestro mcp` | ✅ `@wdio/mcp`; Appium has `appium-mcp` |
| Agent skill / plugin | ✅ Claude Code plugin with the CLI and a `tb` skill ([plugin/](plugin)); `tb agent-guide` for AGENTS.md | None official (docs publish `llms.txt`) | Agent skills (`wdio-session`) and an AGENTS.md section from `npm init wdio`; no Claude Code plugin |
| AI steps in tests | ❌ | Experimental (`assertWithAI`, ...); needs a free Maestro Cloud account | `@wdio/ai-service` (`act`, `extract`) with your own model |

### Sources

- Maestro: [install](https://docs.maestro.dev/maestro-cli/how-to-install-maestro-cli),
  [how it works](https://docs.maestro.dev/get-started/how-maestro-works),
  [web (beta)](https://docs.maestro.dev/get-started/supported-platform/web-browser),
  [real iOS devices (#686)](https://github.com/mobile-dev-inc/Maestro/issues/686),
  [multi-device (#2563)](https://github.com/mobile-dev-inc/Maestro/issues/2563),
  [sharding](https://docs.maestro.dev/maestro-flows/flow-control-and-logic/specify-and-start-devices),
  [MCP](https://docs.maestro.dev/get-started/maestro-mcp),
  [AI test analysis](https://docs.maestro.dev/maestro-flows/workspace-management/ai-test-analysis),
  [changelog](https://github.com/mobile-dev-inc/maestro/blob/main/CHANGELOG.md).
- WebdriverIO: [getting started](https://webdriver.io/docs/gettingstarted),
  [driver binaries](https://webdriver.io/docs/driverbinaries),
  [Appium service](https://webdriver.io/docs/appium-service),
  [multiremote](https://webdriver.io/docs/multiremote),
  [sessions](https://webdriver.io/docs/session), [MCP](https://webdriver.io/docs/mcp),
  [AI agents](https://webdriver.io/docs/ai-agents), [AI service](https://webdriver.io/docs/ai-service),
  [v10 release](https://webdriver.io/blog/2026/10/05/webdriverio-v10-release).
- Appium: [requirements](https://appium.io/docs/en/latest/quickstart/requirements/),
  [UiAutomator2 setup](https://appium.io/docs/en/latest/quickstart/uiauto2-driver/),
  [XCUITest requirements](https://appium.github.io/appium-xcuitest-driver/latest/getting-started/system-requirements/),
  [appium-mcp](https://github.com/appium/appium-mcp).

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
the screen listing and the hierarchy in the debug folder. Every flow also gets a `log.txt`
there: what tb did, step by step, with times (what each lookup found, where each tap went
and whether the screen changed, every wait and what it waited for). `TB_DEBUG=1` prints it
as the run goes.

On Android, `tb test` turns system animations off for the run and restores them after
(`--keep-animations` to leave them). Web: `--headless` and `--screen-size WxH`.

## Check app code for tb (`tb a11y`)

tb finds elements through what the app exposes for accessibility. `tb a11y` reads app
source, with no device, and lists the elements tb could not detect or tap, one per line in
`tb look`'s notation with the check each one fails. Each failed check is explained once at
the end, with its fix. A new screen can be checked this way before it is merged:

```sh
tb a11y                       # the current folder
tb a11y app/src --changed     # only lines added since the merge base with main (for PRs)
tb a11y --format github       # GitHub Actions annotations on the changed lines
tb a11y --rules               # every check
```

```
app/src/main/res/layout/home.xml
  :42 ImageButton ✗ android-no-selector
src/Login.tsx
  :4 Pressable ✗ rn-no-selector
  :7 Text #row_title ! rn-nested-testid

✗ android-no-selector: image button or clickable image with no id or contentDescription: tb can tap it only by position. Fix: add android:id="@+id/…" (tb's id: selector) or android:contentDescription
! rn-nested-testid: testID inside a touchable: iOS reports the touchable as one element, so tb cannot see this testID on iOS. Fix: move the testID to the touchable, or set accessible={false} on the touchable
✗ rn-no-selector: touchable with no testID, accessibilityLabel or text inside: tb can tap it only by position. Fix: add testID="…" (tb's id: selector) or accessibilityLabel="…"
✗ 2 elements tb cannot detect or tap, ! 1 to check (118 files checked)
```

Or, when nothing is wrong: `ok: 118 files checked, every element tb can detect and tap`.
`✗` fails the check (exit 1); `!` is a likely problem, which fails only with
`--fail-on warning`. It reads Android layouts, Jetpack Compose (including `testTag`s that tb
cannot see without `testTagsAsResourceId = true`), SwiftUI/UIKit, React Native, Flutter and
web code (HTML, JSX/TSX, Vue, Svelte). A `tb-a11y-ignore` comment on a line (or the one
before) silences it; `tb-a11y-ignore: rn-nested-testid` silences one check. `--json` has the
same shape: findings grouped by file, and each check explained once.

In CI (GitHub Actions; `fetch-depth: 0` so the merge base is there):

```yaml
- uses: actions/checkout@v4
  with: { fetch-depth: 0 }
- run: tb a11y --changed --base origin/${{ github.base_ref }} --format github
```

The checks read source text, not a running app: they catch the common causes, not every
one. `tb look` on a device is the final word.

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

- One tb at a time per phone or simulator: a `tb test` run, or an open `tb look`/`do`
  session, locks the device, so agents sharing a machine cannot drive the same device at once.
  A second runner is refused straight away with who holds it (or waits, with
  `--wait-for-device`); with several devices, a run uses a free one. Locks go with their
  process, so a crashed run never leaves a device stuck.

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
| `internal/legacy` | Maestro's names (`maestro` object, `MAESTRO_*`, `.maestro`), accepted as aliases |
| `internal/orchestrate` | Device pool, work queue, multi-device flows |
| `internal/look` | Compact screen listing, refs, diffs |
| `internal/a11y` | `tb a11y`: static checks of app code for UI tb cannot detect or tap |
| `internal/session`, `internal/daemon` | The background daemon agents talk to |
| `internal/driver/android`, `agents/android` | Android driver and on-device agent (Kotlin) |
| `internal/driver/ios`, `agents/ios` | iOS driver and XCUITest agent (Swift) |
| `internal/driver/web` | Chrome driver (DevTools protocol) and its in-page script |
| `bench/` | Buddy Bench: the same test app for Android, iOS and web, its flows, and the benchmark |
| `docs/` | Architecture, protocol, multi-device syntax, research notes |

## License

Copyright 2026 Jonathan Muller. Licensed under the [Apache License 2.0](LICENSE).
