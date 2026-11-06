# Android focused-field typing optimization - 2026-11-06

Base commit: af1b3249763836c4bbba5f17f0b4c55a02e291e0, plus the uncommitted working tree. App: native Everyday on emulator-5554. Same APK and shared 64-step journey for both binaries. Three before/after pairs alternate order; each runner releases its agent after a pair member. No build, unit test, or other device benchmark ran concurrently with these timings.

## Results

| Version | Three flow times | Median |
|---|---|---:|
| before | 45.270 s, 39.960 s, 40.913 s | 40.913 s |
| after | 37.967 s, 39.579 s, 37.628 s | 37.967 s |

Median flow time improved by 2.946 s (7.2%). All six runs passed. Matched typing totals fell from 10.322 to 7.550 s; tap totals stayed approximately unchanged.

A fresh Speedrun 0.4.7 reference run passed in 33.023 s (34.788 s including runner setup). This is one reference run, not a repeated statistical comparison. Flow times exclude runner setup; the JSON also retains wall times.

## Why TestBuddy was slower

The preceding single-run comparison was TestBuddy 38.187 s versus Speedrun 32.592 s. Step totals attributed approximately 3.3 s of the gap to taps, 1.8 s to typing, and 1.4 s to launch plus assertions; faster scrolling and keyboard handling offset part of that. Launch alone exaggerates the startup difference because Speedrun spends much of app startup in the first assertion.

TestBuddy protects newly focused fields against a dropped first key. The runner previously read the whole hierarchy before and after that key, with an extra action/settle cycle before typing the remainder. This was repeated for every multi-character input. The new agent performs fresh focused-node queries and waits on accessibility callbacks locally. It retains the one-second timeout and single retry, verifies focus changes for OTP boxes, and splits Unicode at code-point boundaries. A missing direct focus node returns without injecting, so the runner can retain its full-screen guard for custom widgets.

The inspected Speedrun source (69c1d08e1046eb742177c216747ad9a357d0cd2f) sends the text in one type operation followed by settling. It also uses a targeted focus query around field taps. TestBuddy still performs fresh bounds-stability checks and motion recovery around taps. Current tap totals are higher; attributing an exact part of that remaining cost to each check would require another isolated experiment. These checks remain in place because the adversarial animation scenarios depend on them.

## Step totals

| Category | TB before median | TB after median | Speedrun single run |
|---|---:|---:|---:|
| Launch | 4.033 s | 3.968 s | 0.436 s |
| Assert | 1.121 s | 1.038 s | 3.950 s |
| Tap | 20.361 s | 20.366 s | 14.121 s |
| Input | 10.322 s | 7.550 s | 8.716 s |
| Hide | 1.270 s | 1.381 s | 0.996 s |
| Scroll | 3.365 s | 3.251 s | 4.000 s |
| Erase | 0.723 s | 0.615 s | 0.804 s |

Category medians are calculated independently and need not sum to the median flow time.

## Validation and final binary

Two agent tests first failed against the previous type operation: a dropped first key lost a character, and delayed field updates allowed the remainder to be sent before confirmation. They pass with device verification. Additional tests cover OTP focus changes, surrogate pairs and selection, bounded retries for fields with no observable text, and the no-injection fallback to the original screen guard. The complete Android agent unit suite passed; all Go packages and race checks on engine/Android driver passed.

The measured binary predates the final custom-widget fallback. The final change only routes an unavailable direct focus lookup back through the original runner guard, before injecting anything. Both binary hashes are recorded. The final binary was separately exercised on native and Flutter Everyday, animations, and keyboard flows. The first full native regression and Flutter regression used the measured binary.

- native-regression: 16/16 passed.
- flutter-regression: 3/3 passed.
- final-native: 3/3 passed.
- final-flutter: 3/3 passed.

Android unit tests: 91, failures/errors: 0. These samples found no reliability regression; they do not establish a universal failure rate.

[Detailed results and hashes](android-typing-optimization-2026-11-06.json). Local raw logs and JUnit reports: build/android-optimization-2/. Emulators remain running. No commit created.
