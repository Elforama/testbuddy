#!/bin/sh
# The Android e2e job's flows (.github/workflows/ci.yml), on the running emulator: the journey,
# the kill-app flow, the native scenarios, and the Android regressions. Each runs even
# when a previous flow failed. The regression report is separate from the native timing series.
set -u
adb install -r "$BENCH_APK" || exit 1
# The device log while the flows run (the agent, the bench app, the system), in the artifact.
mkdir -p build/e2e
adb logcat -c
adb logcat -v threadtime > build/e2e/android-logcat.txt 2>&1 &
logcat=$!
status=0
bin/tb test bench/flows/kill-app-android.yaml -p android --format junit --output build/e2e/android-kill-app.xml --debug-output build/e2e/android-kill-app || status=1
bin/tb test bench/flows/scenarios bench/flows/device/android/webview-dom.yaml bench/flows/device/navigation-deep-link.yaml \
    -p android --format junit --output build/e2e/android-scenarios.xml --debug-output build/e2e/android-scenarios || status=1
# Measure the full journey once the emulator and input method have handled the
# native scenarios. The journey clears the app state before signing in.
bin/tb test bench/flows/journey.yaml -p android --format junit --output build/e2e/android.xml --debug-output build/e2e/android || status=1
mkdir -p build/e2e/android-regressions
if adb install -r "$FLUTTER_BENCH_APK"; then
    bin/tb test bench/flutter/flows/regressions bench/flutter/flows/device/10-all-files-permission.yaml \
        bench/flows/device/android/webview-dom.yaml bench/flows/device/accessibility-values.yaml bench/flows/device/controls-gestures.yaml \
        -e BENCH=dev.testbuddy.bench.flutter -p android --retries 0 --format junit \
        --output build/e2e/android-regressions/report.xml --debug-output build/e2e/android-regressions/debug || status=1
else
    status=1
fi
kill $logcat 2>/dev/null
exit $status
