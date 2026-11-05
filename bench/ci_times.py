#!/usr/bin/env python3
"""CI benchmark log: how long each bench flow, and the whole suite, takes in the CI e2e jobs.

CI runners are much slower than a developer's machine, so these times are kept apart from
bench/results, on their own branch of the repository (`bench-log`), one file per platform per
CI run:

  ci/<platform>/<date>-<commit>-<run id>-<attempt>.json

Each e2e job fetches that branch, runs the flows with --format junit, then runs this script,
which:

  1. reads each flow's time and result, and the suite's total, from tb's junit reports;
  2. compares them with the average of the last 5 logged runs from main (any branch when main
     has none yet), in a table in the job summary, with a warning for each number that
     regressed;
  3. writes this run's log file, which the workflow's last job commits to `bench-log`.

  bench/ci_times.py --platform web --history build/bench-log --out build/e2e/bench-log build/e2e/*.xml

A regression is a warning, never a failure: CI times are noisy, and failing flows already fail
the tb steps.
"""
import argparse
import glob
import json
import os
import sys
import xml.etree.ElementTree as ET
from datetime import datetime, timezone

# The baseline: the average of this many of the latest logged runs.
BASELINE_RUNS = 5
# Flagged when both this much slower, relatively, and this many seconds slower.
FLOW_RATIO, FLOW_SECONDS = 1.25, 2.0      # one flow
TOTAL_RATIO, TOTAL_SECONDS = 1.15, 10.0   # the whole suite


def read_junit(paths):
    """{flow file: {name, seconds, passed}}, and the suite's total (each tb run's own time)."""
    flows, total = {}, 0.0
    for path in paths:
        try:
            root = ET.parse(path).getroot()
        except (OSError, ET.ParseError) as e:
            print(f"skipping {path}: {e}", file=sys.stderr)
            continue
        suites = [root] if root.tag == "testsuite" else root.findall("testsuite")
        for suite in suites:
            total += float(suite.get("time") or 0)
            for case in suite.iter("testcase"):
                key = case.get("file") or case.get("name")
                passed = (case.get("status", "SUCCESS") == "SUCCESS"
                          and case.find("failure") is None and case.find("error") is None)
                flows[key] = {"name": case.get("name") or key,
                              "seconds": round(float(case.get("time") or 0), 2), "passed": passed}
    return flows, round(total, 2)


def baseline(history, platform, n=BASELINE_RUNS):
    """The average of this platform's last n logged runs from main (from any branch while main
    has none), or None without any: each flow's average over the runs it passed in, and the
    total's over the runs in which every flow passed."""
    logs = []
    for path in glob.glob(os.path.join(history, "ci", platform, "*.json")):
        try:
            with open(path) as f:
                logs.append(json.load(f))
        except (OSError, ValueError):
            continue
    logs.sort(key=lambda r: r.get("date", ""))
    runs = ([r for r in logs if r.get("branch") == "main"] or logs)[-n:]
    if not runs:
        return None
    flows = {}
    for r in runs:
        for key, f in r.get("flows", {}).items():
            if f.get("passed", True):
                flows.setdefault(key, []).append(f["seconds"])
    totals = [r["total_seconds"] for r in runs
              if r.get("flows") and all(f.get("passed", True) for f in r["flows"].values())]
    return {
        "branch": runs[-1].get("branch", "?"),
        "runs": runs,
        "flows": {k: {"seconds": sum(v) / len(v), "count": len(v)} for k, v in flows.items()},
        "total_seconds": sum(totals) / len(totals) if totals else None,
        "total_count": len(totals),
    }


def slower(now, before, ratio, seconds):
    return before > 0 and now > before * ratio and now - before > seconds


def change(now, before):
    pct = (now - before) / before * 100 if before else 0
    return f"{now - before:+.1f} s ({pct:+.0f} %)"


def compare(platform, run, prev):
    """Markdown table of this run against prev, and the regressions (label, now, before)."""
    lines = [f"### Bench times, {platform} (CI)", ""]
    if prev:
        runs = ", ".join(f"[`{r.get('commit', '?')[:7]}`]({r['url']})" if r.get("url") else f"`{r.get('commit', '?')[:7]}`"
                         for r in prev["runs"])
        lines.append(f"Compared with the average of the last {len(prev['runs'])} logged runs from "
                     f"`{prev['branch']}` ({runs}): a flow's average counts only the runs it passed in.")
    else:
        lines.append("No logged run for this platform yet: this one starts the log.")
    lines += ["", "| Flow | this run | average | change |", "|---|---|---|---|"]
    regressions = []
    old = (prev or {}).get("flows", {})
    for key in sorted(run["flows"]):
        f = run["flows"][key]
        now = f"{f['seconds']:.1f} s" + ("" if f["passed"] else " ❌ failed")
        o = old.get(key)
        if not o:
            lines.append(f"| {f['name']} | {now} | none | new |")
            continue
        was = f"{o['seconds']:.1f} s" + (f" ({o['count']} runs)" if o["count"] < len(prev["runs"]) else "")
        note = change(f["seconds"], o["seconds"])
        # Only passing runs of a flow say how long it takes.
        if f["passed"] and slower(f["seconds"], o["seconds"], FLOW_RATIO, FLOW_SECONDS):
            note += " ⚠️ slower"
            regressions.append((f["name"], f["seconds"], o["seconds"]))
        lines.append(f"| {f['name']} | {now} | {was} | {note} |")
    passed = sum(f["passed"] for f in run["flows"].values())
    total = f"| **Total** ({passed}/{len(run['flows'])} passed) | **{run['total_seconds']:.1f} s** |"
    if prev and prev["total_seconds"] is not None:
        note = change(run["total_seconds"], prev["total_seconds"])
        # A total is compared only when every flow passed: a failed flow's time is no measure.
        if passed == len(run["flows"]) and slower(run["total_seconds"], prev["total_seconds"], TOTAL_RATIO, TOTAL_SECONDS):
            note += " ⚠️ slower"
            regressions.append(("Total", run["total_seconds"], prev["total_seconds"]))
        counted = f" ({prev['total_count']} runs)" if prev["total_count"] < len(prev["runs"]) else ""
        total += f" {prev['total_seconds']:.1f} s{counted} | {note} |"
    else:
        total += " none | |"
    lines.append(total)
    lines += ["", f"A number is flagged when it is over {FLOW_RATIO * 100 - 100:.0f} % and "
              f"{FLOW_SECONDS:.0f} s slower (a flow) or over {TOTAL_RATIO * 100 - 100:.0f} % and "
              f"{TOTAL_SECONDS:.0f} s slower (the total, when every flow passed)."]
    return "\n".join(lines) + "\n", regressions


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--platform", required=True)
    ap.add_argument("--history", required=True, help="a checkout of the bench-log branch (may be empty)")
    ap.add_argument("--out", required=True, help="folder to write this run's log into (as ci/<platform>/...)")
    ap.add_argument("junit", nargs="+", help="tb junit reports of this run")
    a = ap.parse_args()

    flows, total = read_junit(a.junit)
    if not flows:
        print("no flow results in the junit reports: nothing to log")
        return 0
    env = os.environ.get
    now = datetime.now(timezone.utc)
    commit = env("GITHUB_SHA", "local")
    server, repo, run_id = env("GITHUB_SERVER_URL", ""), env("GITHUB_REPOSITORY", ""), env("GITHUB_RUN_ID", "")
    run = {
        "platform": a.platform,
        "date": now.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "commit": commit,
        # A pull request's own branch, not the merge ref CI checks out.
        "branch": env("GITHUB_HEAD_REF") or env("GITHUB_REF_NAME", ""),
        "event": env("GITHUB_EVENT_NAME", ""),
        "run_id": run_id,
        "run_attempt": env("GITHUB_RUN_ATTEMPT", ""),
        "url": f"{server}/{repo}/actions/runs/{run_id}" if run_id else "",
        "runner": " ".join(filter(None, [env("RUNNER_OS"), env("RUNNER_ARCH"), env("ImageOS"), env("ImageVersion")])),
        "total_seconds": total,
        "flows": flows,
    }
    prev = baseline(a.history, a.platform)
    table, regressions = compare(a.platform, run, prev)
    print(table)
    if env("GITHUB_STEP_SUMMARY"):
        with open(env("GITHUB_STEP_SUMMARY"), "a") as f:
            f.write(table + "\n")
    for label, secs, was in regressions:
        print(f"::warning title=Bench time regressed ({a.platform})::{label}: {secs:.1f} s, "
              f"average of recent runs {was:.1f} s ({change(secs, was)})")

    name = f"{now.strftime('%Y-%m-%dT%H%M%SZ')}-{commit[:7]}-{run_id or 'local'}-{run['run_attempt'] or '1'}.json"
    out = os.path.join(a.out, "ci", a.platform, name)
    os.makedirs(os.path.dirname(out), exist_ok=True)
    with open(out, "w") as f:
        json.dump(run, f, indent=2, sort_keys=True)
        f.write("\n")
    print(f"wrote {out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
