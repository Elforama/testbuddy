# Android input fixes — validation

**Both failed input cases are fixed on the tested Android API 36 emulator.** The unchanged full inputs flow passed **20 consecutive times**, with zero flow retries. The complete original upstream suite then passed **21/21**.

Tested source commit: `bdae2d4c8e28b6b60add7986e0a5022cf52ba360`. Agent: 0.1.29. Runner SHA-256: `c356b97d19beaadbdc4dceb1f31409a15f33873c8044ef949d5a7203f13b3959`. Device: emulator-5554. All upstream flow/support hashes and the selected keyboard remained unchanged.

| Check | Result |
| --- | --- |
| AND-006 original submission prefix | 20/20 before adding Backspace support |
| Idle socket start/stop/rebind regression | Old build fails cycle 3; corrected shutdown passes 20/20 |
| Complete original inputs flow, both fixes | 20/20, zero flow retries |
| Native keyboard and animation/history flows | 2/2 |
| Flutter keyboard flow | 1/1 |
| First full suite, configuration-interrupted | 20/21; preserved |
| Clean repeat of original upstream Android suite | 21/21 |
| Android agent unit checks | 111 passed |
| Full Go suite; engine/Android race checks | Passed |
| Android lint and APK build | Passed |

## Changes

- **AND-006:** API 30+ uses an advertised IME submit action on a focused single-line field. Multiline Enter, long presses and unavailable actions retain key injection. A handled action is never followed by another Enter. See [Android's API contract](https://developer.android.com/reference/android/view/accessibility/AccessibilityNodeInfo.AccessibilityAction#ACTION_IME_ENTER).
- **AND-005:** API 33+ Backspace sends a real soft DEL DOWN/UP through the focused editor's [InputConnection](https://developer.android.com/reference/android/accessibilityservice/InputMethod.AccessibilityInputConnection), preserving native key handlers and framework callbacks. The helper checks node/session identity and wakes readiness from callbacks. Older APIs, disabled services and explicitly unavailable editors keep the existing key route. Rejected or uncertain delivery fails without another deletion.
- The host reaches the helper through an ADB forward because direct shell-to-app sockets are blocked. Helper package updates recover a signing mismatch only for TestBuddy's own helper, and report other installer failures.
- Rapid restarts initially exposed a Unix listener leak: blocked accept retained the address after close. Shutdown now wakes accept and waits for worker termination. The old failed batch and crash log are preserved.

## Coverage and timing

Complete inputs median: 40.341s; range 32.549–181.575s. The high outlier coincided with host contention; this is correctness validation, not a paired speed measurement. The first full round was 20/21 after Android recreated the activity during an unrelated instrumentation run; the identical binary passed 21/21 on the clean repeat. Both attempts remain in JSON.

- Device execution validated on Android API 36; older API guards/fallbacks are covered by unit checks and Android lint, not an old-device matrix.
- Inputs runs 6–8 encountered host contention while a separate Android Studio Gradle 9.5.1 build used several cores and host swap exceeded 6 GB. The task's idle Gradle 9.8.2 daemon was stopped; both emulators and the other build were preserved. These durations are not a controlled speed comparison.
- The original failed prototype and endpoint-rebind stress attempt remain in this record; final results refer to the corrected pinned build only.

Input readiness uses callbacks; socket teardown waits for worker completion. No blanket sleeps, whole-screen polling, or selected-keyboard replacement were added by these fixes.

Raw corrected validation: `build/android-input-fixes/validation-02`. [JSON evidence](android-input-issue-fixes-2026-11-06.json) retains source hashes, commands, every outcome and the earlier failed attempt; [case CSV](android-input-fix-validation-2026-11-06.csv) contains all 65 post-fix case executions, including the interrupted round. [Issue catalog](../../docs/android-benchmark-issues.md). Both emulators remain running; emulator-5556 was left untouched.
