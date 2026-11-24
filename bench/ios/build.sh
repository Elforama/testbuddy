#!/bin/sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
build="$here/build"

mkdir -p "$build"
log="$build/xcodebuild.log"
if ! xcodebuild build \
  -project "$here/BuddyBench.xcodeproj" -scheme BuddyBench -configuration Release \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath "$build" >"$log" 2>&1; then
  grep -E "error:|BUILD FAILED" "$log" | head -40 >&2
  echo "xcodebuild failed; full log: $log" >&2
  exit 1
fi
echo "$build/Build/Products/Release-iphonesimulator/BuddyBench.app"
