# tb command reference

Exit status for every command: **0** ok, **1** a command or flow failed, **2** bad input.
`-d` takes a session name (from `tb open --name`), a serial/UDID or a device name; with one
device it is optional.

## Run tests

`tb test <flow files or folders> [options]`

| Option | Meaning |
|---|---|
| `-e, --env KEY=VALUE` | Set a variable (repeatable). A flow's own `env:` is applied after it, so write overridable defaults there as `KEY: ${KEY \|\| 'default'}`. |
| `-p, --platform android\|ios\|web` | Run on this platform only. |
| `--device a,b` (alias `--udid`) | Devices to use (serial, UDID or name). |
| `--device role=DEVICE` | Pin a multi-device flow's role (repeatable): `--device alice="iPhone 17"`. |
| `--config FILE` | Workspace config (default: `config.yaml` in the folder). |
| `--include-tags a,b` / `--exclude-tags a,b` | Filter flows by `tags:`. |
| `--shard-split N` | Spread flows over N devices (a work queue). |
| `--shard-all N` | Run every flow on each of N devices. |
| `--format junit\|html\|html-detailed\|noop` | Report format. |
| `--output FILE` | Report path (default `report.xml` / `report.html`). |
| `--test-suite-name NAME` | JUnit suite name. |
| `--debug-output DIR` | Where each flow's `log.txt` (what tb did, step by step) and its failure files (screenshot, screen listing, hierarchy, steps) go: a folder per flow, named after it (flows of a run that share a name add their directory, like `smoke-login`). |
| `--flatten-debug-output` | Write debug files straight into `--debug-output`. |
| `--retries N` | Re-run a failed flow up to N times (on another device if one is free). |
| `--wait-for-device TIME` | A device another tb holds (another run, an open session): wait up to TIME (`5m`, `90s`) for it instead of failing at once. |
| `--keep-animations` | Do not turn Android animations off during the run. |
| `--keep-agents` | Leave tb's agent running on each device after the run: the next run starts faster, but other test tools cannot use the device until `tb release`. |
| `--headless` | Web: Chrome without a window. |
| `--screen-size WxH` | Web: browser window size (default 1280x800). |
| `--json` | Print a JSON summary: `passed`, `failed`, `skipped` and each flow with its `status` (`SUCCESS`, `WARNING`, `ERROR`, `SKIPPED`) and `error` (for a skipped flow, why it did not run); failures include the screen listing. |
| `--record` | Record each flow's screen from start to finish: `recording.mp4` in its debug folder, in the JSON summary (`videos`), the HTML report (a player), JUnit (`video` property, `[[ATTACHMENT\|path]]` in system-out) and the timeline ("▶ Video" per step). Web needs `ffmpeg`. A device that cannot record does not fail the flow. |
| `--timeline` | Also write `timeline.html` in each flow's debug folder: every step with the screen before and after it, taps and swipes drawn where they went, errors and trace lines. Slower (a screenshot per step). The JSON summary gives its path as `timeline`. |
| `--quiet` | Print only failures and the summary. |

`tb check <flows or folders...>` parses flows (and the sub-flows they run) without a device; a
folder means every .yaml in it and its sub-folders but `config.yaml`. `--lint` also lists
fragile steps under each flow (`  ! :7 coordinate_tap: …`): `coordinate_tap`,
`coordinate_swipe`, `indexed_selector`, `regex_text` (text such as `"Sign in?"` read as a
regex that matches more than written; escape it `'Sign in\?'`), `optional_assertion`,
`missing_assertion`, `unknown_header_key` (a header key neither tb nor Maestro reads, such as
`tgas:`, with the known key it is closest to), `duplicate_key` (a key written twice in one
map; YAML keeps the last). Warnings fail only with `--fail-on warning`; `--json` gives
`{files: [{file, ok, error, notes, warnings}], errors, warnings}`.

`tb a11y [paths...]` reads app source (no device) and lists elements tb cannot detect or tap,
grouped by file, one per line: `:line Kind "label" #id ✗ check-name`. Each check that failed
is explained once at the end (`✗ check-name: problem. Fix: …`). With nothing wrong it prints
`ok: N files checked, …`. `✗` fails (exit 1), `!` is a likely problem (fails only with `--fail-on warning`).
Options: `--changed` (only lines added since `--base`, default the merge base with main),
`--format text|json|github`, `--rules`. Silence a line with a `tb-a11y-ignore[: rule-id]`
comment on it or the line before. `--fix` adds the missing selector in place (`android:id`,
React Native `testID`, web `data-testid`, Compose `testTag` once `testTagsAsResourceId` is
set), named after what the element does and unique in its file; `--dry-run` shows the edits
without writing. Flutter labels, warnings and elements a loop repeats (`.map()`, `v-for`,
`{#each}`, Compose `items { }`/`forEach`/`for`: they need a per-item id) are left for a
person (`left FILE:LINE …: why`).

A folder run uses the folder's `config.yaml` (flows globs, include/exclude tags,
execution order); see [flows.md](flows.md#workspaces).

## Drive a device

| Command | What it does |
|---|---|
| `tb devices` | List Android devices, iOS simulators and Chrome. |
| `tb open [-d DEVICE] [--name alice] [--app ID]` | Connect, optionally name the device and set its app. |
| `tb look [-d alice] [--shot] [--all] [--json] [--scale 0.5]` | The screen, one line per element, laid out (see below). `--shot` adds a screenshot path; `--all` appends every raw node; `--json` the same listing as JSON. |
| `tb do [-d alice] '<command>' ...` | Run flow steps (refs allowed); prints `ok`/`FAILED` with timing per step, then what changed. |
| `tb do - < steps.yaml` | Steps from stdin. |
| `tb wait [-d alice] <text> [--gone] [--timeout ms]` | Wait (on device events) until text appears, or with `--gone` disappears. |
| `tb watch [-d alice] [--until text] [--timeout ms]` | Stream screen changes as they happen. |
| `tb shot [-d alice] [path] [--scale 0.5]` | Save a screenshot. |
| `tb save <file.yaml> [-d alice]` | Write the steps that passed as a flow. |
| `tb save --preview [file.yaml] [-d alice] [--json]` | Inspect the recorded YAML and warnings without writing a file. |
| `tb replay <file.yaml> -d DEVICE [--json]` | Verify one saved flow once on a specific test device. |
| `tb log` / `tb clear [-d alice]` | Show / reset the recorded steps. `tb log` answers during a running `tb do`; `tb clear` waits for it. |
| `tb close [-d alice \| all]` | Disconnect a device (or all), stopping tb's agent on it. |
| `tb release [-d DEVICE \| all]` | Free devices for other test tools: stop any tb agent left on them (not on a device another tb is using). |
| `tb hierarchy [-d DEVICE] [--json]` | The screen outline, as `tb look` prints it, without a session. `--json`: every raw node. |
| `tb agent-guide` | A portable CLI usage guide for coding agents. |
| `tb mcp` | The same as MCP tools over stdio (`devices`, `open`, `look`, `run`, `wait`, `save_flow`, `close`). The CLI is enough; use MCP only if a client needs it. |
| `tb daemon stop` | Stop the background process. |

### Preview and replay

Preview JSON contains `yaml`, `steps`, and `warnings`. Each warning has a `code` and
`message`, plus a one-based `step` when it refers to a command. The text form puts
warnings above the YAML as comments. Pass the intended output file to resolve relative
references from the directory where the flow will be saved. Preview flags syntax issues,
coordinate gestures, selectors that depend on match order, text read as an unintended
regex, optional assertions, a missing fresh launch, and a missing assertion. Use
`tb locate --json` to check live selector ambiguity.

Recording keeps selectors stable: `@refs`, typed points and indexed selectors are saved as a
selector that finds the same element without coordinates or an index (`{text: "Edit",
childOf: {containsChild: "Eggs"}}`, `{text: "Edit", below: "Eggs"}`, `{childOf: {id:
"toolbar"}}`) when the last listed screen is still current; otherwise as typed. The first of
several matches gets the same treatment: a bare `"Edit"` that finds it only by coming first
is kept only when nothing stable tells it apart.

Replay requires a single-device flow whose first executed command is a required
`launchApp` with app restart enabled. `launchApp: {clearState: true}` resets app data at
launch. Close an open interactive session on the target after saving, then pass a serial,
UDID, or unique device name to `-d`. The interactive session alias belongs to the daemon.

Replay accepts `-e KEY=VALUE` for variables, `--headless` for Chrome, `--debug-output DIR`
for traces and failure artifacts, and `--json` for the test summary. On a failing replay,
read the error and open the artifacts in `flows[].debugDir` before deciding whether to
change the flow or the starting state. Replay uses zero retries.

### Reading `tb look`

```
ios 390x844 com.example.app, 18 elements
@1 Button "Back" #BackButton
@4 Field "Search items" hint="Search items" focused
column scrollable (24 more below):
  @12 Button "Apples" #item_checkbox_42 unchecked
  *@13 Button "Pears" #item_checkbox_43 checked
@15 Switch "Notifications" #notify_switch checked
row:
  @19 Button "Cancel" #cancel
  @20 Button "Save" #save disabled
```

`@N` ref, kind, label (text or accessibility label), `#id`, then state: `hint=`, `value=` (an
iOS element's accessibility value, such as a stepper's count, when it has a label), `focused`,
`checked`/`unchecked`, `selected`, `disabled`, `scrollable`. Refs are valid until the screen
changes; use them in `tb do` (`tapOn: @12`), never in saved flows.

Indentation shows layout, only where it adds something. Unindented lines stack top to
bottom. A header groups what is under it: `row:` (side by side), `grid 3 cols:`, `column:`
(a stack inside a row), `... scrollable:` (scroll it to see more), `#id` when the container
has one, and `dialog:` / `app:` when a system dialog is over the app. Lines indented under
an element are inside it (the texts in a tappable card). Containers that hold one thing are
left out, and siblings laid out flat are grouped into rows by where they sit.

Scroll areas say what they hold out of view, after the header or the element line:
`(24 more below)`, `(3 more above, 24 more below)`, `(more to the right)`. A number counts
elements (on Android: list rows). "more" without a number: it can scroll that way, count
unknown. On Android the counts come from the list's size and "can scroll" state; lists that
report neither show nothing. A last line `(N more below)` with no indent is off-screen
content outside any scroll area, such as the rest of a web page.

`*` before a ref marks an element that was not on the screen at your last `tb look` or
`tb do`. `tb do`'s own diff uses `+` for these; `*` shows up in `tb look`, in `tb watch`, and
when `tb do` prints the whole new screen. Nothing is marked on a first look, in another app,
or when everything is new.

### `tb look --json`

The same listing as data:

```json
{"platform": "android", "width": 1080, "height": 2400, "app": "com.example.app",
 "elements": [{"ref": 13, "kind": "Button", "label": "Pears", "id": "item_checkbox_43",
               "states": ["checked"], "new": true, "bounds": [0, 640, 1080, 760]}, ...],
 "blocks": [{"layout": "column", "scrollable": true, "more": {"below": 24},
             "children": [{"ref": 12}, {"ref": 13}]},
            {"layout": "row", "children": [{"ref": 19}, {"ref": 20}]}],
 "more": {"below": 3}, "keyboard": false}
```

`blocks` is the outline: a block is an element (`ref`, look it up in `elements`) or a group
(`layout`: `row`, `grid` with `cols`, `column`; optional `id`, `scrollable`, `more`), with
`children` under it. With a system dialog over the app, the top blocks are windows
(`"window": "dialog"` / `"app"`). In `more`, -1 means more out of view with the count unknown.

`tb hierarchy` prints the same outline without a session (fresh refs, no `*`);
`tb hierarchy --json` dumps every raw node instead.

### Reading `tb do`

Each step prints `ok` or `FAILED` and its time, then the change: `+` new elements, `~`
changed ones, `-` gone ones, or the whole new screen when most of it changed. A failed step
prints the screen and a `similar:` line with near matches.

## Environment variables

| Variable | Effect |
|---|---|
| `TB_DEBUG=1` | Log agent calls, covers steered around and dialogs answered to stderr. |
| `TB_HOME` | State directory (default `~/.testbuddy`). |
| `TB_CHROME` | Path to Chrome/Chromium for web flows. |
| `TB_WEB_HEADLESS=1` / `TB_WEB_SIZE=WxH` | Same as `--headless` / `--screen-size`. |
| `TB_IOS_FAST_ANIMATIONS=0` | Keep normal UIKit animation speed on iOS. |
| `TB_IOS_PROBE=0` | Do not inject the change probe into iOS apps (slower waits). |
| `TB_SILENT_MS` | The safety net that re-reads a screen when a device sends no events (default 1500, doubling); `0` turns it off. |
| `NO_COLOR` | Plain output. |

Variables passed to every flow: `TB_FILENAME` (the flow file's name), `TB_DEVICE_UDID`,
`TB_SHARD_ID` (1-based) and `TB_SHARD_INDEX` (0-based), and `TB_ROLE` in multi-device
flows. Maestro's names for these (`MAESTRO_FILENAME`, ...) are set too, and `MAESTRO_*`
variables in the environment reach flows, as under Maestro.
