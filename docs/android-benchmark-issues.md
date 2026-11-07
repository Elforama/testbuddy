# Android benchmark issue catalog

Checkpoint: `ff21b3c` (2026-11-06). The saved full Speedrun app round passed **16/21** with runner SHA-256 `f5ee8a9d3107a0c9cb8352ac705d15a786d5f4136cc209607616ba2720ba7f36`.

[Evidence and diagnosis](../bench/results/android-speedrun-failure-analysis-2026-11-06.md) · [Paired runner comparison](../bench/results/android-tb-speedrun-comparison-2026-11-06.md)

Work proceeds sequentially. After AND-002, investigate AND-004 before AND-003: a mounted-overlay observation may give firmer readiness evidence than a timer-driven shift. Each issue needs a regression that fails before its fix, package checks after the fix, and its unchanged device flow passing three consecutive times with zero flow retries. Keep the native animation/history recovery test working. Use emulator-5554; preserve both emulators and leave emulator-5556 untouched. Avoid extra fixed delays and whole-screen polling where existing events or state observations suffice.

| Order / ID | Scenario | Status | Why this order |
| --- | --- | --- | --- |
| AND-001 | Profile / final PIN confirmation | Validated | Clear stale-node/screen return bypasses the existing navigation guard; narrow host fix likely. |
| AND-002 | Selectors / Show more | Validated | Deterministic double action; detect successful content replacement without weakening missed-tap recovery. |
| AND-004 | Closing account list / amount input | Validated | Accessibility presence and pixels do not establish touch readiness or field focus. |
| AND-003 | Timing / delayed layout shift | Validated | Same replay decision, but a timer moves the button independently; must preserve recovery of genuinely missed taps. |
| AND-005 | Inputs / Backspace callback counter | Open: fixture/input-method limitation | Both tools delete two characters but report different callback counts (TB 1, Speedrun 3); separate fixture/IME behavior from runner delivery. |
| AND-006 | Inputs / Enter closes keyboard without submitting | Editor-submit fix validated; combined inputs pending AND-005 | New failure mode in the inputs flow; do not attribute it to the Backspace callback limitation without isolating it. |

## AND-001 — stale recovery after successful navigation

- Reproduction: upstream `app/flows/profile.yaml`, final confirmation `key-4`.
- Observed: PIN completes and Profile appears; TB scrolls five times searching for the old keypad.
- Cause: `atRest` returns the last-seen node/screen after disappearance. Anonymous scroll-container matching accepts a different route's list. The later context guard requires a nil target, so it misses this branch.
- Acceptance: disappearing during stabilization across a route boundary performs no scroll/replay, while a target displaced within the same surviving list is still recovered. Original profile flow passes 3/3.
- Evidence/status: red regression failed by scrolling the obsolete keypad; engine and full Go checks pass after the fix. Profile 3/3 (34.191s, 33.774s, 34.608s), native animation 1/1 (13.530s), zero flow retries. [Machine-readable validation](../bench/results/android-benchmark-issue-fixes-2026-11-06.json).

## AND-002 — replay closes successfully opened content

- Reproduction: upstream `app/flows/selectors.yaml`, first `more-toggle` tap.
- Observed: first tap inserts `more-text` above the button; recovery taps again at the new position and closes it.
- Cause: missing native click notification plus movement is mistaken for a missed tap despite new content replacing the old point.
- Acceptance: exactly one tap opens the content, the next closes it, and `retryTapIfNoChange: false` remains exactly one tap. Preserve genuine missed-tap/banner recovery. Original selectors flow passes 3/3.
- Evidence/status: full-flow red reproduced the unwanted close. Engine/full Go suites pass; original selectors flow 3/3 (27.018s, 26.263s, 27.503s), native animation 1/1 (16.095s), zero retries. Confirmation uses only an immediately following static assertion changing from unsatisfied to satisfied; dynamic expressions retain normal timing and an already-true assertion proves nothing. The assertion still executes normally. [Validation](../bench/results/android-benchmark-issue-fixes-2026-11-06.json).

## AND-003 — timer-driven shift receives an extra tap

- Reproduction: upstream `app/flows/timing.yaml`, `shift` subscene.
- Observed: delayed banner moves the button after a successful second tap; recovery adds a fourth actual tap to three requested taps.
- Cause: movement alone does not prove delivery failed. Unlike AND-002, the layout change is independent of the tap.
- Acceptance: three requested taps give exactly three counts, while a deliberately lost moving-target tap is still recovered. Original timing flow passes 3/3; native animation/history regression remains green.
- Evidence/status: red regression produced an unwanted fourth tap; engine/full Go/race checks pass. Original timing flow passed 3/3 (17.092s, 17.201s, 17.057s), native animation 1/1 (16.489s), upstream 12-scene animation flow 1/1 (38.859s), zero retries. Existing adjacent result labels are compared only in already-captured snapshots, with stable unique IDs and window identity. This is conservative evidence against replay, not proof of causality: an unrelated label immediately following the control can suppress recovery, so flow assertions remain authoritative. New banners, unchanged results, unrelated text above the control, interactive labels, duplicate IDs and changed windows do not suppress recovery. No device reads or waits added. [Validation](../bench/results/android-benchmark-issue-fixes-2026-11-06.json).

## AND-004 — visible form is not yet touch-ready

- Reproduction: upstream `app/flows/transitions.yaml`, choose Savings then tap `transfer-amount`.
- Observed: closing list receives the tap and selects Credit card; amount has no focus; typing returns success without changing the field.
- Cause: list leaves accessibility and fades before it stops intercepting touches. Exact early-return branch in the held-overlay guard was not recorded.
- Acceptance: wait for actual target readiness/focus without a blanket delay; type into the requested field, preserve the deliberate lost first reference-field tap check, and finish both account choices. Original transitions flow passes 3/3.
- Evidence/status: agent hidden-node/callback regressions, engine unsafe-cover/focus regressions all failed before their fixes. Agent 0.1.22: 101/101 unit tests; Go suite and engine/Android race checks passed. Original transitions 3/3 (10.132s, 10.372s, 9.933s); animation, overlays and toolkits each 1/1, zero retries. Callback waits in traces were 250–556ms; no fixed sleep or remote screen polling was added. A swallowed reference-field tap is corrected by one verified refocus before any characters. [Validation](../bench/results/android-benchmark-issue-fixes-2026-11-06.json).

## AND-005 — Backspace text edits and callback count disagree

- Reproduction: upstream `app/flows/inputs.yaml`, Enter then two Backspaces.
- Observed: `Hello`/`hello` becomes `Hel`, but TB shows one callback and Speedrun reports three; expected two. TB's screenshot confirms its counter, so it is not just a stale accessibility read.
- Cause: the fixture counts React Native Android `onKeyPress`, which is documented for soft-keyboard input only. TB and ADB injected deletes remove one character each without those callbacks. An extra callback already appears after Enter. Exact Enter/composition internals remain uninstrumented.
- Acceptance: isolate native key delivery, IME composition/autocorrection, and React Native callback behavior; fix the responsible layer with a red/green regression and unchanged inputs flow 3/3. Retain shared-tool evidence if this is a fixture defect.
- Evidence/status: isolated TB, ADB and Gboard probes completed. TB/ADB show count 1 after Enter and after both correct deletions; Gboard taps show 1 → 2 → 3; without Enter, Gboard taps show 0 → 1 → 2. Original flow remains failing and unedited. No runner workaround was introduced. [Detailed diagnosis and proposed fixture correction](../bench/results/android-key-callback-diagnosis-2026-11-06.md).

## Final combined regression round

Runner code commit `877a27e`; Android agent 0.1.22; emulator-5554; zero retries. All 21 upstream flow SHA-256 hashes match the pre-fix comparison, and the upstream checkout remains clean.

**20/21 passed**, up from 16/21 at the checkpoint. All four repaired scenarios passed again together: Profile 35.280s, Selectors 26.836s, Timing 17.468s, Transitions 10.272s. The only failure is the unchanged `Backspaces: 2` assertion in AND-005. This is one full-suite round, not three full-suite rounds. Each repaired flow separately passed its required three consecutive validation runs. Native animation/history checks also passed throughout the fixes.

Local checks passed: full Go suite, engine/Android race checks for the overlay/input change, engine race checks for the layout-shift change, and 101 Android agent unit tests. No new full-screen polling or blanket sleeps were introduced by these fixes. The layout-shift result-label heuristic has the limitations recorded under AND-003; it does not replace flow assertions.

[Per-flow outcomes, source/binary hashes and raw evidence paths](../bench/results/android-benchmark-issue-fixes-2026-11-06.json). Both emulators were kept running; emulator-5556 was left untouched.

## Twenty-round follow-up

Pinned workspace `117f771`, runner code `877a27e`, agent 0.1.22. Completed **20 full rounds / 420 scenario executions**, zero flow retries, on emulator-5554. Every round scored **20/21**, for **400 passed / 20 failed**. Each repaired scenario (AND-001 through AND-004) passed **20/20**. All flow/support files and the runner binary remained unchanged throughout the measurement.

All failures are in `inputs`, but they have two distinct signatures: **19** at `Backspaces: 2` (AND-005), and **1** at the earlier `Submitted: hello` assertion (AND-006, round 17). The latter is a newly observed failure mode; it is not established to be an external fixture defect. Later input-flow steps were not reached in these runs.

[Full report](../bench/results/android-full-suite-20-2026-11-06.md) · [420-case CSV](../bench/results/android-full-suite-20-2026-11-06.csv) · [Machine-readable evidence](../bench/results/android-full-suite-20-2026-11-06.json).

## AND-006 — Enter closes the keyboard without a submitted result

- Reproduction evidence: upstream `app/flows/inputs.yaml`, round 17 of the unchanged twenty-round batch, first Enter after typing `hello`. Observed 1/20; the submission assertion passed in the other 19 rounds before those runs failed on Backspace counting.
- Observed: first-key-verified typing reports success, Enter reports a change in 60ms, then `Submitted: hello` remains absent for 17 seconds and six reads. The field visibly contains `Hello`, the keyboard is hidden, and the submitted label still says `Submitted: nothing`. No Backspace command was reached in this round; its displayed counter is already 1.
- Cause: unresolved. Distinguish key/IME readiness, composition/autocapitalization, and React Native submit dispatch. A successful key injection or keyboard dismissal alone does not establish app submission. The screenshot confirms this is not just a stale hierarchy.
- Acceptance: isolate a red regression at the responsible layer, then preserve exactly one Enter action and one submission without a blanket sleep or blind resend. Keep the official flow unchanged. Validate submission independently of AND-005 and preserve normal native input behavior.
- Evidence: [trace](../bench/results/artifacts/android-full-suite-20-2026-11-06/round-17-inputs/log.txt), [screen listing](../bench/results/artifacts/android-full-suite-20-2026-11-06/round-17-inputs/screen.txt), [screenshot](../bench/results/artifacts/android-full-suite-20-2026-11-06/round-17-inputs/screenshot.png), and [hierarchy](../bench/results/artifacts/android-full-suite-20-2026-11-06/round-17-inputs/hierarchy.json). No production change was made during the twenty-round measurement.


### AND-006 editor-submit validation

Agent 0.1.23 uses the focused single-line field's advertised `ACTION_IME_ENTER` on API 30+, falling back to ordinary key injection only when unavailable/unhandled. Multiline Enter and long presses retain hardware semantics. A handled editor action is never followed by another Enter. This removes the ambiguous hardware/IME submission path without adding a delay or polling; the original internal cause was not instrumented.

Red: the old key path ignored the available editor action and produced zero submissions in `InputTest.enterUsesTheEditorsSubmitActionWithoutInjectingAnotherKey`. Green: 103 agent tests and affected Go packages passed. Native keyboard Next/Done, multiline, and key-handler checks passed. The original inputs sequence through the first `Submitted: hello` assertion passed **20/20** with zero retries; this is isolated submission coverage, not a claim that the full inputs flow passed before AND-005 is fixed. [Evidence](../bench/results/android-input-issue-fixes-2026-11-06.json).
