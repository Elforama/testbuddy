#!/usr/bin/env python3
"""Time tb on the bench journey: one warm-up run (not counted), then N timed runs of the whole
`tb test` command per platform, start-up included. Prints a Markdown table.

  bench/time.py                                   # android, ios, web; 5 runs each
  bench/time.py --platforms ios --runs 3
  BENCH_ANDROID=emulator-5556 BENCH_IOS=<udid> bench/time.py

Build tb first (scripts/build.sh), serve the web app (go run ./bench/web), and install the
bench app on the emulator and simulator (bench/README.md).
"""
import argparse
import os
import statistics
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TB = os.environ.get("TB", "bin/tb")


def command(platform):
    if platform == "web":
        return [TB, "test", "bench/flows/journey-web.yaml", "--headless", "--quiet", "--keep-agents"]
    dev = os.environ.get("BENCH_ANDROID" if platform == "android" else "BENCH_IOS", "")
    cmd = [TB, "test", "bench/flows/journey.yaml", "--quiet", "--keep-agents", "-p", platform]
    return cmd + (["--device", dev] if dev else [])


def run(cmd, timeout):
    t0 = time.perf_counter()
    try:
        ok = subprocess.run(cmd, cwd=ROOT, capture_output=True, timeout=timeout).returncode == 0
    except subprocess.TimeoutExpired:
        ok = False
    return time.perf_counter() - t0, ok


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--platforms", default="android,ios,web")
    ap.add_argument("--runs", type=int, default=5)
    ap.add_argument("--timeout", type=int, default=300)
    args = ap.parse_args()

    rows = []
    for platform in args.platforms.split(","):
        cmd = command(platform)
        secs, ok = run(cmd, args.timeout)
        print(f"[{platform}] warm-up {secs:.1f}s {'ok' if ok else 'FAILED'}", file=sys.stderr, flush=True)
        times, fails = [], 0
        for i in range(args.runs):
            secs, ok = run(cmd, args.timeout)
            print(f"  run {i + 1}: {secs:.2f}s {'ok' if ok else 'FAILED'}", file=sys.stderr, flush=True)
            if ok:
                times.append(secs)
            else:
                fails += 1
        if times:
            cell = f"{statistics.mean(times):.1f} s ({min(times):.1f}–{max(times):.1f})"
        else:
            cell = "all failed"
        rows.append(f"| {platform} | {cell} | {fails} |")

    print("| Platform | tb avg (min–max) | failed |")
    print("|---|---|---|")
    print("\n".join(rows))


if __name__ == "__main__":
    main()
