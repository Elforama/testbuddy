# Android full validation — 2026-11-06

TestBuddy against both native and Flutter benchmark apps, both Everyday apps, and the official Speedrun benchmark app and Settings fixture. All runs use emulator-5554, Android 16/API 36, with zero flow retries. Emulator-5556 was left untouched.

Base commit: `af1b3249763836c4bbba5f17f0b4c55a02e291e0`; working-tree changes are uncommitted.

## Results

| Pass | Suite | Passed | Wall time |
| --- | --- | --- | --- |
| Original | native-suite | 14/16 | 388.3s |
| Original | flutter-suite | 11/16 | 469.2s |
| Original | everyday-native | 1/1 | 38.2s |
| Original | everyday-flutter | 1/1 | 36.1s |
| Original | speedrun-app | 13/21 | 512.3s |
| Original | speedrun-settings | 0/1 | 22.8s |
| Full candidate | candidate-native | 16/16 | 272.2s |
| Full candidate | candidate-flutter | 12/16 | 341.2s |
| Full candidate | candidate-everyday-native | 1/1 | 27.0s |
| Full candidate | candidate-everyday-flutter | 1/1 | 23.4s |
| Full candidate | candidate-speedrun | 15/21 | 572.6s |
| Full candidate | candidate-settings | 0/1 | 22.5s |
| Final targeted | final-native-regressions | 2/2 | 64.2s |
| Final targeted | final-speedrun-followups | 2/5 | 139.8s |
| Label adaptation | settings-android16-label-adaptation | 1/1 | 12.4s |

## Latest observed per scenario

This combines the full pass and targeted follow-ups; it is not a fresh full pass on the final runner.

| Suite | Latest passed |
| --- | --- |
| candidate-native | 16/16 |
| candidate-flutter | 12/16 |
| candidate-everyday-native | 1/1 |
| candidate-everyday-flutter | 1/1 |
| candidate-speedrun | 17/21 |
| candidate-settings | 0/1 |

## Candidate changes

- Clear virtual accessibility caches for WebView as well as Compose before reads.
- Invalidate the host snapshot after the first-key fallback before typing the remainder.
- Use Android input transaction completion before timestamping/injecting paced swipes.
- Stop post-tap motion recovery when its missing target is on a different screen context, checking both immediate and delayed navigation.
- Only center a scroll target while it is present, including when a pending edge correction survives a snapping animation.

Each change has a captured failing regression test followed by a passing test. All 99 Android agent unit tests, the full Go suite, and race checks for the engine/Android driver passed. Red/green logs and raw flow output are under `build/full-android-validation/`.

## Remaining failures

- candidate-flutter / Scenario good accessibility: Element not found: id: order_track
- candidate-flutter / Scenario navigation and back stack: Assertion is false: "Depth 3" is visible
- candidate-flutter / Scenario overlays: Assertion is false: "Under taps: 1" is visible
- candidate-flutter / Scenario screen transitions: Assertion is false: "Send 25 to Savings" is visible
- candidate-speedrun / Typing, keys, erasing, copy and paste, random text, a draft kept across restarts: Assertion is false: "Backspaces: 2" is visible
- candidate-speedrun / Elements told apart by place (index, relative, parent, child) and by state: Assertion is false: id: more-text is visible
- candidate-speedrun / Confetti, a button that turns on late, a layout shift, a busy JavaScript thread: Assertion is false: "Taps: 3" is visible
- candidate-speedrun / A JavaScript stack's screen closing while the JavaScript thread is busy: Assertion is false: "Send 25 to Savings" is visible
- candidate-settings / Settings benchmark (Android): Element not found: "Display size & text"

## Reproduction and limits

Pinned original runner SHA-256: `e9d43d0d2a4da94e5850e6e1ed867639e0e649e6574d3adcf85d118197e0bb53`. Candidate SHA-256: `1e54e6f50b750c61e759463db636d34bbe263f9f3f05ef3b2b6f545138e38a72`; Android agent 0.1.20. Final runner SHA-256: `f5ee8a9d3107a0c9cb8352ac705d15a786d5f4136cc209607616ba2720ba7f36` (targeted checks after pager/delayed-navigation fixes). Exact commands are in the JSON companion.

Speedrun fixture source: https://github.com/hubastard/speedrun-bench.git at `a06388182d590d7f192775396ee9a9dafe355051`. Official released APK SHA-256 matches the published release digest: `fbb5ee3c74c933a1517c4b6e4eb83627a30335246678a2d47c9f45f8a1be5040`. No upstream app flows were modified.

- One Android 16/API 36 emulator. One full pass per pinned runner; no retries.
- These are TestBuddy runs against Speedrun fixtures, not new Speedrun-versus-TestBuddy timings.
- Upstream Settings fixture expects Display size & text, but installed Android Settings exposes Display size and text. Original fixture retained without edits.
- Four Flutter scenario failures also appear in the previously recorded adapter baseline.
- The final runner has targeted native animation/lists and Speedrun failure checks after the last two host fixes. The complete 56-case matrix used the earlier candidate; both fingerprints and all failures are retained.
