#!/bin/bash
# Checks that a healthy CI-mode run never gets the stall allowance (issue #243): in CI mode a
# find, scroll or assert wait whose time a simulator stall of 10 s or more used up is given the
# stalled time back, once, and its trace says "giving the step ... more, once (CI mode)". On a
# simulator that answers as usual no step may get it, or the allowance would hide slowness that
# is not a stall. Runs the scenario flows with TB_CI=1 and looks for that line in their traces.
#
#   bench/checks/ios-no-stall-grace.sh <simulator UDID> [flow ...]   (TB=path/to/tb, default bin/tb)
#
# Flows default to bench/flows/scenarios/*.yaml. Needs Buddy Bench (bench/ios/build.sh) installed.
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/ios-no-stall-grace.sh <simulator UDID> [flow ...]}
shift
flows=("$@")
[ ${#flows[@]} -eq 0 ] && flows=(bench/flows/scenarios/*.yaml)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

TB_CI=1 "$TB" test --keep-agents --device "$DEV" --debug-output "$work/debug" "${flows[@]}" >"$work/run.log" 2>&1
status=$?
graced=$(grep -rl 'more, once (CI mode)' "$work/debug" 2>/dev/null)
traces=$(find "$work/debug" -name log.txt 2>/dev/null | wc -l | tr -d ' ')
if [ "$traces" -eq 0 ]; then
  echo "FAIL  no traces written: $(tail -3 "$work/run.log")"
  exit 1
fi
if [ -n "$graced" ]; then
  echo "FAIL  a step was given more time for a stall on a healthy simulator:"
  grep -rh 'more, once (CI mode)' "$work/debug"
  exit 1
fi
echo "ok    no step of $traces flows was given more time for a stall (the run exited $status)"
exit 0
