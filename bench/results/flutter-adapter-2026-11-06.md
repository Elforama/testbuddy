# Flutter adapter benchmark, 6 November 2026

Five complete rounds per platform on the dedicated Android 16 emulator and iPhone 17 / iOS 26.4 simulator. Each used zero retries. The iOS Flutter application had the same Dart kernel SHA-256 as the baseline. The Android APK was rebuilt to include the first-frame bridge. The Android agent and actions are identical to the baseline.

## Results

| Platform | Baseline passed | Adapter passed | Passes by round | Median round before → after | Native app regression suite |
|---|---:|---:|---|---|---:|
| android | 59/80 | 60/80 | 12, 12, 12, 12, 12 | 517.5 s → 575.1 s | 16/16 |
| ios | 31/75 | 46/75 | 9, 10, 8, 9, 10 | 445.3 s → 501.8 s | 15/15 |

Round times include failing flows stopping early, and both devices share one host. On the animation flow, which passed every time on each platform, median iOS time was 32.9 s before and 31.9 s after. Android animation medians were 13.1 s before and 24.8 s after. These runs were collected at different times under concurrent load. The timing differences do not isolate adapter overhead.

## Flow passes

| Flow | Android before → after | iOS before → after |
|---|---:|---:|
| Bench journey | 5/5 → 5/5 | 0/5 → 5/5 |
| Scenario animations | 5/5 → 5/5 | 5/5 → 5/5 |
| Scenario controls | 5/5 → 5/5 | 1/5 → 5/5 |
| Scenario errors and retries | 5/5 → 5/5 | 5/5 → 5/5 |
| Scenario good accessibility | 0/5 → 0/5 | 0/5 → 0/5 |
| Scenario keyboard | 5/5 → 5/5 | 5/5 → 5/5 |
| Scenario lists | 5/5 → 5/5 | 0/5 → 4/5 |
| Scenario native toolkits | 5/5 → 5/5 | 0/5 → 2/5 |
| Scenario navigation and back stack | 0/5 → 0/5 | 0/5 → 0/5 |
| Scenario onboarding and login | 4/5 → 5/5 | 0/5 → 0/5 |
| Scenario overlays | 0/5 → 0/5 | 0/5 → 0/5 |
| Scenario permissions | 5/5 → 5/5 | 5/5 → 5/5 |
| Scenario poor accessibility | 5/5 → 5/5 | 5/5 → 5/5 |
| Scenario screen transitions | 0/5 → 0/5 | 0/5 → 0/5 |
| Scenario slow and janky | 5/5 → 5/5 | 5/5 → 5/5 |
| killApp keeps the saved state (Android) | 5/5 → 5/5 | Android only |

## First failures on Flutter

| Platform | Flow | Count | First failure |
|---|---|---:|---|
| android | Scenario good accessibility | 5 | Element not found: id: order_track |
| android | Scenario navigation and back stack | 5 | Assertion is false: "Depth 3" is visible |
| android | Scenario overlays | 4 | Assertion is false: "Under taps: 1" is visible |
| android | Scenario overlays | 1 | Element not found: id: sheet_close |
| android | Scenario screen transitions | 5 | Assertion is false: "Send 25 to Savings" is visible |
| ios | Scenario good accessibility | 5 | Element not found: id: order_track |
| ios | Scenario lists | 1 | Assertion is false: "Picked Card 6-12" is visible |
| ios | Scenario native toolkits | 3 | Assertion is false: id: tk_title is visible |
| ios | Scenario navigation and back stack | 5 | Assertion is false: id: leave_dialog is not visible |
| ios | Scenario onboarding and login | 5 | Assertion is false: "Wrong code" is visible |
| ios | Scenario overlays | 5 | Assertion is false: "Under taps: 1" is visible |
| ios | Scenario screen transitions | 5 | Assertion is false: "Send 25 to Savings" is visible |

## Verification

The simulator integration test checked the Flutter menu, a UIKit screen in the same process, a Flutter point after dismissal, and the system keyboard over the Flutter app. It verified the actual drag routing on each screen. Native Journey smoke tests passed on both devices. Go, Swift and Android agent unit suites passed. Package detection tests covered split APKs, IPA bundle boundaries and simulator apps.

The Android APK emitted first-display callbacks through the existing accessibility channel. The callback wakes a check and does not certify action delivery. The iOS probe observes visible Flutter semantics updates. The host verifies the requested postcondition through the existing accessibility snapshot.

The Android harness started round 1 with an earlier packaging of the iOS agent. Rounds 2–5 used the final runner. The Android agent and Android app were identical throughout. Per-round runner hashes appear in the detailed results.

[Detailed results and build fingerprints](flutter-adapter-2026-11-06.json) · [Baseline](flutter-mobile-2026-11-06.md) · [Adapter setup](../../adapters/flutter/README.md)
