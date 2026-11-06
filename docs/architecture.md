# Architecture

```
tb (CLI) ──unix socket──▶ tb daemon ──┐        (agent commands: look, do, wait, watch)
tb test (in-process) ─────────────────┤
                                      ├─ engine.Runner per device ── driver.Driver
                                      │
          Android: adb forward ──▶ agent (app_process, shell uid, UiAutomation + InputManager)
          iOS simulator: TCP  ──▶ agent (XCUITest runner, kept warm)
          Web: DevTools websocket ──▶ Chrome tab + in-page script (page.js)
```

- **One binary.** `tb` embeds both agents. It pushes or starts them on first use and reuses a
  running agent on later runs (a reconnect takes ~20 ms).
- **One connection per device**, JSON lines in both directions ([protocol.md](protocol.md)).
  The agent answers requests and also pushes events at any time: `changed` (the UI changed)
  and `settled` (no change for the quiet window), each with a change counter `seq`.
- **The engine** (`internal/engine`) runs flow commands around those events. Selector
  matching runs on the host (`internal/match`), so Android, iOS and the web share one
  implementation of the flow format's selector rules.

## How waiting works

| Wait | How |
|---|---|
| After an action | The agent injects the input, then waits on its own event stream: the UI changed and then went quiet, or nothing changed within the no-change window (1 s for taps, 300 ms for typing). One request, answered once. |
| After a tap | Only until the UI starts changing (`react`), so the next command never acts on the screen the tap left. The rest of the wait goes to the next command that needs a still screen: every command except `assertVisible`, `assertNotVisible`, `extendedWaitUntil` and taps first waits for the UI to settle, and a tap waits for a still screen before it taps anyway. Assertions can so pass while a new screen is still coming in; everything that acts, types, scrolls, evaluates a condition or takes a screenshot still sees a settled screen. |
| Element lookup (`tapOn`, `assertVisible`, `when: visible`, ...) | Read the screen once. If the element is not there, sleep until the agent pushes `settled` with a newer `seq`, then read again. A screen with no change since the last read is never read twice. |
| `assertNotVisible` / `notVisible` | Absent on a screen with no change pending. |
| App launch | The agent waits for a window of the app (window events). |
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
service that does nothing; the host installs the APK and enables the service while the agent
runs, and the agent removes it when it exits. Verified on a Compose app: without it a tap
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
starts its own.

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
- **Waits out a loading overlay** (Android): apps lay a see-through layer with a spinner over a
  screen while it loads; the element shows through and stays in the tree, but the layer takes
  the touches. A node at the element's center, outside it and not one of its containers, that
  is a spinner (`ProgressBar`, which Compose's indicators report too) or holds one is such a
  layer: tb waits for it to go (up to the lookup's time), lets the screen settle and finds the
  element again. Not on iOS yet (its tree is ordered by place, not as drawn; a hit test is the
  way there).
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
- **Answers system dialogs** a lookup finds: Android's "isn't responding" / "keeps stopping"
  (Wait the first time, Close app if it comes back; the button ids are the same in every
  language), and iOS's notifications alert as launchApp's permissions say (`unset` leaves it).
- **Types on iOS once the field shows the first character** (not on just any change: a ticker
  is no sign the keyboard is ready), and types at the slow rate when no field reports focus.

## Timers that remain, and why

"No polling" means no loop that repeatedly asks the device whether something changed. These
timers remain:

| Timer | Where | Why |
|---|---|---|
| Quiet window (120 ms) | Android agent | "Settled" means "no event for a while". The timer is re-armed by events; it is a debounce, not a poll. |
| Restless read (400 ms) | Host | A screen that changes forever (a ticker) never settles; a waiting lookup reads after 400 ms of continuous change. Only runs while events arrive. |
| Silence safety net (1.5 s, doubling) | Host | If a device sends no event at all during a wait, the screen is read again, in case the app changes without reporting it. With events flowing it never fires; when a re-read finds a change, `tb` warns. `TB_SILENT_MS=0` turns it off. |
| Gesture timing | Agents | A swipe's moves, a long press's hold, a double tap's gap: the gesture itself. |
| `travel`, tap `repeat` `delay` | Engine | Asked for by the flow. |
| Idle exit (60 s check) | Agents, daemon | Housekeeping; never on a test's path. |
| Connect retries (50 ms) | Host | Agent start-up only. |

## When the connection drops

On Android the adb link can drop mid-run (an adb server restart, a USB hiccup). The driver
notices (the connection closes, or a request gets no answer because of it), re-creates the
port forward and reconnects to the agent, which kept running on the device, then sends the
request again once. Its change counter never goes backwards, even if the agent had to be
restarted, so a wait in progress resumes on the next event. Measured: killing the adb server
mid-journey cost the next step ~0.7 s and the flow passed.

## Speed techniques

- Android agent as the shell user through `app_process` (no instrumentation start-up), input
  straight to `InputManagerGlobal.injectInputEvent` (~1 ms per tap instead of ~60 ms through
  UiAutomation).
- Agents persist across runs; a run reconnects in ~20 ms.
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
