# Gotchas

What was learned building and running tb, grouped by where it bites. Each item says what
happens and what to do.

## Before blaming tb

- **The installed app and the flows disagree.** Most failures on a real app are a different
  build: a label renamed ("Shopping Lists" became "Lists"), a button removed ("Done"), an
  accessibility label reworded ("Mark Apples done" became "Apples"). The failure output
  prints the screen as it was: compare it with the step. Same flow, same failure in another
  runner means it is the app or the flow.
- **The data is not what the flow assumes.** A flow written for a fresh account
  (`assertVisible: "No lists yet"`) fails on a device with data. Either start from a clean
  state (`launchApp: {clearState: true}`) or write the flow to tolerate existing data.
- **The device is not the one you think.** Emulator serials change across restarts
  (`emulator-5554` → `emulator-5556`). `tb devices`, then pin with `--device`. A flow only
  goes to a device that has its `appId` installed; when none does, the flow fails with
  `app ... is not installed`.

## Devices and other tools

- **One UI automation client per Android device.** Android allows one UiAutomation
  connection at a time. Another UI test tool's on-device server (or one left running by a
  crashed run) and `tb` fight over it: taps stop registering, reads come back empty. Stop
  the other tool and its server apps (`am force-stop <their packages>`) before running `tb`;
  `tb` stops its own agent when done (`tb daemon stop` / `tb close all`).
- **Android accessibility service.** While the agent runs, its accessibility service is on
  (Compose apps only send UI change events when one is). It is turned off when the agent
  exits. Another tool's run right after `tb` should not see it; if it does, `tb close all`.
- **Animations.** `tb test` sets Android's animation scales to 0 for the run and restores
  them (`--keep-animations` to leave them). `tb do`/`look` sessions do not change them.
- **iOS: simulators only**, and the simulator must be booted. The first run on a simulator
  installs tb's runner (~20 s); later runs reuse it.
- **iOS: let `launchApp` start the app.** `tb` injects a probe into apps it launches that
  reports every in-place UI change. An app already running, or opened by another tool, only
  reports screen-level changes, so waits fall back to slower checks. Also: UIKit animations
  run 10x faster in apps tb launches (`TB_IOS_FAST_ANIMATIONS=0` to keep normal speed).
- **iOS bundle id ≠ Android package.** One flow with `appId: ${APP_ID}` needs a different
  `-e APP_ID` per platform.
- **adb drops.** If the adb server restarts mid-run, the Android driver reconnects to its
  agent and retries the request once; the step costs ~1 s, the flow goes on.

## Signed-in apps and real backends

- **`clearState` signs the user out** and deletes local data (Android `pm clear`; iOS empties
  the app's containers and resets its permissions). On a device the user signed in by hand,
  start with plain `- launchApp`. On Android, if the phone refuses to clear (some Oppo,
  Realme, Vivo, Xiaomi builds), the step fails saying so and which developer option allows
  it, rather than running the flow on the old data.
- **Flows act on real accounts.** Sign-in, sharing, invites, deletes and purchases hit the
  app's real backend. Ask before running such flows on the user's signed-in devices, and
  tell them what test data a run leaves behind (lists, items, settings it flipped).
- **Credentials** belong in `-e` values or the project's env files, never typed into a flow
  you write.

## Waiting and timing

- **No sleeps.** tb waits on device events: the next step starts when the UI settles. Do not
  add `waitForAnimationToEnd` or retries to make a flow "safer"; they only slow it.
- **Slow things outside the UI** (a backend call, an email, a push) need
  `extendedWaitUntil: {visible: ..., timeout: 20000}`. A flow that passed elsewhere only
  because that runner was slow needs one of these.
- **Never-still screens work.** Spinners, tickers and stopwatches are detected after ~0.5 s
  of continuous change and stop holding the screen up; tb remembers them when you come back
  to the screen. Endless CSS/system spinners send no events at all.
- **Assertions can pass during a transition** (the element is already on screen while the
  new screen slides in). Anything that acts waits for a settled screen first, so this never
  makes a tap land early.
- **`assertNotVisible`** passes once the element is gone from a settled screen.
- **Visible means on screen and inside its scrolling lists**: a row scrolled up under a header
  (still inside the screen, out of its list) is not visible, and is not tapped.
- A bare `containsDescendants` (nothing else in the selector) picks the innermost element that
  holds the descendants, not the screen's root.

## Taps

- tb waits for a still screen, finds the element again, then taps; and each tap must start a
  UI change within 1 s or it counts as "did nothing" (`retryTapIfNoChange` then retries).
- **Covers**: something drawn over the element where the tap would land (a sticky footer,
  bottom tab bar, navigation bar, floating button, the keyboard) would take the tap. tb drags
  the list to bring the element clear (up to 2 drags), else taps its largest uncovered part.
  Not on the web yet (taps go to the element's center there).
- **Loading overlays** (Android): a see-through layer with a spinner over the screen takes
  the taps while it shows. tb waits for it to go before tapping. Not on iOS yet.
- **Disabled buttons**: tb gives a disabled target up to 3 s to turn on (a form validating),
  then taps anyway. A selector with `enabled: false` is tapped at once.
- **iOS tap holds**: 10 ms by default; 20 ms for switches, sliders, steppers, pickers,
  alert/sheet buttons and point taps (a UISwitch needs ~17 ms).
- **Point taps** (`point: "50%,8%"`) depend on the layout and the device size. Use selectors.

## Typing

- `inputText` types into the **focused** field: tap the field (by `id`) first. On iOS, if no
  field reports focus, tb types slowly anyway rather than failing.
- Tapping a placeholder text only works while the field is empty: select fields by `id`.
- `eraseText: N` deletes N characters from the end of the focused field (on Android the
  cursor is moved to the end first: a tap leaves it where the finger landed). On iOS it is
  capped at the field's length. In one-box-per-digit code fields extra deletes move back
  through the boxes.
- The keyboard can cover the next button: tb steers around it, or use `hideKeyboard`.
  Only what the keyboard draws counts (with a hardware keyboard, Gboard shows just a small
  toolbar).
  (some apps ignore it; tapping outside the field always works).

## Scrolling

- `scrollUntilVisible` drags without flinging (the list stops where the finger lifts), reads
  after each drag, and only checks for rest once the element shows: fast and it never
  overshoots. Paging lists (carousels, pagers) spring back from such drags; after two drags
  that bring nothing new into view it switches to flinging swipes.
- Sideways `scrollUntilVisible` drags the sideways list itself (a carousel above the middle
  of the screen), not the middle of the screen.
- `speed` barely matters for `scrollUntilVisible`; `swipe`/`scroll` keep their usual fling.
- Lists that load more on reaching the end: a drag that moved nothing waits up to 2 s for
  the list to change before the next.
- On the web, scrolling moves the element under the point directly (instant), so a later
  tap never lands one row off.

## Android

- **Toasts** count as on screen for 3.5 s from when they show (Android does not list them;
  tb reads their text from the system's event): `assertVisible` on a Toast's text works, and
  `assertNotVisible` passes once that time is up.
- **`setLocation`** on Android 12+ uses the system's test location providers (gps, network,
  fused), sent again every second while the run lasts and removed at the end; older versions
  need an emulator (its GPS).
- **`setDarkMode`** switches the system's night mode (`cmd uimode night`); on iOS simulators
  the appearance (`simctl ui appearance`).

## Web

- Web flows use `url:`; `tb devices` lists `chrome`. `--headless` for CI; without it a
  window opens (useful to watch). `url: ${URL}` with `-e URL=...` runs in Chrome too; a flow
  whose `appId` is an app id never runs in Chrome.
- `startRecording` on the web records nothing yet (a log line says so); the flow goes on.
- Each run gets a fresh browser profile (no cookies kept). Password manager and translate
  prompts are off.
- Cross-origin iframes are supported; selectors reach into them.
- `css:` selectors work only on the web.

## Multi-device

- Roles get free devices matching their `platform`/`device`; pin with
  `--device role=DEVICE`.
- Commands outside a role block are allowed only when they do not touch a device (scripts,
  signals), or the flow has one role.
- `waitForSignal` times out after 120 s by default.

## Running many flows

- `--shard-split N` spreads flows over N devices from one queue; `--retries 1` re-runs a
  failed flow, on another device if one is free.
- A folder run follows its `config.yaml` (globs, tags, execution order).
