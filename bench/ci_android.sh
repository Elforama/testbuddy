#!/bin/sh
# The Android e2e job's flows (.github/workflows/ci.yml), on the running emulator: the journey,
# the kill-app flow, then every scenario flow. Each runs even when one before it failed, so
# one run reports, and times, every flow; the exit status says whether any failed.
set -u
adb install -r "$BENCH_APK" || exit 1
status=0
bin/tb test bench/flows/journey.yaml -p android --format junit --output build/e2e/android.xml --debug-output build/e2e/android || status=1
bin/tb test bench/flows/kill-app-android.yaml -p android --format junit --output build/e2e/android-kill-app.xml --debug-output build/e2e/android-kill-app || status=1
bin/tb test bench/flows/scenarios -p android --format junit --output build/e2e/android-scenarios.xml --debug-output build/e2e/android-scenarios || status=1
exit $status
