# Benchmark results

## Android scenarios at `9b40e5d`

All 14 scenario flows passed on the Android 16 arm64 emulator. After one warm-up,
the timed suite took **298.5 seconds** on an Apple M1 Pro with macOS 26.5.2.
Lists took 42.3 s and animations took 30.2 s.
The [per-flow report](scenarios-9b40e5d.json) records the full commit SHA and device.

## iOS scenarios at `e7f26a6`

Test Buddy passed all 14 scenarios on each of three timed passes (**42/42**).
The whole scenario suite took **317.2 seconds** at the median (313.6–317.6 s).
Animations took 27.5 s, controls 37.9 s, and onboarding 25.2 s at the median.

Measured on an Apple M1 Pro with macOS 26.5.2 and an iPhone 17 simulator running iOS 26.4.
`bench/scenarios.py --platforms ios --runs 3` ran one warm-up before the timed passes.
The [per-flow timings](scenarios-e7f26a6.json) record the full commit SHA and the clean working tree.

## tb at `7147070` (2026-11-03)

Measured with tb built from commit `7147070` ("Hide the keyboard when a tap's target is under
it; portable bench flows"), on one Mac (Apple M5 Pro, macOS 27.0.1):

- **Android:** emulator `sdk gphone64 arm64`, Android 16, no hardware keyboard (the full
  on-screen keyboard shows).
- **iOS:** iPhone 17 simulator, iOS 27.
- **Web:** headless Chrome, bench web app on `http://127.0.0.1:7360/`.

One run at a time: nothing else drove the devices while a run was timed. Times are for the
whole `tb test` command, start-up included.

### Journey

[`bench/flows/journey.yaml`](../flows/journey.yaml) (web:
[`journey-web.yaml`](../flows/journey-web.yaml)), timed with [`bench/time.py`](../time.py):
one warm-up run (not counted), then 5 timed runs per platform.

| Platform | Average | Range | Passed |
|---|---|---|---|
| Android | **23.9 s** | 21.8–25.3 s | 5/5 |
| iOS | **20.0 s** | 19.5–20.9 s | 5/5 |
| Web | **9.3 s** | 9.2–9.5 s | 5/5 |

### Scenario suite

Every flow in [`bench/flows/scenarios`](../flows/scenarios) ([SCENARIOS.md](../SCENARIOS.md)),
one `tb test` of the whole folder per platform (one pass, not an average).

| Scenario | Android | iOS | Web |
|---|---|---|---|
| Good accessibility | 13.2 s | 15.9 s | 5.9 s |
| Poor accessibility | 6.2 s | 7.2 s | 1.6 s |
| Animations | 14.3 s | 26.8 s | 6.6 s |
| Controls | 38.3 s | 31.7 s | 15.9 s |
| Errors and retries | 23.6 s | 22.4 s | 12.9 s |
| Keyboard | 12.2 s | 12.0 s | 3.3 s |
| Lists | 58.0 s | 50.9 s | 11.5 s |
| Native toolkits | 27.1 s | 21.5 s | 16.4 s |
| Navigation and back stack | 18.5 s | 20.8 s | 6.0 s |
| Onboarding and login | 26.3 s | 21.2 s | 10.5 s |
| Overlays | 18.5 s | 15.5 s | 22.4 s |
| Permissions | 9.7 s | 13.1 s | 1.2 s |
| Slow and janky | 25.0 s | 20.2 s | 16.9 s |
| **Passed** | **13/13** | **13/13** | **13/13** |
| **Whole run** | **292 s** | **285 s** | **131 s** |

The baseline these fixes started from, at `4e92d08`, is in
[`scenarios-4e92d08.json`](scenarios-4e92d08.json): 36/39 on Android, 9/39 on iOS and 38/39 on
web over 3 passes ([../README.md](../README.md#scenario-baseline)).
