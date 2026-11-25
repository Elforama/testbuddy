#!/bin/bash
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/relabel-tap.sh <device> <app id or URL>}
APP=${2:?usage: bench/checks/relabel-tap.sh <device> <app id or URL>}
failed=0
pass() { echo "ok    $*"; }
fail() { echo "FAIL  $*"; failed=1; }
finish() { "$TB" close -d "$DEV" >/dev/null 2>&1; exit $failed; }
open=(open -d "$DEV" --app "$APP")
[ "$DEV" = web ] && open+=(--headless)

"$TB" "${open[@]}" >/dev/null || { fail "tb open"; finish; }
"$TB" do -d "$DEV" \
  'launchApp: {clearState: true}' \
  'scrollUntilVisible: {element: {id: scenario_slow}}' \
  'tapOn: {id: scenario_slow}' \
  'scrollUntilVisible: {element: {id: relabel_open}}' \
  'tapOn: {id: relabel_open}' >/dev/null || { fail "opening the Relabel screen"; finish; }

look=$("$TB" look -d "$DEV")
if grep -q '"Search" #relabel_search' <<<"$look"; then
  pass "tb look shows \"Search\" before the label changes"
else
  fail "tb look: want \"Search\" #relabel_search, got: $(grep relabel_search <<<"$look")"
  finish
fi

sleep 6
start=$(date +%s)
out=$("$TB" do -d "$DEV" 'tapOn: "Search"' 2>&1)
code=$?
took=$(($(date +%s) - start))
if [ "$code" -ne 0 ] && grep -q '"Search" (in your last look) is gone; at its place now: .*Search for' <<<"$out" && [ "$took" -le 8 ]; then
  pass "tapOn \"Search\" after the label changed: failed in ${took}s, naming the new label"
else
  fail "tapOn \"Search\" after the label changed: exit $code in ${took}s (want a failure within 8 s naming the new label):"
  sed 's/^/      /' <<<"$out"
fi

if "$TB" do -d "$DEV" 'tapOn: {id: relabel_search}' 'assertVisible: "Search taps: 1"' >/dev/null; then
  pass "the relabelled button is still tapped by its id"
else
  fail "tapOn {id: relabel_search} did not count a tap"
fi

finish
