# Benchmark flow portability tasks

Each task covers a platform branch found in the benchmark audit. Shared UI should use
shared steps. A platform condition is appropriate when the product or system presents
a different experience that needs different actions or assertions.

| Flow | Task | Disposition |
| --- | --- | --- |
| Controls | Use one keyboard dismissal before asserting the greeting. | Shared step. |
| Controls | Run the reaction and numeric placeholder checks on every fixture that has those controls. | Standalone flow for native iOS and both Flutter builds. The Android agent now holds each contact in a double tap long enough for the shared reaction control. |
| Controls | Review the fruit dropdown. | Keep the branch. The mobile popup exposes a tappable option, while Chrome presents a native select popup outside the page. |
| Good accessibility | Exercise the value-bearing stepper and switch on every fixture that includes them. | Standalone flow for native iOS and both Flutter builds. One label pattern accepts the value prefix Flutter supplies through Android accessibility. |
| Lists | Exercise pull-to-refresh on Chrome as well as mobile. | Shared swipe and assertion. |
| Native toolkits | Share the WebView interaction while keeping coverage of each toolkit. | Shared WebView steps. Keep the branches for the different Android, iOS, and web toolkit screens. |
| Navigation | Use the same deep-link assertions for all targets. | Standalone flow with the target URL supplied as configuration. Keep Android system Back coverage because that interaction has no equivalent system control on iOS or web. |
| Overlays | Dismiss the tooltip with the same outside tap everywhere. | Shared tap and assertions. |
| Permissions | Check the same permission results after refreshing on both mobile targets. | Shared mobile assertions. Keep the different system prompt sequences and the browser path, which exercises the in-app rationale. |
| Embedded browser | Remove the wrapper around the iOS system browser check. | Standalone flow selected by the iOS suite. |
| WebView DOM | Remove the wrapper around the Android DevTools hierarchy check. | Standalone flow selected by the Android suite. |
| All-files permission | Remove the wrapper around the Android storage permission check. | Standalone flow selected by the Android Flutter suite. |
| Browser handoff | Supply the reachable server address with the test invocation. | Shared flow with `BENCH_BROWSER_URL` as configuration. |
| Flutter editor setup | Use the same lifecycle command before reopening the editor. | Shared `killApp` step. The drivers handle the lifecycle differences. |
| Flow features example | Remove the unreachable platform branch from the Android Settings example. | Removed. Multi-device examples still use platform declarations to select devices. |

- [ ] Preserve exact label matching for Android Flutter controls that combine a value
  and label in their accessibility description. Identify the components from framework
  metadata before treating part of a description as a selector match.
