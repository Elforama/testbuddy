#!/usr/bin/env python3
"""Summarise the web click instrument's log (bench/web/site/instrument.js).

For each run (one page load), it checks every pointerdown against the element the flow meant
to tap, and counts the ones that landed while a pushed screen was still sliding in.

  bench/clicks.py bench/results/web-clicks.jsonl
"""
import argparse
import collections
import json

# The taps the journey makes, in order (bench/flows/steps/journey.yaml), starting with its row
# on the scenario list.
EXPECTED = ["scenario_journey", "login", "username", "password", "login", "Item 3", "add_to_cart", "add_to_cart", "back",
            "Item 30", "back", "load_more", "open_live", "tap_me", "tap_me", "tap_me", "notify_switch",
            "back", "logout"]
SAME = {"notify_switch": {"notify_label"}}  # the switch's label toggles it too


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("log")
    args = ap.parse_args()
    expected = EXPECTED

    runs = collections.defaultdict(list)
    for line in open(args.log):
        line = line.strip()
        if line:
            e = json.loads(line)
            runs[e["run"]].append(e)

    total = during = 0
    runs_during = 0
    wrong, count_off = [], []
    for run, evs in runs.items():
        downs = [e for e in sorted(evs, key=lambda e: e["t"]) if e["type"] == "pointerdown"]
        if not downs:
            continue
        total += 1
        d = [e for e in downs if e.get("sliding")]
        during += len(d)
        runs_during += bool(d)
        if len(downs) != len(expected):
            count_off.append((run, len(downs)))
        for i, (e, want) in enumerate(zip(downs, expected)):
            if e["target"] != want and e["target"] not in SAME.get(want, ()):
                wrong.append((run, i + 1, want, e["target"], e["x"], e["y"], e.get("sliding", "")))
                break  # the rest of the run is off script

    print(f"runs: {total}")
    print(f"taps during a slide-in: {during} (in {runs_during} runs)")
    print(f"taps on the wrong element: {len(wrong)}")
    for w in wrong:
        print("  run %s tap %d: meant %s, hit %s at (%d, %d)%s" % (w[0], w[1], w[2], w[3], w[4], w[5],
              f", {w[6]} sliding in" if w[6] else ""))
    print(f"runs with a tap count other than {len(expected)}: {len(count_off)}")
    for run, n in count_off:
        print(f"  run {run}: {n} taps")


if __name__ == "__main__":
    main()
