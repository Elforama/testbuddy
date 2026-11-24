#!/bin/bash
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/look-values.sh <simulator UDID>}
failed=0
pass() { echo "ok    $*"; }
fail() { echo "FAIL  $*"; failed=1; }
finish() { "$TB" close -d "$DEV" >/dev/null 2>&1; exit $failed; }

"$TB" open -d "$DEV" --app dev.testbuddy.bench >/dev/null || { fail "tb open"; finish; }
"$TB" do -d "$DEV" \
  'launchApp: {clearState: true}' \
  'tapOn: {id: scenario_a11y_good}' \
  'scrollUntilVisible: {element: {id: good_push}, centerElement: true}' >/dev/null ||
  { fail "opening the Good accessibility scenario"; finish; }

look=$("$TB" look -d "$DEV")
for want in \
  'Button "Decrease quantity" #good_qty_minus value="1"' \
  'Button "Increase quantity" #good_qty_plus value="1"' \
  'Switch "Push notifications" #good_push unchecked'; do
  if grep -qF "$want" <<<"$look"; then
    pass "tb look: $want"
  else
    id=$(grep -o '#[a-z_]*' <<<"$want")
    fail "tb look: want $want, got: $(grep -F "$id" <<<"$look" | sed 's/^ *//')"
  fi
done

finish
