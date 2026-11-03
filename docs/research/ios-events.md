# iOS: how the runner learns that the UI changed

Question: can an XCUITest runner on iOS be told, without polling, that the app's UI changed and
that it settled? Measured on Xcode 27.2 (27B52), an iOS 27.0 simulator ("iPhone 17"), Apple
silicon Mac, November 2026. The agent is `agents/ios`; the fixture app used for the change table
is `agents/ios/TBFixture` (`agents/ios/build.sh --fixture`).

**Short answer.** iOS gives a runner push signals for *structural* changes (screens, alerts,
first responder, input completion) and for *idle* (run loop and animations), but **no push signal
for in-place content changes** (a label's text, a color, a view shown, hidden or added). To close
that gap the agent injects a 100 KB probe dylib (`TBProbe`) into the apps it launches; the probe
pushes one Darwin notification per Core Animation commit that changed the layer tree. With it,
every change is pushed and nothing on the device polls. Without it (an app not launched by tb),
in-place changes are seen only by a read: once after each action (event-anchored), and otherwise
not until the host reads again.

## 1. What exists (found in the binaries, then called at runtime)

Found with `nm` and `lldb disassemble` on `XCUIAutomation.framework`, `XCTAutomationSupport` and
`XCTestCore` from the simulator platform, then listed at runtime (op `methods`).

### Accessibility notifications through testmanagerd

| Entry point | What it does on iOS 27 |
|---|---|
| `-[XCTRunnerDaemonSession registerForAccessibilityNotification:(int) reply:^(NSNumber *token, NSError *)]` | Forwards to the daemon proxy's `_XCT_registerForAccessibilityNotification:reply:`. **Works.** testmanagerd accepts any id (all 77 we asked for: ~10 ms together). |
| `-[XCTRunnerDaemonSession unregisterForAccessibilityNotification:registrationToken:reply:]` | The inverse. |
| `-[XCTRunnerDaemonSession receivedAccessibilityNotification:(int) fromElement:(XCAccessibilityElement) payload:(NSData)]` | Where testmanagerd's `_XCT_receivedAccessibilityNotification:fromElement:payload:` lands; it calls `axNotificationHandler` (the `XCAXClient_iOS`) `handleAccessibilityNotification:fromElement:payload:`. **The agent swizzles this method** (and calls the original). The element carries the posting app's pid; the payload is an archived dictionary. |
| `-[XCAXClient_iOS _registerForAXNotification:error:]` | A synchronous wrapper (a future with XCTest's AX timeout) around the session call above. |
| `-[XCAXClient_iOS addObserverForAXNotification:handler:]`, `registerForAXNotificationsForApplicationWithPID:timeout:completion:`, `removeObserver:forAXNotification:`, `unregisterForAXNotificationsForApplicationWithPID:` | **Stubs on iOS**: they call `handleFailureInMethod:` (an assertion; macOS only). Not usable. |
| `-[XCAXClient_iOS notifyOnNextOccurrenceOfUserTestingEvent:handler:]` | One-shot wait for a user-testing event (below). |
| `XCTStringFromAXNotification` (XCTAutomationSupport) | Names XCTest knows: 1006 Alert, 1011 OrientationChanged, 1018 FirstResponderChanged, 1021 PidStatusChanged, 1028 HIDEventReceived, 1053 NativeFocusItemDidChangeWithContext, 4002 UserTesting. XCTest's own handler acts on 1006, 1021 and 4002 only. UIKit's public ones: 1000 ScreenChanged, 1001 LayoutChanged, 1008 Announcement, 1009 PageScrolled. |

**User-testing events** (4002, posted by UIKit's accessibility in the app; payload `event=`):
`TouchEventsCompleted`, `ScrollEventCompleted`, `ButtonEventsCompleted`, `RunLoopIsIdle`,
`ViewDidAppear` / `ViewDidDisappear` (with `controllerClass`, `controllerTitle`,
`navigationItemTitle`). Strings found in the simulator's dyld cache; all but `RunLoopIsIdle`,
`ScrollEventCompleted` and `ButtonEventsCompleted` seen arriving.

### Idle, pushed by the app

`XCUIApplicationProcess _notifyWhenMainRunLoopIsIdle:(void (^)(NSError *))` and
`_notifyWhenAnimationsAreIdle:` (plus `_supportsAnimationsIdleNotifications`) are the asynchronous
halves of `waitForQuiescenceIncludingAnimationsIdle:isPreEvent:`. They go over the
`XCTRunnerAutomationSession` (a DTX connection straight to `XCTAutomationSession` inside the app):
`XCTMainRunLoopIdleNotifier` (a CFRunLoopObserver) and `XCTAnimationsIdleNotifier` (it swizzles
UIView's animation start/stop and counts). When the session is missing, XCTest falls back to
testmanagerd's `notifyWhenEventLoopIsIdleForApplication:reply:`. The agent calls them without
blocking any thread.

### App state and orientation, pushed by testmanagerd

`_XCT_deviceOrientationDidChange:` and `_XCT_interfaceOrientationDidChange:` (category
`XCTMessagingRole_UIApplicationStateUpdating` on `XCTRunnerDaemonSession`) are hooked too.


`-[XCTRunnerDaemonSession daemonDidUpdatePID:forApplicationWithBundleID:andState:]` (from
`_XCT_applicationWithBundleID:didUpdatePID:andState:`), hooked. And SpringBoard posts 1021
PidStatusChanged with `{pid, suspended-status, display-type}` whenever a process is suspended or
resumed: the agent uses it to know which apps are in the background.

## 2. Which changes are announced (fixture app, AX notifications 1000–1075 and 4002 registered)

| Change in the app | AX notifications that arrived | Probe |
|---|---|---|
| Label text changed on tap | **none** | 1 commit, at the change |
| Label text changed 1 s after the tap (no input) | **none** | 1 commit, at +1.0 s |
| `backgroundColor` changed | **none** | yes |
| `isHidden` toggled | **none** | yes |
| Arranged subview inserted | **none** | yes |
| Activity indicator started / stopped | **none** | yes |
| `UIView.animate` (1 s move) | AX1025 at the end only | 1 commit at the start; the animation itself runs in the render server; XCTest's animations-idle answered at ~1.05 s |
| UIAlertController presented | 1018 FirstResponderChanged, 1001 LayoutChanged (`AXForceUpdateChange`), 1025, 4002 ViewDidAppear (`UIAlertController`, "Hello"), 1006 Alert, 1020 | yes |
| Any tap | 4002 TouchEventsCompleted from the app **and** from SpringBoard, 45–60 ms after the touch began (~200 ms before testmanagerd confirms the event) | the button's highlight |
| Settings: push (SwiftUI NavigationStack) | 1001 at the start, AX1025 per frame, then 4002 ViewDidDisappear + ViewDidAppear and 1000 ScreenChanged when it ends | ~60 commits/s for the whole transition |
| iOS 27 UISwitch toggled | AX1025 **every frame** (16.7 ms) for 1.5 s | 61 commits over 1.15 s; animations idle at ~1.5 s |
| Notification permission requested (SpringBoard alert over the app) | from SpringBoard: 4002 ViewDidAppear `SBAlertItemRootViewController` at +129 ms, then ViewDidAppear `_SBAlertController` and 1006 Alert at +530 ms; `activeApplications` then lists SpringBoard alone | the app does not change |
| Device rotated (`XCUIDevice.orientation`) | testmanagerd's `_XCT_deviceOrientationDidChange:` at +5 ms and `_XCT_interfaceOrientationDidChange:` at +34 ms; **1011 OrientationChanged at +340 ms, when the rotation animation is over** (touches during it are lost) | relayout commits at +45–137 ms |
| A snapshot (tree read) | none | none: reading does not change the app (no feedback loop) |

Notifications XCTest also registers itself (4002, 1006) arrive twice. AX1025 is undocumented; it
follows frame/geometry updates.

**Conclusion.** UIKit's accessibility runtime, as loaded for automation, announces screen
transitions, alerts, focus and input completion — not content. Nothing else in XCTest pushes
content changes, and no AX notification id in 1000–1075 or 2000–2002 fired for them.

## 3. What does not work as a change signal

- **The idle callbacks** answer at once when the app is idle: 0.2–0.9 ms round trip (the first
  call per app ~210 ms, setting up the automation session). They tell "idle now", not "something
  happened", so chaining them would just be polling with extra steps.
- **The trimmed snapshot** (`XCAXClient_iOS requestSnapshotForElement:attributes:parameters:error:`
  with the 11 key paths a node needs, via `+[XCElementSnapshot axAttributesForElementSnapshotKeyPaths:isMacOS:]`)
  is **slower** than `XCUIApplication.snapshot()`: Settings (167 nodes) 57–59 ms vs 35–48 ms
  median. The public call is answered inside the app over the automation session; the trimmed one
  goes through testmanagerd. Reads use the public call (op `treeMode` switches).
- `XCAXClient_iOS`'s observer API (`addObserverForAXNotification:handler:`) is macOS-only.

## 4. The probe (agents/ios/TBProbe)

Loaded with `SIMCTL_CHILD_DYLD_INSERT_LIBRARIES` when the driver launches an app through
`simctl launch` (verified in the fixture app and in Settings, a system app). It:

- swizzles the CALayer entry points every tree change goes through — `actionForKey:` (asked on
  every animatable property change, including sublayers ordered in/out), `setNeedsDisplay(InRect:)`,
  `setNeedsLayout`, `setContents:`, `setHidden:`, `setOpacity:`, `setBackgroundColor:`,
  `setPosition:`, `setBounds:`, `addSublayer:`, `insertSublayer:...`, `replaceSublayer:with:`,
  `removeFromSuperlayer`, `setSublayers:` — each sets one atomic flag and calls the original;
- runs one run loop observer after Core Animation's commit (BeforeWaiting, last order); if the flag
  is set it posts the Darwin notification `dev.testbuddy.probe.changed` with `pid<<32 | count` as
  its state. At most one per run loop turn; nothing at all while the app sleeps;
- on `dev.testbuddy.probe.resign.<pid>` resigns the first responder (hideKeyboard).

The runner listens with `notify_register_dispatch`; a commit reaches it in under 1 ms.

What it cannot see: animations that run only in the render server after they start (covered by
XCTest's animations-idle, which the settle waits for), content drawn with Metal/OpenGL (games,
Flutter's canvas: untested), and other processes' UI (WKWebView's web content process, SpringBoard
alerts — those are covered by AX notifications 1006/1000). `TB_IOS_PROBE=0` turns it off.

Overhead: Settings' push/pop settled in 0.30–0.35 s with the probe and 0.31–0.34 s without
(no measurable difference); per change it costs one atomic store, per changed run loop turn one
`notify_post`. What it buys: the first `changed` event 7–12 ms after a tap instead of 56–302 ms,
and every in-place change pushed.

## 5. How the agent turns signals into `changed` / `settled`

- **changed**: a probe commit from the foreground app; for apps without the probe, any AX
  notification in 1000–1075 except 1011/1021/1028 from the foreground app or SpringBoard; a
  ViewDidAppear/ViewDidDisappear. For a probed app, non-structural AX notifications (1001, 1025...)
  are ignored: they arrive later than the probe and describe nothing it missed. Each bumps `seq`;
  pushes are coalesced to one per 30 ms.
- **settled**: a one-shot quiet timer (100 ms, re-armed by each change — a debounce, not a poll);
  when it fires the app is asked for run loop idle, then animations idle (each capped at 3 s for
  animations that never end). If nothing changed meanwhile, `settled` is pushed.
- **action settle** (`tap` etc.): `changed` once seq moved and settled; `no_change` once the
  no-change window passed **and** the app reported idle after the input (so a change about to
  start is not missed); `timeout` otherwise.
- **input completion**: taps and swipes return at the app's TouchEventsCompleted (pushed, ~45 ms)
  instead of testmanagerd's confirmation (~250 ms); the confirmation is still awaited before the
  next synthesized event, because overlapping records can mix.
- **rotate** returns once 1011 OrientationChanged arrived (capped at 1.5 s for an app that does
  not turn) and the UI settled: 0.34–0.67 s; 7 of 7 taps right after it landed, versus misses
  when it returned at the first settle (~230 ms), mid-animation.
- **SpringBoard** counts as a change source only for its structural notifications (alerts,
  screens) while an app is in front; its status bar's layout notifications are ignored.
- **foreground**: `XCAXClient_iOS activeApplications` (2–7 ms on the main thread) is asked again only after a
  structural event; it also lists apps just suspended, which the PidStatusChanged payloads exclude.
  `awaitPackage` re-checks only when an event arrives.

## 6. Measurements (driver end to end: `TB_IOS_SIM=1 go test ./internal/driver/ios -run Simulator -v`)

| What | Time |
|---|---|
| Runner cold start (xcodebuild test-without-building → hello) | 3.6–3.9 s |
| Runner reuse (port probe + info) | 1 ms |
| launchApp Settings (terminate, permissions, simctl launch, awaitPackage) | 1.9–2.7 s, mostly the app's cold launch; simctl steps ~100–500 ms each |
| First settle after launch | 0.52–1.1 s with the probe; without it `quiet` returned at once in one run of two (the launch's own rendering is not announced) |
| Screen read, Settings, 167 nodes | 38–47 ms median (first read after launch up to 0.4 s) |
| Screen read, fixture, 65–97 nodes | 20–51 ms |
| Tap → settled, Settings push General | 0.30–0.35 s (first time 0.60 s); first `changed` event 7–12 ms after the tap |
| Tap → settled, Settings pop | 0.32–0.33 s |
| Same without the probe | push 0.32–0.54 s, pop 0.31 s; first `changed` 56–302 ms after the tap |
| Tap on nothing → no_change | 0.30–0.36 s (250 ms window + idle) |
| Tap → settled, in-place change (fixture) | 0.15–0.20 s |
| Change 1 s after a tap, no input | pushed ~0.9 s after the tap returned (when it happened); found with 2 event-triggered reads |
| iOS 27 switch toggle | settled at ~1.5 s (real per-frame animation) |
| Type 11 characters / erase 6 / hideKeyboard (probe) | 0.55–0.76 s / 0.29–0.46 s / 0.15–0.46 s |
| Screenshot PNG full size / JPEG half size | 59 ms / 132 ms |
| Foreground lookup (`activeApplications` + cached bundle ids) | 1.7–3.4 ms on the main thread; **~205 ms off it** |

The last row mattered: before the agent moved that call to the main thread, every settle after a
structural change paid ~200 ms (Settings' push settled in 0.51–0.57 s instead of 0.30–0.35 s).
XCTest's accessibility client apparently waits for the main thread in ~200 ms steps when called
from elsewhere; every XCTest call the agent makes now runs on the main thread, except the idle
callbacks and event synthesis (measured fast off it).

10x UIKit animations (`-UIAnimationDragCoefficient 0.1` with `FEATUREFLAGS_ENABLED=UIKit/UIPreferences`,
an earlier finding) are on for apps the driver launches (`TB_IOS_FAST_ANIMATIONS=0` turns them off);
SwiftUI's NavigationStack transition and the keyboard are not sped up by it.

## 7. Where polling or pulling remains (all of it)

| Where | What | Why |
|---|---|---|
| Apps **without** the probe (not launched by tb: already running, launched by XCTest's `launch` op) | In-place changes are not pushed. After each action the agent compares one read taken after the app reported idle with the read before (`seenBy: "read"`): one pull per action, triggered by events, not a loop. A change not caused by an action (a timer, a network reply) is not seen until the host reads again. | iOS has no push for it (section 2). |
| Host engine | If the engine waits for an element on a screen that changes without events (unprobed app, Metal content, web content), only its own timeout-based re-read finds it. | Same. |
| `type` / `erase` / `clear` | One XCUIElement query for the focused field's value before typing (pull, ~30–50 ms). The wait for the first character to land is event-driven (seq change, 500 ms cap). | Needs the field's current value. |
| `info.keyboard`, `hideKeyboard` | `XCUIApplication.keyboards.firstMatch.exists` (a query) per check. | No keyboard-shown notification arrived that was reliable to key on. |
| Foreground app | `activeApplications` pull (1–3 ms), only after a structural event invalidated the cached answer, and when two apps are listed and SpringBoard did not say which is suspended, `XCUIApplication.state`. | No "frontmost app changed" push with the pid. |
| Host: connecting | `wire.Dial` retries every 50 ms while the runner starts; replacing a stale runner waits for its port in 100 ms steps. | Startup only, never during a test. |
| Timers that are not polls | The quiet window (one-shot debounce per change), the idle caps (one-shot), the runner's 30-minute idle exit (one timer, re-armed when the last client leaves). | |

Nothing else polls: no sleep loops on the device, no periodic snapshots, no screenshot diffs.

## 8. Not tried / future options

- **SimulatorKit framebuffer damage callbacks** (`SimDisplayIOSurfaceRenderable`'s damage-rectangle
  callbacks, what Simulator.app and idb's FBFramebuffer use) would push every pixel change,
  Metal and web content included, from the Mac side without touching the app. It needs a small
  helper linked against private macOS frameworks, and the cursor blink and spinners would need
  filtering. The best candidate for unprobed and non-UIKit content.
- **Physical devices**: the probe relies on `DYLD_INSERT_LIBRARIES` through simctl; on a device the
  AX-only path (plus event-anchored reads) is what remains.
- Typing faster than 100 characters/s after the first one was not measured here (numbers reported elsewhere).
