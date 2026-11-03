# Debugging a failure

## 1. Read what tb printed

```
  ✗ Tap on "Mark Apples done"  16.9s
      Element not found: "Mark Apples done"
  (the screen as it was, one line per element)
  similar: Button "Apples" #item_checkbox_f8e5...
```

The failing step, the error, the screen at that moment and near matches. Most failures are
explained here: the label differs, the element is on another screen, or a dialog is in the
way.

## 2. The debug folder

`tb test ... --debug-output DIR` (add `--flatten-debug-output` to skip the per-run
subfolders; without `--debug-output`, `~/.tb/tests/<time>/`). Every flow gets a `log.txt`;
each failed flow also gets the rest:

| File | What |
|---|---|
| `log.txt` | **What tb did, step by step** (every flow, passed or failed). Read it first when the printed failure is not enough. |
| `error.txt` | The failure. |
| `screen.txt` | The screen listing at the failure (as `tb look` prints it). |
| `screenshot.png` | A screenshot at the failure. |
| `hierarchy.json` | Every node with bounds and flags. |
| `commands.json` | Every step with its status (`passed`/`failed`/`warned`/`skipped`) and duration. |

Multi-device flows add one of each per role (`screen-bob.txt`, ...); their `log.txt` is one
file, each line tagged with its role (`[alice]`). `--json` puts the same summary on stdout.

### Reading log.txt

```
   3.479s ▶ Tap on id: next  (login.yaml:13)
   3.855s   found id: next: Button "Next" #next [42,902][1038,1033]
   4.072s   waited 215 ms for a still screen
   4.213s   tap at (540, 967): the screen changed (sent in 8 ms, waited 110 ms)
   4.213s ✓ Tap on id: next  733 ms
```

Seconds since the flow started; `▶` a command starts (with its file and line), `✓ ✗ ⚠ ○` it
ended (passed, failed, warned, skipped) with its time; the lines between are what tb did for
it, indented like nested flows:

- `found X: <element>`: what a lookup matched (kind, text, `#id`, bounds), and how long it
  waited. `not found: X, after ... (screen: ..., allowed ...)` plus `similar on screen:`.
- `waited N ms for ...`: every wait that took 50 ms or more, and what it waited for (the last
  action's change, a still screen, a loading overlay, a disabled button).
- `tap at (x, y): ...`: where the tap went and what the device reported: `the screen
  changed`, `nothing changed` (the tap may have missed: check what was at that point), or
  `still changing when the wait ran out`.
- `covered at its middle by ...`, `under a loading overlay ...`: something over the target,
  and what tb did about it.
- `drag N: ...`, `fling N: ...`, `in view after N swipes`: how a `scrollUntilVisible` went.
- `condition not met (...): skipped`, `retry: attempt 1 of 2 failed (...)`: why a block was
  skipped or repeated.
- `system dialog: ...`, `log: ...`: dialogs tb answered; notes and `console.log` lines.

To find why a step failed, read up from the `✗`: the last `found`, `tap at` and `nothing
changed` lines usually show which earlier step left the app somewhere unexpected.

## 3. Reproduce by hand

```bash
tb open -d <device>          # optional
tb look                      # where are we?
tb do '<the failing step>'   # does it work now? what changed?
tb look --all                # every raw node, if the element seems missing
tb hierarchy                 # full JSON: ids, classes, bounds, flags
```

If the step works by hand but not in the flow, the flow reached it in a different state:
look at the steps before it in `commands.json`.

## 4. Turn on logging

- `TB_DEBUG=1 tb test ...` prints `log.txt` to stderr as the run goes, plus Android agent
  request times. With `tb do`, it prints the same lines for each command.
- Android agent events (every accessibility event, every quiet check):
  ```bash
  adb -s <serial> shell setprop log.tag.TestBuddyAgent DEBUG
  adb -s <serial> logcat -s TestBuddyAgent
  ```
  `adb shell setprop log.tag.TestBuddyAgent INFO` turns it back off.
- Web: run without `--headless` to watch the browser.

## Common errors

| Error | Usually |
|---|---|
| `Element not found: X` | Label/id differs in this build, element on another screen or below the fold (use `scrollUntilVisible`), or text regex too strict (`"Item 3"` does not match "Item 30"; use `"Item 3.*"`). |
| `Assertion is false: "X" is visible` | Same as above; or a slow backend (use `extendedWaitUntil`). |
| A tap "worked" but nothing happened | The element was disabled for longer than 3 s, or covered on the web, or the app ignores fast taps: check `screen.txt` for `disabled` and what is over it. |
| `no focused text field` / text typed nowhere | Tap the field by `id` before `inputText`. |
| `app X is not installed` (or `... has no launcher activity`) | The app is not on that device, or the wrong `-e APP_ID` (iOS bundle ids differ from Android packages). |
| `no device is connected` | Start an emulator or boot a simulator (ask the user first). |
| `device 'emulator-5554' not found` | The emulator restarted under a new serial: `tb devices`. |
| Taps ignored, empty screens on Android | Another UI automation tool is connected to the device: stop it, then `tb close all` and retry. |
| `lost the connection to the device agent` | The device or adb went away; tb reconnects once. Check `adb devices` / the simulator. |
| iOS run hangs at start | First run installing the runner (~20 s); or the simulator is not booted. |
| `the screen changed without the device reporting it ...` | The app changes without telling the device (an iOS app tb did not launch has no probe). Start it with `launchApp`. Flows still pass, more slowly. |
