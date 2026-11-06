# Android benchmark issue catalog

Checkpoint: `ff21b3c` (2026-11-06). The saved full Speedrun app round passed **16/21** with runner SHA-256 `f5ee8a9d3107a0c9cb8352ac705d15a786d5f4136cc209607616ba2720ba7f36`.

[Evidence and diagnosis](../bench/results/android-speedrun-failure-analysis-2026-11-06.md) · [Paired runner comparison](../bench/results/android-tb-speedrun-comparison-2026-11-06.md)

Work proceeds sequentially. Each issue needs a regression that fails before its fix, package checks after the fix, and its unchanged device flow passing three consecutive times with zero flow retries. Keep the native animation/history recovery test working. Use emulator-5554; preserve both emulators and leave emulator-5556 untouched. Avoid extra fixed delays and whole-screen polling where existing events or state observations suffice.

| Order / ID | Scenario | Status | Why this order |
| --- | --- | --- | --- |
| AND-001 | Profile / final PIN confirmation | In progress | Clear stale-node/screen return bypasses the existing navigation guard; narrow host fix likely. |
| AND-002 | Selectors / Show more | Open | Deterministic double action; detect successful content replacement without weakening missed-tap recovery. |
| AND-003 | Timing / delayed layout shift | Open | Same replay decision, but a timer moves the button independently; must preserve recovery of genuinely missed taps. |
| AND-004 | Closing account list / amount input | Open | Accessibility presence and pixels do not establish touch readiness or field focus. |
| AND-005 | Inputs / Backspace callback counter | Open, cause unresolved | Both tools delete two characters but report different callback counts (TB 1, Speedrun 3); separate fixture/IME behavior from runner delivery. |

## AND-001 — stale recovery after successful navigation

- Reproduction: upstream `app/flows/profile.yaml`, final confirmation `key-4`.
- Observed: PIN completes and Profile appears; TB scrolls five times searching for the old keypad.
- Cause: `atRest` returns the last-seen node/screen after disappearance. Anonymous scroll-container matching accepts a different route's list. The later context guard requires a nil target, so it misses this branch.
- Acceptance: disappearing during stabilization across a route boundary performs no scroll/replay, while a target displaced within the same surviving list is still recovered. Original profile flow passes 3/3.
- Evidence/status: pending red/green regression and device verification.

## AND-002 — replay closes successfully opened content

- Reproduction: upstream `app/flows/selectors.yaml`, first `more-toggle` tap.
- Observed: first tap inserts `more-text` above the button; recovery taps again at the new position and closes it.
- Cause: missing native click notification plus movement is mistaken for a missed tap despite new content replacing the old point.
- Acceptance: exactly one tap opens the content, the next closes it, and `retryTapIfNoChange: false` remains exactly one tap. Preserve genuine missed-tap/banner recovery. Original selectors flow passes 3/3.
- Evidence/status: pending.

## AND-003 — timer-driven shift receives an extra tap

- Reproduction: upstream `app/flows/timing.yaml`, `shift` subscene.
- Observed: delayed banner moves the button after a successful second tap; recovery adds a fourth actual tap to three requested taps.
- Cause: movement alone does not prove delivery failed. Unlike AND-002, the layout change is independent of the tap.
- Acceptance: three requested taps give exactly three counts, while a deliberately lost moving-target tap is still recovered. Original timing flow passes 3/3; native animation/history regression remains green.
- Evidence/status: pending.

## AND-004 — visible form is not yet touch-ready

- Reproduction: upstream `app/flows/transitions.yaml`, choose Savings then tap `transfer-amount`.
- Observed: closing list receives the tap and selects Credit card; amount has no focus; typing returns success without changing the field.
- Cause: list leaves accessibility and fades before it stops intercepting touches. Exact early-return branch in the held-overlay guard was not recorded.
- Acceptance: wait for actual target readiness/focus without a blanket delay; type into the requested field, preserve the deliberate lost first reference-field tap check, and finish both account choices. Original transitions flow passes 3/3.
- Evidence/status: pending.

## AND-005 — Backspace text edits and callback count disagree

- Reproduction: upstream `app/flows/inputs.yaml`, Enter then two Backspaces.
- Observed: `Hello`/`hello` becomes `Hel`, but TB shows one callback and Speedrun reports three; expected two. TB's screenshot confirms its counter, so it is not just a stale accessibility read.
- Cause: unresolved; do not label it a dropped text deletion or weaken the assertion to pass.
- Acceptance: isolate native key delivery, IME composition/autocorrection, and React Native callback behavior; fix the responsible layer with a red/green regression and unchanged inputs flow 3/3. Retain shared-tool evidence if this is a fixture defect.
- Evidence/status: pending investigation.
