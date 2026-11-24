#!/bin/bash
set -u
cd "$(dirname "$0")/../.."
TB=${TB:-bin/tb}
DEV=${1:?usage: bench/checks/ios-busy-runner.sh <simulator UDID>}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failed=0
pass() { echo "ok    $*"; }
fail() { echo "FAIL  $*"; failed=1; }

cat >"$work/settings.yaml" <<'EOF'
appId: com.apple.Preferences
---
- launchApp
- assertVisible: "General"
EOF

cat >"$work/runner.py" <<'EOF'
import json, socket, sys, time
udid, what = sys.argv[1], sys.argv[2]
h = 0x811c9dc5
for b in udid.encode():
    h = ((h ^ b) * 0x01000193) & 0xffffffff
for port in range(22100 + h % 800, 22100 + h % 800 + 8):
    try:
        s = socket.create_connection(("127.0.0.1", port), 1)
        s.settimeout(3)
        f = s.makefile("rb")
        hello = json.loads(f.readline())
    except (OSError, ValueError):
        continue
    if hello.get("udid") != udid:
        continue
    if what == "pid":
        print(hello.get("pid"))
    elif what == "exit":
        s.sendall(b'{"id":1,"op":"exit"}\n')
    else:
        s.sendall(b'{"id":1,"op":"awaitPackage","pkg":"no.such.app","timeout":15000}\n')
        s.settimeout(30)
        f.readline()  # held until the op ends, or the runner drops this connection
    sys.exit(0)
sys.exit(1)
EOF

"$TB" test --keep-agents --device "$DEV" "$work/settings.yaml" >"$work/first.log" 2>&1 ||
  { fail "starting a runner: $(tail -3 "$work/first.log")"; exit 1; }
before=$(python3 "$work/runner.py" "$DEV" pid) || { fail "no runner serves $DEV"; exit 1; }

python3 "$work/runner.py" "$DEV" busy &
holder=$!
sleep 1
t0=$(date +%s)
"$TB" test --keep-agents --device "$DEV" "$work/settings.yaml" >"$work/second.log" 2>&1 ||
  fail "the run on the busy runner: $(tail -3 "$work/second.log")"
wait "$holder" 2>/dev/null
after=$(python3 "$work/runner.py" "$DEV" pid)
if [ "$after" = "$before" ]; then
  pass "the busy runner was reused (pid $before, run took $(($(date +%s) - t0)) s)"
else
  fail "the busy runner was replaced: pid $before -> ${after:-none}"
fi

python3 "$work/runner.py" "$DEV" exit
exit $failed
