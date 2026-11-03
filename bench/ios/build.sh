#!/bin/sh
# Builds Buddy Bench (bench/SPEC.md) for the iOS Simulator into bench/ios/build and prints the
# path of the .app on the last line. Install it on a booted simulator with:
#
#   xcrun simctl install <udid> "$(bench/ios/build.sh | tail -n 1)"
set -eu
here=$(cd "$(dirname "$0")" && pwd)
build="$here/build"

mkdir -p "$build"
log="$build/xcodebuild.log"
# The full log goes to a file; on failure print the errors.
if ! xcodebuild build \
  -project "$here/BuddyBench.xcodeproj" -scheme BuddyBench -configuration Release \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath "$build" >"$log" 2>&1; then
  grep -E "error:|BUILD FAILED" "$log" | head -40 >&2
  echo "xcodebuild failed; full log: $log" >&2
  exit 1
fi
echo "$build/Build/Products/Release-iphonesimulator/BuddyBench.app"
