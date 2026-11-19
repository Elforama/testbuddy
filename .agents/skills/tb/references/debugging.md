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

`tb test ... --debug-output DIR` (a subfolder per run; `--flatten-debug-output` writes
straight into DIR). Without it: `$TB_HOME/tests/<time>/` (`~/.testbuddy/tests/` by default).
With `--json`, a failed flow's `debugDir` gives the path. Every flow gets a `log.txt`; each
failed flow also gets the rest:

| File | What |
|---|---|
| `log.txt` | **What tb did, step by step** (every flow, passed or failed). Read it first when the printed failure is not enough. |
| `error.txt` | The failure. |
| `screen.txt` | The screen listing at the failure (as `tb look` prints it). |
| `screenshot.png` | A screenshot at the failure. |
| `hierarchy.json` | Every node with bounds and flags. |
| `commands.json` | Every step with its status (`passed`/`failed`/`warned`/`skipped`) and duration. |

Multi-device flows add one of each per role (`screen-bob.txt`, ...); their `log.txt` is one
file, each line tagged with its role (`[alice]`).

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
it. Read up from the `✗`: the last `found`, `tap at` and `nothing changed` lines usually
show which earlier step left the app somewhere unexpected.

| Log line | Means | Fix |
|---|---|---|
| `not found: X, after ...` + `similar on screen:` | The selector matches nothing on this screen. | Use the `similar` element's `#id` or text; or the app is elsewhere: look at the steps before. |
| `found X: <element>` (the wrong one) | Several matched; tb took this one. | Scope it: `id`, `id` + `text`, `childOf`. Check with `tb locate`. |
| `tap at (x, y): nothing changed` | The tap reached nothing that reacted. | Was the element disabled, or was the screen still arriving? Assert the screen's content before tapping. |
| `covered at ... by ...`, `tapping its largest uncovered part` | A bar over the target; tb steered around it. | Nothing, unless the tap then did nothing. |
| `it is under a modal layer (...)` | A drawer, sheet or dialog lies over the target. | Close it in the flow before the step. |
| `under a loading overlay (...)` | A spinner layer was in the way; tb waited. | Nothing; a long wait is the app being slow. |
| `drag N: ...`, `fling N: ...` | How a `scrollUntilVisible` went. | Not reached: wrong `direction`, or the element is in another list. |
| `condition not met (...): skipped` | A `when:` block did not run. | Check the condition against `screen.txt`. |
| `animation wait: still moving at the timeout: the part at ...` | `waitForAnimationToEnd` timed out (it never fails). | Usually remove it; assert the end state instead. |

Never fix a failure with sleeps or retries: find which step left the app in the wrong state.

## 3. Reproduce by hand

```bash
tb open -d <device>          # optional
tb look                      # where are we?
tb do '<the failing step>'   # does it work now? what changed?
tb locate '{text: "Edit"}'   # every match of a selector and which one tb picks
tb look --all                # every raw node, if the element seems missing
tb hierarchy --json          # full JSON: ids, classes, bounds, flags
```

If the step works by hand but not in the flow, the flow reached it in a different state:
look at the steps before it in `commands.json`.

## 4. Turn on logging

- `TB_DEBUG=1 tb test ...` prints `log.txt` to stderr as the run goes. `tb do` runs in the
  daemon: its trace goes to `$TB_HOME/daemon.log`, and only when the daemon was started with
  `TB_DEBUG=1` set.
- Android agent events:
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
| `Assertion is false: "X" is visible` | Same as above; the assertion already waited 17 s. Only an end state that takes longer (a backend job) needs `extendedWaitUntil` with a timeout. |
| `X is under <cover>` | A modal layer (drawer, sheet, dialog) is still over the target after 2 s: close it in the flow first. |
| `no tap reached X: N taps changed nothing ...` | iOS: something tb cannot see (a hidden veil) took every tap. Assert the screen is ready (the veil gone, the content loaded) before the tap. |
| A tap "worked" but nothing happened | The element was disabled for longer than 3 s, or covered on the web, or the app ignores fast taps: check `screen.txt` for `disabled` and what is over it. |
| Text typed nowhere (`log.txt`: `typing N characters, but no field has focus`) | Tap the field by `id` before `inputText`. |
| `app X is not installed` (or `... has no launcher activity`) | The app is not on that device, or the wrong `-e APP_ID` (iOS bundle ids differ from Android packages). |
| `no device is connected` | Start an emulator or boot a simulator (ask the user first). |
| `device 'emulator-5554' not found` | The emulator restarted under a new serial: `tb devices`. |
| Taps ignored, empty screens on Android | Another UI automation tool is connected to the device: stop it, then `tb close all` and retry. |
| `lost the connection to the device agent` | The device or adb went away; tb reconnects once. Check `adb devices` / the simulator. |
| iOS run hangs at start | First run installing the runner (~20 s); or the simulator is not booted. |
| `the screen changed without the device reporting it ...` | The app changes without telling the device (an iOS app tb did not launch). Start it with `launchApp`. Flows still pass, more slowly. |
