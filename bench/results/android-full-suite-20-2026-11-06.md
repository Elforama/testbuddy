# Android full suite — 20 rounds

**400/420 passed; 20 failed.** Completed all 20 rounds of the unchanged 21-scenario upstream suite with zero flow retries on `emulator-5554` (API 36). Fully passing rounds: **0/20**.

Workspace commit: `117f771632798e9a1a41f7e52fddcc452af82f0d`. Tested runner code: `877a27e328f7dc348b0652d7d238e6590f317c71`. Agent: 0.1.22. Upstream benchmark: `a06388182d590d7f192775396ee9a9dafe355051`.

Runner SHA-256: `97e27164f6b6d308a1fa8b687ca545fbe9d9217f1f19cdd78cb28d6a6685f254`. App APK SHA-256: `fbb5ee3c74c933a1517c4b6e4eb83627a30335246678a2d47c9f45f8a1be5040`. All original flow and support-file hashes remained unchanged. Both emulators were preserved; emulator-5556 was left untouched.

| Flow | Passed | Failed | Median successful duration (s) |
| --- | ---: | ---: | ---: |
| `animations` | 20/20 | 0/20 | 39.162 |
| `appearance` | 20/20 | 0/20 | 9.143 |
| `cart` | 20/20 | 0/20 | 19.243 |
| `chat` | 20/20 | 0/20 | 24.188 |
| `deep_links` | 20/20 | 0/20 | 24.913 |
| `device` | 20/20 | 0/20 | 34.926 |
| `drag` | 20/20 | 0/20 | 24.559 |
| `feed` | 20/20 | 0/20 | 31.710 |
| `gestures` | 20/20 | 0/20 | 12.645 |
| `inputs` | 0/20 | 20/20 | — |
| `native` | 20/20 | 0/20 | 24.246 |
| `onboarding` | 20/20 | 0/20 | 5.659 |
| `popups` | 20/20 | 0/20 | 29.374 |
| `profile` | 20/20 | 0/20 | 34.593 |
| `remember_me` | 20/20 | 0/20 | 11.160 |
| `scripting` | 20/20 | 0/20 | 13.638 |
| `selectors` | 20/20 | 0/20 | 26.994 |
| `shop_checkout` | 20/20 | 0/20 | 70.470 |
| `sign_in` | 20/20 | 0/20 | 10.747 |
| `timing` | 20/20 | 0/20 | 17.065 |
| `transitions` | 20/20 | 0/20 | 10.440 |

## Failures

- `inputs`: Assertion is false: "Backspaces: 2" is visible. Rounds: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 18, 19, 20.
- `inputs`: Assertion is false: "Submitted: hello" is visible. Rounds: 17.

The AND-005 key callback diagnosis and proposed fixture correction are recorded in [the keyboard report](android-key-callback-diagnosis-2026-11-06.md). The original assertion was not modified or treated as passing.

Round 17 is a distinct, unresolved failure mode, cataloged as **AND-006**. After Enter, the field visibly contains `Hello` and the keyboard is hidden, but the app still displays `Submitted: nothing`. The assertion remained false for 17 seconds and six reads. This is not established to be the Backspace callback fixture issue. Its [trace](artifacts/android-full-suite-20-2026-11-06/round-17-inputs/log.txt), [screen listing](artifacts/android-full-suite-20-2026-11-06/round-17-inputs/screen.txt), [screenshot](artifacts/android-full-suite-20-2026-11-06/round-17-inputs/screenshot.png) and [hierarchy](artifacts/android-full-suite-20-2026-11-06/round-17-inputs/hierarchy.json) are retained with these results. All four previously repaired scenarios passed 20/20 each.

## Timing and coverage

Twenty round processes took 170.1 minutes total. Median round wall time: 509.500s; range 502.947–535.632s.

- One emulator, one runner binary, sequential repetitions; not a new paired Speedrun comparison.
- Zero flow retries; in-flow repeat/retry commands and normal action recovery remain part of the unchanged original flows.
- Inputs stops at Backspaces in 19 rounds and at the earlier Enter assertion in round 17; later steps in that flow are not covered.
- Success duration summaries exclude failed executions. Round wall time includes startup, reporting and failing assertion time.

Raw logs, JUnit reports and screenshots: `build/android-full-suite-20-2026-11-06/round-01` through `round-20`. Full commands, per-case results and input hashes are retained in [JSON](android-full-suite-20-2026-11-06.json); [CSV](android-full-suite-20-2026-11-06.csv) has all 420 case executions. The [issue catalog](../../docs/android-benchmark-issues.md) records each repair's earlier red/green and three-consecutive-pass evidence.
