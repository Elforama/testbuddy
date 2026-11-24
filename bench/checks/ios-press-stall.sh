#!/bin/bash
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/ios-press-stall.sh <simulator UDID>}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failed=0
pass() { echo "ok    $*"; }
fail() { echo "FAIL  $*"; failed=1; }

for box in short long; do
  cat >"$work/$box.yaml" <<EOF
appId: dev.testbuddy.bench
---
- runFlow: {file: "$PWD/bench/flows/scenarios/steps/open.yaml", env: {SCENARIO: controls}}
- scrollUntilVisible: {element: {id: stall_${box}_status}, centerElement: true}
EOF
done
echo "- doubleTapOn: {id: stall_short_box}" >>"$work/short.yaml"
echo "- longPressOn: {id: stall_long_box}" >>"$work/long.yaml"

for box in short long; do
  what="double tap on the 150 ms box"
  [ "$box" = long ] && what="long press on the 2.2 s box"
  "$TB" test --keep-agents --device "$DEV" --debug-output "$work/$box" "$work/$box.yaml" >"$work/$box.log" 2>&1
  status=$?
  note=$(grep -rh "the app saw only [0-9]* ms of the [0-9]* ms press, after a [0-9]* ms stall in its down; a control that needs a longer press may have missed it" "$work/$box" 2>/dev/null | head -1)
  if [ "$status" -eq 0 ] && [ -n "$note" ]; then
    pass "$what passed, noting: ${note#*: }"
  else
    fail "$what: exit $status, note: '${note}'; $(tail -3 "$work/$box.log")"
  fi
done
exit $failed
