#!/bin/sh
# Builds the Buddy Bench debug APK and prints its path.
# Usage: bench/android/build.sh [extra gradle args, e.g. --offline]
set -e
cd "$(dirname "$0")"
if [ ! -f local.properties ]; then
    sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
    echo "sdk.dir=$sdk" > local.properties
fi
./gradlew --quiet assembleDebug "$@"
echo "$(pwd)/app/build/outputs/apk/debug/app-debug.apk"
