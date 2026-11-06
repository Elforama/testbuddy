# Flutter mobile benchmark, 6 November 2026

TestBuddy ran the shared mobile flows five times per platform against the Flutter debug app (`dev.testbuddy.bench.flutter`). The Android emulator and iPhone simulator ran concurrently on the same Mac. Each platform used one device, a fixed build and zero retries. Independent build checks and diagnostic runs are excluded.

Android used emulator-5554 (Android 16, API 36). iOS used the TestBuddy Suite iPhone 17 simulator (iOS 26.4). Flutter was 3.41.7. Both builds use the native toolkit mini-screens for the toolkit scenario and real OS permission dialogs.

| Platform | Round 1 | Round 2 | Round 3 | Round 4 | Round 5 | Total passed | Median round |
|---|---:|---:|---:|---:|---:|---:|---:|
| android | 12/16 | 12/16 | 11/16 | 12/16 | 12/16 | 59/80 | 517.5 s |
| ios | 6/15 | 6/15 | 6/15 | 6/15 | 7/15 | 31/75 | 445.3 s |

## Passes by flow

| Flow | Android | iOS |
|---|---:|---:|
| Bench journey | 5/5 | 0/5 |
| Scenario animations | 5/5 | 5/5 |
| Scenario controls | 5/5 | 1/5 |
| Scenario errors and retries | 5/5 | 5/5 |
| Scenario good accessibility | 0/5 | 0/5 |
| Scenario keyboard | 5/5 | 5/5 |
| Scenario lists | 5/5 | 0/5 |
| Scenario native toolkits | 5/5 | 0/5 |
| Scenario navigation and back stack | 0/5 | 0/5 |
| Scenario onboarding and login | 4/5 | 0/5 |
| Scenario overlays | 0/5 | 0/5 |
| Scenario permissions | 5/5 | 5/5 |
| Scenario poor accessibility | 5/5 | 5/5 |
| Scenario screen transitions | 0/5 | 0/5 |
| Scenario slow and janky | 5/5 | 5/5 |
| killApp keeps the saved state (Android) | 5/5 | Android only |

## Failed steps

Each count is the number of runs that stopped at that step. Flows stop at their first failure, so later steps may have had fewer than five attempts.

| Platform | Flow | Count | Reported failure |
|---|---|---:|---|
| ios | Bench journey | 5 | Assertion is false: "Item 30 details" is visible |
| ios | Scenario controls | 4 | Element not found: "Cherry" |
| android | Scenario good accessibility | 5 | Element not found: id: order_track |
| ios | Scenario good accessibility | 5 | Element not found: id: order_track |
| ios | Scenario lists | 5 | Assertion is false: "Picked Row 120" is visible |
| ios | Scenario native toolkits | 5 | Assertion is false: id: tk_title is visible |
| android | Scenario navigation and back stack | 5 | Assertion is false: "Depth 3" is visible |
| ios | Scenario navigation and back stack | 5 | Assertion is false: id: leave_dialog is not visible |
| android | Scenario onboarding and login | 1 | Assertion is false: "Wrong code" is visible |
| ios | Scenario onboarding and login | 5 | Assertion is false: "Wrong code" is visible |
| android | Scenario overlays | 5 | Assertion is false: "Under taps: 1" is visible |
| ios | Scenario overlays | 5 | Assertion is false: "Under taps: 1" is visible |
| android | Scenario screen transitions | 5 | Assertion is false: "Send 25 to Savings" is visible |
| ios | Scenario screen transitions | 5 | Assertion is false: "Send 25 to Savings" is visible |

## Reproduction

See [the Flutter benchmark instructions](../flutter/README.md) for the build, installation and five-round commands. The [machine-readable results](flutter-mobile-2026-11-06.json) include individual flow timings, errors, source base commit and artifact hashes. Full local JUnit, screenshots and accessibility hierarchies are under `build/flutter-validation/measured-{android,ios}/round-*/`.

## Interpreting failures

These measurements cover the new Flutter fixture and the existing TestBuddy runner together.
The platform agents and runner were held fixed. A failed assertion identifies the first
observable mismatch in a flow. Attribution to the runner or the app requires a separate
reproduction. Debug build timings from concurrent devices on one Mac are specific to this run.

## Flutter handling in other runners

Maestro mobile uses the semantic information Flutter publishes to the OS. The Maestro team
contributed `Semantics.identifier` to Flutter so compiled apps can expose IDs for both
platforms without a Dart VM connection or an app-side test SDK.
[Maestro Flutter documentation](https://docs.maestro.dev/get-started/supported-platform/flutter) · [Maestro engineering account](https://maestro.dev/blog/the-power-of-open-source-making-maestro-work-better-with-flutter)

Speedrun checks the installed iOS app bundle for `Frameworks/Flutter.framework`, caches the
answer by bundle ID for the run, and uses the Done key when hiding a Flutter keyboard. It also
recognizes an iOS tree shape in which scroll rows appear as siblings of their scroll view.
That tree repair is applied by shape on iOS. Its iOS scroll gesture ends at a lower velocity
to reduce flings that can consume a subsequent row tap. Android uses platform accessibility
events, frame stats and an idle probe. I found no Flutter-specific render callback in the
reviewed mobile execution path.
[Speedrun detector](https://github.com/hubastard/speedrun/blob/69c1d08e1046eb742177c216747ad9a357d0cd2f/host/runner.odin#L893-L904) · [Speedrun scrolling and keyboard notes](https://github.com/hubastard/speedrun/blob/69c1d08e1046eb742177c216747ad9a357d0cd2f/dev/DEVELOPMENT.md#L885-L905)

For TestBuddy, a candidate design is a separate Flutter adapter enabled for Flutter-owned
views. Detect the bundled engine and assets once, then use current-view evidence before
applying Flutter behavior. System dialogs, keyboards and native toolkit screens use the
platform drivers. An optional in-process bridge could wake a pending assertion when iOS
publishes a semantics update or Android starts displaying Flutter UI. The expected element
or action result still needs verification. This design was not enabled for these runs.
[Flutter iOS semantics notification](https://api.flutter.dev/ios-embedder/_flutter_view_controller_8h.html) · [Flutter Android display listener](https://api.flutter.dev/javadoc/io/flutter/embedding/engine/renderer/FlutterUiDisplayListener.html)
