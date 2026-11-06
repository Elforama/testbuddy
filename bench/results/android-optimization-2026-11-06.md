# Android runner optimization, 6 November 2026

Three alternating before/after pairs ran the identical Everyday native Android APK and shared journey on emulator-5554, with no flow edits or retries. The first pair ran before then after, the second after then before, and the third before then after. Timing pairs ran on the same emulator without concurrent simulator tests. Each version passed all three runs.

| Version | Flow times | Median |
|---|---|---:|
| before | 51.557 s, 53.685 s, 53.192 s | 53.192 s |
| after | 40.487 s, 40.683 s, 39.355 s | 40.487 s |

Median flow time fell by **12.7 seconds (23.9%)**. The earlier Speedrun native Android run was 46.360 seconds. It was collected separately and is included as a reference, with no new Speedrun measurement in these pairs.

## Cause and changes

The original cross-runner comparison measured 37.4 seconds in TestBuddy taps versus 20.7 seconds in Speedrun taps. TestBuddy repeatedly ran motion-recovery reads after successful Compose interactions. After navigation, recovery waited one second for the button that had just disappeared. Keyboard hiding tried Escape, waited one second without a change, then used Back.

Recovery now accepts an already reported click or a unique same-window checkbox/focus state change as delivery evidence. If recovery is still needed, it waits for settling, checks the target once, and retains the existing recovery for a target displaced outside its scrolling list. The next flow assertion determines the navigation result.

Android keyboard hiding checks for the input-method window and sends Back on the device, then waits for window events until the keyboard disappears. It sends no key if the keyboard is already absent and reports an error if it remains visible.

## Median step totals

| Category | Before | After |
|---|---:|---:|
| Tap | 31.849 s | 20.262 s |
| Hide | 3.671 s | 1.220 s |
| Input | 8.781 s | 9.868 s |
| Launch | 3.314 s | 3.311 s |
| Scroll | 3.252 s | 3.410 s |
| Assert | 0.839 s | 1.322 s |
| Erase | 0.562 s | 0.700 s |

Category medians are calculated separately, so they need not sum to the median total.

## Validation

Regression tests first reproduced unnecessary recovery reads, waiting for a removed navigation button, and the missing on-device keyboard operation. The final engine and Android driver tests and Android agent unit tests passed. After timing collection, the focused-field shortcut was tightened to require newly acquired focus. The final binary then passed the Everyday, controls and keyboard flows, plus the Flutter checks listed below. The full Go checks and race results are retained locally with the device reports.

- native-regression: 16/16 passed.
- ios-regression: 1/1 passed.
- final-validation: 3/3 passed.
- flutter-regression: 3/3 passed.

[Detailed results and fingerprints](android-optimization-2026-11-06.json) · [Original runner comparison](everyday-native-runners-2026-11-06.md)

Local logs and paired JUnit reports are under build/android-optimization/.
