#!/usr/bin/env python3
"""Baseline the scenario app: one warm-up pass of every scenario flow (not counted), then N timed
passes per platform. Records each flow's time and result, stamped with the commit it ran at, to
bench/results/scenarios-<commit>.json, and prints a Markdown table.

  bench/scenarios.py                                  # android, ios, web; 3 passes each
  bench/scenarios.py --platforms web --runs 5
  bench/scenarios.py --compare bench/results/scenarios-4e92d08.json   # against a baseline
  BENCH_ANDROID=emulator-5554 BENCH_IOS=<udid> bench/scenarios.py

Build tb (scripts/build.sh) and the bench apps from the same commit first, install them, and serve
the web app (go run ./bench/web). Flows run one at a time on one device, so times compare run to
run. A dirty tree is recorded as such: commit first for a baseline you can point back to.
"""
import argparse
import json
import os
import platform as host
import statistics
import subprocess
import sys
import time
from datetime import datetime, timezone

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TB = os.environ.get("TB", "bin/tb")
FLOWS = "bench/flows/scenarios"
WEB = os.environ.get("BENCH_WEB", "http://127.0.0.1:7360/")


def sh(*cmd):
    try:
        return subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, timeout=30).stdout.strip()
    except Exception:
        return ""


def command(plat):
    cmd = [TB, "test", FLOWS, "--json"]
    if plat == "web":
        return cmd + ["-e", "BENCH=" + WEB, "--headless"]
    dev = os.environ.get("BENCH_ANDROID" if plat == "android" else "BENCH_IOS", "")
    return cmd + ["-p", plat] + (["--device", dev] if dev else [])


def run(cmd, timeout):
    """One pass of every flow: wall time, and {flow file: (seconds, passed)}."""
    t0 = time.perf_counter()
    try:
        out = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, timeout=timeout).stdout
    except subprocess.TimeoutExpired:
        return time.perf_counter() - t0, {}, ""
    wall = time.perf_counter() - t0
    try:
        summary = json.loads(out[out.index("{"):])
    except ValueError:
        return wall, {}, ""
    flows, device = {}, ""
    for f in summary.get("flows", []):
        name = os.path.basename(f["file"]).removesuffix(".yaml")
        flows[name] = (f.get("seconds", 0), f.get("status") == "SUCCESS")
        device = device or f.get("device", "")
    return wall, flows, device


def stamp():
    commit = sh("git", "rev-parse", "HEAD")
    return {
        "commit": commit,
        "short": commit[:7],
        "subject": sh("git", "log", "-1", "--format=%s"),
        "dirty": bool(sh("git", "status", "--porcelain", "--untracked-files=no")),
        "date": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "tb": sh(TB, "--version"),
        "machine": f"{host.machine()} {sh('sysctl', '-n', 'machdep.cpu.brand_string') or host.processor()}, "
                   f"macOS {host.mac_ver()[0]}",
    }


def summarize(plat, passes):
    names = sorted({n for _, flows, _ in passes for n in flows})
    rows = {}
    for n in names:
        times = [flows[n][0] for _, flows, _ in passes if n in flows]
        ok = sum(1 for _, flows, _ in passes if flows.get(n, (0, False))[1])
        rows[n] = {"median": round(statistics.median(times), 2), "min": round(min(times), 2),
                   "max": round(max(times), 2), "passed": ok, "runs": len(passes),
                   "seconds": [round(t, 2) for t in times]}
    walls = [w for w, _, _ in passes]
    return {"device": next((d for _, _, d in passes if d), ""), "flows": rows,
            "wall": {"median": round(statistics.median(walls), 1), "min": round(min(walls), 1),
                     "max": round(max(walls), 1), "seconds": [round(w, 1) for w in walls]}}


def table(result, base=None):
    lines = []
    for plat, p in result["platforms"].items():
        b = (base or {}).get("platforms", {}).get(plat, {})
        lines += [f"\n### {plat} ({p['device']})\n",
                  "| Scenario | median | range | passed |" + (" vs baseline |" if b else ""),
                  "|---|---|---|---|" + ("---|" if b else "")]
        for n, r in p["flows"].items():
            row = f"| {n} | {r['median']:.1f} s | {r['min']:.1f}–{r['max']:.1f} | {r['passed']}/{r['runs']} |"
            if b:
                old = b.get("flows", {}).get(n)
                row += f" {delta(r['median'], old['median'])} |" if old else " new |"
            lines.append(row)
        w = p["wall"]
        row = f"| **whole run** | **{w['median']:.0f} s** | {w['min']:.0f}–{w['max']:.0f} | " \
              f"{sum(r['passed'] for r in p['flows'].values())}/{sum(r['runs'] for r in p['flows'].values())} |"
        if b:
            row += f" {delta(w['median'], b['wall']['median'])} |"
        lines.append(row)
    return "\n".join(lines)


def delta(new, old):
    if not old:
        return "–"
    pct = (new - old) / old * 100
    return f"{pct:+.0f}%"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--platforms", default="android,ios,web")
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--timeout", type=int, default=1800, help="seconds per pass")
    ap.add_argument("--out", help="result file (default bench/results/scenarios-<commit>.json)")
    ap.add_argument("--compare", help="a baseline result file to compare against")
    a = ap.parse_args()

    result = {**stamp(), "runs": a.runs, "platforms": {}}
    if result["dirty"]:
        print("warning: uncommitted changes; the result will not match the commit exactly", file=sys.stderr)
    for plat in a.platforms.split(","):
        cmd = command(plat)
        print(f"{plat}: warm-up ({' '.join(cmd)})", file=sys.stderr)
        run(cmd, a.timeout)
        passes = []
        for i in range(a.runs):
            wall, flows, device = run(cmd, a.timeout)
            bad = [n for n, (_, ok) in flows.items() if not ok]
            print(f"{plat}: pass {i + 1}/{a.runs} {wall:.0f} s, {len(flows) - len(bad)}/{len(flows)} passed"
                  + (f" (failed: {', '.join(bad)})" if bad else ""), file=sys.stderr)
            passes.append((wall, flows, device))
        if any(flows for _, flows, _ in passes):
            result["platforms"][plat] = summarize(plat, passes)

    name = result["short"] + ("-dirty" if result["dirty"] else "")  # never over a commit's baseline
    out = a.out or os.path.join(ROOT, "bench", "results", f"scenarios-{name}.json")
    with open(out, "w") as f:
        json.dump(result, f, indent=2)
        f.write("\n")
    base = json.load(open(a.compare)) if a.compare else None
    print(f"## Scenarios at {result['short']}{' (dirty)' if result['dirty'] else ''}: {result['subject']}")
    print(f"{result['date']}, {result['tb']}, {result['machine']}, {a.runs} passes after a warm-up")
    if base:
        print(f"Compared with {base['short']} ({base['date']})")
    print(table(result, base))
    print(f"\nSaved {os.path.relpath(out, ROOT)}", file=sys.stderr)


if __name__ == "__main__":
    main()
