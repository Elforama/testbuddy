# tb command reference

Exit status for every command: **0** ok, **1** a command or flow failed, **2** bad input.
`-d` takes a session name (from `tb open --name`), a serial/UDID or a device name, and defaults
to `TB_DEVICE`; for the session commands (`look`, `do`, ...) it is optional with one device
connected, and with several tb asks which. A flag's
value is checked before anything runs: a number flag takes a number, an on/off flag given a
value takes `true` or `false` (`--json=false`), and anything else is bad input.
`tb test` and `tb open` need the platform, `-p android|ios|web`, unless a device is named (`-d`,
`--device`, `TB_DEVICE`) or the flows name it (`url:` for the web, `devices:` roles; for `tb open`,
an `--app` URL); tb does not pick one for you. A `TB_DEVICE` that names neither a connected device
nor an open session does not count.

## Run tests

`tb test <flow files or folders> [options]`

With no phone running and a flow that needs one, `tb test` starts one of the platform asked
for: with `-p android` the first Android emulator (AVD) it finds, with `-p ios` (on a Mac) a
shut-down iPhone simulator of the newest iOS, without its window. It says which on stderr, waits for the boot, and leaves the device running.
Not with `--device`, shards, a multi-device flow, or on CI (`CI` / `TB_CI` set).

| Option | Meaning |
|---|---|
| `-e, --env KEY=VALUE` | Set a variable (repeatable). A flow's own `env:` is applied after it, so write overridable defaults there as `KEY: ${KEY \|\| 'default'}`. |
| `-p, --platform android\|ios\|web` | The platform to run on: required unless a device is named (`--device`, a connected `TB_DEVICE` or a session's name) or every flow names its platform (`url:`, `devices:` roles). |
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
| `--from N` | One flow, on a phone (not the web): start at its Nth step, on the screen the device is on now; `onFlowStart` and the steps before N do not run (the console and reports show them as skipped, so step numbers match the file). A failed run says the step to pass (`failed at step 12: to run this flow from there, pass --from 12`). |
| `--wait-for-device TIME` | A device another tb holds (another run, an open session): wait up to TIME (`5m`, `90s`) for it instead of failing at once. |
| `--keep-animations` | Do not turn Android animations off during the run. |
| `--keep-agents` | Leave tb's agent running on each device after the run: the next run starts faster, but other test tools cannot use the device until `tb release`. |
| `--headless` | Web: Chrome without a window. |
| `--screen-size WxH` | Web: browser window size (default 1280x800), each side from 200 to 10000 pixels. Chrome on macOS makes a window at least 500 wide; the flow's `log.txt` says so when the page came out wider than asked. |
| `--json` | Print a JSON summary: `passed`, `failed`, `skipped` and `flows`, each with `name`, `file`, `device`, `status` (`SUCCESS`, `ERROR`, `SKIPPED`), `seconds`, `error` (the message; for a skipped flow, why it did not run), `debugDir` (a failed flow's artifacts, `screen.txt` among them), `timeline` and `videos` (with `--timeline` / `--record`). The failure's screen listing goes to stderr and `screen.txt`, not the JSON. |
| `--record` | Record each flow's screen from start to finish: `recording.mp4` in its debug folder, in the JSON summary (`videos`), the HTML report (a player), JUnit (`video` property, `[[ATTACHMENT\|path]]` in system-out) and the timeline ("▶ Video" per step). Web needs `ffmpeg`. A device that cannot record does not fail the flow. |
| `--annotate[=TIME]` | With `--record`, for a video a person will watch (one to attach to a PR). A step that taps or swipes is held until a second has passed since its touch, so its result shows under its own caption; another step that acts on the app (input, launch, ...) and took less than a second is held until it has lasted a second, and a longer one is not lengthened. `--annotate=TIME` (or `--annotate TIME`) sets that time (`0.5s`, `3s`, seconds: `2`; up to a minute; `0` for no holds). The video then gets a ring where each tap, double tap and long press landed, moving along each swipe, and a band added below the recording that names the step that ran (also written beside it as `recording.srt`), so the text never covers the app. tb draws them and `ffmpeg` (on PATH, or `TB_FFMPEG`) places them, at a steady 30 frames a second, on every platform; without ffmpeg the video is kept as recorded and the log says why. tb times them from when the device says its video began, then checks them against the video: when the touches line up with the picture's changes only shifted (over 150 ms), it moves them and logs the shift. Not for ordinary test runs. `tb replay --annotate` too. |
| `--events-port N` | With `--annotate`: while the run lasts, tb takes events for the videos at `POST http://127.0.0.1:N/event` (default 7357; `0` for none). Each event sent while a flow records shows in the video's event log, a band below the step's text: one row per event with its time, the newest highlighted, each up for 2 s. A burst gets a row per event, as many as the run's busiest moment needs (up to 6); a bigger burst holds the video's frame ("paused for events") until each event has been up a second, so none goes by unread (the video then runs longer; the rows keep the real times). Events near the end hold the last frame ("paused for events") until each has been up its 2 s. tb stamps each event as it arrives, by the clock it times the captions by, so the sender needs no clock. An event sent after its flow began but before the video started (an app logging on launch), or between two flows on a device (up to 30 s), shows at the video's first frame with its time before 0; when the run ends, tb says how many events are in no video. Send from a script with `tb mark TEXT` (or `tb mark -`, a line an event), or from the app's debug build: a text body (a line an event) or JSON (`"text"`; a list of events; or a tracking call as it is, `{"event": "purchase", "properties": {...}}`). `"device"` (or `?device=`) names the device it is for (serial, name or role); one that names none is for every device recording. An Android device reaches it on its own `localhost` (tb sets up `adb reverse`), a simulator and a web page (CORS allowed) on the computer's; the URL is also in `$TB_EVENTS_URL` and in the flows' `${TB_EVENTS_URL}` (to hand to the app in `launchApp` arguments). Text in scripts the built-in font lacks (CJK, Arabic, Hebrew, symbols) is drawn with a font of the computer's that has it, or as `[U+8D2D]` if none does (emoji usually: colour emoji fonts have no outlines); control characters and colour codes are left out. The events are written beside the video as `recording.events.json`; a held video has no sound. An Android debug build must allow cleartext HTTP to `127.0.0.1`; a physical iPhone cannot reach it; `curl` needs `--data-binary` for a text body of several lines. `-e TB_EVENTS_URL=...` gives the flows another URL. A port another run holds: the run goes on without events. Without `--annotate`, `--events-port` is refused. |
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
| `tb devices` | List Android devices, iOS simulators and Chrome, and who has each phone or simulator: `session: NAME` or `connected` (this daemon's), `closing` (this daemon's, its connection dropped), `in use: tb test flows/ (pid …)` (another tb process: a run, another `TB_HOME`'s session), or `free`. `--json` has `session`, `closing`, `in_use` (the holder) and `free` (left out where tb cannot tell: browsers, `TB_NO_DEVICE_LOCK=1`). |
| `tb open -p PLATFORM \| -d DEVICE [--name alice] [--app ID]` | Connect, optionally name the device and set its app. |
| `tb look [-d alice] [--shot] [--all] [--json] [--scale 0.5]` | The screen, one line per element, laid out (see below). `--shot` adds a screenshot path; `--all` appends every raw node; `--json` the same listing as JSON. |
| `tb do [-d alice] [-e K=V] [--json] '<command>' ...` | Run flow steps (refs allowed); prints `ok`/`FAILED` with timing per step, then what changed. `--json`: `exit`, `steps` (`description`, `status`, `duration_ms`, `error`, and on the failed step a `reason`), and on failure the `screen`, the `query` that matched nothing (the selector's text, or its id when it has no text) and the `similar` elements. |
| `tb do - < steps.yaml` | Steps from stdin. |
| `tb wait [-d alice] <text> [--gone] [--timeout ms]` | Wait (on device events) until text appears, or with `--gone` disappears. |
| `tb locate [-d alice] [--json] <text, @ref or {selector}>` | Every element a selector matches on the current screen and the one a tap would pick (`chosen`): check a selector is unambiguous before saving it. |
| `tb watch [-d alice] [--until text] [--timeout ms]` | Stream screen changes as they happen, each diff headed `+<time since start>`. It also re-reads after 1.5 s (then every 3 s) of silence, so `--until` sees content the device does not report (an embedded Safari view, an OAuth sheet). |
| `tb shot [-d alice] [path] [--scale 0.5]` | Save a screenshot. `--scale` (also on `tb look`) is from 0 to 1; 0 and 1 mean full size. |
| `tb save <file.yaml> [-d alice]` | Write the steps that passed as a flow. |
| `tb save --preview [file.yaml] [-d alice] [--json]` | Inspect the recorded YAML and warnings without writing a file. |
| `tb replay <file.yaml> -d DEVICE [--json]` | Verify one saved flow once on a specific test device. |
| `tb log` / `tb clear [-d alice]` | Show / reset the recorded steps. `tb log` answers during a running `tb do`; `tb clear` waits for it. |
| `tb close [-d alice \| all] [-p ios] [--force]` | Disconnect a device (or all; with `-p`, all on that platform), stopping tb's agent on it. `all` closes the caller's sessions (`TB_SESSION_OWNER`) only; with `--force`, every agent's (and `-d` one of another agent's). A device named without `-d` (`tb close emulator-5554`) is that device. Waits for a command running on the device; to stop a stuck one, Ctrl-C it or run `tb daemon stop`. |
| `tb release [-d DEVICE \| all] [-p ios] [--force]` | Free devices for other test tools (other agents' sessions, by `TB_SESSION_OWNER`, only with `--force`): stop any tb agent left on them (not on a device another tb is using). With `-p`, only that platform's devices and sessions. A device named without `-d` (`tb release emulator-5554`) is that device; with no device named, all of them. |
| `tb hierarchy [-d DEVICE] [--json]` | The screen outline, as `tb look` prints it, without a session. `--json`: every raw node. |
| `tb mark [-d DEVICE] <text>...` | Send events to a running `tb test --record --annotate` (see `--events-port`): each shows in the videos' event log at the time tb got it; several texts are one burst. `tb mark -` reads a line an event. `--url` names the endpoint (default `$TB_EVENTS_URL`, else the running run's). |
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

In `tb do`, an id can be written the way `tb look` prints it: `tapOn: #login` is
`tapOn: {id: login}`. It works as the whole value of a command or key that takes an
element (`tapOn`, `doubleTapOn`, `longPressOn`, `assertVisible`, `assertNotVisible`,
`copyTextFrom`, `visible`, `notVisible`, `element`); anywhere else a `#` still starts a YAML
comment. It is saved as the id selector.

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
prints the error, then a `similar:` line: up to 5 elements on the screen now whose text or id
shares a word of 4+ letters with the selector that matched nothing (its text, or its id when
it has no text), or has a word one typo away from one of its words of 5+ letters; for a tap
as for a failed `assertVisible` or `extendedWaitUntil: {visible: ...}`. Each is a
`tb look` line, so retry with its `@ref`, text or `#id`; `partly off-screen` and
`covered by ...` on it say why it could not be used as it is.

With `--json`, the failed step carries a `reason`:

| `reason` | Means | Try |
|---|---|---|
| `not_found` | The selector matched nothing on the screen. | A `similar` element; else `tb look`. |
| `screen_changed` | The element of your last look was replaced, the app left the foreground, or the screen changed while tb scrolled back to it. | `tb look`, then act on the new screen. |
| `assertion_failed` | An assertion or `extendedWaitUntil` condition was false. | A `similar` element if it was a visibility check; else the app is in another state. |
| `other` | Anything else (script error, device error, bad value, a screen tb could not read): read `error`. | |

## Environment variables

| Variable | Effect |
|---|---|
| `TB_DEBUG=1` | Print the step log (as in `log.txt`) and agent calls to stderr as the run goes. |
| `TB_NO_DEVICE_LOCK=1` | Do not lock devices against other tb runs. |
| `TB_HOME` | State directory (default `~/.testbuddy`). |
| `TB_CHROME` | Path to Chrome/Chromium for web flows. |
| `TB_WEB_HEADLESS=1` / `TB_WEB_SIZE=WxH` | Same as `--headless` / `--screen-size`; a bad `TB_WEB_SIZE` fails opening the browser. |
| `TB_IOS_FAST_ANIMATIONS=0` | Keep normal UIKit animation speed on iOS. |
| `TB_IOS_PROBE=0` | Do not inject tb's change probe into iOS apps it launches (an app that breaks with it); waits are slower. |
| `TB_SILENT_MS` | How long a wait hears nothing from the device before it re-reads the screen anyway (default 1500); `0` turns the re-reads off. |
| `NO_COLOR` | Plain output. |

`-d` defaults to `TB_DEVICE` for the session commands (`look`, `do`, `open`, `close`, ...), so
an agent can be pinned to its device once: a serial or device name, or the name of a session
already open. A command with `-p` does not use it. With no device named and several phones or
simulators connected, a session command is refused and lists them, rather than taking one;
the exception is one session open, which `look`, `do` and the rest use (`tb open` does not).
In a `tb test` run of one device at a time (no `--device`, `-p`, sharding or multi-device flow),
`TB_DEVICE` names the device tried first and, with no `-p`, the platform (a serial or device
name of a connected device, or a session's name for the device it has open; anything else gets
the run refused):
your own idle session on it gives it back, and when it cannot run a flow or is in use (another
agent's session, or yours running a command) the flow goes to another of the same platform. It is not used when every flow names its platform
(`url:`, `devices:` roles). When the run had
a choice of phones or simulators, it says which one it took and why not the others before its
first flow there.

Commands on one session run one at a time. A command sent while another runs there waits for
it, and after a second says so on stderr ("waiting for the command running on a1: tb do ...
(started 5s ago)"), then again when it starts; with `--no-wait` it fails at once with that
message instead.

`TB_SESSION_OWNER` makes the sessions a caller opens its own: set it once per agent (any name).
Another agent (another owner, or a caller with none) is refused on them ("emulator-5750 is
session "alice" of another agent: use another device"), a command with no `-d` does not use
them, `tb close all` leaves them (unless `--force`), `tb test` does not take their device back,
and `tb devices` marks them "(another agent's)". Sessions opened with no owner are anyone's, as
before.

Variables passed to every flow: `TB_FILENAME` (the flow file's name), `TB_DEVICE_UDID`,
`TB_SHARD_ID` (1-based) and `TB_SHARD_INDEX` (0-based), and `TB_ROLE` in multi-device
flows. Maestro's names for these (`MAESTRO_FILENAME`, ...) are set too, and `MAESTRO_*`
variables in the environment reach flows, as under Maestro.
