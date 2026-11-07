# Speedrun versus TestBuddy on Flutter

Speedrun v0.4.7 ran one round per platform. TestBuddy figures come from the five Flutter-adapter rounds on the same devices. Android and iOS ran concurrently on one host in each benchmark. The installed APK and Dart kernel hashes match. Each tool used its normal mobile animation settings.

| Platform | Speedrun completed flows | TestBuddy across the same flows | Speedrun total wall time |
|---|---:|---:|---:|
| android | 9/15 passed | 55/75 passed | 568.0 s |
| ios | 6/14 passed | 42/70 passed | 334.3 s |

Lists was excluded on both platforms. Speedrun rejected the scoped horizontal scroll command `scrollUntilVisibleIn` before executing any flow. The other scenarios used the original test files. Wall time includes setup, failures and early exits. Matched successful-flow timing below compares equivalent completed flows. A single Speedrun round cannot establish its failure rate.

## android

| Flow | Speedrun | Seconds | TestBuddy passes | TestBuddy median seconds on passes |
|---|---|---:|---:|---:|
| Scenario good accessibility | ERROR | 39.7 | 0/5 | — |
| Scenario poor accessibility | SUCCESS | 15.5 | 5/5 | 14.8 |
| Scenario animations | SUCCESS | 82.7 | 5/5 | 24.8 |
| Scenario controls | SUCCESS | 52.2 | 5/5 | 53.3 |
| Scenario errors and retries | SUCCESS | 34.9 | 5/5 | 33.1 |
| Scenario keyboard | SUCCESS | 32.2 | 5/5 | 34.2 |
| Scenario native toolkits | SUCCESS | 41.8 | 5/5 | 40.6 |
| Scenario navigation and back stack | ERROR | 48.8 | 0/5 | — |
| Scenario onboarding and login | ERROR | 26.1 | 5/5 | 53.7 |
| Scenario overlays | ERROR | 35.3 | 0/5 | — |
| Scenario permissions | ERROR | 28.0 | 5/5 | 21.9 |
| Scenario slow and janky | SUCCESS | 22.2 | 5/5 | 25.5 |
| Scenario screen transitions | ERROR | 27.6 | 0/5 | — |
| Bench journey | SUCCESS | 54.4 | 5/5 | 40.6 |
| killApp keeps the saved state (Android) | SUCCESS | 16.1 | 5/5 | 29.2 |

### First failures

- **Scenario good accessibility** — Element not found: Id matching regex: order_track
- **Scenario navigation and back stack** — Assertion is false: "Depth 3" is visible
- **Scenario onboarding and login** — Assertion is false: "Page 2 of 3" is visible
- **Scenario overlays** — Assertion is false: "Under taps: 1" is visible
- **Scenario permissions** — Assertion is false: "Location: denied" is visible
- **Scenario screen transitions** — Assertion is false: "Send 25 to Savings" is visible

For the 9 flows that Speedrun passed and TestBuddy passed 5/5, summed successful-flow times were **352.0 s for Speedrun** and **296.1 s for TestBuddy**. TestBuddy uses the median for each flow.

## ios

| Flow | Speedrun | Seconds | TestBuddy passes | TestBuddy median seconds on passes |
|---|---|---:|---:|---:|
| Scenario good accessibility | ERROR | 25.1 | 0/5 | — |
| Scenario poor accessibility | SUCCESS | 6.4 | 5/5 | 9.3 |
| Scenario animations | SUCCESS | 24.5 | 5/5 | 31.9 |
| Scenario controls | ERROR | 37.2 | 5/5 | 51.1 |
| Scenario errors and retries | ERROR | 7.9 | 5/5 | 23.1 |
| Scenario keyboard | ERROR | 9.4 | 5/5 | 17.9 |
| Scenario native toolkits | ERROR | 7.6 | 2/5 | 26.6 |
| Scenario navigation and back stack | ERROR | 26.0 | 0/5 | — |
| Scenario onboarding and login | SUCCESS | 25.0 | 0/5 | — |
| Scenario overlays | ERROR | 30.8 | 0/5 | — |
| Scenario permissions | SUCCESS | 11.1 | 5/5 | 15.9 |
| Scenario slow and janky | SUCCESS | 18.5 | 5/5 | 24.7 |
| Scenario screen transitions | ERROR | 23.3 | 0/5 | — |
| Bench journey | SUCCESS | 65.8 | 5/5 | 28.7 |

### First failures

- **Scenario good accessibility** — Element not found: Id matching regex: order_track
- **Scenario controls** — Element not found: Id matching regex: field_secret
- **Scenario errors and retries** — Couldn't hide the keyboard: its field has no Done key (a multiline field or a number pad has none), and a Flutter app closes the keyboard on nothing else unless it asks to. Tap where the app closes it (outside the field, if it unfocuses on a tap there), or give the field a Done action (textInputAction: TextInputAction.done).
- **Scenario keyboard** — Couldn't hide the keyboard: its field has no Done key (a multiline field or a number pad has none), and a Flutter app closes the keyboard on nothing else unless it asks to. Tap where the app closes it (outside the field, if it unfocuses on a tap there), or give the field a Done action (textInputAction: TextInputAction.done).
- **Scenario native toolkits** — Couldn't hide the keyboard: its field has no Done key (a multiline field or a number pad has none), and a Flutter app closes the keyboard on nothing else unless it asks to. Tap where the app closes it (outside the field, if it unfocuses on a tap there), or give the field a Done action (textInputAction: TextInputAction.done).
- **Scenario navigation and back stack** — Assertion is false: id: leave_dialog is not visible
- **Scenario overlays** — Assertion is false: "Under taps: 1" is visible
- **Scenario screen transitions** — Assertion is false: "Send 25 to Savings" is visible

For the 5 flows that Speedrun passed and TestBuddy passed 5/5, summed successful-flow times were **126.2 s for Speedrun** and **110.6 s for TestBuddy**. TestBuddy uses the median for each flow.

[Per-flow data](speedrun-flutter-2026-11-06.json) · [TestBuddy baseline](flutter-adapter-2026-11-06.md)
