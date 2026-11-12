# TestBuddy architecture

TestBuddy runs YAML UI tests and interactive device sessions. The Go host owns flow
semantics, selection, recovery, and reporting. Platform code exposes screens, inputs,
and readiness signals. The Android and iOS agents run alongside the app under test.
The web driver controls Chrome directly and installs a helper in its pages.

```text
tb open / look / do / watch / save
    └─ local socket → tb daemon → session.Service → engine.Runner
tb test / replay
    └─ flow parser → scheduler + device pool → engine.Runner
                                                   │
                                               driver.Driver
                         ┌─────────────────────────┼──────────────────────┐
                  Android driver                iOS driver              Web driver
                  ADB port forward             localhost TCP          CDP WebSocket
                         │                         │                      │
                 shell agent + optional       XCUITest agent       Chrome tab + page.js
                 accessibility helper        + optional TBProbe
```

## Host components

| Component | Responsibility |
|---|---|
| [CLI](../cmd/tb) | Parses flags and dispatches to the local daemon or the in-process test runner. Owns text and JSON output, test reports, and failure artifact collection. |
| [Flow parser](../internal/flow) and [workspace](../internal/workspace) | Decode YAML into typed commands, resolve relative subflow and script paths, and validate command options. Workspace planning expands paths, orders flows, and applies tag filters for `tb test`. |
| [Session service](../internal/session) and [daemon](../internal/daemon) | Maintain open interactive sessions, one runner per session device, the most recent listing, and a recording. The daemon carries these requests over a local socket. |
| [Orchestrator](../internal/orchestrate) | Chooses eligible devices, acquires device locks, manages driver lifetimes, and schedules queued flows, shards, or multi-device roles. |
| [Runner](../internal/engine), [matcher](../internal/match), and [JS evaluation](../internal/js) | Evaluate variables and command options at runtime, resolve selectors against the current screen, sequence actions and assertions, and perform bounded recovery. |
| [Driver interface](../internal/driver), [screen model](../internal/ui), and [listings](../internal/look) | Give the runner a common screen and input model. The listing layer presents that model as compact text or JSON and derives selectors for interactive refs. |
| [Mobile transport](../internal/wire) | Multiplexes requests, replies, and pushed events on one mobile connection. The separate web transport uses Chrome DevTools Protocol. |

### A test from the CLI to the screen

1. The CLI parses the YAML header and commands. The header carries the application or web URL,
   environment, hooks, and any named device roles. A subflow is resolved relative to its
   declaring file. The runner evaluates expressions when their values are needed.
2. The scheduler matches the flow against connected devices and opens or reuses the
   appropriate driver. A single-device flow runs on one runner. A multi-device flow uses
   one runner per role and coordinates shared signals on the host.
3. The runner asks the driver for a screen as needed. Each driver converts its platform's
   hierarchy into windows, nodes, bounds, attributes, and a screen change sequence.
   The matcher applies shared selector rules. CSS lookup and browser hit testing delegate to
   the web driver because the live DOM owns those answers.
4. The driver injects input or starts the app, then reports what it observed. The runner
   applies settling, action-specific recovery, and assertion checks. An event that wakes
   a check is a reason to read again. The assertion succeeds when the requested condition
   holds on the resulting screen.
5. The CLI collects step and flow outcomes. A failure can produce the step log,
   screenshot, compact screen listing, raw hierarchy, and command results. Inspect the
   trace and hierarchy together when the accessibility tree and rendered screen disagree.

For role selection and synchronization, see [multi-device flows](multi-device.md).

### Interactive sessions and flow verification

`tb open` connects a named session. `tb look` creates a compact listing with `@refs`.
`tb do` expands refs into flow selectors against the latest listed screen, then uses the
same runner as a file-based test. It reports each command and the resulting screen diff.
`tb locate` exposes the current matches and the runner's preferred candidate without
acting. `tb watch` consumes settle events and streams screen changes.

The session keeps replayable YAML snippets for recorded steps, with refs already resolved.
Commands on a session take its device lock one at a time; the recorded steps also have a
lock of their own, so `tb log` answers at once with the steps recorded so far, even
while a long `tb do` runs. `tb clear` and `tb save` take the device lock and wait for it.
Closing a session takes the device lock too, whether `tb close` (one device or all), a
dropped agent connection or the daemon stopping closes it: it waits for the command running
on the device, then closes the driver and frees the device, so neither happens under a
command. A command that was waiting for the device when its session closed fails; to stop
a long `tb do` instead of waiting for it, cancel it (Ctrl-C) or stop the daemon. A `tb
close` cancelled while it waits leaves the session open, and `tb close all` closes the
devices side by side, so a busy one does not hold up the rest. A `tb close` that comes after
a dropped connection already closed the session still stops tb's agent, unless another tb
process has taken the device since. The daemon's own lock, over its list of devices and
their session names, is held only briefly and never while waiting for a device lock.
`tb save --preview [path] --json` builds the same YAML as `tb save` and returns it with a
step count and warnings. The optional path establishes the base directory for relative
references. Preview creates no directories or files. Plain-text preview puts warnings
in YAML comments. Its checks cover syntax and top-level risks such as coordinate gestures,
match indexes, a missing launch, and a missing assertion. Live locator ambiguity can be
inspected separately with `tb locate`.

After saving, close the interactive session on the replay target so the test runner can
take its lock. `tb replay flow.yaml -d DEVICE` parses that one file and runs it once with
the ordinary test runner. It requires a single-device flow whose first executed step is a
required `launchApp` that restarts the app. The YAML chooses the starting state. A plain
launch keeps mobile app data, and `clearState: true` requests a reset at launch. On web the
driver starts its own browser profile, and the flow launches by navigating to its URL.
Replay reports the test result and uses the same failure artifacts as `tb test`.

## Platform components

| Platform | Screen source | Input and lifecycle | Events |
|---|---|---|---|
| Android | `UiAutomation` accessibility windows and nodes. A flow can opt into DOM reads for a debuggable WebView. | The shell agent injects input. The host coordinates package manager and activity manager operations. | Accessibility window, content, scroll, text, and click events, with frame observation for some silent motion. |
| iOS simulator | XCTest accessibility snapshots. On iOS 26, a full-screen embedded browser may supply its tree from a system service. | The XCTest runner synthesizes input. The host uses `simctl` and agent app operations for lifecycle. | XCTest and accessibility notifications. The simulator probe adds in-app rendering and motion signals where available. |
| Web | DOM data collected by `page.js` and queried over CDP | CDP input dispatch, DOM scrolling, and browser navigation. | DOM mutations and interactions from the page helper, plus CDP navigation and network events. |

### Android agent lifecycle and code map

With `androidWebViewHierarchy: devtools` in a flow header, the Android driver can
add DOM nodes for a single visible WebView in the flow's app. The app must enable
WebView debugging. The driver uses a temporary loopback ADB forward to the app's
DevTools socket and retains the accessibility tree for native controls and fallback.

The Go driver discovers the device through ADB, pushes its embedded APK, and starts
`Main.kt` through `app_process` as the shell user. The agent serves a local abstract
socket and the host reaches it through an ADB port forward. The host checks the agent
build when connecting. The separately installed accessibility helper lives in the
same APK and is enabled for features that require an accessibility service.

- `Main.kt` wires the agent and establishes the `UiAutomation` connection.
- `Server.kt` handles clients. `Ops.kt` maps protocol requests to agent operations.
- `Tree.kt` acquires and serializes the accessibility hierarchy. `ReadProfile.kt` measures
  tree acquisition when requested.
- `Input.kt` implements input over platform abstractions. `Events.kt` maintains change
  and settle state and coordinates event-based waits.
- `Platform.kt` defines the interfaces used by the agent core. `AndroidPlatform.kt`
  binds those interfaces to Android. `NoopService.kt` and the editor files handle
  accessibility-service and input-connection capabilities.
- On the host, `android.go` owns connection setup and reconnection. `screen.go` decodes
  hierarchies, `input.go` builds action requests, `editor.go` handles the guarded editor
  connection, and `apps.go` handles app lifecycle and permissions.

See the [Android agent](../agents/android/app/src/main/java/dev/testbuddy/agent) and
[host driver](../internal/driver/android).

### iOS agent lifecycle and code map

The host unpacks the embedded XCTest runner and starts it for the selected simulator
through `xcodebuild test-without-building`. A simulator shares host loopback, so the
driver can connect by TCP without forwarding a port. A compatible runner can be adopted
on a later connection. The app probe is a separate library that can be loaded into apps
launched on the simulator. It reports in-process UI changes to the XCTest agent.

- `Agent.swift` is the XCTest entry point. `Server.swift`, `Ops.swift`, and `Replies.swift`
  implement the service and its operations.
- `Tree.swift` builds accessibility snapshots. `Input.swift` and `Gestures.swift`
  synthesize input. `Typing.swift` decides how `type` and `erase` give a field the focus and
  bounds every wait they make on the app by the op's deadline. `Events.swift` combines observations and supplies readiness signals.
- `Apps.swift` deals with app identity and readiness. `TBProbe` covers app-side UIKit,
  Core Animation, and optional Flutter observations.
- On the host, `runner.go` handles startup and teardown, `connection.go` handles requests
  and events, and `apps.go`, `simctl.go`, and `permissions.go` handle launch and simulator
  state. `screen.go` converts snapshots and `input.go` forwards actions.

See the [XCTest agent](../agents/ios/TBAgentUITests), [probe](../agents/ios/TBProbe),
and [host driver](../internal/driver/ios).

### Web driver lifecycle and code map

The Go driver starts Chrome or Chromium with a temporary profile and attaches to the tab
through a DevTools WebSocket. It registers an embedded JavaScript helper early in page
startup. It attaches to supported out-of-process frames and joins their snapshots to the
parent screen. Closing the driver ends the managed browser session.

- `web.go` starts and closes Chrome. `cdp.go` handles DevTools transport and request IDs.
- `page.js` records DOM changes and collects visible element data. `events.go` combines
  page callbacks with navigation and network events.
- `screen.go` assembles the hierarchy and implements CSS lookup and hit testing.
  `input.go` dispatches keyboard, pointer, and scroll actions. `navigation.go` handles
  URLs, site state, and browser permissions. `dialogs.go` answers JavaScript dialogs.

See the [web driver](../internal/driver/web).

### Flutter surfaces

A Flutter package can contain native screens. Build inspection can detect the Flutter
engine and assets, while runtime routing depends on the current surface.

- **Android** uses the regular accessibility and input paths. An optional test-build
  hook emits an accessibility change once an attached, visible Flutter view displays.
- **iOS simulator** can use the app probe to observe Flutter semantics notifications.
  For automatic scrolling, the adapter uses a live hit test to identify Flutter content
  and selects a drag suited to it. Native views, ambiguous hits, or unavailable probes
  keep the normal gesture path. The adapter can also restore Flutter row ancestry from
  bounds in an existing XCTest snapshot.

See [Flutter adapter setup and tests](../adapters/flutter/README.md).

### Mobile transport and reconnects

A mobile connection begins with `hello`. The host sends JSON-line requests with IDs
and matches replies to those IDs. The agent can publish unsolicited `changed` and
`settled` messages alongside them. Drivers translate platform-local change counters
into a host sequence and fan out events to subscribers.

Mobile drivers stamp selected state-changing operations with a request token. If the
connection drops and the driver resends a stamped operation to a surviving agent, that
agent can answer from the original request instead of dispatching input again. A
restarted agent cannot retain the previous process's request cache. See
[agent protocol](protocol.md) for message fields and supported operations.

## How waiting works

| Wait | How |
|---|---|
| After an action | The agent injects the input, then waits on its own event stream: the UI changed and then went quiet, or nothing changed within the no-change window (1 s for taps, 300 ms for typing). One request, answered once. |
| After a tap | Only until the UI starts changing (`react`), so the next command never acts on the screen the tap left. The rest of the wait goes to the next command that needs a still screen: every command except `assertVisible`, `assertNotVisible`, `extendedWaitUntil` and taps first waits for the UI to settle, and a tap waits for a still screen before it taps anyway. Assertions can so pass while a new screen is still coming in; everything that acts, types, scrolls, evaluates a condition or takes a screenshot still sees a settled screen. |
| Element lookup (`tapOn`, `assertVisible`, `when: visible`, ...) | Read the screen once. If the element is not there, sleep until the agent pushes `settled` with a newer `seq`, then read again. A screen with no change since the last read is never read twice. |
| `assertNotVisible` / `notVisible` | Absent on a screen with no change pending. |
| App launch | The agent waits for a window of the app (window events). |
| `waitForAnimationToEnd` | On Android and iOS, the explicit command first waits on the agent and then samples small screenshots within the same timeout. This catches motion that repaints pixels without changing accessibility. A part that keeps moving for a second (a spinner, a marquee, a pulsing button) is a loop and is set aside while all loops together cover at most a tenth of the screen; the rest must be still for 250 ms. Chrome uses its page animation events. |
| `waitForSignal` (multi-device) | A channel close. |
| The work queue | A device takes the next flow when its last one finishes (a channel receive). |

### Android: where the events come from

`UiAutomation.setOnAccessibilityEventListener` delivers window, content, scroll and text
events. Each event bumps `seq` and re-arms a quiet timer (120 ms: Android sends repeated
content-change events at most every 100 ms). When the timer fires with no newer event, the
agent pushes `settled`. Threads that wait for the UI block on a monitor the listener
notifies; nothing spins.

A view's click event counts as a change: a tap whose effect merges into a never-still
subtree (below) still registers at once. The keyboard's own window (its suggestion bar
redraws after every key) does not hold the UI unsettled.

Jetpack Compose sends accessibility events only while an accessibility service is enabled,
and a UiAutomation client does not count. The agent APK therefore includes `NoopService`, a
service enabled by the host while the agent runs and removed on exit. On API 33+ it also
provides a guarded input connection for Backspace: the host reaches it through an ADB forward,
the service checks the focused node and active input session, then delivers one DEL DOWN/UP
pair through the app's InputConnection. Input-session callbacks wake a pending request;
there is no polling or keyboard replacement. Older APIs and unavailable editor capabilities
retain native key injection. Uncertain delivery is never followed by another deletion. Verified on a Compose app: without it a tap
waited for the full no-change window and waits on Compose screens never woke; with it, taps
settle on real events (~0.4 s).

### iOS

See [research/ios-events.md](research/ios-events.md). testmanagerd forwards accessibility
notifications and the app's run-loop and animation idle callbacks, but UIKit posts nothing
for in-place changes. A small probe library, injected into apps that `tb` launches, posts a
notification for each Core Animation commit that changed the layer tree.

Motion that makes no commit is reported too, so a tap waits for it:

- **SwiftUI animations** (iOS 26+: a transition, a spring, `withAnimation`) run frame by frame
  through Core Animation presentation modifiers, from SwiftUI's own thread and with no commit.
  The probe counts each modifier update as a change (at most one notification per 30 ms).
  Before this, a 1.5 s custom push made one commit at its start, the screen "settled" 100 ms
  later, and a tap on Back landed on the screen still underneath.
- **Core Animation animations** (`UIView.animate` and the like) run in the render server once
  added. The probe sees each finite one added (`addAnimation:forKey:`) and tells the agent how
  long the app stays busy; settles wait for that end. Animations that repeat forever, or run
  longer than 3 s, do not count. For a probed app this replaces XCTest's "animations idle",
  which counts endless animations too and so never answered on a screen with a spinner.
- **Scroll views** that are dragged, decelerating or bouncing back always hold the screen: a
  tap on a list still bouncing at its end only stops it.

### Web

An in-page script (installed before the page's own scripts, in every frame) reports DOM
mutations, input, scrolling and the start and end of finite CSS animations through a CDP
binding. Navigation and the page's own XHR/fetch requests come from CDP events. "Settled"
means: no change for 120 ms, no navigation, no first-party request in flight (third-party
analytics are ignored; a request counts for at most 2 s), and no finite animation running
(for at most 1 s past the last change). Cross-origin iframes are attached through their own
CDP sessions and get the same script.

A JavaScript dialog (`alert`, `confirm`, `prompt`, a `beforeunload` "Leave site?") stops the
page's script until it is answered, so the driver answers each one as it opens, as a person
clicking through would: OK (`confirm()` returns true, `prompt()` its default text), and leave
the page. Each answer is a line in the run's trace (the dialog's type, its message and what
tb answered). A flow does not tap the dialog's buttons; it sees the page after the answer.

### Screens that never stop moving

A ticker, a stopwatch or a progress counter changes the UI forever, so "no change for
120 ms" never comes. Every change carries its source (the Android accessibility event's
source node, the web mutation's target element). A source that has changed continuously
(gaps under 400 ms) for 500 ms is **restless**: its changes still bump `seq`, so a lookup
waiting for new content re-reads, but they no longer hold the screen unsettled. A real
change (a new screen, a list loading) still has to go quiet before an action settles.
Endless CSS animations and Android indeterminate spinners send no change events at all. On
the web a CSS transition holds the page unsettled while it runs (up to 1 s), unless it only
changes a color (a hover, a completed item's text going gray): that moves and hides nothing.

A source found restless stays restless, so a ticker that paused while its screen was hidden
costs nothing when the screen comes back (web: the element; iOS: the layer, tagged with an
associated object so a new layer at a reused address does not inherit it; scroll views are
never kept). On Android only a view whose own text keeps changing is kept: when several views
change at once the platform reports their common parent, and a parent kept as restless could
hide a new screen coming into it.

Changes that follow the test's own input do not count towards a source's restless time (web:
within 400 ms of a key, click or drag): a list that grows with every Enter of a quick test is
not a ticker, and taking it for one would stop tb waiting for its next change.

Faster detection was tried and dropped: taking per-frame changes as restless after 150 ms
would also take a scripted 300 ms slide-in for one, and let a tap land on a moving screen.

On iOS, a source that changes every frame (an animation, not a ticker) is restless only after
1 s, and a SwiftUI animation (presentation modifiers, followed per layer and key path, since
SwiftUI replaces the modifiers of a `repeatForever` animation every few seconds) only after
2.5 s, so a finite animation is waited out and only an endless one (a pulse, a rotating logo)
stops holding the screen. With tb's 10x faster animations (`-UIAnimationDragCoefficient 0.1`,
which SwiftUI follows too) these scale down, to no less than 500 ms and 1 s. A screen with an
endless SwiftUI animation so costs its first settle about 1 s; later ones do not wait for it.
SwiftUI also replaces some layers every few frames (a `TimelineView`'s progress bar), so a
source is also a band: the layers of one class at the same height in the same superlayer.

Android also watches the window's frames, for animations that send no events (a sheet
sliding in): a window that drew within the quiet window holds "settled" for up to 600 ms past
the last change. With animations off (`tb test` turns them off), apps' own animations end at
once, so frames drawn while a restless view is changing are that view's: then they do not
hold the screen. With animations on, they still do.

## One tb per device

Two runners on one device replace each other's on-device agent and drive the same screen, so
both fail in confusing ways. `internal/devlock` gives a phone or simulator to one tb process at
a time: an flock(2) on a file per device in `~/.testbuddy/locks` (machine-wide, whatever
`TB_HOME` is; `TB_LOCK_DIR` overrides), holding who took it (command, pid, folder, start
time). The system drops the lock when its process exits, however it exits. The pool
(`tb test`) takes a device's lock the first time it hands the device to a flow and keeps it
for the run; a device another process holds is passed over for a free one, and a flow only
held devices could run fails at once naming the holder, or polls the lock every 0.5 s up to
`--wait-for-device` (another process's lock sends no event). The daemon takes the lock when a
session connects to a device and frees it on `tb close`, a dropped connection or exit; it also
refuses to open a device under a second session name. Browsers are not locked: each run
starts its own. When the daemon stops (`tb daemon stop`, its idle exit, SIGINT or SIGTERM) it
cancels the commands still running, then closes every session (10 s at most): a browser
session's Chrome and profile go, a phone's agent is kept for the next run.

## Making sure an action lands

Taps are where a fast runner gets flaky: a runner that clicks the moment an element is
displayed clicks during transitions, and lands on the wrong thing now and then without
noticing (the bench's click instrument, `bench/clicks.py`, measures it). Before acting, tb:

- **Waits for a still screen**, then finds the element again (`findStable`), so the tap goes
  where the element ends up. Restless elements do not count against "still".
- **Remembers recently visible targets**: the runner keeps up to 16 owned accessibility-tree
  snapshots for 30 seconds, gathered from normal reads (no extra screenshots or background
  polling). Trees larger than 8192 nodes are not retained. A missing, uniquely ID-selected
  clickable target can be recovered from a prior snapshot when its scroll container still
  exists and is the only scroll container on screen. After the initial lookup grace, tb uses
  the old position to choose a bounded scroll direction, then verifies the live element's
  ID, class, label, window and container before tapping its current position. Old coordinates
  are never tapped. Recovery attempts are explained in the debug trace.
  History is cleared on explicit navigation/app lifecycle commands and changes in windows,
  viewport, keyboard or app labels outside scroll containers. System-bar text changes do not
  reset app history. Identical-looking routes are not
  distinguishable without app-provided identity. CSS, indexed and relational selectors, unseen
  elements and multiple scroll containers do not use this recovery. A removed target still
  fails after the bounded search; history does not rewind app state.
- **Waits for the tap to take**: the UI must start changing within 1 s (both platforms), or
  the tap counts as having done nothing; the next command never reads the screen it left.
- **Steers clear of covers**: something drawn over the element where the tap would land takes
  the tap. The keyboard window; on Android a view after the element in the tree that takes
  touches (a sticky footer, a floating button) or a sticky section header; on iOS the
  navigation, tab and tool bars (drawn over content whatever their place in the tree) and later
  controls. An element in a list is dragged just clear (up to 2 drags, no fling), else tapped
  on the largest part that shows. A plain label over it takes no touches and is no cover. On
  the web a page's element order is not its drawing order, so the browser's hit test decides
  (`elementFromPoint`, shadow roots followed): while what it reaches at the tap point is not
  the element, inside it or around it, tb waits on change events for the cover to go.
- **Waits for the target to rest**: motion that sends no events (a view sliding or springing
  in: Android reports no change for a new position, and iOS animations play in the render
  server) can leave a "settled" screen with the target still moving. Before acting tb reads it
  again 40 ms later and, while it keeps moving, again, until two reads agree (up to 2 s); a
  lookup whose own two reads already agreed skips this.
- **Waits out a loading overlay**: apps lay a see-through layer with a spinner over a
  screen while it loads; the element shows through and stays in the tree, but the layer takes
  the touches. A node at the element's center, outside it and not one of its containers, that
  is a spinner (`ProgressBar`, which Compose's indicators report too) or holds one is such a
  layer on Android. On iOS a veil can expose an activity indicator with the bounds of the
  whole overlay, or a compact indicator inside a separate container. The runner waits
  within the lookup budget when that indicator or its dedicated container covers the
  target, then finds the element again.
- **Waits out a cover that left the tree but is still drawn** (Android, iOS): a screen, sheet
  or list closing over another can leave the accessibility tree at once and stay drawn, still,
  and taking touches for a while (a React Native screen closing while its JavaScript thread is
  busy). What was under it is listed at once, and no event says the cover is still there; only
  the pixels do. A tap on an element in a container drawn over other listed elements (Android
  reports the drawing order; on iOS two such elements are needed) takes a quarter-size
  screenshot first. When the next tap's target was under that cover and the cover is no longer
  listed, the screen where the tap would land is compared with that screenshot: unchanged, the
  cover is still drawn there, and tb waits on change events and 150 ms looks until it changes
  (up to 3 s, then taps anyway). A cover really gone shows what is under it at once: no wait.
  Other taps take no screenshot.
- **Gives a disabled target up to 3 s to turn on** (a button waiting for its form to validate),
  unless the selector asks about `enabled` itself; then taps it anyway.
- **Holds iOS taps per control**: 10 ms (React Native cancels a press when a nearby scroll view
  moves during a longer touch), 20 ms for switches and other tracking controls, alert and
  sheet buttons and taps at a point (a UISwitch needs ~17 ms).
- **Sends an iOS double tap as one gesture** with two 100 ms contacts starting 200 ms apart.
  The longer contacts give press handlers in scrollable rows time to recognize both taps.
- **Sends an Android double tap on the agent's clock**: two 100 ms contacts, the second DOWN
  80 ms after the first UP, in one request. Only the last UP waits for the app to handle it, so
  a slow device cannot push the taps out of the 300 ms double-tap window (issue #89).
- **Checks Android typing at the field**, one-character text included: with no field focused
  yet it waits in CI mode (on events, up to 1 s) for one, and elsewhere leaves the text to the
  host's own guard at once; its first key must change the focused field
  within 1 s, or it is typed once more (after an Enter that moves focus, the keyboard can still
  be connecting to the next field and drops it). If it still shows no change, the rest is
  typed as before; a native text field that shows no change at all after the whole text fails
  the step ("no field took the text") instead of passing with nothing typed. A field that
  rejects only some characters (a filter, a digits-only field) still passes (issue #97).
- **Answers system dialogs** a lookup finds: Android's "isn't responding" / "keeps stopping"
  (Wait the first time, Close app if it comes back; the button ids are the same in every
  language), and iOS's notifications alert as launchApp's permissions say (`unset` leaves it).
- **Types on iOS once the field shows the first character** (not on just any change: a ticker
  is no sign the keyboard is ready), and types at the slow rate when no field reports focus.
- **Does not pass a tap that did not happen** (iOS, probed apps): a touch withheld because its
  target moved after the agent read it (issue #103), or one the app received and cancelled
  before it ended (issue #131), fails the step and says so. In CI mode tb taps again instead:
  once on the target found again at rest after a withheld touch, up to twice after cancelled
  ones. A touch withheld because its point was off the target is re-aimed once in either mode.

## What CI mode changes

`internal/ci` says whether tb runs in CI (`TB_CI=1` or `0` decides; unset, `CI=true`). CI
runners are slower than a developer's machine, and some fixes for flakes seen there give the
device more time or a second try. Each applies only in CI mode, with a comment at its gate
saying so; elsewhere tb behaves and times as before the fix:

| Allowance | Where | Outside CI |
|---|---|---|
| iOS first screen read after a launch: 70 s (XCTest waits up to 60 s for a new app) | `driver/ios/apps.go`, `screen.go` | 30 s |
| iOS launch: wait on, and ask up to three times about, an app whose accessibility has not answered yet (issues #85, #126) | `driver/ios/apps.go`, iOS agent `LaunchWait.swift` | One ask (a late answer is asked once more, as before); the agent's error fails the launch |
| Listing booted simulators: 60 s | `device/discover.go` | 10 s |
| iOS swipe from an element rests 200 ms at each end (issue #127) | `engine/swipe.go` | No rest |
| A lookup that reaches its deadline while the device is silent reads the screen once more (issue #92), and so does a wait for an element to go (`assertNotVisible`, `notVisible` conditions; issue #96) | `engine/find.go` | Judged on the screen in hand |
| `takeScreenshot` with `cropOn`: up to 3 shots when the element moves as it is taken (issue #43) | `engine/screenshots.go` | One shot; an element that moved fails the command, saying so |
| iOS tap withheld because the target moved: found again and tapped again (issue #103) | `engine/tap.go` | The step fails: the target was moving |
| iOS tap cancelled before it ended: tapped again, up to twice (issue #131) | `engine/tap.go` | The step fails: the touch was cancelled |
| iOS type/erase: the field is refocused by a touch (no XCTest idle wait), and a request that gave up before any key is asked once more (issue #46) | iOS agent `Typing.swift`, `driver/ios/input.go` | `XCUIElement.tap()`, bounded by the op's deadline; no second ask |
| Android checked typing waits up to 1 s for a field to take the focus (issue #97) | Android agent `Input.kt` | The host's guard at once |

The agents cannot read the host's environment: the host sends `"ci": true` on the requests
concerned (`docs/protocol.md`, "CI mode"). What stays on everywhere: detecting and reporting
what did not happen (a withheld or cancelled touch, a field that took no text, an element that
moved under a crop), the iOS typing deadline that makes a hung `type` fail instead of hanging,
and the Android double tap's spacing on the agent's clock (issue #89), which adds no time.

## Timers that remain, and why

"No polling" means no loop that repeatedly asks the device whether something changed. These
timers remain:

| Timer | Where | Why |
|---|---|---|
| Quiet window (120 ms) | Android agent | "Settled" means "no event for a while". The timer is re-armed by events; it is a debounce, not a poll. |
| Restless read (400 ms) | Host | A screen that changes forever (a ticker) never settles; a waiting lookup reads after 400 ms of continuous change. Only runs while events arrive. |
| Silence safety net (1.5 s, then 3 s) | Host | If a device sends no event at all during a wait, the screen is read again, in case the app changes without reporting it. A lookup's gap doubles once and stays at 3 s while the device stays silent, so content that appears late in a long wait is read within 3 s (issue #96; uncapped, it went 6, 12, 24 s). A wait for an element to go re-reads every 1.5 s. With events flowing it never fires; when a re-read finds a change, `tb` warns. In CI mode, a lookup, or a wait for an element to go, that reaches its deadline more than 400 ms after its last read reads once more before it judges (issues #92, #96). `TB_SILENT_MS` sets the first gap; `0` turns the re-reads off. |
| Gesture timing | Agents | A swipe's moves, a long press's hold, a double tap's gap: the gesture itself. In CI mode, an iOS swipe from an element rests 200 ms at each end, so an app that is behind on a slow runner still sees the finger where it went down and where it lifted (issue #127). |
| `travel`, tap `repeat` `delay` | Engine | Asked for by the flow. |
| Idle exit (60 s check) | Agents, daemon | Housekeeping; never on a test's path. |
| Connect retries (50 ms) | Host | Agent start-up only. |

## When the connection drops

On Android an ADB server restart or USB interruption can drop the connection. The driver
can recreate the port forward and reconnect to the surviving agent. On iOS the driver can
redial the runner and restart it when needed. Driver-level sequence numbers account for a
new agent's counter, so pending waits can compare new observations with earlier ones. A
driver that cannot reconnect emits a disconnection event or returns the error to the caller.

## Speed techniques

- Android agent as the shell user through `app_process` (no instrumentation start-up), input
  straight to `InputManagerGlobal.injectInputEvent` (~1 ms per tap instead of ~60 ms through
  UiAutomation).
- A compatible mobile agent can be reused across connections while it remains running.
  Explicit release or idle exit means the next connection starts a new agent.
- Settle decided on the device, in one round trip per action.
- Screen reads reused while the device reports no change.
- `launchApp` on Android is one on-device shell script (stop, clear, grant only missing
  permissions, start), then a window event.
- Shell commands run inside the agent (no `adb` process per command).
- Android animations off during `tb test`.
- `scrollUntilVisible` drags without flinging, so the content stops when the finger lifts:
  the screen is read right after each drag, dragged again at once while the element is not
  there, and checked for rest only once it shows (two reads in a row agree on where it is).
  A drag that moved nothing (a list still loading, or at its end) waits up to 2 s for the
  screen to change before the next. On iOS drags start 70% into the list, so each moves the
  content ~1.4x as far. (A classic scroll flings, looks the element up for 500 ms between
  swipes and settles after each one; a fling also coasts a varying distance.) Measured on the bench:
  scrolling to row 30 went from 7.7 s to 1.8 s on iOS. A paging list springs back from a drag
  shorter than a page: after two drags that bring nothing new into view, the swipes fling, as
  classic swipes do. A drag that seems to have brought nothing new is judged again on the
settled screen first (the app can be a frame or two behind the drag), since flinging can carry
the element past the screen. Sideways drags cross 70% of the width, past half a page, across
the sideways list the element is in, or the one on screen when the middle has none (a
carousel above the middle of the screen). iOS drags are built
  from explicit move events (`XCPointerEvent`), each with its own start and duration, rather
  than `moveToPoint:atOffset:`, whose timing testmanagerd has not played the same way in every
  Xcode (that remains the fallback). On the web a drag scrolls the element under the point
  directly (`scrollBy`, instant): a wheel event's scroll can land after the screen was read,
  which once put a tap one row off.
- A tap waits for the UI to start reacting, not to finish (see [How waiting works](#how-waiting-works)).

## Build, verification, and diagnostics

`scripts/build.sh` builds the Android APK and, on macOS with Xcode, the iOS simulator
runner bundle. The Go binary embeds those artifacts. Use `--skip-agents` for host-only
changes when the embedded assets already match the intended device code. Chrome and
the platform SDK tools are host dependencies. The web helper is embedded with the Go
driver source.

| Change | First verification | Device verification |
|---|---|---|
| YAML, matching, runner, CLI, or session | `go test ./...`. Host tests use fake drivers, sockets, or protocol peers for isolated behavior. | Replay an exported flow on a real test target or run the appropriate Buddy Bench flows. |
| Android agent | `./gradlew testDebugUnitTest` from `agents/android`. The Kotlin tests drive platform interfaces with fakes. | Rebuild the agent and run Android bench scenarios on an emulator or device. |
| iOS agent | `swift test` from `agents/ios`. The Swift package tests agent core logic. | Rebuild the XCTest runner and run iOS bench scenarios on a booted simulator. |
| Web driver or helper | Go driver tests exercise CDP behavior through fake peers. The web CI job checks the browser helper. | Serve `bench/web` and run the web journey and scenarios in Chrome. |
| Flutter adapter | Run the package-detection tests and the platform's normal agent tests. | Run Flutter flows on the affected platforms. For mixed UIKit and Flutter routing, run the simulator test linked in the adapter guide. |

For a failed run, start with the failing assertion or action and the flow's
`log.txt`. Compare `screen.txt` and `hierarchy.json` with the screenshot when diagnosing
a missed selector, stale hierarchy, or visual cover. A missing event, an incorrect
selector, and input that arrived before the target was ready can look alike at the
assertion level. The trace helps separate them. For repeated checks, keep the same app
build, flow, device, and retry settings, and record first-attempt outcomes.

See [benchmark setup](../bench/README.md), [scenario coverage](../bench/SCENARIOS.md),
and [CI jobs](../.github/workflows/ci.yml).
