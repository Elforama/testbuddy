# TB failures on Speedrun Android benchmark — 2026-11-06

Analysis of the fresh comparison round: **16/21 passed, 5 failed**. Runner SHA-256: `f5ee8a9d3107a0c9cb8352ac705d15a786d5f4136cc209607616ba2720ba7f36`. Evidence is from saved logs, actual failure screenshots, benchmark app source, and runner source; no new device runs were used.

Four failures are runner recovery/readiness defects. The Backspace mismatch is shared with Speedrun and its underlying callback cause remains unresolved.

## Inputs and keys — inputs.yaml

**What happened:** The Backspaces: 2 assertion fails. Two Backspace commands change Hello to Hel, but the actual screen shows Backspaces: 1. The app increments this counter only in its onKeyPress Backspace callback.

**Why:** Confirmed mismatch between text edits and callback count. The precise keyboard/React Native event cause is not established by these logs. Speedrun fails the same assertion; its failure log reports Backspaces: 3, also with Hel. This is not evidence that TB failed to delete two characters, and the screenshot rules out a stale TB accessibility snapshot as the sole explanation.

**Confidence:** Unknown underlying callback cause; observed mismatch confirmed.

[Trace](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Typing_keys_erasing_copy_and_paste_random_text_a_draft_kept_across_restarts/log.txt:163) · [Actual failure screenshot](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Typing_keys_erasing_copy_and_paste_random_text_a_draft_kept_across_restarts/screenshot.png)

## Profile and PIN — profile.yaml

**What happened:** The last PIN-confirmation tap succeeds and the app returns to Profile, but TB then searches for the old key-4 and scrolls the Profile screen five times before failing. It never reaches the subsequent PIN set assertion and sign-out steps.

**Why:** atRest detects that the key disappeared, but its away-recovery return retains the last-seen node and old screen (find.go:334). An anonymous scroll container on Profile matches the old container key, so awayDir treats navigation as an offscreen target. tapMovedTarget only applies its later context-change exit when n is nil (exec.go:1040); the retained old node is non-nil and the screen is old too. The history trace notices context changes, but this motion-recovery path discards that newer observation. This explains why the earlier targeted test passed yet a later timing variant still failed.

**Confidence:** Confirmed false failure after successful navigation; runner recovery defect.

[Trace](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Edit_the_profile_change_settings_set_a_PIN_sign_out/log.txt:472) · [Actual failure screenshot](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Edit_the_profile_change_settings_set_a_PIN_sign_out/screenshot.png)

## Selectors / Show more — selectors.yaml

**What happened:** The first tap opens more-text and pushes more-toggle downward. TB explicitly replays the tap at its new location, toggling the content closed. The following more-text assertion fails.

**Why:** The app deliberately keeps the button ID, label and size unchanged while inserting content above it. tapMovedTarget sees no reported native click and a moved target, and incorrectly infers that the first tap missed. The replay is confirmed at 26.766s; the final screenshot shows the content closed.

**Confidence:** Confirmed runner double-action defect.

[Trace](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Elements_told_apart_by_place_index_relative_parent_child_and_by_state/log.txt:251) · [Actual failure screenshot](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Elements_told_apart_by_place_index_relative_parent_child_and_by_state/screenshot.png)

## Timing / layout-shift subscenario — timing.yaml

**What happened:** Confetti and the delayed-enable button pass. The shift scene fails with Taps: 4 instead of Taps: 3. The later busy-JavaScript subscenario is never reached.

**Why:** A timer inserts a banner one second after this scene opens. The second planned tap lands, then the banner moves the button from y=523–653 to y=922–1052. At 12.757s TB replays that second tap at the new position. Three requested taps plus this recovery replay produce four actual taps. The same no-native-click-plus-movement inference is unsafe here. This is distinct from the separate 12-scene animations.yaml flow, which passed.

**Confidence:** Confirmed runner double-action defect.

[Trace](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Confetti_a_button_that_turns_on_late_a_layout_shift_a_busy_JavaScript_thread/log.txt:140) · [Actual failure screenshot](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/Confetti_a_button_that_turns_on_late_a_layout_shift_a_busy_JavaScript_thread/screenshot.png)

## Closing screen / busy JavaScript — transitions.yaml

**What happened:** After Account Savings is chosen, the next tap targets transfer-amount but is received by the still-mounted closing account list. The final screen shows Credit card selected, an empty amount, and Send nothing to Credit card.

**Why:** The app exposes the underlying form to accessibility before the closing list stops accepting touches. It fades over 250 ms, blocks JavaScript for 800 ms, and disables form touch routing until list closure. TB treats the field as stable/available too early. Its held-overlay guard does not delay this tap, and later recovery does not establish field focus. The log explicitly says no field has focus, yet inputText 25 falls back and returns success with nothing changed. The exact early-return branch in awaitHeldCover is not recorded, so that narrower gate remains unconfirmed.

**Confidence:** Confirmed touch-readiness/focus-verification failure; precise overlay-guard branch not traced.

[Trace](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/A_JavaScript_stack_s_screen_closing_while_the_JavaScript_thread_is_busy/log.txt:58) · [Actual failure screenshot](/Users/jonathan/Documents/workspace/Test Buddy/build/android-runner-comparison-2026-11-06/speedrun-benchmark/tb/debug/2026-11-06_144602/A_JavaScript_stack_s_screen_closing_while_the_JavaScript_thread_is_busy/screenshot.png)

## Relevant runner code

- [Motion stabilization and old-screen return](/Users/jonathan/Documents/workspace/Test Buddy/internal/engine/find.go:315)
- [Anonymous scroll-container matching](/Users/jonathan/Documents/workspace/Test Buddy/internal/engine/find.go:400)
- [Post-tap recovery and replay](/Users/jonathan/Documents/workspace/Test Buddy/internal/engine/exec.go:992)
- [Context guard requiring a missing node](/Users/jonathan/Documents/workspace/Test Buddy/internal/engine/exec.go:1040)
- [Closing-overlay guard](/Users/jonathan/Documents/workspace/Test Buddy/internal/engine/cover.go:517)
- [Typing fallback when focus verification is unavailable](/Users/jonathan/Documents/workspace/Test Buddy/internal/engine/exec.go:547)
