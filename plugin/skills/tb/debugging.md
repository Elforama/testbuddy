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
subfolders). Each failed flow gets:

| File | What |
|---|---|
| `error.txt` | The failure. |
| `screen.txt` | The screen listing at the failure (as `tb look` prints it). |
| `screenshot.png` | A screenshot at the failure. |
| `hierarchy.json` | Every node with bounds and flags. |
| `commands.json` | Every step with its status (`passed`/`failed`/`warned`/`skipped`) and duration. |

Multi-device flows add one of each per role (`screen-bob.txt`, ...). `--json` puts the same
summary on stdout.

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

- `TB_DEBUG=1 tb test ...` logs agent requests and results (Android: each action's inject
  and settle times), covers tb steered around (`... covered by ...`) and system dialogs it
  answered.
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
