#!/bin/sh
set -u
install_apk() {
    out=$(adb install -r "$1" 2>&1)
    rc=$?
    echo "$out"
    [ $rc -eq 0 ] && return 0
    case "$out" in
    *INSTALL_FAILED_UPDATE_INCOMPATIBLE*)
        echo "$2 on the emulator is signed with another key: uninstalling it and installing again"
        adb uninstall "$2"
        adb install "$1"
        ;;
    *) return 1 ;;
    esac
}
install_apk "$BENCH_APK" dev.testbuddy.bench || exit 1
mkdir -p build/e2e
adb logcat -c
adb logcat -v threadtime > build/e2e/android-logcat.txt 2>&1 &
logcat=$!
status=0
bin/tb test bench/flows/kill-app-android.yaml -p android --format junit --output build/e2e/android-kill-app.xml --debug-output build/e2e/android-kill-app || status=1
bin/tb test bench/flows/scenarios bench/flows/scripting.yaml bench/flows/device/android/webview-dom.yaml bench/flows/device/android/compose-multiline.yaml bench/flows/device/android/compose-interop.yaml bench/flows/device/android/kept-stack.yaml bench/flows/device/android/restored-feed.yaml \
    bench/flows/device/android/keyboard-scroll-clear.yaml bench/flows/device/android/keyboard-shelf.yaml bench/flows/device/android/launch-clean-task.yaml \
    bench/flows/device/navigation-deep-link.yaml -p android --format junit --output build/e2e/android-scenarios.xml --debug-output build/e2e/android-scenarios || status=1
bin/tb test bench/flows/journey.yaml -p android --format junit --output build/e2e/android.xml --debug-output build/e2e/android || status=1
if [ -n "${ANNOTATE_DIR:-}" ]; then
    bin/tb test bench/flows/journey.yaml -p android --record --annotate --debug-output "$ANNOTATE_DIR" ||
        echo "::warning title=Annotated recording failed::the --annotate journey failed on Android ($ANNOTATE_DIR in the android-e2e artifact)"
fi
mkdir -p build/e2e/android-regressions
if install_apk "$FLUTTER_BENCH_APK" dev.testbuddy.bench.flutter; then
    bin/tb test bench/flutter/flows/regressions bench/flutter/flows/device/10-all-files-permission.yaml \
        bench/flows/device/android/webview-dom.yaml bench/flows/device/accessibility-values.yaml bench/flows/device/controls-gestures.yaml \
        -e BENCH=dev.testbuddy.bench.flutter -p android --retries 0 --format junit \
        --output build/e2e/android-regressions/report.xml --debug-output build/e2e/android-regressions/debug || status=1
else
    status=1
fi
kill $logcat 2>/dev/null
exit $status
