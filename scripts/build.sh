#!/bin/sh
# Builds tb: the Android agent APK, the iOS agent bundle (macOS only), then the Go binary,
# which embeds both agents. Output: bin/tb
#
# Usage: scripts/build.sh [--skip-agents]
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

if [ "${1:-}" != "--skip-agents" ]; then
  echo "building the Android agent"
  (cd agents/android && ./gradlew -q assembleDebug)
  cp agents/android/app/build/outputs/apk/debug/app-debug.apk internal/driver/android/assets/tb-agent.apk

  if [ "$(uname)" = "Darwin" ] && command -v xcodebuild >/dev/null; then
    echo "building the iOS agent"
    agents/ios/build.sh
  else
    echo "skipping the iOS agent (needs macOS and Xcode)"
  fi
fi

echo "building tb"
go build -trimpath -ldflags "-s -w" -o bin/tb ./cmd/tb
echo "bin/tb ($(du -h bin/tb | cut -f1))"
