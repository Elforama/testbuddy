#!/usr/bin/env python3
"""Issue #74: a timed-out `sh` op kills what the command started, not only `sh`.

Starts tb's agent (a flow that does nothing on screen, --keep-agents), connects to it through
its own adb forward, and sends `sh` with a command that leaves a child running past its 500 ms
timeout (`sleep 30 & echo $!; wait`). The child's pid comes back in the output; the device must
have no such process left. A flow cannot see processes on the device, so this asks it.

  bench/checks/android-shell-timeout.py --device emulator-5554 [--tb bin/tb] [--adb adb]

Removes only its own forward and releases the agent at the end. Needs the bench app installed.
"""
import argparse
import json
import socket
import subprocess
import tempfile
import time
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument("--device", required=True)
p.add_argument("--tb", default="bin/tb")
p.add_argument("--adb", default="adb")
p.add_argument("--rounds", type=int, default=3)
a = p.parse_args()
tb = str(Path(a.tb).resolve())
adb = [a.adb, "-s", a.device]

with tempfile.TemporaryDirectory(prefix="tb-shell-timeout-") as tmp:
    flow = Path(tmp) / "start.yaml"
    flow.write_text("appId: dev.testbuddy.bench\n---\n- evalScript: output.started = true\n")
    subprocess.run([tb, "test", str(flow), "-p", "android", "--device", a.device, "--retries", "0", "--keep-agents"],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.STDOUT, timeout=120)

port = subprocess.check_output(adb + ["forward", "tcp:0", "localabstract:testbuddy-agent"], text=True).strip()
failed = 0
try:
    with socket.create_connection(("127.0.0.1", int(port)), timeout=10) as s:
        f = s.makefile("rwb")

        def reply(want_id):
            while True:
                m = json.loads(f.readline())
                if m.get("id") == want_id:
                    return m  # events ("hello", "changed", ...) are skipped

        hello = json.loads(f.readline())
        print(f"agent {hello.get('version')} on API {hello.get('sdk')}")
        for n in range(1, a.rounds + 1):
            req = {"id": n, "op": "sh", "cmd": "sleep 30 & echo $!; wait", "timeout": 500}
            t0 = time.monotonic()
            f.write((json.dumps(req) + "\n").encode())
            f.flush()
            r = reply(n)
            took = (time.monotonic() - t0) * 1000
            child = r.get("out", "").strip().splitlines()[0] if r.get("out", "").strip() else ""
            if not r.get("ok") or not r.get("timedOut") or not child.isdigit():
                print(f"round {n}: FAILED: unexpected reply {r}")
                failed += 1
                continue
            alive = subprocess.run(adb + ["shell", f"kill -0 {child} 2>/dev/null && echo alive || echo gone"],
                                   capture_output=True, text=True).stdout.strip()
            if alive != "gone":
                subprocess.run(adb + ["shell", f"kill -9 {child}"], capture_output=True)
                print(f"round {n}: FAILED: child {child} still running after the timeout (reply in {took:.0f} ms)")
                failed += 1
            else:
                print(f"round {n}: passed: child {child} gone, reply in {took:.0f} ms")
finally:
    subprocess.run(adb + ["forward", "--remove", "tcp:" + port], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    subprocess.run([tb, "release", "-d", a.device], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
raise SystemExit(1 if failed else 0)
