---
name: tb
description: Run, write and debug UI tests with tb (Test Buddy), a fast event-driven runner for Maestro-compatible YAML flows (appId + --- + steps like tapOn, assertVisible, inputText, scrollUntilVisible, runFlow; existing Maestro flows run unchanged) on Android emulators/devices, iOS simulators and Chrome. Also drives a device from the shell for exploring an app (look at the screen as text, tap by @ref, see what changed). Use when the user wants to run UI/e2e flows or a test suite, test an app on an emulator, simulator or browser, inspect or interact with what is on a device screen, write or fix a flow, run one flow across several devices, or find out why a UI test failed.
when_to_use: Trigger on mentions of tb, Test Buddy, Maestro flows or a .maestro folder, UI flows, e2e flows in a flows/ or .yaml folder with appId or url headers, emulator/simulator testing, "run the flows", "tap through the app", "what's on the screen", flaky UI tests.
allowed-tools: Bash(tb *) Bash(adb devices*) Bash(xcrun simctl list*)
---

# Test Buddy (`tb`)

`tb` runs Maestro-compatible YAML flows on Android (emulator or USB device), iOS simulators
and Chrome (existing Maestro flows run unchanged, `maestro test` options included), and lets
you drive a device from the shell. Use `tb test` where a project's scripts would run
`maestro test`. The device agents push "the UI changed / settled"
events, so `tb` waits on events instead of sleeping or polling, and screens that never stop
moving (spinners, tickers, stopwatches) still work.

`tb` is on your PATH while this plugin is enabled (the plugin's `bin/tb`, a macOS Apple
silicon binary with the Android and iOS agents built in). If `which -a tb` shows another
`tb` first, run this plugin's copy by its full path: `${CLAUDE_PLUGIN_ROOT}/bin/tb`. Setup,
device requirements and what tb installs on devices: [setup.md](setup.md).

## First steps

```bash
tb devices                  # what tb can drive: Android serials, iOS simulators, chrome
```

- Android: an emulator running, or a phone with USB debugging (`adb devices` lists it).
- iOS: a **booted simulator** (`xcrun simctl list devices booted`). Physical iPhones are not supported.
- Web: Google Chrome installed. Web flows have `url:` instead of `appId:`.

Emulator serials change when an emulator restarts (`emulator-5554` can come back as
`emulator-5556`): run `tb devices` again rather than reusing an old serial.

## Run flows

```bash
tb test flows/                                   # a folder (uses its config.yaml if any)
tb test flows/login.yaml --device emulator-5554  # one flow, one device
tb test flows/ -e APP_ID=com.example.app -e USER=qa@example.com
tb test flows/ --include-tags smoke --format junit --output report.xml
tb test flows/ --json                            # machine-readable summary (best for you)
tb test flows/ --debug-output /tmp/tb-debug      # where failure artifacts go
tb test web-flow.yaml --headless                 # web flows: Chrome without a window
```

- Exit status: 0 passed, 1 a flow failed, 2 bad input (bad YAML, unknown option).
- Flows that use `appId: ${APP_ID}` need `-e APP_ID=...`. iOS bundle ids often differ from
  Android package names (e.g. `com.example.app` vs `com.example.app.App`): pass the right one
  per platform.
- `tb check flows/` parses flows without a device: run it first when editing YAML.
- `tb test` turns Android system animations off for the run and restores them after
  (`--keep-animations` to leave them on).
- Full options: [cli.md](cli.md).

When a step fails, `tb` prints the screen as it was (one line per element) and a `similar:`
line of near matches. Read those before anything else. Then [debugging.md](debugging.md).

## Drive a device (explore, reproduce, write flows)

```bash
tb look                          # the screen: one line per element
tb do 'tapOn: @5'                # act; prints ok/FAILED, then what changed (+ new, ~ changed, - gone)
tb do 'tapOn: "Sign in"' 'inputText: "qa@example.com"' 'pressKey: Enter'
tb wait "Welcome" --timeout 10000   # waits on device events, never a sleep loop
tb look --shot                   # adds a small screenshot path: only when text is not enough
tb save flows/sign-in.yaml       # the commands that passed, as a flow file
```

A listing looks like:

```
android 1080x2400 com.example.app, 12 elements
@3 Button "Sign in" #login_button
@7 Field "Email" #email hint="Email" focused
@9 Switch "Notifications" unchecked
```

- `@N` refs come from the latest `look`/`do` output only; look again after the screen changes.
- Read the diff `tb do` prints instead of calling `tb look` after every step.
- In saved flows prefer `id:` (from `#id`) or visible text over refs and points.
- Several devices: `tb open -d <serial|name> --name alice`, then `-d alice` on each command.
- `tb close all` disconnects; `tb daemon stop` stops the background process.

## Write flows

A flow file is a config block, `---`, then steps. Full reference: [flows.md](flows.md).

```yaml
appId: com.example.app          # web: url: https://example.com
name: Sign in
tags: [smoke]
---
- launchApp:
    clearState: true            # fresh install state (this signs the user out)
- tapOn: {id: email}
- inputText: ${USER}
- tapOn: "Sign in"
- assertVisible: "Welcome.*"     # text matchers are regular expressions, full match
- scrollUntilVisible:
    element: "Settings"
- runFlow: common/logout.yaml
```

Habits that keep flows fast and stable with `tb`:

- Never add sleeps or `waitForAnimationToEnd` "just in case": `tb` already waits for a still
  screen before every tap and for each tap to take effect. Use `extendedWaitUntil` only for
  something slow outside the UI (a backend, an email).
- Select by `id` first, then exact visible text. Avoid `point:` taps.
- For a device that is already signed in, start with `- launchApp` **without** `clearState`.
- One flow per behavior; share setup with `runFlow` sub-flows.
- Several devices in one test (chat between two users, phone + web): [multi-device.md](multi-device.md).

## What tb does for you (do not work around it)

- **Waits on events, not time.** Steps start the moment the UI settles.
- **Never-still screens are fine.** An element that keeps changing (ticker, stopwatch,
  progress) is marked restless and no longer holds the screen up.
- **Taps land where intended.** It waits for a still screen, finds the element again, steers
  clear of anything drawn over it (a sticky footer, tab bar, the keyboard) by dragging the list
  or tapping the uncovered part, gives a disabled button up to 3 s to turn on, and waits up
  to 1 s for each tap to take effect.
- **Assertions may pass mid-transition**: anything that acts (tap, type, scroll,
  screenshot, `when:` conditions) still waits for a settled screen.
- **System dialogs are answered**: Android "isn't responding" / "keeps stopping" (Wait, then
  Close app if it returns), iOS "Would Like to Send You Notifications" per `launchApp`
  `permissions` (default allow; `notifications: unset` leaves it to the flow).
- **scrollUntilVisible drags without flinging** and checks after each drag; for paging lists
  it falls back to flinging swipes. Its `speed` hardly matters.

## Gotchas (read before diagnosing a failure)

The full list, with the why: [gotchas.md](gotchas.md). The ones that bite most:

1. **One UI automation client per Android device.** Stop other UI test tools (and their
   on-device servers) before `tb` runs on that device, and vice versa. (A leftover Maestro
   driver is stopped by tb itself when Maestro is not running.)
2. **The installed app must match the flows.** Most "element not found" failures on a real
   app are a different build (renamed labels, removed buttons). Check the printed screen.
3. **`clearState` signs users out** and wipes app data; on a signed-in device omit it.
4. **iOS apps should be started by `launchApp`.** Apps `tb` launches get a small probe that
   reports in-place changes; an app opened another way only reports screen changes, so
   waits are slower. The first iOS run on a simulator installs `tb`'s runner (~20 s).
5. **Typing goes to the focused field.** Tap the field (by `id`) before `inputText`.
6. **Web cover detection is off**: on web, taps go to the element's center.
7. **Flows that change real accounts** (sign-in, sharing, deleting) act on real backends:
   confirm with the user before running them on signed-in devices.

## Debugging a failure

1. Read the failure output: failing step, error, printed screen, `similar:` line.
2. Open the debug folder (`--debug-output`): `screen.txt`, `screenshot.png`,
   `hierarchy.json`, `commands.json` (every step, status, duration).
3. Reproduce interactively: `tb look`, then `tb do` the failing step.
4. Still unclear: `TB_DEBUG=1 tb test ...` (agent calls, covers, dialogs). Details and
   Android agent logs: [debugging.md](debugging.md).
