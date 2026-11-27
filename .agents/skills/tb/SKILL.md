---
name: tb
description: Run, write, and debug TestBuddy YAML UI tests on Android, iOS simulators, and Chrome. Use when a task calls for inspecting or driving an app, reproducing a UI failure, or previewing, saving, and replaying an interactive session as a test.
---

# TestBuddy

Use the `tb` CLI from the shell. Run `tb help` and `tb replay --help` to confirm the
installed binary supports this workflow. In a TestBuddy checkout, use the built
`bin/tb` if it is newer than the copy on PATH. See `tb agent-guide` for a portable
command overview.

## References

- [CLI reference](references/cli.md): commands, flags, JSON output, how to read `tb look`.
- [YAML flow format](references/flows.md): headers, selectors, commands, conditions,
  expressions, workspaces. Read it before writing or changing a flow.
- [Gotchas](references/gotchas.md): waits, taps, covers, typing, scrolling, per platform.
- [Debugging](references/debugging.md): the debug folder, reading `log.txt`, common errors.
- [Multi-device flows](references/multi-device.md): roles, parallel commands, signals.

## First steps

Use `tb devices` to discover a running Android device or emulator, booted iOS simulator,
or Chrome, and which are free or in use (by whom). Choose the intended device explicitly when
several are available.

## Write a good flow

<!-- shared: write-a-good-flow (keep identical in both SKILL.md copies) -->
- **Assert each action's outcome**, scoped by id when you can:
  `assertVisible: {id: status, text: "Saved"}`. An assertion is the cheapest wait. It is safe
  right after a tap (the tap waited for the UI to start changing); assert something only the
  new state shows, not a title the old screen has too.
- **Before acting on a new screen, assert something on it.** A tap that opens a screen
  returns once the change starts; on a slow slide-in, an assertion on the new screen keeps
  the next tap from racing it.
- **Waits, in this order:**
  1. a plain lookup (`tapOn`, `assertVisible`, ...) already waits up to 17 s on device
     events (7 s when `optional`); slow content in the UI needs nothing more;
  2. `extendedWaitUntil: {visible: "Report ready", timeout: 60000}` (or `notVisible`) only
     when the end state can take longer than 17 s (a backend job, an email);
  3. `waitForAnimationToEnd: {timeout: 5000}` only for a long one-shot animation you must
     wait out (default 15 s; it never fails, and ignores small endless loops after 3 s);
  4. never a sleep, a `retry` or a `waitForAnimationToEnd` "just in case".
- **Selectors:** `id` > `id` + `text` > exact text > relative (`below`, `childOf`) > `index`
  (`-1` is the last). Text matches the label exactly as written, or as a regular expression
  over the whole label. A pattern with `.*` for a dynamic part must escape the rest's
  `? ( ) [ ] + * $ . |`: `'Total: \$.*'`, `"Order #.* placed"`. `rightOf`/`below` match
  anything further right/lower, in any row. Then `tb check --lint`.
- **Speed:** clear state only in flows that need a fresh start; keep every flow runnable on
  its own. An optional tap that is usually absent gets `timeout: 1000` (tb-only; omit it in
  flows Maestro must also run). No scrolling to what is already on screen; no `hideKeyboard`
  before a tap (a tap whose target is not found with the keyboard up hides it and looks
  again). `pressKey: Enter` moves along a Next chain of fields.
- **Platforms:** `appId: ${APP_ID || 'com.example.app'}`; platform-only steps go in
  `runFlow: {when: {platform: iOS}, commands: [...]}`. iOS has no back key: `tapOn: {id: BackButton}`.
<!-- /shared -->
- `tb test` turns Android animations off; a `tb do` session leaves them on.
- In a TestBuddy checkout, `bench/flows/scenarios` holds working flows to model on.

## Run and diagnose a flow

1. `tb check --lint flows/example.yaml` after editing YAML. Run the flow with
   `tb test flows/example.yaml -d DEVICE --json`. Add `--headless` for Chrome.
2. On failure, read the first failing step and its debug artifacts (`debugDir`) before
   retrying. `log.txt` says what tb did at each step; map its lines to fixes with
   [debugging.md](references/debugging.md). Fix the cause, never with sleeps or retries.
3. Flows that change real accounts (sign-in, sharing, deleting) act on real backends:
   confirm with the user before running them on signed-in devices.

## Record a flow

```sh
tb open -d DEVICE --name explorer --app APP_ID
tb do -d explorer 'launchApp'
tb look -d explorer --json
tb do -d explorer 'tapOn: @5' 'assertVisible: {id: status, text: "Saved"}'
tb locate -d explorer --json '{text: "Edit"}'
tb save --preview flows/example.yaml -d explorer --json
tb save flows/example.yaml -d explorer
tb close -d explorer
tb replay flows/example.yaml -d DEVICE --json
```

1. Open the target (web: `tb open -p web --headless --name explorer --app URL`), then start
   with a plain `launchApp`, which keeps app data; add `clearState: true` only when the test
   calls for clearing data on the selected target.
2. Look, then `tb do` each action with the assertion that proves it. Refs come from the
   latest listing and are saved as stable selectors. `tb locate` shows every match of a
   selector and which one tb picks. Type a password or code with
   `tb do --secret PW 'inputText: ${PW}'` (PW from the environment): tb shows `•••` for it
   and saves `${PW}`, to replay with `-e PW=...`.
3. Preview, fix its `warnings`, then save. Preview leaves the filesystem alone.
4. Close the session (it releases the device lock; `tb close` waits for a running command:
   Ctrl-C it), then replay once on the serial, UDID or unique name from `tb devices`
   (`-d web --headless` for Chrome). Replay runs once with zero retries. Report the device,
   starting state, and whether the replay passed.

For a short flow, writing the YAML by hand after exploring with `tb do` is just as good:
then `tb check --lint` and `tb test`.
