# Buddy Bench

Our own test app, built three times with the same screens, ids and timings
([SPEC.md](SPEC.md)): Android (`bench/android`), iOS (`bench/ios`) and web (`bench/web`). One
set of flows (`bench/flows`) runs on all three, so timings compare like for like across
platforms and need no third-party sites.

The app is deliberately hard to automate quickly: a sign-in spinner, a list that loads more
after a spinner, reviews that appear late, and screens that **never stop moving** (spinners,
a 100 ms ticker on Home, a per-frame stopwatch and a bouncing ball on Live). A runner that
waits for the screen to go still would wait forever; one that does not wait would tap too
early.

## Run it

```sh
go run ./bench/web                         # web app on http://127.0.0.1:7360/
bench/android/build.sh                     # then: adb install -r <apk>
bench/ios/build.sh                         # then: xcrun simctl install booted <app>

tb test bench/flows/journey.yaml -p android
tb test bench/flows/journey.yaml -p ios
tb test bench/flows/journey-web.yaml
```

## Time it

```sh
bench/time.py                      # every platform: a warm-up, then 5 timed runs each
bench/time.py --platforms ios --runs 3
```

Each run is the whole `tb test` command, start-up included: what a person waits for.

## Results (2026-11-02, one Mac, 5 timed runs after a warm-up, whole command)

| Platform | tb |
|---|---|
| Android (medium_phone emulator, Android 16) | **10.7 s** (10.3–11.1) |
| iOS (iPhone 17 simulator) | **15.4 s** (15.4–15.5) |
| Web (headless Chrome) | **7.6 s** (7.6–7.8) |

- `tb test` turns Android animations off during a run (its default).
- After the first run tb's agents stay warm, so later runs skip their start-up.
- Part of the Android set overlapped a web reliability run with 4 CPU hogs on the same Mac:
  its numbers may be a little high.

Compared with Maestro and WebdriverIO on the same journey: the chart in the main
[README](../README.md#speed).

## Tap reliability (web)

Speed is only half of it: a runner that taps before the screen is ready taps the wrong thing.
The web app has a click instrument (`bench/web/site/instrument.js`, on with `?instrument=1`)
that logs every click: what it hit, and whether a pushed screen was still in its 250 ms
slide-in.

```sh
go run ./bench/web -clicklog bench/results/web-clicks.jsonl
tb test bench/flows/journey-web-instrumented.yaml --headless     # as many times as you like
bench/clicks.py bench/results/web-clicks.jsonl
```

tb, with the instrument (2026-11-02):

| Build | Runs | Taps during a slide-in | Taps on the wrong element |
|---|---|---|---|
| Before the fix below | 52 | 0 | 12 ("Item 30" meant, "Item 34" hit) |
| After it | 15 | 0 | 0 |

The 12 were a race in tb's new scroll: on the web, scrollUntilVisible's drags were wheel
events, and Chrome sometimes applied one's scroll after tb had read the screen as still, so
the next tap landed one wheel step off (`results/web-clicks-before-fix.jsonl`,
`results/click-runs-before-fix.csv`; after: `results/web-clicks-after-fix.jsonl`). Drags now
scroll the element under the point directly (`scrollBy`, instant). tb never tapped during a
slide-in: it waits for a still screen before every tap.
