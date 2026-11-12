#!/bin/bash
# Checks that tb's iOS agent answers a screen read before tb gives up on it while the app does
# not answer accessibility (issue #130), instead of tb timing out blind ("agent did not answer
# "tree" within 30s"). The app is stopped (SIGSTOP) for 40 s, which, like an app whose main
# thread is busy, answers no accessibility request; a flow reads the screen meanwhile.
#
#   bench/checks/ios-busy-app.sh <simulator UDID>      (TB=path/to/tb, default bin/tb)
#
# Needs Buddy Bench (bench/ios/build.sh) installed. The app is always resumed. Two runs:
#   TB_CI=0: the step fails at 30 s with the agent's error, which says what it waited for;
#   TB_CI=1: the read is asked once more and the step passes once the app answers again.
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/ios-busy-app.sh <simulator UDID>}
work=$(mktemp -d)
fz=
trap '[ -n "$fz" ] && kill "$fz" 2>/dev/null; wait 2>/dev/null; rm -rf "$work"' EXIT
failed=0
pass() { echo "ok    $*"; }
fail() { echo "FAIL  $*"; failed=1; }

cat >"$work/open.yaml" <<EOF
appId: dev.testbuddy.bench
---
- runFlow: {file: "$PWD/bench/flows/scenarios/steps/open.yaml", env: {SCENARIO: slow}}
- assertVisible: {id: jank_button}
EOF
cat >"$work/read.yaml" <<'EOF'
appId: dev.testbuddy.bench
---
- assertVisible: {id: jank_button}
EOF

# freeze.py <udid> <seconds>: stops the bench app in front on <udid> (its pid from that
# simulator's own runner), then resumes it; resumes it on any exit.
cat >"$work/freeze.py" <<'EOF'
import json, os, signal, socket, sys, time
udid, secs = sys.argv[1], float(sys.argv[2])
h = 0x811c9dc5
for b in udid.encode():
    h = ((h ^ b) * 0x01000193) & 0xffffffff
pid = 0
for port in range(22100 + h % 800, 22100 + h % 800 + 8):
    try:
        s = socket.create_connection(("127.0.0.1", port), 1)
        s.settimeout(10)
        f = s.makefile("rb")
        if json.loads(f.readline()).get("udid") != udid:
            continue
        s.sendall(b'{"id":1,"op":"foreground"}\n')
        m = json.loads(f.readline())
        if m.get("pkg") == "dev.testbuddy.bench":
            pid = m["pid"]
        break
    except (OSError, ValueError):
        continue
if pid <= 0:
    sys.exit("the bench app is not in front")
def resume(*_):
    os.kill(pid, signal.SIGCONT)
    sys.exit(0)
signal.signal(signal.SIGTERM, resume)
os.kill(pid, signal.SIGSTOP)
try:
    time.sleep(secs)
finally:
    os.kill(pid, signal.SIGCONT)
EOF

for ci in 0 1; do
  TB_CI=$ci "$TB" test --keep-agents --device "$DEV" "$work/open.yaml" >"$work/open.log" 2>&1 ||
    { fail "opening the slow screen: $(tail -3 "$work/open.log")"; continue; }
  python3 -I "$work/freeze.py" "$DEV" 40 &
  fz=$!
  sleep 1
  TB_CI=$ci "$TB" test --keep-agents --device "$DEV" "$work/read.yaml" >"$work/read.log" 2>&1
  status=$?
  kill "$fz" 2>/dev/null; wait "$fz" 2>/dev/null; fz=
  if grep -q 'did not answer "tree"' "$work/read.log"; then
    fail "TB_CI=$ci: tb timed out blind: $(grep -m1 'did not answer' "$work/read.log")"
  elif [ "$ci" = 0 ] && [ $status -ne 0 ] && grep -q 'the app did not answer accessibility' "$work/read.log"; then
    pass "TB_CI=0: the read answered with what it waited for: $(grep -m1 -o 'reading the screen.*' "$work/read.log")"
  elif [ "$ci" = 1 ] && [ $status -eq 0 ]; then
    pass "TB_CI=1: the read was asked once more and the step passed"
  else
    fail "TB_CI=$ci: exit $status: $(grep -E 'fail|error' "$work/read.log" | head -3)"
  fi
done
exit $failed
