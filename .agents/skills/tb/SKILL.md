---
name: tb
description: Run, write, and debug TestBuddy YAML UI tests on Android, iOS simulators, and Chrome. Use when a task calls for inspecting or driving an app, reproducing a UI failure, or previewing, saving, and replaying an interactive session as a test.
---

# TestBuddy

Use the `tb` CLI from the shell. Run `tb help` and `tb replay --help` to confirm the
installed binary supports this workflow. In a TestBuddy checkout, use the built
`bin/tb` if it is newer than the copy on PATH. See `tb agent-guide` for a portable
command overview.

## CLI and YAML references

- [CLI reference](references/cli.md) covers commands, flags, JSON output, and diagnostics.
- [YAML flow format](references/flows.md) covers headers, selectors, commands, conditions,
  expressions, and workspace configuration. Read it before writing or changing a flow.
- [Multi-device flows](references/multi-device.md) covers roles, parallel commands, and signals.

## Run and diagnose a flow

1. Use `tb devices` to discover a running Android device or emulator, booted iOS
   simulator, or Chrome. Choose the intended device explicitly when several are available.
2. Use `tb check flows/example.yaml` after editing YAML. Run the flow with
   `tb test flows/example.yaml -d DEVICE --json`. Add `--headless` for Chrome.
3. On failure, read the first failing step and its debug artifacts before retrying.
   Compare `log.txt`, `screen.txt`, and the screenshot to distinguish a selector
   issue from an input or readiness issue.

## Explore and verify a recording

Open the target with `tb open -d DEVICE --name explorer --app APP_ID`. For web,
use `tb open -p web --headless --name explorer --app URL`. Start the recording with
a required `launchApp` step. Use a plain launch to preserve mobile app data.
Use `clearState: true` only when the requested test calls for clearing data on
the selected target.

Inspect with `tb look -d explorer --json` and run steps with `tb do -d explorer`.
Use refs from the latest listing when exploring. Prefer stable IDs and add
assertions that prove the action's outcome. Use `tb locate -d explorer --json`
with a selector to inspect live matches before tapping. TestBuddy waits on UI
events, so use its waits and assertions for expected UI changes.

```sh
tb save --preview flows/example.yaml -d explorer --json
tb save flows/example.yaml -d explorer
tb close -d explorer
tb replay flows/example.yaml -d DEVICE --json
```

Review `warnings` in the preview before saving. Preview leaves the filesystem
alone. Closing after saving releases the session's device lock. `tb close` waits for
a command still running on the device; Ctrl-C that command, or `tb daemon stop`, to
end a stuck one. Replay takes the
device serial, simulator UDID, or unique device name from `tb devices`. Use
`-d web --headless` for Chrome. Replay runs once with zero retries. Report the
device, starting state, and whether the replay passed.
