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
`{"id": 7, "ok": false, "error": "human readable reason"}`. An error may add `"transient": true`:
the device, not the request, failed this time, and the host may ask again (the iOS agent's
launch readiness, and its typing that gave up before any key, below). Exactly one per request. The agent may
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
| `tree` | `all`, `fresh` (bool); Android diagnostics: `profile`, `prefetchChildren`, `includeHidden` (bool) | `seq`, `keyboard`, `windows: [Window]` (see below); optional Android `profile` |
| `tap` | `x`, `y` + settle params | action result |
| `doubleTap` | `x`, `y` + settle params | action result |
| `press` | `x`, `y`, `ms` (hold) + settle params | action result |
| `swipe` | `x1`,`y1`,`x2`,`y2`, `ms` (duration), `drag` (bool), `rest` (bool) + settle params | action result. `rest` (iOS; a swipe from an element, or between two points from a slider's thumb, sent only in CI mode): the finger stays still 200 ms before it moves, and creeps the last 8 pt over 1.5 s before it lifts, so an app that is behind on a slow runner sees where it went down and still gets moves after a stall of up to about a second (issues #127, #182). `drag`: a drag that does not fling, the content stops at the lift (Android: back-dated moves sent at once, then a still pointer and a CANCEL for up/down drags; iOS: a short kick, a fast stretch and a slow tail). `ms` is then unused |
| `key` | `code` (Android key code), `long` (bool) | action result. iOS maps: 66 → return, 67 → delete, 3 → home, 26 → lock, 24/25 volume |
| `type` | `text`, `verifyFirst` (Android bool), `replyMs` (iOS: see "iOS typing deadline"), `ci` (bool, see "CI mode") | action result. Types into the focused field. With `verifyFirst`, checks the first code point against fresh focused-field state, waiting on accessibility callbacks for up to 1 s and retrying once before sending the remainder; with `ci` too, a field not focused yet is first waited for, up to 1 s (issue #97). |
| `erase` | `n`, `replyMs` (iOS: see "iOS typing deadline", as for `type`), `ci` (iOS bool, as for `type`) | action result. Deletes up to n characters before the cursor. |
| `clear` | | `cleared` (bool): empties the focused field in one step when possible |
| `settle` | `since` (seq), `noChange` (ms), `timeout` (ms), optional `react` (bool, default false) | `result`: `changed` / `no_change` / `timeout`, `seq`; `react` answers on first change |
| `quiet` | `timeout` (ms) | `quiet` (bool): true once no change is pending |
| `awaitPackage` | `pkg`, `timeout` (ms) | `found` (bool): the app owns the foreground. Event-driven. |
| `foreground` | | `pkg`: the foreground app's package / bundle id |
| `screenshot` | `scale` (0–1), `format` (`png`/`jpeg`), `quality` | `w`, `h`, `format`, binary image |
| `rotate` | `to`: `portrait` / `landscapeLeft` / `landscapeRight` / `upsideDown` | |
| `setQuiet` | `ms` | the quiet window, ms |
| `exit` | | the agent exits |

Android Enter first uses an advertised IME action on a focused single-line editor (API 30+). Multiline fields, long presses and unsupported providers keep ordinary key injection. This avoids a keyboard consuming Enter only to commit/dismiss its composition; a handled editor action is never followed by another Enter. A field is multi-line when its accessibility node says so, or when the editor's keyboard settings (its `EditorInfo`, read afresh from the input method service only when Enter could become the action: `dumpsys input_method --proto`, about 15 ms, and the text dump only where that gives none, issue #143) ask for no Enter action or multi-line text: a multi-line Jetpack Compose field reports this only there (issue #32).

Android API 33+ Backspace uses an optional editor connection when the accessibility service is enabled. `editorTarget` returns `seq` and an optional `target` containing `pkg`, `window`, node `hashCode`, `id` and `cls`, and prepares action tracking without injecting input or serializing a screen. The host sends that descriptor through an ADB forward to `localabstract:testbuddy-editor` (direct shell-to-app sockets are blocked by Android). The service accepts only shell/root peers, matches the fresh focus and input-session identity, and sends one soft DEL DOWN/UP pair through the app's InputConnection. No field text is transported.

The editor reply is `sent`, `unavailable` (no dispatch), `rejected`, or `error`. Readiness waits wake on input callbacks, with a 1 s bound. The host waits on ordinary agent settle callbacks after `sent`. On `unavailable`, it passes the original descriptor as `expectedEditor` in the hardware `key` request. The shell checks fresh focus immediately before injecting. A rejected request or any uncertain reply fails without another delete. Missing editors, disabled accessibility support and older APIs retain the existing key path. The `key` reply reports `keyPath` (`hardware` or `editor-action`) for diagnostics.

When updating its installed accessibility helper, the driver checks package-manager results. A signature mismatch permits replacement of **only** `dev.testbuddy.agent`; other installation failures stop startup with their actual error.

Android also supports `hideKeyboard` (`timeout` in ms → `hidden` bool and `seq`). The agent checks for an input-method window before sending Back and waits for its disappearance on window callbacks. It sends no key when the keyboard is absent. With `pendingMs`, a keyboard not up yet may still be coming (for the field the last tap focused, or the screen it opened): when no read has found the keyboard up since the last tap, double tap or long press (within 10 s), the agent waits for one on window callbacks until `pendingMs` after the last action ended, or after the screen's last change while it keeps changing (at most twice `pendingMs`). One that shows and stays up for 100 ms is closed the same way (`late: true`; issue #175). The host sends 1000, or 2500 in CI mode.

Android screen reads enumerate windows once and derive `keyboard` from that same observation.
On API 33+, child-node reads use uninterrupted hybrid descendant prefetching by default;
`prefetchChildren: false` selects the previous child-fetch behavior for comparisons. Older
Android versions retain their existing child API. This changes acquisition only: complete
snapshots, fresh-cache reads and snapshot history are preserved.

With `profile: true`, an Android tree reply includes `profile` with `totalMs`, `cacheMs`,
`windowsMs`, `rootsMs`, `childrenMs`, `walkEncodeMs`, the corresponding `*Calls` counters,
and the number of serialized `nodes`. Root/child timings include cached and remote API calls.
`walkEncodeMs` is the remaining local traversal, cached-attribute access and JSON-writing time.
These timings exclude response string/UTF-8 conversion, socket transfer and host decoding.
Profiling is off by default and then reads no timing clocks.

If Android's direct focus lookup cannot observe an editable field before checked typing,
`type` returns `verifyUnavailable: true` without injecting text. The runner then uses its
full-screen first-key check, preserving support for custom accessibility widgets.

Android only: `sh` (`cmd`, `timeout` → `code`, `out`): runs a shell command as the shell user.
`animations` (`on` bool): the host turned animations off or on (the agent reads the setting at
start); see [architecture](architecture.md#screens-that-never-stop-moving).
`location` (`lat`, `lon`, `everyMs` (default 1000) → `providers`; or `off: true` → `off`): mock
location on Android 12+. The agent adds the shell's test providers (gps, network, fused) the
first time, sets the point on each, and sends it again every `everyMs` on a thread of its own
(the fused provider forgets a fix that does not come again), off the request path: the host
sends one request per point and nothing in between (issue #72). A new point replaces the
repeated one. The repeats end and the providers are removed on `off`, when the agent exits on
its own (`exit`, the idle exit), and 50 s after the connection that set the point closed, unless
a new connection set a point by then: the host sets it again on any new connection, so the
location holds through a drop (the grace outlasts the host's 45 s reconnect window and ends
before the agent's 60 s orphan exit). An agent stopped with a signal cannot: it lists the
providers it added in `/data/local/tmp/tb-location-providers` while they are on, and the host
removes the ones listed when it stops an agent (`tb release`, a new agent's start). The
providers are listed before they are added. `hello` carries `location` (bool): a point is on. A
host that did not set one (a run that follows a killed one, within the grace) sends `off` when it
connects. Fails when no provider can be added, or the point cannot
be set; providers that request added are removed again. It carries no request token: sending
a point again is harmless.

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
Android adds `clicked` (bool: a view reported a click during the action) and `clicksBefore`
(the agent's click count before it).

**Android touches into a new window** (issue #204). Android drops a touch sent into a window
just added whose input channel is not up yet (InputDispatcher: NO_INPUT_CHANNEL), and no app
sees it. The agent notes when each window is first listed (on `TYPE_WINDOWS_CHANGED`, at the
event's time; windows there when it started are old), and holds a `tap`, `doubleTap` or `press`
until the top-most window at its point is 100 ms old, or 400 ms with `ci` (bool, sent by the
host in CI mode). Other windows coming or going never hold it; with no window that young on
screen nothing is read. The reply's `windowWaitMs` says how long it held the touch (absent when
it did not). A `tap` whose touch the platform did not inject says so in `notInjected`: `down`
(the DOWN was refused and nothing else was sent: no app saw a tap, so the host sends it again
once, and fails the step if it is refused again) or `up` (the UP, injected waiting for the
result, was not delivered; the agent then sends a CANCEL so no touch is left down). After `up`
the DOWN may have reached the app, so the host does not send the tap again: the step fails,
saying the tap may or may not have taken effect.

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
| `pb` | | Optional sub-point bounds `[left, top, right, bottom]` when an edge is fractional. Screenshot cropping converts these to physical pixels. |

Flags `f` (bits): 1 clickable, 2 enabled, 4 focused, 8 scrollable, 16 checked, 32 selected,
64 checkable, 128 password, 256 editable, 512 long-clickable, 1024 focusable,
2048 showing hint, 4096 visible to user, 8192 can scroll forward (more below or to the right),
16384 can scroll backward (more above or to the left). The last two are Android only.

Android also sends, when the app reports them, `ci`: a list's `[rows, columns]` (-1 when
unknown) and `ii`: an item's `[row, column]` in its list.

## iOS

The agent is an XCUITest runner (`agents/ios`), listening on `127.0.0.1:<TB_PORT>`. Points, not
pixels. How it learns about changes, and what is pushed versus read, is in
[research/ios-events.md](research/ios-events.md).

**hello / info extras**: `agent` (agent version), `build` (the build id the host started it with,
`TB_BUILD`; `version` carries it too), `udid` (the simulator), `pid`, `axHook` (bool: the
accessibility notification hook is installed). `info` also has `seq`, `settledSeq`, `quietMs`,
`axRegistered`, `treeMode`; `keyboard` costs a query, pass `"keyboard": false` to skip it.

**Liveness (issue #48)**: the agent runs requests one at a time, and some take long (a launch
waits up to a minute). The hello, `ping` and `info` with `"keyboard": false` are answered at once
instead, even while another request runs: a runner that is only busy still answers the host's
probe and is not replaced. These answers carry `busy`, the op running (`""` when none),
`busyMs`, how long it has run, and `busyLimitMs`, when it counts as stuck (90 s, or the op's own
`timeout` plus 30 s if longer); `keyboard` is the one seen at the last tree read. Once the op has
run past its limit they wait behind it again, so silence to them means the runner is stuck, and
the host replaces it. The host waits for a busy runner of its build to finish its op before
using it, up to that limit.

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

**iOS launch readiness.** With `pid` and `readTree: true`, `awaitPackage` resolves for the
specific launched process once its accessibility tree is readable. It returns the normal tree
fields and generation in that response. The host can consume this tree once while the
generation is current. Foreground changes wake the waiter. An exited PID fails immediately
when observed. A system permission alert can be returned above the launched app. While the
app is not ready yet (XCTest's ui-testing error 10001) the agent keeps waiting. With `ci: true`
(sent only in CI mode) it also keeps waiting while the app is not answering accessibility yet:
XCTest's "Error getting main window" (automation-support error 8) with `kAXErrorIPCTimeout`
(issue #85), `kAXErrorServerNotFound` (issue #126) or `kAXErrorCannotComplete`; without it
those fail the launch at once. After one of the accessibility errors it reads again on the next
event or after 250 ms. If the wait ends on one of them, the error reply carries
`"transient": true`. In CI mode the host asks the same launch again (up to three asks, at least
0.5 s apart, within the first ask's reply timeout plus one more budget) when the answer is late,
the error is marked transient, or, from an agent that does not mark it, the error's text is
that "Error getting main window" family; every other error fails the launch at once. Outside
CI mode it asks again only after a late answer, once. Each retry is written to the run's trace.

**iOS targeted input.** For a unique named app button, `tap` can carry `targetID`,
`targetPID` and `targetSeq`. The iOS agent checks the live frame even when the tree generation
still matches, because layout can move before the change event arrives. In a
probed simulator app, a single armed touch at the requested point can also be withheld at
delivery when its unique accessibility frame has moved: off the point, or (issue #103) with its
middle more than half a point from where the agent's own check read it just before sending the
touch. A move that starts in that gap can leave the point on the frame while SwiftUI's button,
laid out where the move ends, does not take the touch. `targetMoving: true` drops the second
check, for a target that never stops moving; the runner sends it on the one newly resolved
attempt after a withheld tap. The arm is one-use and expires after a second.
The agent waits briefly for the probe's completion callback for that touch, because XCTest can
report completion before UIKit processes the touch ending. The probe acknowledges rejection
after consuming that touch through its end, with a flag (the top bit of its rejected state) when
it withheld the touch because the target moved. Multi-touch or incomplete identification yields
no rejection receipt. The agent returns `withheld: "preflight"` (its own check found the point
off the target), `"delivery"` (the probe did) or `"moved"` (the probe found the target moved
since the agent's read, issue #103) when no tap reached the app, permitting one newly resolved
attempt; after `"moved"` the runner makes it only in CI mode, and elsewhere fails the step,
saying the target was moving. A matching touch-driven UIKit
action can return `clicked: true`. SwiftUI gestures may provide no such action receipt; the
probe can instead report `targeted: true` when it observed the full touch that began at the
named button's live frame. When the probe saw that touch end cancelled (issue #131: the
system took it back before it lifted, so no button acted on it) and no control reported its
action, the agent returns `cancelled: true` instead. In CI mode the runner finds the target
again and taps it again, at most twice; elsewhere it fails the step, saying the touch was
cancelled before it ended. For consecutive probe-guarded taps, the runner can reuse an already
visible button after a fresh observation agrees on its identity, location, window, scroll
container, and screen structure. With only a touch receipt, it also checks that the next
button stays put. Other targets follow the usual settling path. The runner does not implicitly
replay an ambiguous guarded tap. For typing immediately after
a named editable-field tap, `type` carries the target ID and PID and checks keyboard focus
before sending characters. It can refocus the field once and waits on agent notifications.
With `ci: true` (CI mode) the refocus is a touch at the middle of the field's part on screen
through the same input path as `tap` (no XCTest wait for the app to go idle; another field's
keyboard is hidden first, and a field off screen is not touched, issue #46); without it, any
keyboard is hidden first and the field is tapped with XCTest's `XCUIElement.tap()`, bounded by
the op's deadline like every other wait.

**iOS typing deadline (issue #46).** `type` and `erase` carry `replyMs`, how long the host waits
for their answer. Every XCTest read they make (which field has the focus, the keyboard, the
field's frame) waits for the app on the agent's main thread, and an app whose main thread is
busy answers none: the agent gives up on such a read at its own deadline (2 s before `replyMs`,
less the time kept for the keys) instead of waiting with it. Before any key it answers
`"transient": true` with what it waited for ("typing did not start: the app did not answer
accessibility for 16.0 s (checking which field has keyboard focus); no keys were sent"). In CI
mode the host asks once more as a new request, noted in the trace; elsewhere that error fails
the step. Keys that went but were not confirmed are reported (how many of how many) and never
sent again: that error is not transient.

**CI mode (`ci`).** The agents cannot read the host's environment, so the host tells them when
it runs in CI mode (`internal/ci`: `TB_CI`, else `CI=true`) with `"ci": true` on the requests
whose behaviour it changes, and sends nothing for it otherwise: Android `type` with
`verifyFirst` (the focus wait), iOS `awaitPackage` with `readTree` (waiting on accessibility
errors) and iOS `type` and `erase` (the refocus touch). An agent treats a missing `ci` as off.

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


### Android mounted-cover readiness

`awaitCoverGone` takes `window` (accessibility window ID), `pkg`, `nodeId`, `class`,
`x`, `y`, and `timeout` (milliseconds). It returns `observed`, `gone`, and `seq`.
The agent observes a previously identified cover at the next input point, including
invisible nodes, and wakes on device events until that node no longer covers the
point. It does not stream repeated screen snapshots to the runner. The runner
refuses the tap if an observed cover persists through the timeout.

Normal `tree` reads still omit hidden nodes. Android's diagnostic-only
`includeHidden: true` includes them with their original visibility flag; `all`
continues to control window inclusion rather than node visibility.
