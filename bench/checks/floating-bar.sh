#!/bin/bash
# Checks that a tap on a floating bar does not wait for a spinner in the layer drawn under it
# (issue #11): the Overlays scenario's "Floating bar" (bench/SCENARIOS.md). The flow passes
# either way, since tb taps anyway once its wait runs out, so this reads the flow's log.txt.
#
#   bench/checks/floating-bar.sh <device serial>      (TB=path/to/tb, default bin/tb)
#
# Needs the bench app on the device (bench/README.md).
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/floating-bar.sh <device serial>}
failed=0
pass() { echo "ok    $*"; }
fail() { echo "FAIL  $*"; failed=1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cat >"$tmp/floating-bar.yaml" <<EOF
appId: dev.testbuddy.bench
---
- runFlow: {file: $PWD/bench/flows/scenarios/steps/open.yaml, env: {SCENARIO: overlays}}
- tapOn: {id: show_floating_bar}
- assertVisible: {id: floating_layer_spinner}
- tapOn: {id: floating_bar_action}
- assertVisible: {id: overlay_result, text: "Bar action"}
EOF

if "$TB" test --device "$DEV" --debug-output "$tmp/debug" --flatten-debug-output "$tmp/floating-bar.yaml" >"$tmp/out.txt" 2>&1; then
  pass "the flow passes"
else
  fail "the flow: $(tail -5 "$tmp/out.txt")"
fi

log=$(find "$tmp/debug" -name log.txt | head -1)
step=$(awk '/▶ Tap on id: floating_bar_action/,/✓ Tap on id: floating_bar_action/' "$log")
if grep -q "under a loading overlay" <<<"$step"; then
  fail "the bar tap waited for the layer under it: $(grep 'under a loading overlay' <<<"$step" | sed 's/^ *//')"
else
  pass "the bar tap does not wait for the layer under it"
fi
ms=$(grep -o '✓ Tap on id: floating_bar_action *[0-9]* ms' <<<"$step" | grep -o '[0-9]* ms$' | grep -o '[0-9]*')
if [ -n "$ms" ] && [ "$ms" -lt 2000 ]; then
  pass "the bar tap took ${ms} ms"
else
  fail "the bar tap took ${ms:-?} ms (want < 2000)"
fi

exit $failed
