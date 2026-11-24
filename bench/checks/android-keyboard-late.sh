#!/bin/bash
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
  sleep 3
  shown=$(adb -s "$DEV" shell dumpsys input_method | grep -o -m1 "mInputShown=[a-z]*")
  case "$shown" in
    mInputShown=false) echo "ok    round $i: the keyboard stayed hidden" ;;
    mInputShown=true) echo "FAIL  round $i: the keyboard is up 3 s after hideKeyboard"; failed=1 ;;
    *) echo "FAIL  round $i: dumpsys input_method does not say whether the keyboard is up"; failed=1 ;;
  esac
done
exit $failed
