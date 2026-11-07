# TestBuddy versus Speedrun on Android — 2026-11-06

Complete.

One sequential round per tool, same installed apps and unchanged flows on emulator-5554 (Android 16/API 36). Both tools use their default Android animation settings (off), with zero flow retries. The other emulator was left untouched.

TestBuddy `tb 0.3.0` (final local fixes, uncommitted) versus `speedrun v0.4.7`. Base commit: `af1b3249763836c4bbba5f17f0b4c55a02e291e0`. Exact runner, flow and app fingerprints are in the JSON companion.

## Comparison

Timing is the sum of **only the flows both tools passed**, from JUnit testcase durations. Each row reports the number of matched flows. Failed runs are never treated as faster completions.

| Benchmark | TB passed | Speedrun passed | Matched flows | TB time | Speedrun time | Less completion time |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| native-benchmark | 16/16 | 11/16 (1 unsupported) | 11 | 181.8s | 325.4s | TB 44.1% |
| flutter-benchmark | 12/16 | 9/16 (1 unsupported) | 9 | 136.6s | 171.5s | TB 20.4% |
| everyday-native | 1/1 | 1/1 | 1 | 24.4s | 20.4s | Speedrun 16.4% |
| everyday-flutter | 1/1 | 1/1 | 1 | 23.4s | 19.7s | Speedrun 15.9% |
| speedrun-benchmark | 16/21 | 20/21 | 16 | 396.3s | 425.3s | TB 6.8% |
| speedrun-settings-original | 0/1 | 0/1 | 0 | — | — | — |
| speedrun-settings-android16-label | 1/1 | 1/1 | 1 | 7.4s | 7.3s | Speedrun 2.2% |

Per-flow wins among shared successful flows:

- native-benchmark: TB faster on 1, Speedrun faster on 10.
- flutter-benchmark: TB faster on 5, Speedrun faster on 4.
- everyday-native: TB faster on 0, Speedrun faster on 1.
- everyday-flutter: TB faster on 0, Speedrun faster on 1.
- speedrun-benchmark: TB faster on 3, Speedrun faster on 13.
- speedrun-settings-original: TB faster on 0, Speedrun faster on 0.
- speedrun-settings-android16-label: TB faster on 0, Speedrun faster on 1.

Everyday native and Flutter are shown separately, covering both implementations in the requested Everyday group. Speedrun app and Settings fixtures are reported separately. The original Settings fixture and the separate Android 16 label adaptation are both retained.

## Interpretation

- The fresh full TestBuddy pass on Speedrun fixtures passed 16/21, including a recurring profile recovery failure. Earlier targeted passes are not substituted into this comparison.
- Speedrun passed 20/21 on its own fixtures. Both tools failed the Backspaces: 2 assertion.
- Matched aggregate timing is dominated by particular long flows: the native stress journey and the Speedrun app animation flow. Per-flow win counts and durations are reported too.

## Failures and compatibility

- Native and Flutter lists: scrollUntilVisibleIn is not supported by Speedrun v0.4.7. The original parse failure is retained; remaining Speedrun flows execute unchanged.
- native-benchmark / speedrun / Scenario animations: Element not found: Id matching regex: anim_target
- native-benchmark / speedrun / Scenario keyboard: Element not found: Id matching regex: kb_submit
- native-benchmark / speedrun / Scenario permissions: Assertion is false: "Location: denied" is visible
- native-benchmark / speedrun / Scenario screen transitions: Assertion is false: "Send 25 to Savings" is visible
- flutter-benchmark / tb / Scenario good accessibility: Element not found: id: order_track
- flutter-benchmark / tb / Scenario navigation and back stack: Assertion is false: "Depth 3" is visible
- flutter-benchmark / tb / Scenario overlays: Assertion is false: "Under taps: 1" is visible
- flutter-benchmark / tb / Scenario screen transitions: Assertion is false: "Send 25 to Savings" is visible
- flutter-benchmark / speedrun / Scenario good accessibility: Element not found: Id matching regex: order_track
- flutter-benchmark / speedrun / Scenario errors and retries: Couldn't hide the keyboard
- flutter-benchmark / speedrun / Scenario navigation and back stack: Assertion is false: "Depth 3" is visible
- flutter-benchmark / speedrun / Scenario onboarding and login: Assertion is false: "Wrong code" is visible
- flutter-benchmark / speedrun / Scenario overlays: Assertion is false: "Under taps: 1" is visible
- flutter-benchmark / speedrun / Scenario screen transitions: Assertion is false: "Send 25 to Savings" is visible
- speedrun-benchmark / speedrun / Typing, keys, erasing, copy and paste, random text, a draft kept across restarts: Assertion is false: "Backspaces: 2" is visible
- speedrun-benchmark / tb / Typing, keys, erasing, copy and paste, random text, a draft kept across restarts: Assertion is false: "Backspaces: 2" is visible
- speedrun-benchmark / tb / Edit the profile, change settings, set a PIN, sign out: Element not found: id: key-4 (it went out of sight while moving, and scrolling UP for 5s did not bring it back)
- speedrun-benchmark / tb / Elements told apart by place (index, relative, parent, child) and by state: Assertion is false: id: more-text is visible
- speedrun-benchmark / tb / Confetti, a button that turns on late, a layout shift, a busy JavaScript thread: Assertion is false: "Taps: 3" is visible
- speedrun-benchmark / tb / A JavaScript stack's screen closing while the JavaScript thread is busy: Assertion is false: "Send 25 to Savings" is visible
- speedrun-settings-original / tb / Settings benchmark (Android): Element not found: "Display size & text"
- speedrun-settings-original / speedrun / Settings benchmark (Android): Element not found: Text matching regex: Display size & text

## Per-flow results

Times on failed rows are elapsed time to failure, not completion time.

### native-benchmark

| Flow | TB | Speedrun |
| --- | --- | --- |
| Scenario good accessibility | Pass (10.605s) | Pass (10.548s) |
| Scenario poor accessibility | Pass (4.491s) | Pass (3.532s) |
| Scenario animations | Pass (17.095s) | Fail (87.304s) |
| Scenario controls | Pass (27.445s) | Pass (22.270s) |
| Scenario errors and retries | Pass (19.626s) | Pass (19.307s) |
| Scenario keyboard | Pass (10.615s) | Fail (21.825s) |
| Scenario lists | Pass (41.168s) | Unsupported |
| Scenario native toolkits | Pass (17.748s) | Pass (12.294s) |
| Scenario navigation and back stack | Pass (12.984s) | Pass (12.815s) |
| Scenario onboarding and login | Pass (20.204s) | Pass (17.808s) |
| Scenario overlays | Pass (16.465s) | Pass (15.255s) |
| Scenario permissions | Pass (7.249s) | Fail (20.133s) |
| Scenario slow and janky | Pass (21.328s) | Pass (19.442s) |
| Scenario screen transitions | Pass (6.774s) | Fail (20.095s) |
| Bench journey | Pass (24.608s) | Pass (187.076s) |
| killApp keeps the saved state (Android) | Pass (6.285s) | Pass (5.088s) |

### flutter-benchmark

| Flow | TB | Speedrun |
| --- | --- | --- |
| Scenario good accessibility | Fail (23.342s) | Fail (22.890s) |
| Scenario poor accessibility | Pass (6.267s) | Pass (6.591s) |
| Scenario animations | Pass (10.882s) | Pass (42.782s) |
| Scenario controls | Pass (29.261s) | Pass (31.163s) |
| Scenario errors and retries | Pass (19.394s) | Fail (10.453s) |
| Scenario keyboard | Pass (12.545s) | Pass (12.291s) |
| Scenario lists | Pass (34.456s) | Unsupported |
| Scenario native toolkits | Pass (19.868s) | Pass (14.227s) |
| Scenario navigation and back stack | Fail (35.788s) | Fail (30.135s) |
| Scenario onboarding and login | Pass (27.466s) | Fail (40.898s) |
| Scenario overlays | Fail (31.137s) | Fail (30.938s) |
| Scenario permissions | Pass (8.592s) | Pass (8.644s) |
| Scenario slow and janky | Pass (19.995s) | Pass (17.460s) |
| Scenario screen transitions | Fail (23.154s) | Fail (23.552s) |
| Bench journey | Pass (17.097s) | Pass (30.814s) |
| killApp keeps the saved state (Android) | Pass (12.046s) | Pass (7.571s) |

### everyday-native

| Flow | TB | Speedrun |
| --- | --- | --- |
| Everyday app journey | Pass (24.405s) | Pass (20.394s) |

### everyday-flutter

| Flow | TB | Speedrun |
| --- | --- | --- |
| Everyday app journey | Pass (23.373s) | Pass (19.655s) |

### speedrun-benchmark

| Flow | TB | Speedrun |
| --- | --- | --- |
| Taps on screens that never stop moving | Pass (41.011s) | Pass (120.035s) |
| Dark mode, screenshots and a recording | Pass (9.485s) | Pass (9.450s) |
| Cart lines, steppers and swipe to remove | Pass (18.859s) | Pass (17.690s) |
| Chat with the bot, long press and double tap a message, paste, quick replies | Pass (24.768s) | Pass (17.928s) |
| Deep links to a product, a lab screen and the cart | Pass (25.053s) | Pass (26.536s) |
| Launch arguments, location, permissions, photos, orientation, app lifecycle, airplane mode | Pass (36.036s) | Pass (25.789s) |
| Drag a slider and a card, scroll sideways, a long list with sticky headers | Pass (25.088s) | Pass (21.154s) |
| Feed with a ticker, stories, likes, a double tap, more posts and pull to refresh | Pass (31.742s) | Pass (29.482s) |
| Swipes in four directions, taps at a point, single, double and long press | Pass (13.684s) | Pass (7.999s) |
| Typing, keys, erasing, copy and paste, random text, a draft kept across restarts | Fail (29.181s) | Fail (28.365s) |
| Native UI (Jetpack Compose, SwiftUI): texts that change, views that come and go, a spring, a loading overlay, a lazy list | Pass (24.494s) | Pass (19.655s) |
| Onboarding pages, and no onboarding on the next launch | Pass (6.107s) | Pass (5.863s) |
| Popups that come one after another (native modals, a sheet) | Pass (29.222s) | Pass (29.296s) |
| Edit the profile, change settings, set a PIN, sign out | Fail (40.173s) | Pass (26.279s) |
| A remembered account survives clearState on iOS, but not clearKeychain | Pass (11.598s) | Pass (10.397s) |
| Scripts, env, conditions, repeat and retry | Pass (13.591s) | Pass (12.715s) |
| Elements told apart by place (index, relative, parent, child) and by state | Fail (43.768s) | Pass (23.933s) |
| Buy twice, with search, options, a promo code that fails once and the popups after | Pass (74.876s) | Pass (62.128s) |
| Sign in with a phone number and a code, a wrong one first | Pass (10.714s) | Pass (9.194s) |
| Confetti, a button that turns on late, a layout shift, a busy JavaScript thread | Fail (30.731s) | Pass (13.164s) |
| A JavaScript stack's screen closing while the JavaScript thread is busy | Fail (23.338s) | Pass (8.331s) |

### speedrun-settings-original

| Flow | TB | Speedrun |
| --- | --- | --- |
| Settings benchmark (Android) | Fail (18.800s) | Fail (18.762s) |

### speedrun-settings-android16-label

| Flow | TB | Speedrun |
| --- | --- | --- |
| Settings benchmark (Android 16 label adaptation) | Pass (7.425s) | Pass (7.260s) |


## Limits

- One round per tool and app variant on one Android 16/API 36 emulator. This is not a measured failure rate.
- Timing comparisons only sum flows that both tools passed; early failures are excluded from speed comparisons.
- Native/Flutter lists remain unedited and unsupported in Speedrun v0.4.7. They are included in coverage totals but excluded from matched timing.
- Tool order alternates across groups. Startup state, keyboard state and host load can still affect one-round measurements.
- The host had approximately 5.9 GB of allocated swap during and after the round. Host snapshots are retained; this round does not isolate host-load effects.

All original outcomes are preserved under `/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06`. No test app, runner source or assertion was changed during this comparison. Both emulators remain running.
