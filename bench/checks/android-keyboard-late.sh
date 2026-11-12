#!/bin/bash
# Checks that hideKeyboard closes an Android keyboard still on its way up (issue #175): it
# opens the Keyboard scenario's autofocus screen, whose field asks for the keyboard as the
# screen opens, and hides the keyboard at once. On a slow emulator the keyboard shows after
# the tap returned; hideKeyboard must close it once it shows, not pass and let it stay up. A
# flow cannot see whether the keyboard is up 3 s later, so this asks the device.
#
#   bench/checks/android-keyboard-late.sh <serial> [rounds]   (TB=path/to/tb, default bin/tb)
#
# The race shows on a loaded device (CI's x86 emulator; locally, busy loops in adb shell).
# Needs the bench app on the device (bench/README.md).
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/android-keyboard-late.sh <serial> [rounds]}
ROUNDS=${2:-5}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
open="$PWD/bench/flows/scenarios/steps/open.yaml"
cat >"$tmp/setup.yaml" <<EOF
appId: dev.testbuddy.bench
---
- runFlow: {file: "$open", env: {SCENARIO: keyboard}}
- scrollUntilVisible: {element: {id: kb_autofocus_open}}
EOF
cat >"$tmp/hide.yaml" <<EOF
appId: dev.testbuddy.bench
---
- tapOn: {id: kb_autofocus_open}
- hideKeyboard
EOF
failed=0
for i in $(seq 1 "$ROUNDS"); do
  "$TB" test "$tmp/setup.yaml" -d "$DEV" >/dev/null 2>&1 || { echo "FAIL  round $i: opening the Keyboard scenario"; failed=1; continue; }
  "$TB" test "$tmp/hide.yaml" -d "$DEV" >/dev/null 2>&1 || { echo "FAIL  round $i: hideKeyboard failed"; failed=1; continue; }
  sleep 3 # the keyboard of the bug shows up to 2 s after hideKeyboard returned
  if adb -s "$DEV" shell dumpsys input_method | grep -q "mInputShown=true"; then
    echo "FAIL  round $i: the keyboard is up 3 s after hideKeyboard"
    failed=1
  else
    echo "ok    round $i: the keyboard stayed hidden"
  fi
done
exit $failed
