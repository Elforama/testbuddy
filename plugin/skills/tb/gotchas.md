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

- **One tb per device**: a `tb test` run locks the phones and simulators it uses until it
  ends; a session (`tb open`, `do`, `look`) locks its device until `tb close`. A second
  runner is refused at once with who holds the device: `device emulator-5554 is in use by tb
  test flows/ (pid 4242, since 10:31:07, in /path)`. Use another device (with several
  connected, `tb test` picks a free one), or wait (`tb test --wait-for-device 5m`). Your own
  session (the same daemon, `TB_HOME`) gives its device to your `tb test` / `tb replay` run when
  no command is running in it, and the next `tb look` or `tb do` opens it again: no `tb close`
  needed in between. Locks die with their process. Browsers are not locked.
  `TB_NO_DEVICE_LOCK=1` turns locking off.
- In one daemon, a device open as session "a" is not opened again under another name: use
  `-d a`, or close it first.
- **One UI automation client per Android device.** Another UI test tool's on-device server
  and `tb` fight over the device: taps stop registering, reads come back empty. Stop the
  other tool (and its server apps) before running `tb`. tb frees its devices when done
  (`tb test` at the end of the run unless `--keep-agents`, `tb close` for a session);
  `tb release [-d DEVICE | all]` stops a tb agent left behind (a crashed run). The same holds
  for tb's runner on an iOS simulator. The agent of a killed run exits on its own after
  about a minute.
- **Animations.** `tb test` sets Android's animation scales to 0 for the run and restores
  them (`--keep-animations` to leave them). `tb do`/`look` sessions do not change them.
- **iOS: simulators only**, booted. The first run on a simulator installs tb's runner (~20 s).
- **iOS: let `launchApp` start the app.** An app already running, or opened by another tool,
  reports fewer UI changes, so waits are slower. UIKit animations run 10x faster in apps tb
  launches (`TB_IOS_FAST_ANIMATIONS=0` keeps normal speed).
- **iOS bundle id ≠ Android package.** One flow with `appId: ${APP_ID}` needs a different
  `-e APP_ID` per platform.

## Signed-in apps and real backends

- **`clearState` signs the user out** and deletes local data (Android `pm clear`; iOS empties
  the app's containers and resets its permissions). On a device the user signed in by hand,
  start with plain `- launchApp`. If an Android phone refuses to clear (some Oppo, Realme,
  Vivo, Xiaomi builds), the step fails saying which developer option allows it.
- **Flows act on real accounts.** Sign-in, sharing, invites, deletes and purchases hit the
  app's real backend. Ask before running such flows on the user's signed-in devices, and
  tell them what test data a run leaves behind.
- **Credentials** belong in `-e` values or the project's env files, never typed into a flow
  you write.

## Waiting and timing

- **Every lookup already waits.** `tapOn`, `assertVisible` and the other lookups wait up to
  17 s (7 s when `optional`) on device events, re-reading only when the screen changed. Slow
  content inside the UI (a list loading, a spinner then a result) needs nothing more.
- **`extendedWaitUntil`** (default timeout 17 s, like a lookup) is for an end state that can
  take longer: `extendedWaitUntil: {visible: "Report ready", timeout: 60000}`.
- **`waitForAnimationToEnd`** (default timeout 15 s) never fails. On Android and iOS it waits
  for the events to settle, then compares small screenshots until the screen is still for
  250 ms; on the web it is the event settle only. A small part that never stops moving (at
  most a tenth of the screen: a spinner, a pulsing dot) and a loop of any size that repeats
  (a shimmer) are set aside after 3 s. A wait that times out ends after one last screenshot
  at its timeout. Use it only for a long one-shot animation you must wait out, never "just
  in case": taps already wait for a still screen.
- **No sleeps or retries** to make a flow "safer"; they only slow it and hide the cause.
- **Assertions after a tap are safe.** A tap returns once the UI starts changing, so the next
  assertion never reads the screen from before it. An assertion can pass while a new screen
  is still sliding in; anything that acts (tap, type, scroll, `when:`) waits for a still
  screen first. On a slow transition, assert something on the new screen before acting.
- **`assertNotVisible`** passes once the element is gone from a settled screen.
- **Visible means on screen and inside its scrolling list**: a row scrolled up under a header
  (still inside the screen, out of its list) is not visible, and is not tapped.
- A bare `containsDescendants` picks the innermost element that holds the descendants.

## Taps

- tb waits for a still screen, finds the element again, then taps; each tap must start a UI
  change within 1 s or it counts as "did nothing" (`retryTapIfNoChange` then retries).
- **Covers**: something drawn over the element where the tap would land takes the tap. A bar
  (sticky footer, bottom tab bar, navigation bar, floating button, the keyboard, a section
  header pinned over a list's rows) is steered around: tb drags the list to bring the element
  clear (up to 2 drags), else taps its largest uncovered part. A modal layer over the target
  (a drawer, a sheet, a dialog) is waited for up to 2 s, then the step fails
  `<selector> is under <cover>`: close or finish the layer in the flow first. On the web the
  browser's hit test decides: tb waits for a veil, backdrop or toast at the tap point to go.
- **Loading overlays**: a see-through layer with a spinner takes taps while it shows; tb waits
  for it to go.
- **A closing screen still drawn** (a sheet leaving while the app is busy) is waited out (up
  to 3 s) before the next tap.
- **Moving targets**: an element still sliding in is read again until it rests (up to 2 s).
- **A target pushed away**: when a banner grew and pushed the element out of view, tb does
  not tap what is there now: the step fails on the target. Assert the banner first.
- **iOS taps nothing took**: a button shows every touch it gets. A tap on one that showed
  nothing while the screen stayed still was taken by something tb cannot see (a hidden
  veil): it is made again when the screen changes. A button that is a whole list row is also
  tapped again on a still screen (up to 2 more). When no tap gets through before the lookup
  timeout, the step fails `no tap reached <selector>: ...`.
- **System dialogs are answered**: Android "isn't responding" / "keeps stopping" gets Wait,
  then Close app if it comes back; the iOS notification prompt is allowed by default per
  `launchApp` `permissions` (`notifications: unset` leaves it to the flow).
- **iOS system alerts** (permission prompts) ignore taps while they animate in: a tap that
  leaves one up is made again (up to 3 more). One left up when `launchApp` restarts the app
  is declined first.
- **iOS 27: a half-height sheet over a full-screen cover** is missing from the accessibility
  tree, so tb (like VoiceOver) cannot see it.
- **Relative selectors are Maestro's**: `rightOf: X` means "further right than X", in any row;
  `below: X` "lower than X", in any column. Rows of identical buttons: use `index` (by
  position, top-left first; `-1` is the last), or combine `below`/`above` to box a row in.
- **Disabled buttons**: tb gives a disabled target up to 3 s to turn on (a form validating),
  then taps anyway. A selector with `enabled: false` is tapped at once.
- **Point taps** (`point: "50%,8%"`) depend on the layout and the device size. Use selectors.

## Typing

- `inputText` types into the **focused** field: tap the field (by `id`) first.
- Tapping a placeholder text only works while the field is empty: select fields by `id`.
- `eraseText: N` deletes N characters from the end of the focused field.
- `pressKey: Enter` on a field whose action is Next moves focus to the next field.
- The keyboard can cover the next button: a tap steers around it, and a tap whose target is
  not found while the keyboard is up hides it and looks again. `hideKeyboard` is rarely
  needed; after it, assert the element you act on next.

## Scrolling

- `scrollUntilVisible` defaults: `direction: DOWN`, `timeout: 20000`, the whole element in
  view (`visibilityPercentage` below 100 accepts any part). It reads first, so an element
  already on screen costs no drag.
- It drags without flinging, with drags sized to the list, and reads after each one, so it
  does not overshoot. Paging lists (carousels, pagers) spring back from drags; it then
  switches to flinging swipes. `speed` barely matters.
- `centerElement: true` stops when a drag leaves the element where it was (the list's end).
- It drags the list the element is in (a side pane, a sideways shelf), and moves on to
  another list after drags that show nothing new.
- Lists that load more at the end: a drag that moved nothing waits up to 2 s for the list to
  change before the next.

## Android

- **Toasts** count as on screen for 3.5 s from when they show: `assertVisible` on a Toast's
  text works, and `assertNotVisible` passes once that time is up.
- **`setLocation`** on Android 12+ uses the system's test location providers; older versions
  need an emulator.

## Jetpack Compose

- Compose leaves out of the accessibility tree a node that another node's bounds cover: a
  label just above a scrolled list can vanish from the tree. If a text above a scrolled
  Compose list "is not visible", give it room, or scroll back.

## Web

- Web flows use `url:`; `tb devices` lists `chrome`. `--headless` for CI. `url: ${URL}` with
  `-e URL=...` works too; a flow whose `appId` is an app id never runs in Chrome.
- Elements inside open shadow roots and cross-origin iframes are read and tapped like any
  other. `css:` selectors work only on the web.
- A `swipe` that starts on something made to be dragged (a range slider, a `draggable` item)
  is a real mouse drag; any other swipe scrolls.
- A `<select>`'s option list is drawn by the browser: tap the select, then `inputText` the
  option and `pressKey: Enter`.
- Finite CSS transitions and animations are waited out, up to 4 s.
- Each run gets a fresh browser profile (no cookies kept).

## Running many flows

- `--shard-split N` spreads flows over N devices from one queue; `--retries 1` re-runs a
  failed flow, on another device if one is free.
- A folder run follows its `config.yaml` (globs, tags, execution order).
- Multi-device: roles get free devices matching their `platform`/`device` (pin with
  `--device role=DEVICE`); `waitForSignal` times out after 120 s by default.
