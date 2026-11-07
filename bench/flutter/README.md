# Buddy Bench for Flutter

This edition uses a shared Dart UI on Android and iOS. TestBuddy drives its
platform accessibility tree. Flutter opens the original native mini-screens for
the toolkit scenario and uses platform channels to request OS permissions.

See the [Flutter adapter](../../adapters/flutter/README.md) for runtime detection,
callbacks and native-screen routing.

The app ID is `dev.testbuddy.bench.flutter`. The Flutter deep link is
`benchflutter://level/3`, so it can be installed alongside the native edition.

With Flutter 3.41.7, the Android SDK, Xcode, and a built `bin/tb`, build from this directory:

```sh
flutter pub get
flutter test
flutter build apk --debug
flutter build ios --debug --simulator
adb -s "$ANDROID_SERIAL" install -r build/app/outputs/flutter-apk/app-debug.apk
xcrun simctl install "$IOS_UDID" build/ios/iphonesimulator/Runner.app
```

From the repository root, start the two commands on their respective devices.
Each creates a new results directory and runs five consecutive rounds with zero
retries. Android also runs the process-death restoration flow.

```sh
python3 bench/flutter/run.py android "$ANDROID_SERIAL" build/flutter-android
python3 bench/flutter/run.py ios "$IOS_UDID" build/flutter-ios
```

Each directory contains a build and runner fingerprint, JUnit results, TestBuddy
logs, and a JSON summary. Timings include work on the simulator or emulator and
depend on the host load.
