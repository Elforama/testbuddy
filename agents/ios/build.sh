#!/bin/sh
# Builds the iOS agent (the XCUITest runner and the TBProbe dylib) for the iOS Simulator and
# packs what `xcodebuild test-without-building` needs into internal/driver/ios/assets/
# tb-ios-agent.tar.gz, which the Go driver embeds (go:embed) and unpacks on first use:
#
#   tb-agent.xctestrun                                   the test run description
#   Release-iphonesimulator/TBAgentUITests-Runner.app    the runner (with the test bundle)
#   Release-iphonesimulator/libTBProbe.dylib             injected into apps tb launches
#
# Usage: agents/ios/build.sh [--fixture]   (--fixture also builds TBFixture.app, the
#        measurement app, into agents/ios/build; it is not packed)
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
build="$here/build"
out="$root/internal/driver/ios/assets/tb-ios-agent.tar.gz"

mkdir -p "$build"
log="$build/xcodebuild.log"
# The full log goes to a file (-quiet prints spurious "failed with exit code 0" lines).
run() {
  if ! "$@" >"$log" 2>&1; then
    grep -E "error:|BUILD FAILED" "$log" | head -40
    echo "xcodebuild failed; full log: $log" >&2
    exit 1
  fi
}

run xcodebuild build-for-testing \
  -project "$here/TBAgent.xcodeproj" -scheme TBAgentUITests \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath "$build"

if [ "${1:-}" = "--fixture" ]; then
  run xcodebuild build -project "$here/TBAgent.xcodeproj" -scheme TBFixture \
    -destination 'generic/platform=iOS Simulator' -derivedDataPath "$build"
  echo "fixture: $build/Build/Products/Release-iphonesimulator/TBFixture.app"
fi

products="$build/Build/Products"
xctestrun=$(ls -t "$products"/TBAgentUITests_*.xctestrun | head -n 1)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/Release-iphonesimulator"
cp "$xctestrun" "$stage/tb-agent.xctestrun"
cp -R "$products/Release-iphonesimulator/TBAgentUITests-Runner.app" "$stage/Release-iphonesimulator/"
cp "$products/Release-iphonesimulator/libTBProbe.dylib" "$stage/Release-iphonesimulator/"
# Debug symbols and module files are not needed to run.
find "$stage" \( -name '*.dSYM' -o -name '*.swiftmodule' \) -prune -exec rm -rf {} +

mkdir -p "$(dirname "$out")"
# No timestamps or owners in the archive: the same build packs to the same bytes, and the
# archive's hash is the build id the host checks a running agent against.
find "$stage" -exec touch -h -t 202601010000 {} +
(cd "$stage" && find . -type f -o -type l | LC_ALL=C sort | \
  tar --uid 0 --gid 0 --uname '' --gname '' --no-mac-metadata -cf - -T - | gzip -n -9 > "$out")
echo "packed $(du -h "$out" | cut -f1) into $out"
