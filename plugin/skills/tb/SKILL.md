---
name: tb
description: Run, write and debug UI tests with tb (Test Buddy), a fast event-driven runner for Maestro-compatible YAML flows (appId + --- + steps like tapOn, assertVisible, inputText, scrollUntilVisible, runFlow; existing Maestro flows run unchanged) on Android emulators/devices, iOS simulators and Chrome. Explore an app from the shell, then preview, save and replay the recorded session as a flow. Use when the user wants to run UI/e2e flows, inspect or interact with a device screen, turn an interactive session into a verified flow, run a flow across devices, or diagnose a UI test failure.
when_to_use: Trigger on mentions of tb, Test Buddy, Maestro flows or a .maestro folder, UI flows, e2e flows in a flows/ or .yaml folder with appId or url headers, emulator/simulator testing, "run the flows", "tap through the app", "what's on the screen", "save or replay a flow", flaky UI tests.
allowed-tools: Bash(tb *) Bash(adb devices*) Bash(xcrun simctl list*)
---

# Test Buddy (`tb`)

`tb` runs Maestro-compatible YAML flows on Android (emulator or USB device), iOS simulators
and Chrome (existing Maestro flows run unchanged, `maestro test` options included), and lets
you drive a device from the shell. Use `tb test` where a project's scripts would run
`maestro test`. tb waits on "the UI changed / settled" events from the device, never on
sleeps, and screens that never stop moving (spinners, tickers) do not hold it up.

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

## Write a good flow

<!-- shared: write-a-good-flow (keep identical in both SKILL.md copies) -->
- **Assert each action's outcome**, scoped by id when you can:
  `assertVisible: {id: status, text: "Saved"}`. An assertion is the cheapest wait. It is safe
  right after a tap (the tap waited for the UI to start changing); assert something only the
  new state shows, not a title the old screen has too.
- **Before acting on a new screen, assert something on it.** A tap that opens a screen
  returns once the change starts; on a slow slide-in, an assertion on the new screen keeps
  the next tap from racing it.
- **Waits, in this order:**
  1. a plain lookup (`tapOn`, `assertVisible`, ...) already waits up to 17 s on device
     events (7 s when `optional`); slow content in the UI needs nothing more;
  2. `extendedWaitUntil: {visible: "Report ready", timeout: 60000}` (or `notVisible`) only
     when the end state can take longer than 17 s (a backend job, an email);
  3. `waitForAnimationToEnd: {timeout: 5000}` only for a long one-shot animation you must
     wait out (default 15 s; it never fails, and ignores small endless loops after 3 s);
  4. never a sleep, a `retry` or a `waitForAnimationToEnd` "just in case".
- **Selectors:** `id` > `id` + `text` > exact text > relative (`below`, `childOf`) > `index`
  (`-1` is the last). Text matches the label exactly as written, or as a regular expression
  over the whole label. A pattern with `.*` for a dynamic part must escape the rest's
  `? ( ) [ ] + * $ . |`: `'Total: \$.*'`, `"Order #.* placed"`. `rightOf`/`below` match
  anything further right/lower, in any row. Then `tb check --lint`.
- **Speed:** clear state only in flows that need a fresh start; keep every flow runnable on
  its own. An optional tap that is usually absent gets `timeout: 1000` (tb-only; omit it in
  flows Maestro must also run). No scrolling to what is already on screen; no `hideKeyboard`
  before a tap (a tap whose target is not found with the keyboard up hides it and looks
  again). `pressKey: Enter` moves along a Next chain of fields.
- **Platforms:** `appId: ${APP_ID || 'com.example.app'}`; platform-only steps go in
  `runFlow: {when: {platform: iOS}, commands: [...]}`. iOS has no back key: `tapOn: {id: BackButton}`.
<!-- /shared -->
- One flow per behavior; share setup with `runFlow`. Several devices in one test:
  [multi-device.md](multi-device.md).

## Run flows

```bash
tb check --lint flows/                           # parse and lint, no device: after every edit
tb test flows/                                   # a folder (uses its config.yaml if any)
tb test flows/login.yaml --device emulator-5554  # one flow, one device
tb test flows/ -e APP_ID=com.example.app -e USER=qa@example.com
tb test flows/ --json                            # machine-readable summary (best for you)
tb test web-flow.yaml --headless                 # web flows: Chrome without a window
```

- Exit status: 0 passed, 1 a flow failed, 2 bad input (bad YAML, unknown option).
- iOS bundle ids often differ from Android package names: pass the right `-e APP_ID` per platform.
- `tb test` turns Android animations off for the run (`--keep-animations` to keep them); a
  `tb do` session leaves them on.
- `tb a11y` checks app code (no device) for elements tb cannot find or tap. Run it after
  adding or changing a screen; `--changed` checks only new lines.
- Full options: [cli.md](cli.md).

When a step fails, `tb` prints the screen as it was (one line per element) and a `similar:`
line of near matches. Read those before anything else. Then [debugging.md](debugging.md).

## Record a flow from the shell

```bash
tb open -d emulator-5554 --name rec --app com.example.app
tb do -d rec 'launchApp'
tb look -d rec                   # the screen: one line per element, @refs, layout
tb do -d rec 'tapOn: @5' 'assertVisible: {id: status, text: "Saved"}'
tb locate -d rec '{text: "Edit"}'   # every match and which one tb would pick
tb save --preview flows/save-item.yaml -d rec --json
tb save flows/save-item.yaml -d rec
tb close -d rec
tb replay flows/save-item.yaml -d emulator-5554 --json
```

1. Open the device, then start with a plain `launchApp` (it keeps app data); add
   `clearState: true` only when the test needs a fresh install state.
2. `tb look`, then `tb do` each action with the assertion that proves it. Read the diff
   `tb do` prints (`+` new, `~` changed, `-` gone) instead of looking again. `@refs` come
   from the latest output; they are saved as stable selectors.
3. `tb save --preview ... --json`: fix its `warnings` (selectors that depend on order,
   accidental regex, a missing assertion), then save. Preview writes nothing.
4. Close the session, then replay once on the device serial, UDID or name from `tb devices`
   (`-d web --headless` for Chrome).

For a short flow, writing the YAML by hand after exploring with `tb do` is just as good: then
`tb check --lint` and `tb test`. How to read a listing: [cli.md](cli.md#reading-tb-look).
`tb close all` disconnects everything; `tb close` waits for a command still running (Ctrl-C it).

```yaml
appId: ${APP_ID || 'com.example.app'}   # web: url: https://example.com
name: Sign in
tags: [smoke]
---
- launchApp
- tapOn: {id: email}
- inputText: ${USER}
- tapOn: {id: sign_in}
- assertVisible: {id: greeting, text: "Welcome.*"}
- scrollUntilVisible:
    element: {id: settings_row}
- tapOn: {id: settings_row}
- assertVisible: "Settings"
```

Full format: [flows.md](flows.md).

## Gotchas (read before diagnosing a failure)

The full list: [gotchas.md](gotchas.md). The ones that bite most:

1. **One tb per device.** A `tb test` run or an open session locks its device; another run is
   refused at once, saying who holds it. `tb test --wait-for-device 5m` waits; your own idle
   session hands its device to your `tb test` / `tb replay` run by itself. Stop other UI test
   tools before tb runs.
2. **The installed app must match the flows.** Most "element not found" failures on a real
   app are a different build (renamed labels, removed buttons). Check the printed screen.
3. **`clearState` signs users out** and wipes app data; on a signed-in device omit it.
4. **iOS apps should be started by `launchApp`**: an app opened another way reports fewer
   changes, so waits are slower. The first iOS run on a simulator installs a runner (~20 s).
5. **Typing goes to the focused field.** Tap the field (by `id`) before `inputText`.
6. **Covers.** A bar over the target (tab bar, sticky footer, a pinned section header, the
   keyboard) is steered around. A modal layer (drawer, sheet, dialog) is waited for up to 2 s,
   then the step fails `<selector> is under <cover>`: close the layer in the flow first.
7. **Flows that change real accounts** (sign-in, sharing, deleting) act on real backends:
   confirm with the user before running them on signed-in devices.

## Debugging a failure

1. Read the failure output: failing step, error, printed screen, `similar:` line.
2. Read `log.txt` in the flow's debug folder (the `debug:` line, or `debugDir` in `--json`):
   what tb did, step by step. Read up from the `✗`. Also there: `screen.txt`,
   `screenshot.png`, `hierarchy.json`, `commands.json`.
3. Reproduce interactively: `tb look`, then `tb do` the failing step.
4. Fix the cause (selector, state, a missing assertion before the step), never with sleeps
   or retries. What each log line means: [debugging.md](debugging.md).
