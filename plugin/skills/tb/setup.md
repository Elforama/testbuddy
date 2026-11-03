# Setting up tb

## The binary

The plugin ships `bin/tb`: one file, macOS on Apple silicon, with the Android agent and the
iOS agent built in. While the plugin is enabled it is on the Bash tool's PATH.

```bash
which -a tb        # the plugin's copy should be listed; an earlier entry wins
tb help
```

- **Another `tb` comes first on PATH** (some other tool is called `tb`): call the plugin's
  copy by its full path, given in SKILL.md, or ask the user to rename or remove the other one.
- **Not macOS on Apple silicon**: the bundled binary will not run. Build from the Test Buddy
  source (Go; `scripts/build.sh --skip-agents` uses the agents already in the source tree) and
  put `bin/tb` on PATH. iOS needs macOS and Xcode in any case.
- `"tb" cannot be opened` / killed at launch on macOS: the file lost its executable bit or
  got quarantined by a download. `chmod +x` it, or `xattr -d com.apple.quarantine <path>`
  (ask the user first; it changes their system).

## Devices

| Platform | Needs | Check |
|---|---|---|
| Android | Android SDK platform-tools (`adb`) on PATH; a running emulator or a USB phone with debugging on | `adb devices` shows it as `device` |
| iOS | Xcode with iOS simulators; a **booted** simulator | `xcrun simctl list devices booted` |
| Web | Google Chrome (or Chromium: `TB_CHROME=/path/to/chrome`) | `tb devices` lists `chrome` |

`tb devices` lists everything it can drive. Each flow goes to a device that has its app
installed (`appId`); a `url:` flow goes to Chrome.

- Starting an emulator or booting a simulator is the user's call when none is running; ask
  before booting one (`emulator -avd <name>`, `xcrun simctl boot <udid>`).
- `tb` does not install the app under test; `installApp`-style setup belongs in the user's
  build scripts (e.g. `./gradlew installDebug`, `xcrun simctl install booted App.app`).

## What tb puts on devices

- **Android**: an agent APK pushed to `/data/local/tmp`, started as the shell user. While it
  runs, an accessibility service from that APK is enabled (Jetpack Compose only reports UI
  changes when one is); it is turned off when the agent exits. `tb test` also sets the three
  animation scales to 0 for the run and restores them.
- **iOS**: an XCUITest runner installed into the simulator on first use (~20 s), reused
  afterwards. Apps that `launchApp` starts get a small probe library injected (it reports
  layer changes) and run with UIKit animations 10x faster (`TB_IOS_FAST_ANIMATIONS=0` turns
  that off).
- **Web**: Chrome starts with a temporary profile (password manager and translate prompts
  off) and is closed at the end.

## State and background process

- `~/.testbuddy` holds state (override with `TB_HOME`).
- `tb open/look/do/wait/watch/shot/save` talk to a background daemon that keeps devices
  connected between calls (so each call takes milliseconds). `tb daemon stop` stops it; a new
  `tb` build replaces an old daemon by itself.
- `tb test` runs in-process and does not need the daemon.
