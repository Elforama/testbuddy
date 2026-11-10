# Roadmap

What would improve tb most next, ranked by reliability first ("a slightly slower but
dependable test beats a faster flaky one"). Each item says why it matters and roughly what it
takes. Done items move to the end with the change that shipped them.

Last reviewed: 2026-11-10.

## Top five

### 1. Make CI deterministic: flaky unit tests and the WebView step

Every pull request into `main` now needs the seven CI jobs to pass (the `Main` ruleset), so
one flaky test blocks every merge.

- `internal/driver/android`: `TestRequestTokens` ("agent connection closed",
  `connect_test.go:697`) and `TestAdoptKeepsSeqIncreasing` (no "changed" event within 3 s,
  `driver_test.go:99`) each failed one `main` run (2026-11-09 and 2026-11-07) and passed on
  the next. Same shape as issue #17: find the race in the test's fake, not a timeout bump.
- `bench/flows/scenarios/native.yaml` fails locally on the WebView toolkit screen: `Tap on
  "Type here"` finds no such element. The timeline of that run shows the field on screen,
  and tb read 77 elements without it, and the trace reports "the screen changed without the
  device reporting it". tb built from `main` before this roadmap fails the same way.

Each gets an issue, a failing test or flow step first, then the fix (AGENTS.md).

### 2. Run other projects' flows in CI as a compatibility check

tb runs Maestro flows unchanged; the best evidence is flows tb's authors did not write.

- Maestro's own end-to-end suite: `e2e/workspaces/demo_app` in the Maestro repository,
  against its `demo_app` (a small Flutter app for Android and iOS; its builds are listed in
  `e2e/manifest.txt`).
- React Native's RN Tester (`packages/rn-tester/.maestro`) and Thunderbird for Android
  (`ui-flows/`) keep flows in their repositories.

Every failure is either a compatibility gap or a tb bug. Fetch the apps and flows in CI (no
copies in this repository), start with `demo_app`, and record which flows pass.

### 3. A built-in flakiness check: `tb test --repeat N`

Run each flow N times (or `--until-fail`) and report, per step, how often it failed and with
which errors. Today the 20-round reliability runs are scripted by hand
(`bench/results/android-full-suite-20-*`); this makes "is this flow dependable?" one
command, for users and for every fix here.

### 4. A visual timeline of each run: done

`tb test --timeline` (and `tb replay --timeline`) writes `timeline.html` in each flow's debug
folder: every step, nested as it ran, with the screen before and after it, where each tap and
swipe went, its error and its trace lines. See [Done](#done).

Follow-ups: link the timeline from the HTML report's summary (done for each flow's entry);
show multi-device flows side by side, one lane per role; optionally keep the screenshots
as files for very long flows.

### 5. Measure agent mode on AndroidWorld

AndroidWorld (Google Research: 116 tasks over 20 apps on an emulator, scored by reading the
device's state over adb) rates agents, not test runners. Rate "a model driving the phone
through `tb look` / `tb do`" against the benchmark's own text-only agent, with the same model,
task seeds and step budget, on a subset first (about 20 tasks). Report the success rate,
tokens per step and time per task. Check first that its accessibility setup and tb's agent
coexist on one emulator.

## Next in line

- **An official GitHub Action**: install tb, boot an emulator or simulator, upload failure
  folders and timelines.
- **Re-run from a step**: `tb test flow.yaml --from 12`, against a live session, to iterate
  on a failing step without the whole flow.
- **Watch mode**: `tb test --watch` re-runs a flow when its YAML changes.
- **Start a device**: start an emulator or boot a simulator when none runs.
- **Record failures as video**: keep a short recording of each failed flow (the recording
  commands exist).
- **Label failures**: say whether a failure looks like the app, a timing race or the device
  connection, with the reason.
- **Real iPhones**: tb drives iOS simulators only.
- **Web speed**: the bench journey takes 7.6 s on the web; part of that is waiting for a
  settled page, which stays, but the settle wait likely has room.
- **Plugin for more systems**: the Claude Code plugin bundles tb for Apple silicon Macs only;
  add Linux and Intel Macs for CI and dev containers.

## Known problems

- AND-005 ([android-benchmark-issues.md](android-benchmark-issues.md)): Backspace edits the
  text but a React Native `onKeyPress` counter disagrees; open.
- `${}` in the middle of a JSON `mockNetwork` body is cut short by the shared interpolation
  rule (each `${` runs to the last `}` before the next `$`). Use one expression for the whole
  body or `file:`; documented.
- `tb check --lint` warns on `bench/flows/scenarios/overlays.yaml:9` (`"Delete item?"` also
  matches "Delete item"); the flow is unchanged.

## Done

| Item | Shipped in |
|---|---|
| `tb a11y --fix`: add missing ids, testIDs, data-testids and testTags in place | #34 |
| `tb check --lint`: coordinates, indexes, accidental regex text, optional assertions, flows that assert nothing | #34 |
| Recorded flows use stable selectors (`childOf`, `below`, id + text) instead of points and indexes | #34 |
| `mockNetwork` / `clearNetworkMocks` for web flows | #34 |
| Android: Enter types a new line in a multi-line Compose field (issue #32) | #35 |
| iOS: a tap held back when its button moved under it (issue #18) | `e7f26a6` |
| Flaky `TestEventsDisconnected` (issue #17) | `25a7fa7` |
| `tb test --timeline`: the run as an HTML timeline | this change |
