# Test Buddy (`tb`)

Fast, event-driven UI tests for Android, iOS and the web. `tb` runs
[Maestro](https://maestro.dev)-compatible YAML flows (your existing Maestro flows run
unchanged), drives several devices in one test, and is built for AI agents to use straight
from a shell.

## Install

**Download.** One binary with the Android and iOS agents built in, no Go needed. Get the
archive for your system from the [latest release](https://github.com/Elforama/testbuddy/releases/latest)
(macOS, Windows and Linux; `checksums.txt` has their SHA-256 sums).

macOS (Apple silicon; on an Intel Mac use `tb_darwin_amd64`):

```sh
curl -fsSL https://github.com/Elforama/testbuddy/releases/latest/download/tb_darwin_arm64.tar.gz | tar -xz
sudo mkdir -p /usr/local/bin && sudo mv tb_darwin_arm64/tb /usr/local/bin/
tb version
```

The binary is not signed by Apple. Fetched with `curl` as above it runs as is. If you
download the archive in a browser instead, macOS refuses to open `tb` ("Apple could not
verify..."); clear the download's quarantine flag once:

```sh
xattr -d com.apple.quarantine /usr/local/bin/tb
```

Windows 10 or later, in PowerShell (on an Arm PC use `tb_windows_arm64`):

```powershell
Invoke-WebRequest https://github.com/Elforama/testbuddy/releases/latest/download/tb_windows_amd64.zip -OutFile tb.zip
Expand-Archive tb.zip -DestinationPath "$env:LOCALAPPDATA\Programs" -Force
$dir = "$env:LOCALAPPDATA\Programs\tb_windows_amd64"
[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";$dir", "User")
```

Open a new terminal, then `tb version`. If you downloaded the zip in a browser and Windows
blocks `tb.exe`, run `Unblock-File "$dir\tb.exe"`. On Windows, tb runs Android and web
flows; iOS needs macOS and Xcode.

Linux: the same as macOS with `tb_linux_amd64` or `tb_linux_arm64`.

**Go.** With Go 1.27+:

```sh
go install github.com/elforama/testbuddy/cmd/tb@latest
tb version
```

`@latest` is the newest release; `@main` builds the newest commit. `tb` lands in
`$(go env GOPATH)/bin`, so put that on your PATH. Keep the module path lowercase
(`github.com/Elforama/...` fails). To build from a clone instead, see
[Build from source](#build-from-source).

**Claude Code plugin.** The `tb` CLI plus a `tb` skill for Claude. The bundled `tb` runs on
Apple silicon Macs only; elsewhere, install the CLI from a download or with `go install`
above.

```sh
claude plugin marketplace add Elforama/testbuddy
claude plugin install test-buddy@test-buddy
```

Or from inside Claude Code: `/plugin marketplace add Elforama/testbuddy`, then
`/plugin install test-buddy@test-buddy`.

**Devices.** Start an Android emulator (or plug in a phone with USB debugging), boot an iOS
simulator, or have Google Chrome installed for web flows. With no phone running, `tb test`
starts one of the platform asked for: with `-p android` the first Android emulator (AVD) it
finds, with `-p ios` (on a Mac) a shut-down iPhone simulator of the newest iOS. It says which, waits for the boot, and leaves
it running (not on CI, where devices are set up on purpose). `tb test` and `tb open` need the
platform: `-p android`, `ios` or `web`, unless a device is named (`-d`, `--device`, `TB_DEVICE`)
or the flows name it (`url:` for the web, `devices:` roles); `tb open --app` with a URL is web.

## Getting started

With `tb` on PATH, two commands set a machine up:

```sh
tb doctor            # what is ready, and the fix for what is not
tb init              # offer tb to the coding agents installed here, then run the doctor checks
```

`tb doctor` boots, stops and changes no device, browser or daemon. It does run
`adb devices`, which starts adb's own server when none is running. It prints one line per
check, `ok <what>` or `problem <what>: <the fix>`, then the platforms that are ready:

```
ok adb /Users/me/Library/Android/sdk/platform-tools/adb
problem android R58M unauthorized: unlock it and accept its "Allow USB debugging" prompt (replug the cable if none shows)
ok xcode /Applications/Xcode.app/Contents/Developer
problem ios simulators none booted: boot one (xcrun simctl boot "<name>"; xcrun simctl list devices names them), or let tb test -p ios boot one
ok chrome /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
ok daemon not running (tb starts it when needed)
ready: web; fix: android R58M
```

It exits 0 when at least one platform is usable and nothing is broken. A platform that is
not set up (no adb, no booted simulator, no Chrome) is a problem line with its fix, but only
something broken fails the check: a device adb lists as `unauthorized` or `offline`, or an
`adb devices` that fails. A daemon left running by another tb build is an `ok` line: the
next tb command replaces it (and closes its sessions). A Mac with only the command line
tools has no simulators: `problem xcode not installed`. Off a Mac, iOS is an
`ok ios needs a Mac` line. `--json` gives the same as one object (`ok`, `usable`,
`checks`).

`tb init` registers tb's MCP server (`tb mcp`) with each coding agent it finds on PATH,
through that client's own command, and writes no file itself:

| Client | Command `tb init` runs | Skipped when |
|---|---|---|
| Claude Code | `claude mcp add --scope user tb -- <tb> mcp` | `~/.claude.json` has an MCP server `tb`, or the test-buddy plugin is installed |
| Codex | `codex mcp add tb -- <tb> mcp` | `~/.codex/config.toml` has `[mcp_servers.tb]` |
| Gemini CLI | `gemini mcp add --scope user tb <tb> mcp` | `~/.gemini/settings.json` has an MCP server `tb` |

`<tb>` is the `tb` on PATH when that is the one that ran, else the full path of the one
that ran; a temporary build (`go run ./cmd/tb init`) is refused, since agents could not
start it later. It prints one line per client (`added`,
`skipped` with the reason, or `failed` with what the client said). `tb init --dry-run`
prints the commands instead of running them. For Cursor, GitHub Copilot and other clients,
set tb up by hand: see [Use with AI coding agents](#use-with-ai-coding-agents).

## Quick start

```sh
tb devices                          # what tb can drive, and which are free or in use (by whom)
tb test login.yaml -p android       # run one flow on Android (-p or a device is required)
tb test flows/ -p ios               # every flow in a folder, on an iOS simulator
tb test flows/ -p web --headless    # web (url:) flows in headless Chrome
tb check flows/                     # check flow syntax, no device needed
tb check --lint flows/              # also list fragile steps (coordinates, indexes, ...)
```

A flow is Maestro YAML (existing Maestro flows run unchanged):

```yaml
appId: com.android.settings
---
- launchApp
- tapOn: "Network & internet"
- assertVisible: "Internet"
```

Drive a device by hand, or let an agent do it:

```sh
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
- **Fast.** The same journey takes tb 12.3 s on Android and 18.1 s on iOS, against
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

![Bench journey, whole run in seconds, lower is better. Android: tb 12.3, WebdriverIO 24.6, Maestro 359.4. iOS: tb 18.1, WebdriverIO 43.3, Maestro 295.4. Web: tb 7.4, WebdriverIO 4.4, Maestro 236.3.](docs/img/bench-times.svg)

| Platform | tb | WebdriverIO | Maestro |
|---|---|---|---|
| Android (emulator, Android 16) | **12.3 s** (11.9–12.5) | 24.6 s (24.2–25.0) | 359.4 s (347.3–369.8) |
| iOS (iPhone 17 simulator) | **18.1 s** (17.8–18.5) | 43.3 s (43.1–43.5) | 295.4 s (293.4–297.2) |
| Web (headless Chrome) | 7.4 s (7.3–7.5) | **4.4 s** (4.2–4.5) | 236.3 s (231.6–239.8) |

The bench journey ([bench/](bench/README.md)): sign in through a spinner, open and edit an
item, scroll a list that loads more, and act on screens that never stop moving (a ticker, a
per-frame stopwatch). The same app is built for Android, iOS and the web. Each number is
the mean (min–max) of 5 timed runs of the whole command after a warm-up, start-up included,
one run at a time on one Mac on 2026-11-26, with each tool's defaults and the same 17 s
element timeout. Versions: tb 0.7.0, Maestro 2.12.1, WebdriverIO 10.2.0 (through Appium
4.0.1 on Android and iOS). The Mac also had other emulators and simulators running, so
every tool's times are a little higher than on a quiet machine.

- **Maestro** waits for the screen to stop changing after each step; the bench's screens
  never do, which most likely accounts for most of its time.
- **WebdriverIO** is faster on the web because it does not wait for the page to settle: it
  clicks the moment an element is displayed, even while a screen is still sliding in. With
  a click instrument on the bench page, 7 of its 120 runs clicked the wrong element, 5 of
  them still reporting a pass. tb waits for a still screen before each tap and never tapped
  during a slide-in; the same instrument did catch a scrolling race in tb (12 of 52 runs
  tapped one row off), fixed since: 0 wrong taps in the 15 runs after the fix
  ([bench/README.md](bench/README.md#tap-reliability-web)).
  On Android and iOS its journey needs a 1 s pause after scrolling, without which the tap
  only stops the still-moving list: without the pause, 4 of 5 Android runs failed (on
  WebdriverIO 9.32.0).

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

## Build from source

Go 1.27+, plus the Android SDK to rebuild the Android agent and Xcode for the iOS agent:

```sh
scripts/build.sh               # builds both agents, then bin/tb (one file, agents embedded)
scripts/build.sh --skip-agents # bin/tb with the agents already in the source tree
```

To release, set the new version in `cmd/tb/main.go` and `plugin/.claude-plugin/plugin.json`,
merge, then push a tag (`git tag v0.4.0 && git push origin v0.4.0`), or run the Release
workflow from the Actions tab on `main` (Run workflow), which tags `v<version>` itself. Only
the repository's owner can start that run; the workflow stops anyone else's. The Release
workflow builds tb for macOS, Windows and Linux, runs each binary on its own system, and
publishes the archives on the release.

## Run tests

```sh
tb test flows/login.yaml -p android
tb test flows/login.yaml -d emulator-5554   # a named device needs no -p
tb test flows/ -p android -e PASSWORD=secret --include-tags smoke
tb test flows/ -p android --format junit --output report.xml
tb test flows/ -p android --shard-split 3 # three devices pull flows from one queue
tb test flows/ -p android --retries 1     # re-run a failed flow, on another device if one is free
tb test flows/login.yaml -p android --from 12  # start at step 12, on the screen the device is on now
tb test flows/ -p android --json          # machine-readable results
tb test flows/ -p android --timeline      # also an HTML timeline of each flow: steps, screens, taps
tb test flows/ -p android --record        # record each flow's screen, start to finish, into the reports
tb replay flows/f.yaml -d DEV --record --annotate  # a video a person can follow, to attach to a PR
tb check --lint flows/                    # syntax, plus steps that run but break easily
```

Each flow goes to a device that has its app (a `url:` flow goes to Chrome).

`tb test` takes the usual options (`-e`, `-p`, `--device`, `--config`, `--include-tags`,
`--exclude-tags`, `--shard-split`, `--shard-all`, `--format`, `--output`,
`--test-suite-name`, `--debug-output`, `--flatten-debug-output`). When a step fails it prints
the screen as it was, with elements similar to what it looked for, and saves a screenshot,
the screen listing and the hierarchy in the debug folder. Every flow also gets a `log.txt`
there: what tb did, step by step, with times (what each lookup found, where each tap went
and whether the screen changed, every wait and what it waited for). `TB_DEBUG=1` prints it
as the run goes. Each flow has a debug folder of its own, named after the flow; flows of a
run that share a name add their directory (`smoke-login`, `smoke-signup`), a retry adds
`-retry-N`.

`tb check --lint flows/` also lists steps that run but break easily: taps and swipes by
coordinates, selectors that rely on match order (`index`), text read as a regex that
matches more than written (`"Sign in?"` also matches "Sign in"; an id like that never
matches; escape it: `'Sign in\?'`), optional assertions that can never fail, flows that
assert nothing, header keys tb does not read (a mistyped `tgas:` keeps the flow out of
`--include-tags` runs; the warning names the key it is closest to: "did you mean tags?"),
and keys written twice in one map (YAML keeps the last). Warnings do not fail the check unless you pass `--fail-on warning`; `--json`
gives each flow with its error or warnings. `tb save --preview` warns about the same things.

`--timeline` also writes `timeline.html` in each flow's debug folder: one page per flow
with every step, nested as it ran, the screen before and after it with each tap and swipe
drawn where it went, its error, and its trace lines; a bar shows where the time went, and a
link jumps to the failed step. Screenshots are embedded, so the page can be shared as one
file. While the flow runs they wait in a temp file, not in memory, and are copied into the
page one at a time; the temp file is removed when the flow ends (if it cannot be written,
they stay in memory and the flow's log says so). Each screenshot waits for the screen to settle (at most a second) and is never taken
between finding a target and acting on it, so a timeline makes a run slower (the bench
journey on Android: 24 s instead of 21 s), not different. `tb replay --timeline` writes one
too.

`--record` records each flow's screen from its start to its end, as `recording.mp4` in its
debug folder (`recording-<role>.mp4` per device in a multi-device flow), and puts it in every
report: the console and `--json` (`videos`) give its path, the HTML report plays it, JUnit
lists it as a `video` property and as a `[[ATTACHMENT|path]]` line (CI systems such as
Jenkins and GitLab show it with the test case), and the timeline plays it, with a "▶ Video"
link on each step that jumps to that moment. Android uses `screenrecord` (in 3-minute parts
before Android 14, `recording-part2.mp4`, ...), iOS `simctl io recordVideo`, and the web
Chrome's screencast encoded with `ffmpeg` (on PATH, or `TB_FFMPEG`). A flow's own
`startRecording` takes the device for its stretch; the run's recording stops before it and
starts again after, so the files cover the whole run, in order. A device that cannot record
says why in the log and the flow goes on. A recording whose stop failed is still listed when
the device saved something, and may be incomplete. Without `--record`, nothing is recorded.
`tb replay --record` records too.

`--annotate` (with `--record` only) makes a video a person can follow, one to attach to a pull
request: tb otherwise acts as soon as the screen lets it, too fast to see what each step did.
A step that taps or swipes is held until a second has passed since its touch, so its result
shows under its own caption: on a slow device most of a step is the wait for its target, and
the touch comes at its end. Another step that acts on the app (typing, a launch, back, ...)
and took less than a second is held until it has lasted a second; one that took longer is
not lengthened. `--annotate=TIME` (or `--annotate TIME`) sets that time: `0.5s`, `500ms`, `3s`,
or seconds, `2`, up to a minute; `--annotate=0` holds nothing and only draws the captions and
touches. Checks, scripts and failed steps are not held. Once the recording stops, tb draws
on it a ring where each tap, double tap and long press landed, moving along each swipe, and
adds a dark band below the recording that names the step that was running (the text is
also written beside the video as `recording.srt`). The band is extra height, so the text
never covers the app. The devices cannot show tb's own touches (iOS and Chrome have no
setting for it, and Android's "Show taps" is a dot too small and brief for a video), so tb
draws them, and the band's text, itself, and `ffmpeg` (on PATH, or `TB_FFMPEG`) only places
them on the video, the same on every platform: no subtitle support is needed from ffmpeg.
The video comes out at a steady 30 frames a second (Android records only when the screen
changes). Without ffmpeg the video is kept as recorded and the log says why.

With `--annotate`, the videos can also show events from outside the flow: an app's tracking
calls, a backend's webhooks, a script's notes, to check that the right event fired at the
right moment. While the run lasts tb takes them at `POST http://127.0.0.1:7357/event`
(`--events-port N` for another port, `0` for none) and stamps each as it arrives, by the same
clock as the captions, so the sender needs no clock and nothing to do with the video:

```bash
tb test flows/checkout.yaml -p android --record --annotate  # prints the events URL
tb mark "purchase_completed {sku: 42}"                 # from another shell or a script
curl --data-binary '{"event":"purchase","properties":{"sku":42}}' http://127.0.0.1:7357/event
```

A debug build can forward its tracking there as it is (a text body, a line an event, or JSON:
`"text"`, a list of events, or `{"event" or "name", "properties", "props" or "params"}`). An
Android device reaches the endpoint on its own `localhost` (tb sets up `adb reverse`), a
simulator and a web page (CORS is allowed) on the computer's; the URL is in
`$TB_EVENTS_URL`, and in the flows' `${TB_EVENTS_URL}` to pass to the app at launch. An event
can name its `device` (serial, name or role; or `?device=`, or `tb mark -d`); one that names
none goes to every device recording at the time. An event sent after its flow began but
before the video started, or between two flows on a device, shows at the video's first frame
with its time before 0; when the run ends, tb says how many events are in no video. Each video shows its events in a band below
the step's text, one row per event with its time, the newest highlighted, each up for 2 s:
a burst gets a row per event, as many as the run's busiest moment needs (up to 6). A bigger
burst holds the video's frame, marked "paused for events", until each event has been up a
second, so none goes by unread; the video then runs longer than the run, and the rows keep
the real times. Events near the end hold the last frame, marked "paused for events", until each has been up
its 2 s. Text in scripts the built-in font lacks (CJK, Arabic, Hebrew, symbols) is drawn with a font
of the computer's that has it, or as `[U+8D2D]` if none does; emoji usually come out that way,
since colour emoji fonts have no outlines to draw.
A held video has no sound (screen recordings rarely have any). The events are
also written beside the video as `recording.events.json`.

A few things to know when sending from an app: an Android app's debug build must allow
cleartext HTTP to `127.0.0.1` (a `network_security_config` domain entry), and `adb reverse`
replaces any reverse you had on the same port for the run; a physical iPhone cannot reach the
computer's `127.0.0.1` (a simulator can); a text body sent with `curl` needs `--data-binary`
(`-d` joins its lines into one event). `-e TB_EVENTS_URL=...` gives the flows another URL (a
proxy in front of tb, say). The endpoint takes events from any page open on the computer
during the run, so it is for test runs on your own machine.

The captions and touches are kept in sync with the video in a few ways. An Android video says itself when each frame showed (`screenrecord` stamps every frame), so tb reads when its first frame showed, measures the device's clock against its own, and times everything from there ("recording.mp4 runs 640 ms ahead of tb's clock (its frame times say so)"); the rest of this paragraph is for when a video does not say. Each driver says when
its video actually began (the first frame tb took, on the web; when `screenrecord` said it
started, on Android), rather than tb taking the moment its start returned. An iOS simulator's
video begins only at the first change on screen, which on a still screen can come long after
the recording started; so right after it starts, tb shows the next minute in the simulator's
status bar for 0.3 s, then clears it, and the video begins at that change. A simulator whose
status bar has overrides of its own (screenshots at 9:41) is left alone. The video does not always begin at that change, so tb looks for it in the video (the status bar's clock changing, then changing back) and times everything from where it shows ("recording.mp4 runs 2433 ms behind tb's clock (its start mark shows at 2.43 s)"). Without a mark found,
before drawing, tb checks against the video itself: the screen changes just after each
touch, so tb finds where the picture changes and the shift that puts each touch just before
a change. When most touches (three in five, two or more) agree on a shift over 150 ms,
everything moves by it and the log says so ("recording.mp4 runs 420 ms behind tb's clock (6
of 7 touches line up)"); each part of a long Android recording is checked on its own. With
fewer touches, or touches that do not line up, the video is drawn by tb's clock and `log.txt`
says why. A web video is not checked: tb takes its frames itself, so its clock is exact.

On Android, `tb test` turns system animations off for the run and restores them after
(`--keep-animations` to leave them). Web: `--headless` and `--screen-size WxH` (each side from 200 to 10000).

## Check app code for tb (`tb a11y`)

tb finds elements through what the app exposes for accessibility. `tb a11y` reads app
source, with no device, and lists the elements tb could not detect or tap, one per line in
`tb look`'s notation with the check each one fails. Each failed check is explained once at
the end, with its fix. A new screen can be checked this way before it is merged:

```sh
tb a11y                       # the current folder
tb a11y app/src --changed     # only lines added since the merge base with main (for PRs)
tb a11y --format github       # GitHub Actions annotations on the changed lines
tb a11y --fix --dry-run       # what --fix would add; --fix writes it
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

`tb a11y --fix` adds the missing selector in place: an `android:id` in Android layouts, a
`testID` in React Native, a `data-testid` on the web, and a Compose `testTag` (once the
project sets `testTagsAsResourceId = true`). Each is named after what the element does (its
click handler, icon, link or image: `close-button`, `open_settings_button`), unique in its
file, and added on the element's own line. A Compose modifier that is more than one chain
(`if (big) Modifier.size(48.dp) else Modifier`) gets the `testTag` on the whole of it, in
parentheses. Labels people see (Flutter tooltips), warnings, and elements a loop repeats
(`.map()`, `v-for`, `{#each}`, Compose `items { }`, `forEach { }`, `for`) or a callback
attribute renders (`renderItem={({ item }) => …}`, a render prop), which need an id built
from each item, are left for a person, each with why. So are rows defined apart from their
loop, in the same file or another: a component or composable a loop repeats (and what it
renders in turn), a function a list is given by name (`renderItem={renderRow}`,
`renderItem={this.renderRow}`, `items.map(renderRow)`), and Android row layouts an adapter
inflates or a list previews with `tools:listitem`. To find them, the check reads the
project's files of the kinds it checks (under the git root, or the paths given), matching
rows by name; JavaScript bundles over 1 MB and `*.min.js` files are skipped. `--dry-run` shows what it would add without changing
anything; with `--changed`, `tb a11y --changed --fix` fixes only new code.

The checks read source text, not a running app: they catch the common causes, not every
one. `tb look` on a device is the final word.

## Drive a device (agents and people)

```sh
tb open -p android --name alice --app com.example.app  # -p or -d is required
tb look                                       # what is on screen
tb do 'tapOn: @7' 'inputText: "user@example.com"' 'tapOn: "Next"'
tb wait "Welcome"                             # waits on device events, not a loop
tb locate '{id: next}' --json                 # find matches and the runner's pick without tapping
tb watch --until "Done"                       # streams changes as they happen
tb look --shot                                # adds a small screenshot
tb save --preview -d alice --json              # inspect the export and its warnings
tb save flows/sign-in.yaml                    # the commands that passed, as a flow
tb close -d alice                             # release the device (tb test takes it from your own idle session by itself)
tb replay flows/sign-in.yaml -d DEVICE --json  # verify once on a specific device
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

When a step fails on Android or an iOS simulator, `tb do` also prints a `crashed:` line if the
app crashed during the command and the app's last error log lines (`log:`); `tb applog` prints
them on demand.

`tb do --json` and MCP `run` with `json: true` return the executed steps with their status
and duration in milliseconds, the final screen when available, and similar elements on a
lookup failure. MCP clients can read the object from `structuredContent`.

Use `tb locate` or the MCP `locate` tool to check a locator on the current screen. Both accept
text, an `@ref`, or a flow selector. The report includes each match and identifies the one
the runner would choose on that snapshot.

`tb save --preview` shows the recorded YAML without writing it. With `--json`, it returns
`yaml`, `steps`, and `warnings`. Pass the intended file path to preview relative file
references from the export directory. Warnings include coordinate gestures, selectors that
depend on a match index, text read as an unintended regex, optional assertions, a missing
initial launch, and a missing assertion.

Recorded flows avoid coordinates and match indexes. `@refs`, typed points and indexed
selectors are saved as a selector that finds the same element, such as
`{text: "Edit", childOf: {containsChild: "Eggs"}}`, `{text: "Edit", below: "Eggs"}` or
`{childOf: {id: "toolbar"}}`, when the last listed screen is still current. Otherwise the
command is kept as typed. The first of several matches is treated like the others: a bare
`"Edit"` that finds it only by coming first is saved that way only when nothing stable
tells it apart.

`tb replay` parses one single-device flow and runs it once on an explicit device. The first
executed step must be a required `launchApp` that restarts the app. Record `launchApp` to
relaunch with existing app data, or `launchApp: {clearState: true}` to reset app data at
launch. For a headless web flow, use `-d web --headless`. Replay uses the ordinary test
runner and writes its usual failure artifacts. See [architecture](docs/architecture.md)
for the recording and replay lifecycle, host components, and platform agents.

For any shell-capable coding agent, `tb agent-guide` prints a portable quick reference.
See [agent setup](#use-with-ai-coding-agents) for automatic skill discovery.
For MCP clients: `tb mcp` (tools: `devices`, `open`, `look`, `locate`, `run`, `wait`, `save_flow`, `app_log`, `close`).

For web, call `open` with `platform: "web"`, `headless: true`, and an HTTP(S) URL in `app`.
Use `run` with `- launchApp` to visit the page. `save_flow` records the session as a web flow.
The shell equivalent is `tb open -p web --headless --app URL`, followed by `tb do launchApp`.

## Use with AI coding agents

The portable [TestBuddy skill](.agents/skills/tb/SKILL.md) describes the same CLI workflow
for inspecting, acting, previewing, and replaying. The agent needs shell access on a host
with `tb` and the required device tools or browser.

| Client | How to load TestBuddy |
|---|---|
| Codex | Open this repo so Codex can discover the skill under `.agents/skills` and read `AGENTS.md`. [Codex skill locations](https://developers.openai.com/api/reference/cli) |
| Gemini CLI | Open this repo and check `/skills list`. `GEMINI.md` imports the shared repo instructions. [Gemini skill discovery](https://geminicli.com/docs/cli/skills/) · [Context imports](https://geminicli.com/docs/cli/gemini-md/) |
| Cursor | Open this repo and select `tb` from the skill menu, or let Cursor discover it when relevant. [Cursor skill directories](https://cursor.com/docs/skills#skill-directories) |
| GitHub Copilot in VS Code | Open this repo with Agent Skills enabled. VS Code can discover `.agents/skills` as a project skill location. [VS Code Agent Skills settings](https://code.visualstudio.com/docs/agents/reference/ai-settings#_agent-skills-settings) |
| Claude Code | Use the bundled plugin below. |

`tb init` sets up Claude Code, Codex and Gemini CLI with tb's MCP server
([Getting started](#getting-started)). The clients it does not cover are manual: load the
skill as in the table (Cursor, GitHub Copilot), or add an MCP server whose command is `tb`
with the argument `mcp` in the client's own MCP settings.

To use the skill in another repo, copy the whole `tb` skill folder, including
`references/`, into that project's skill directory and put the CLI on PATH. The folder
includes the CLI reference, YAML flow format, gotchas, debugging guide and multi-device guide. For a client that
reads project instructions or prompts, use the output of `tb agent-guide` as context.

### Claude Code plugin

`plugin/` is a Claude Code plugin: the `tb` CLI (macOS, Apple silicon, both device agents
built in, on Claude's PATH while the plugin is on) and a `tb` skill with the commands, flow
format, gotchas and debugging steps. This repository is its marketplace ([Install](#install)).
From a clone, `claude plugin marketplace add ./` works too, or try it for one session:
`claude --plugin-dir plugin`. After changing tb, refresh the
bundled binary with `scripts/build-plugin.sh` (it also validates the plugin).
Edit the shared reference files under `.agents/skills/tb/references/`. The same build
script copies them into the Claude plugin.

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

## Mock network requests (web)

`mockNetwork` (tb only) answers a web flow's requests without a server: test empty lists,
errors and slow responses on demand. Mocks add up (the latest matching one wins) and last
until `clearNetworkMocks` or the end of the flow; a `tb do` session keeps them until cleared.

```yaml
url: https://shop.example.com
---
- mockNetwork:
    url: "*/api/items*"            # * any characters, ? one character
    method: GET                    # any method when left out
    status: 200                    # default 200
    file: fixtures/items.json      # or body: '...'
    delay: 1500                    # ms, to see the spinner
- mockNetwork: {url: "*/api/cart", status: 500, body: "Server error"}
- mockNetwork: {url: "*/ads/*", fail: true}   # the request fails like a network error
- launchApp
- assertVisible: "No items yet"
- clearNetworkMocks
```

Mocks cover the page and its cross-origin iframes (not web workers). `body`, `url`, `method`
and header values take `${}`; build a JSON body in one expression
(`body: '${JSON.stringify({items: [NAME]})}'`) or use `file:` (sent as is). Without a
`Content-Type` header tb sends `application/json` for a `.json` file or a JSON body, else
`text/plain`. A page fetching a mocked API on another origin needs no CORS headers in the mock:
tb adds them (the page's origin, credentials allowed) unless the mock sets its own
`Access-Control-Allow-Origin`. tb also answers the `OPTIONS` preflight, at once (no `delay`),
when a mock covers the URL for the method the preflight asks about, even a mock with another
`method`; a preflight for an unmocked method goes on to the network. On Android and iOS the
step fails (or warns when `optional: true`).

## JavaScript dialogs (web)

tb answers `alert`, `confirm` and `prompt` as they open (OK; a prompt with its default
text). `answerDialogs` (tb only) chooses the answer for the dialogs that open from then on,
in every tab, until the flow chooses again:

```yaml
- answerDialogs: CANCEL                 # confirm() returns false, prompt() null
- tapOn: "Delete"
- assertVisible: "Kept"
- answerDialogs: {text: "Grace"}        # OK; prompt() returns "Grace"
- answerDialogs: {button: OK, text: "${NAME}", label: Sign as, optional: true}
- answerDialogs: OK                     # the default again
```

The map form takes `button` (OK or CANCEL; none means OK), `text`, `label` and `optional`.
`button` and `text` take `${}`; `text` with CANCEL is an error. An alert gets OK and a
`beforeunload` "Leave site?" is left whatever the choice. A flow starts and ends with the
default; a `tb do` session keeps its choice until it changes it. On Android and iOS the step
fails (or warns when `optional: true`).

Since tb answers a dialog as it opens, `assertVisible` cannot see it. `assertDialog` (tb only;
Maestro has no command for browser dialogs) checks that one opened, and what it said:

```yaml
- tapOn: "Delete"
- assertDialog: "Delete it?"                       # its message
- assertDialog: {text: "Delete .*", type: confirm} # alert, confirm, prompt or beforeunload
- tapOn: "Save"
- assertDialog                                     # any dialog
```

It looks at the dialogs opened since the last step that acted on the page (a tap, typing,
a navigation): checks in between, such as `assertVisible` or another `assertDialog`, do not
start a new window, so a flow can check the page first and several dialogs in a row. One that
opens a moment later (a timer) is waited for, up to the lookup timeout. Put `assertDialog`
before the next action: once that action runs, a dialog that opened before it no longer
counts, even one a timer opened after the `tapOn` that started it. The message is matched
like an element's text (a regex for the whole message, or the exact message) and takes `${}`.
Dialogs in every tab and frame count, a cross-origin iframe's included. The map form also
takes `label` and `optional`. When none matches, the step fails and lists the dialogs that did
open, with tb's answer to each. On Android and iOS the step fails (or warns when
`optional: true`). In a `tb do` / `tb look` session, which writes no trace, each dialog tb
answered is printed as a `note:` line under the step it opened in (in `--json`, the step's
`notes`); one that opens between commands is printed by the next `tb do`, or after the
listing by the next `tb look` (`notes` in its `--json`). Dialogs from before the session
opened are not printed, and do not count for its first `assertDialog`.

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
- On iOS, when SpringBoard alone comes in front of the flow's app (the home screen or the app
  switcher, with no alert of its own) without the flow asking for it (`pressKey: home`,
  `stopApp`, `openLink`, ...), a step waiting on the app says so in its trace at once. If the
  app is not back after 5 s, the step fails with what is in front, e.g. "the app switcher
  (com.apple.springboard) is in front, not com.example.app", instead of waiting out its
  timeout. Waits for an element to go (`assertNotVisible`, `notVisible` in `extendedWaitUntil`
  or `assertTrue`) never pass on SpringBoard, since it shows none of the app's elements; a
  `when`/`while` condition is then not met, never failed. System UI SpringBoard shows over the
  app (the contacts access picker, say) does not fail a step early. Steps whose screen reads
  were slow (2 s or more: a starved simulator) wait their full time and say so (issue #142).
- Android animations are off during `tb test` runs.
- On Android, stopping the app also clears what other apps left in its task: a permission or
  Google Play services dialog, a share chooser or a document picker the app opened stays in
  the app's task after `am force-stop`. `stopApp` removes the app's task, dialog and all
  (Android 10 and later; before, the next launch clears it), and `launchApp` after a stop
  (`stopApp`, the default, or `clearState`) starts the app in a clean task. `killApp` keeps
  the task and its saved state, and `launchApp: {stopApp: false}` resumes it as it is.
- In CI, tb gives slow runners more room and a second try where they need it. Outside CI
  none of this applies, and tb behaves and times as without it:
  - an iOS app's first screen read after a launch may take 70 s (XCTest waits up to 60 s for
    a new app to answer; 30 s otherwise), and a launched app whose accessibility has not
    answered yet is waited on and asked again, up to three times (otherwise the launch fails
    with XCTest's error);
  - listing the booted simulators may take 60 s (10 s otherwise);
  - an iOS swipe from an element (`swipe: {from: ...}`) holds the finger still 200 ms before
    it moves, and before it lifts creeps the last 8 points over 1.5 s, so a slider or pager in
    an app that is behind (stalled up to about a second) ends where the finger did (+1.7 s);
    so does a mostly sideways swipe between two points (`start`/`end`) that starts on a
    slider's thumb (other swipes still lift while moving, and may fling; every sideways swipe
    between two points reads the screen first to tell, which costs nothing when the screen in hand
    is current);
  - an iOS swipe or drag waits, at most 3 s, for SpringBoard to keep up before it is played:
    a SpringBoard that is behind holds the app's gestures, and a pager did not turn (no time
    while SpringBoard keeps up; otherwise it is played at once);
  - an iOS swipe that drags a slider's thumb sideways and leaves the slider short of where
    the drag ended (an app stalled longer than the creep) drags it again from its thumb, up
    to twice: only when the drag moved it toward the end or not at all, never a disabled
    slider, one under a window that came up, or from a thumb at the screen's edge (one more
    screen read per such swipe; a further +1.9 s per drag again);
  - a lookup, or a wait for an element to go (`assertNotVisible`, `notVisible` conditions),
    that reaches its deadline while the device has been silent reads the screen once more
    before it judges;
  - an iOS step waiting on the app while SpringBoard alone is in front of it gives the app
    10 s to come back before it fails (5 s otherwise), and a failed query of which app is in
    front reads the last app seen in front, not SpringBoard (otherwise SpringBoard, issue #142);
  - a tap whose target went out of sight, scrolled back to bring it into view, stops scrolling
    once the screen it was on is gone (the tap opened another) and passes (otherwise the
    scroll back goes on for its time);
  - `takeScreenshot` with `cropOn` takes the screenshot up to 3 times while the element moves
    as it is taken (otherwise once: an element that moved fails the command, saying so);
  - an iOS tap whose touch was withheld because the target was moving, or cancelled before it
    ended, is sent again on the target found again (otherwise the step fails and says which);
  - an iOS tap on a button that no control confirmed (SwiftUI), when the screen changed just
    before the touch, looks at the target again 500 ms after the touch, and fails the step,
    saying the target moved from under the tap, when it moved off the point with no sign the
    tap took, as when a layout shift lands just before the touch; it never taps a second time,
    and new content where the target was (a row its tap added) counts as the tap taking
    (otherwise the tap stands at once);
  - an iOS tap on a switch is checked by the switch's value: one that did not move within 1 s
    of the touch or 500 ms of the tap's reply, whichever is later, on a screen where nothing
    else changed either, is tapped once more (held 200 ms), as a switch ignores a touch the
    app gets down and up at once while its main thread is busy (otherwise the tap stands; a
    tap that took costs one screen read). A switch whose value the app sets asynchronously
    later than that still gets the second tap;
  - an Android tap into a window just added (whose input channel may not be up yet) is held
    until that window is 400 ms old (otherwise it is sent at once);
  - an Android tap whose touch the platform refused (nothing reached the app) is sent again
    once (otherwise the step fails, saying the platform refused the touch);
  - a tap that reported nothing and whose row is then gone from a list that otherwise did not
    change is taken to be opening its screen, and is not scrolled back for (otherwise the row
    is scrolled back for, as for one pushed out of view);
  - iOS `inputText` and `eraseText` give the field the focus back with a touch of tb's own
    (otherwise with XCTest's tap, which first waits for the app to go idle, within the same
    time limit), and ask once more when the app answered nothing before any key went;
  - an iOS screen read, `pressKey` or `hideKeyboard` that gave up because the app did not answer
    accessibility in time (its main thread busy), before reading or sending anything, is asked
    once more (otherwise the step fails with the agent's error, which says what it waited for);
  - a find, `scrollUntilVisible` or assert wait whose time ran out while the iOS simulator
    stopped answering, or answered slowly, for 10 s or more during that wait is given the
    stalled time back, once per step and at most its own timeout, and the trace says so; a step
    inside `runFlow`, `repeat` or `retry` counts on its own (otherwise the step fails, naming
    the stall);
  - Android `inputText` waits up to 1 s for a field to take the focus;
  - `waitForAnimationToEnd` on Android and iOS wants the screen still for at least 3 times the
    longest gap between its screenshots (otherwise 250 ms), so an app that stops drawing for
    a moment on a slow runner is not taken for still (no cost while screenshots are under
    about 80 ms apart);
  - Android `hideKeyboard`, when no keyboard has shown since the last tap, waits up to 2.5 s
    after the last action (or the screen's last change, at most 5 s) for one still coming
    up, and closes it (otherwise 1 s, at most 2 s);
  - on the web, a page whose DOM is ready but whose load never ends (an image that never
    answers, say) is waited on until its DOM has been still for 5 s (otherwise 2 s);
  - an Android agent that says hello but does not answer tb's first request within 3 s is asked
    again with 12 s, so a slow device is not taken for a stuck one (otherwise the start fails);
  - an Android emulator whose agent runs but gets no answer from Android itself is restarted,
    once per run, and the agent started again (otherwise the error says to restart it); the
    reboot ends a running `screenrecord` and other sessions' port forwards to it. A reopen of
    a lost emulator mid-run allows 3 min more for that restart (otherwise 3 min in all).
  - an iOS runner whose connection dropped is reconnected, or started again, for up to
    2 min, as a runner can take a minute to start on a slow CI Mac (otherwise 60 s).
  - an Android agent whose connection dropped is reconnected for up to 90 s, as a CI
    emulator can go offline for about a minute under load (otherwise 45 s).
  - an Android `launchApp` whose app is not in front 5 s after the start sends the start
    once more and waits the rest of the 20 s (otherwise one start; the error says what
    `am start` answered and what was in front).
  - Android `inputText` into a field that rewrites what is typed (upper-casing, a mask), which
    gets its keys one at a time, waits up to 1 s for the field to show each key (otherwise
    300 ms), and as long for the text at the end.
  - Android and iOS `eraseText` take a field's text as final once it has stayed the same for
    1 s (otherwise 300 ms), and wait as long for each delete of their one repair.

  CI mode follows `CI=true`, which GitHub Actions and most CI services set; `TB_CI=1` or
  `TB_CI=0` sets it either way. The full list is in
  [docs/architecture.md](docs/architecture.md#what-ci-mode-changes).
- On Android, an accessibility service (part of the agent) is enabled while the agent runs:
  Jetpack Compose reports UI changes only when one is. It is removed when the agent exits.
- On Android, when the agent does not start, the error says whether adb sees the device and
  whether the agent's process runs. One that runs but gets no answer from Android itself
  (seen on CI after a launch that never came up) means the device is stuck: the error says
  to restart it, and in CI mode tb restarts an emulator itself, once per run.
- AI commands (`assertWithAI`, ...) are not supported.
- Web: the browser runs with its password manager and translate bar off (they put UI over
  the page). `swipe`/`scroll` scroll what is under the start point with a mouse wheel event;
  `scrollUntilVisible` scrolls that element directly. A JavaScript dialog is answered as it
  opens, the way a person clicking through would: `alert` and `confirm` with OK, `prompt`
  with OK and its default text, and a `beforeunload` "Leave site?" by leaving. The trace
  (`log.txt`) names each dialog, its message and the answer (`tb do` and `tb look` print it
  as a `note:`); a flow does not tap them. To answer otherwise, or to check that a dialog
  opened, see [JavaScript dialogs](#javascript-dialogs-web).
  A page whose load never ends (an image, media or beacon that never answers, or a
  third-party script or frame) is no longer waited for once its DOM is ready and has been
  still for 2 s (5 s in CI); the trace says it is still loading. The site's own scripts and
  frames, stylesheets and fonts are waited for up to 30 s.
  A tap on a `target=_blank` link, or on a button that calls `window.open`, moves the flow to
  the new tab, as a person's attention would; when that tab closes, the flow goes back to the
  tab that opened it. Network mocks cover new tabs from their first request, and `launchApp`
  closes every tab but the first.
  A `launchApp` or `openLink` to this machine (`localhost`, `*.localhost`, `127.0.0.1`,
  `::1`) whose connection is refused is tried again for up to 3 s, so a dev server started
  just before `tb test` has time to listen; then the step fails saying nothing is listening on
  that host and port. Other hosts, and every other error, fail at once with Chrome's error.
  `0.0.0.0` is not this machine's address but "every interface", so a `0.0.0.0` URL gets no
  retry: when a dev server prints `http://0.0.0.0:3000`, write `http://localhost:3000`.
- `scrollUntilVisibleIn` (tb only) finds an element in a list inside the scrolling page, a
  shelf of cards or a pane: the page scrolls until `list` (any item of that list) is in view,
  then that list scrolls until `element` shows.

  ```yaml
  - scrollUntilVisibleIn: {list: "Card 6-1", element: "Card 6-12", direction: RIGHT}
  ```
- `drag` (tb only; iOS and Android, not web) presses and holds, then drags: drag and drop and list
  reorders (SwiftUI `.onDrag`/`.onMove`, a table view's reorder, Android's `ItemTouchHelper`,
  Compose's `detectDragGesturesAfterLongPress`, a View's long-click drag and drop), which lift a row only after
  a long press, so a `swipe` cannot move them. The finger rests on `from` for `hold` ms (default
  800), moves to `to` in even steps over `duration` ms (default 1000), rests and lifts.
  `start`/`end` points can stand for `from`/`to`. `optional: true` on either selector makes the
  whole step optional, as with `swipe`. A drag longer than 10 s (hold + duration) is cut short in
  the iOS gesture trace (`TB_TRACE_MAX_NS`), a diagnostic only.

  ```yaml
  - drag: {from: {id: reorder_A}, to: {id: reorder_C}}
  ```
- Taps wait for a still screen, steer clear of covers (sticky bars, the keyboard) on Android
  and iOS, give disabled buttons up to 3 s, and system dialogs (Android "isn't responding",
  the iOS notifications alert) are answered ([docs/architecture.md](docs/architecture.md)).
  On Android a tap into a window just added waits for it to take input (up to 100 ms, once),
  and a tap whose touch the platform refused fails the step, saying so (in CI mode it is sent
  again once; one whose release the platform did not deliver fails the step either way: it
  may or may not have taken effect).

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
| `docs/` | Architecture, protocol, multi-device syntax, research notes, [roadmap](docs/roadmap.md) |

## License

Copyright 2026 Jonathan Muller. Licensed under the [Apache License 2.0](LICENSE).
