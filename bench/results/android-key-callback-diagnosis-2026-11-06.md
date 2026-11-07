# Android Backspace callback diagnosis

**Follow-up:** These are historical observations from agent 0.1.22. Agent 0.1.29 now uses a guarded API 33+ accessibility input connection for Backspace and an advertised editor action for Enter, without replacing the selected keyboard. The unchanged complete inputs flow passed 20/20 and the clean complete suite passed 21/21 on API 36. See the [final validation report](android-input-issue-fixes-2026-11-06.md). The original measurements and recommendation below are retained as the diagnosis at that earlier checkpoint.

At that checkpoint, issue **AND-005** was classified as a keyboard-dependent fixture limitation, not evidence that TB dropped a text deletion. The original `inputs.yaml` still failed; it had not been edited or counted as passing.

Device: `emulator-5554`, Android API 36, Gboard. Agent: 0.1.22. Runner fixes: `877a27e`. Upstream benchmark: `a06388182d590d7f192775396ee9a9dafe355051`. Each probe starts the unchanged app afresh, opens Inputs, focuses `key-input`, types `hello`, and records fresh accessibility snapshots after each action. The first three probes then submit Enter and refocus the field before deleting twice.

| Delivery path | After typing | After Enter | After Delete 1 | After Delete 2 |
| --- | --- | --- | --- | --- |
| TB agent key events | `hello`, count 0 | `Hello`, count 1 | `Hell`, count 1 | `Hel`, count 1 |
| ADB `input keyevent 67` | `hello`, count 0 | `Hello`, count 1 | `Hell`, count 1 | `Hel`, count 1 |
| Coordinate taps on Gboard Delete | `hello`, count 0 | `Hello`, count 1 | `Hell`, count 2 | `Hel`, count 3 |
| Gboard Delete taps, **without Enter** | `hello`, count 0 | omitted | `hell`, count 1 | `hel`, count 2 |

All paths delete exactly two characters. The extra callback is already present after Enter. TB's injected deletes do not increment this app's callback counter; actual Gboard Delete taps do. The no-Enter control produces exactly two callbacks. Fresh snapshots confirm these are app states, rather than stale runner observations. ADB provides an independent injected-key control; Speedrun itself was not rerun in this diagnosis.

React Native 0.83 documents Android `onKeyPress` as handling soft-keyboard input, excluding hardware-keyboard input. It also documents default autocapitalization as `sentences` and autocorrection as enabled. The fixture uses `onKeyPress` to count Backspaces and leaves those defaults enabled. TB injects ordinary `SOURCE_KEYBOARD` DOWN/UP events; it does not impersonate an input method. See the [versioned TextInput documentation](https://reactnative.dev/docs/0.83/textinput#onkeypress), [autocapitalization](https://reactnative.dev/docs/0.83/textinput#autocapitalize), and [autocorrection](https://reactnative.dev/docs/0.83/textinput#autocorrect).

**Inference:** Enter's capitalization/composition update likely accounts for the spurious callback. These probes did not instrument Gboard or React Native's InputConnection, so the exact internal method is not established. The documented soft-keyboard limitation and the separate delivery-path observations are established independently of that inference.

## Original proposed resolution (superseded by the follow-up)

Keep AND-005 open as an external fixture/input-method limitation. Do not add a duplicate delete, synthesize an app callback, change the asserted count, or globally toggle a user's keyboard settings to make this pass.

For an upstream fixture correction, separate two contracts:

1. Test generic `pressKey` through actual text and submission results, with `autoCapitalize="none"` and `autoCorrect={false}` for predictable text. Two deletes from `hello` must leave `hel`.
2. Test exact `onKeyPress` counts through an explicitly supported soft-input path, resetting the counter immediately before the measured deletions. Hardware key injection cannot promise this React Native callback.

A future explicit IME-backed input mode could cover the second contract, but it needs a separate API, keyboard-state restoration, Unicode and app compatibility validation. It should not silently replace normal Android key injection.

Raw evidence: `build/android-issue-fixes/005-inputs/probe-{agent,adb,soft,soft-no-enter}.{json,log}` and `probe.py`. Compact observations and SHA-256 hashes are retained in [the issue validation record](android-benchmark-issue-fixes-2026-11-06.json). The upstream checkout remains clean.
