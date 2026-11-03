# Agent protocol

The host (`tb`) talks to one on-device agent per device over a single TCP connection.
Android: the agent listens on the abstract unix socket `testbuddy-agent`, reached through
`adb forward tcp:0 localabstract:testbuddy-agent`. iOS simulator: the XCUITest runner listens
on `127.0.0.1:<port>` directly (the simulator shares the Mac's loopback).

## Framing

One JSON object per line (UTF-8, `\n`-terminated), both directions. A message that carries
binary data (a screenshot) has a `"bin": N` field; exactly N raw bytes follow its newline.

## Messages

**Requests** (host → agent): `{"id": 7, "op": "tap", ...params}`. `id` is unique per connection.

**Responses** (agent → host): `{"id": 7, "ok": true, ...result}` or
`{"id": 7, "ok": false, "error": "human readable reason"}`. Exactly one per request. The agent may
answer requests in order; the host sends one request at a time per device in practice, but events
keep flowing while a request runs.

**Events** (agent → host, unsolicited, no `id`):

| Event | Fields | Meaning |
|---|---|---|
| `hello` | `platform`, `version`, `w`, `h`, `seq`, platform extras | First message on every connection. The host waits for it before sending requests. |
| `changed` | `seq`, optional `pkg` | The UI changed. Coalesced (at most one per ~30 ms). |
| `settled` | `seq` | No change for the quiet window since change `seq`. |

`seq` is the agent's UI change counter: it increases on every UI change the agent observes. The
host never polls; it waits for `changed`/`settled` events, or asks an op that waits on the device.

## Ops

Coordinates are in the same space as node bounds (Android: pixels; iOS: points).

| Op | Params | Result |
|---|---|---|
| `ping` | | `seq` |
| `info` | | `w`, `h`, `keyboard` (bool), platform extras |
| `tree` | `all` (bool) | `seq`, `keyboard`, `windows: [Window]` (see below) |
| `tap` | `x`, `y` + settle params | action result |
| `doubleTap` | `x`, `y` + settle params | action result |
| `press` | `x`, `y`, `ms` (hold) + settle params | action result |
| `swipe` | `x1`,`y1`,`x2`,`y2`, `ms` (duration), `drag` (bool) + settle params | action result. `drag`: a drag that does not fling, the content stops at the lift (Android: back-dated moves sent at once, then a still pointer and a CANCEL for up/down drags; iOS: a short kick, a fast stretch and a slow tail). `ms` is then unused |
| `key` | `code` (Android key code), `long` (bool) | action result. iOS maps: 66 → return, 67 → delete, 3 → home, 26 → lock, 24/25 volume |
| `type` | `text` | action result. Types into the focused field. |
| `erase` | `n` | action result. Deletes up to n characters before the cursor. |
| `clear` | | `cleared` (bool): empties the focused field in one step when possible |
| `settle` | `since` (seq), `noChange` (ms), `timeout` (ms) | `result`: `changed` / `no_change` / `timeout`, `seq` |
| `quiet` | `timeout` (ms) | `quiet` (bool): true once no change is pending |
| `awaitPackage` | `pkg`, `timeout` (ms) | `found` (bool): the app owns the foreground. Event-driven. |
| `foreground` | | `pkg`: the foreground app's package / bundle id |
| `screenshot` | `scale` (0–1), `format` (`png`/`jpeg`), `quality` | `w`, `h`, `format`, binary image |
| `rotate` | `to`: `portrait` / `landscapeLeft` / `landscapeRight` / `upsideDown` | |
| `setQuiet` | `ms` | the quiet window, ms |
| `exit` | | the agent exits |

Android only: `sh` (`cmd`, `timeout` → `code`, `out`): runs a shell command as the shell user.
`animations` (`on` bool): the host turned animations off or on (the agent reads the setting at
start); see [architecture](architecture.md#screens-that-never-stop-moving).

iOS only: `launch` (`bundleId`, `args` [string], `env` {string:string}), `terminate` (`bundleId`),
`activate` (`bundleId`), `appState` (`bundleId` → `state`). More in [iOS](#ios) below.

### Settle params (on every action op)

| Param | Default | Meaning |
|---|---|---|
| `settle` | `true` | `false`: answer as soon as the input is injected |
| `react` | `false` | `true`: answer `changed` as soon as the UI starts changing, without waiting for it to settle (the host waits for that when it next needs a still screen) |
| `noChange` | 250 | ms to wait for the UI to start changing before answering `no_change` |
| `timeout` | 5000 | the longest settle wait, ms |

Action result: `result` (`changed` / `no_change` / `timeout`), `injectMs`, `settleMs`, `seq`.

### Window

```json
{"id": 12, "type": 1, "active": true, "focused": true, "pkg": "com.app", "title": "...",
 "b": [l, t, r, b], "nodes": [Node, ...]}
```

`type`: 1 application, 2 input method (keyboard), 3 system, 4 accessibility overlay.
Windows are ordered top-most first.

### Node

Nodes are a pre-order list with depths (no nesting); the host rebuilds the tree.

```json
{"d": 2, "b": [l, t, r, b], "c": "android.widget.Button", "t": "Sign in", "cd": "desc",
 "id": "com.app:id/go", "h": "hint", "e": "error", "sd": "state", "v": "value", "f": 4099}
```

Empty strings and zero flags are omitted.

| Key | Android | iOS |
|---|---|---|
| `c` | class name | element type name, e.g. `Button`, `StaticText`, `TextField` |
| `t` | text | title, else value (fields: their value) |
| `cd` | content description | label |
| `id` | resource id | identifier |
| `h` | hint text | placeholderValue |
| `v` | | value (raw) |

Flags `f` (bits): 1 clickable, 2 enabled, 4 focused, 8 scrollable, 16 checked, 32 selected,
64 checkable, 128 password, 256 editable, 512 long-clickable, 1024 focusable,
2048 showing hint, 4096 visible to user.

## iOS

The agent is an XCUITest runner (`agents/ios`), listening on `127.0.0.1:<TB_PORT>`. Points, not
pixels. How it learns about changes, and what is pushed versus read, is in
[research/ios-events.md](research/ios-events.md).

**hello / info extras**: `agent` (agent version), `build` (the build id the host started it with,
`TB_BUILD`; `version` carries it too), `udid` (the simulator), `pid`, `axHook` (bool: the
accessibility notification hook is installed). `info` also has `seq`, `settledSeq`, `quietMs`,
`axRegistered`, `treeMode`; `keyboard` costs a query, pass `"keyboard": false` to skip it.

**Windows**: the foreground app (type 1, `id` its pid, `pkg` its bundle id, `title` its name); the
keyboard (type 2) split out of the app's tree when it shows; a SpringBoard alert over the app
(type 3, permission prompts and the like). SpringBoard alone (the home screen) is a type 1 window
with `pkg` `com.apple.springboard`. `tree` also returns `timing`: `snapMs`, `walkMs`, `nodes`, `mode`.

**Nodes**: `c` is the XCUIElement type name (`Button`, `StaticText`, `TextField`, `SecureTextField`,
`Switch`, `Cell`, `Other`, `Key`, ...). Flags: clickable (1) for buttons, cells, links, keys,
switches, segmented controls and text fields; enabled (2); focused (4) = keyboard focus; scrollable
(8) for scroll views, tables and collection views; checked (16) for a switch, toggle or checkbox
whose value is `"1"` (or React Native's spelled `"checkbox, checked"`); selected (32);
checkable (64); password (128) for secure fields; editable (256) and focusable (1024) for text
inputs; showing hint (2048) when a field's value is its placeholder; visible (4096) unless the
frame is empty or outside the app's frame.

**Action ops**: `tap` takes an optional `hold` (ms, default 18; the engine sends 10, or 20 for
switches and other tracking controls, alert and sheet buttons, and taps at a point). `type`
no longer fails when no field reports keyboard focus: it types at the first key's slow rate. Results add `probed` (bool: the
foreground app has the probe, so every change is pushed) and, rarely, `seenBy: "read"`: an app
without the probe changed in place, seen by one read after it reported idle (see ios-events.md).
`key` maps Android codes: 66 return, 67 delete, 61 tab, 111 escape, 3 home, 26 lock, 24/25
volume; 4 (back) is an error.

**iOS-only ops**

| Op | Params | Result |
|---|---|---|
| `launch` | `bundleId`, `args` [string], `env` {string:string} | `state`. XCTest's launch (no probe; the host launches with `simctl` instead) |
| `terminate` / `activate` | `bundleId` | |
| `appState` | `bundleId` | `state`: XCUIApplication.State (1 not running, 2 suspended, 3 background, 4 foreground) |
| `hideKeyboard` | settle params | action result + `hidden`. The probe resigns the first responder; without it, a short drag (scroll views dismiss the keyboard on drag) |
| `treeMode` | `mode`: `public` / `fast` | the snapshot method (`public` = XCUIApplication.snapshot(), the default; `fast` = trimmed attributes through testmanagerd) |

Diagnostics (research; not used by the engine): `methods` (`class`, `protocol` bool → selectors with
type encodings), `axRegister` / `axUnregister` (`ids` → `ok`, `failed`), `axChange` (`ids` that count
as a change), `axTrace` (`on`: push every raw notification), `axStats` (`counts` by notification id,
`-1` = probe; `registered`), `fgReport` (what each foreground lookup returns), `idle` (`cap` → `idle`,
`ms`: one app-idle round trip).

**Extra events** (only while `axTrace` is on): `ax` (`n`, `name`, `pid`, `bundle`, `ms`, `payload`),
`probe` (`pid`, `count`; count 0 = the probe loaded), `appState` (`pid`, `bundle`, `state`),
`orientation` (`kind`: `device` / `interface`, `value`).

`rotate` returns once the app has turned (UIKit's OrientationChanged notification, at the end of
the rotation animation) and settled; `foreground` also returns `active` (every app the
accessibility client lists, with pids) and `ms`.

