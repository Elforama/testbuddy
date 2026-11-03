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
| `--debug-output DIR` | Where each flow's `log.txt` (what tb did, step by step) and its failure files (screenshot, screen listing, hierarchy, steps) go. |
| `--flatten-debug-output` | Write debug files straight into `--debug-output`. |
| `--retries N` | Re-run a failed flow up to N times (on another device if one is free). |
| `--keep-animations` | Do not turn Android animations off during the run. |
| `--headless` | Web: Chrome without a window. |
| `--screen-size WxH` | Web: browser window size (default 1280x800). |
| `--json` | Print a JSON summary (failures include the screen listing). |
| `--quiet` | Print only failures and the summary. |

`tb check <flows or folders...>` parses flows (and the sub-flows they run) without a device; a
folder means every .yaml in it and its sub-folders but `config.yaml`.

A folder run uses the folder's `config.yaml` (flows globs, include/exclude tags,
execution order); see [flows.md](flows.md#workspaces).

## Drive a device

| Command | What it does |
|---|---|
| `tb devices` | List Android devices, iOS simulators and Chrome. |
| `tb open [-d DEVICE] [--name alice] [--app ID]` | Connect, optionally name the device and set its app. |
| `tb look [-d alice] [--shot] [--all] [--json] [--scale 0.5]` | The screen, one line per element. `--shot` adds a screenshot path; `--all` appends every raw node; `--json` the structured listing. |
| `tb do [-d alice] '<command>' ...` | Run flow steps (refs allowed); prints `ok`/`FAILED` with timing per step, then what changed. |
| `tb do - < steps.yaml` | Steps from stdin. |
| `tb wait [-d alice] <text> [--gone] [--timeout ms]` | Wait (on device events) until text appears, or with `--gone` disappears. |
| `tb watch [-d alice] [--until text] [--timeout ms]` | Stream screen changes as they happen. |
| `tb shot [-d alice] [path] [--scale 0.5]` | Save a screenshot. |
| `tb save <file.yaml> [-d alice]` | Write the steps that passed as a flow. |
| `tb log` / `tb clear [-d alice]` | Show / reset the recorded steps. |
| `tb close [-d alice \| all]` | Disconnect a device (or all). |
| `tb hierarchy [-d DEVICE]` | The full hierarchy as JSON. |
| `tb agent-guide` | A short usage guide to paste into an AGENTS.md / CLAUDE.md. |
| `tb mcp` | The same as MCP tools over stdio (`devices`, `open`, `look`, `run`, `wait`, `save_flow`, `close`). The CLI is enough; use MCP only if a client needs it. |
| `tb daemon stop` | Stop the background process. |

### Reading `tb look`

```
ios 390x844 com.example.app, 18 elements
@1 Button "Back" #BackButton
@4 Field "Search items" hint="Search items" focused
@12 Button "Apples" #item_checkbox_42 unchecked
@15 Switch "Notifications" #notify_switch checked
@20 Button "Save" #save disabled
```

`@N` ref, kind, label (text or accessibility label), `#id`, then state: `hint=`, `focused`,
`checked`/`unchecked`, `selected`, `disabled`. Refs are valid until the screen changes; use
them in `tb do` (`tapOn: @12`), never in saved flows.

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
