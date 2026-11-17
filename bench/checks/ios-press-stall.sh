#!/bin/bash
# Checks that tb says when a probed iOS app saw a press cut short by a stall of its own main
# thread as the touch came down (issue #337). On CI a double tap's first press reached the app
# while it was stalled: delivering the down took 124 ms, the lift came 5 ms later, and a 75 ms
# press handler saw a 5 ms press and missed it. A flow cannot see this (the step passes or an
# assertion fails later), so this check runs two flows on Controls' stalling press boxes, whose
# touch-down blocks the bench app's main thread on purpose (ControlsScenario's StallingPress):
#   doubleTapOn on the 150 ms box: the step passes, and its trace notes the press cut short;
#   longPressOn on the 2.2 s box (a 3 s press, of which the app sees under half): the step
#   fails, saying "the press was cut short".
#
#   bench/checks/ios-press-stall.sh <simulator UDID>      (TB=path/to/tb, default bin/tb)
#
# Needs Buddy Bench (bench/ios/build.sh) installed, and the probe (TB_IOS_PROBE unset).
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

"$TB" test --keep-agents --device "$DEV" --debug-output "$work/short" "$work/short.yaml" >"$work/short.log" 2>&1
status=$?
note=$(grep -rh "the app saw only .* stall in its down: a control that acts on how long it is pressed" "$work/short" 2>/dev/null | head -1)
if [ "$status" -eq 0 ] && [ -n "$note" ]; then
  pass "double tap on the 150 ms box passed, noting: ${note#*: }"
else
  fail "double tap on the 150 ms box: exit $status, note: '${note}'; $(tail -3 "$work/short.log")"
fi

"$TB" test --keep-agents --device "$DEV" --debug-output "$work/long" "$work/long.yaml" >"$work/long.log" 2>&1
status=$?
why=$(grep -h "the press was cut short: the app saw" "$work/long.log" | head -1)
if [ "$status" -ne 0 ] && [ -n "$why" ]; then
  pass "long press on the 2.2 s box failed: ${why#*: }"
else
  fail "long press on the 2.2 s box: exit $status, no 'cut short' failure; $(tail -3 "$work/long.log")"
fi
exit $failed
