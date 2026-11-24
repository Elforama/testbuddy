#!/bin/bash
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
