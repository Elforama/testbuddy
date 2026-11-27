#!/usr/bin/env python3
import argparse
import datetime
import json
import os
import re
import shutil
import signal
import socket
import statistics
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))

PROMPT = """You are testing a web app with tb, a command-line UI test tool.
`tb` is on your PATH ({wrapper}). Run `tb agent-guide` first to learn how to use it.
Open the app with: tb open -p web --headless --name bench --app {url}
Pass -d bench to every session command. Drive the app only through tb.
Never run `tb daemon stop` or `tb release`.
When you are done, close the session with `tb close -d bench`.

Task: {task}"""

WRAPPER = """#!{python}
import json, os, subprocess, sys, time
os.environ["TB_HOME"] = {home!r}
args = sys.argv[1:]
start = time.time()
if args[:2] == ["daemon", "stop"] or args[:1] == ["release"]:
    print("tb " + " ".join(args[:2]) + ": not allowed in this session", file=sys.stderr)
    code = 2
else:
    code = subprocess.call([{tb!r}] + args)
with open({log!r}, "a") as f:
    f.write(json.dumps({{"t": round(start, 3), "seconds": round(time.time() - start, 3), "args": args, "exit": code}}) + "\\n")
sys.exit(code)
"""

METRICS = [
    ("tb commands", "commands", "{:.0f}"),
    ("looks", "looks", "{:.0f}"),
    ("failed commands", "failed", "{:.0f}"),
    ("turns", "turns", "{:.0f}"),
    ("input tokens", "input_tokens", "{:.0f}"),
    ("output tokens", "output_tokens", "{:.0f}"),
    ("cost (USD)", "cost_usd", "{:.2f}"),
    ("seconds", "seconds", "{:.0f}"),
]
HOST_SESSION_ENV = {
    "CLAUDECODE", "CLAUDE_PID", "CLAUDE_AGENT_SDK_VERSION", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH",
    "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_HOST_SESSION_ID", "CLAUDE_CODE_CHILD_SESSION",
    "CLAUDE_CODE_CHILD_NOTES_DIR", "CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_MESSAGING_SOCKET",
    "CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_SDK_HAS_HOST_AUTH_REFRESH",
}


def read_or_empty(path):
    try:
        with open(path) as f:
            return f.read()
    except OSError:
        return ""


def read_jsonl(text):
    return [json.loads(line) for line in text.splitlines() if line.strip()]


def tb_stats(entries):
    return {
        "commands": len(entries),
        "looks": sum(1 for e in entries if e["args"][:1] == ["look"]),
        "failed": sum(1 for e in entries if e["exit"] != 0),
    }


def after_last(targets, marker):
    if marker and marker in targets:
        return targets[len(targets) - targets[::-1].index(marker):]
    return targets


def last_load_clicks(events, since=None):
    loads = [e["run"] for e in events if e.get("type") == "load"]
    if not loads:
        return []
    clicks = sorted((e for e in events if e["run"] == loads[-1] and e.get("type") == "click"), key=lambda e: e["t"])
    return after_last([e["target"] for e in clicks], since)


def is_subsequence(want, got):
    it = iter(got)
    return all(any(g == w for g in it) for w in want)


def each_follows(targets, each, latest, want):
    current = None
    for t in targets:
        if re.fullmatch(latest, t):
            current = t
        elif t == each and current != want:
            return False
    return True


def check_one(rule, targets):
    scoped = after_last(targets, rule.get("after"))
    if "order" in rule:
        return is_subsequence(rule["order"], scoped)
    if "count" in rule:
        return scoped.count(rule["count"]) == rule["is"]
    if "on" in rule:
        return scoped.count(rule["on"]) % 2 == 1
    if "off" in rule:
        return scoped.count(rule["off"]) % 2 == 0
    if "last" in rule:
        hits = [t for t in scoped if re.fullmatch(rule["last"], t)]
        return bool(hits) and hits[-1] == rule["is"]
    if "net" in rule:
        plus, minus = rule["net"]
        return scoped.count(plus) - scoped.count(minus) == rule["is"]
    if "each" in rule:
        return each_follows(scoped, rule["each"], rule["latest"], rule["is"])
    raise ValueError(f"unknown check: {rule}")


def check_task(task, events):
    targets = last_load_clicks(events, task.get("since"))
    return {"failed_checks": [rule for rule in task["checks"] if not check_one(rule, targets)], "clicks": targets}


def error_result(message):
    return {"is_error": True, "result": message}


def parse_result(out, err=""):
    try:
        result = json.loads(out)
    except ValueError:
        return error_result((out + err).strip()[-2000:] or "the agent printed nothing")
    if not isinstance(result, dict):
        return error_result(f"the agent's result is not a JSON object: {out.strip()[:200]}")
    return result


def agent_stats(result):
    usage = result.get("usage")
    usage = usage if isinstance(usage, dict) else {}
    fresh = usage.get("input_tokens") or 0
    written = usage.get("cache_creation_input_tokens") or 0
    read = usage.get("cache_read_input_tokens") or 0
    return {
        "turns": result.get("num_turns") or 0,
        "input_tokens": fresh + written + read,
        "uncached_input_tokens": fresh,
        "cache_write_tokens": written,
        "cache_read_tokens": read,
        "output_tokens": usage.get("output_tokens") or 0,
        "cost_usd": result.get("total_cost_usd") or 0,
        "seconds": (result.get("duration_ms") or 0) / 1000,
        "agent_error": bool(result.get("is_error")),
        "answer": str(result.get("result") or ""),
    }


def spread(values, fmt):
    if not values:
        return "-"
    return f"{fmt.format(statistics.median(values))} ({fmt.format(min(values))}–{fmt.format(max(values))})"


def task_names(runs):
    return list(dict.fromkeys(r["task"] for r in runs))


def summary_rows(runs):
    finished = [r for r in runs if not r["agent_error"]]
    rows = [(label, spread([r[key] for r in finished], fmt)) for label, key, fmt in METRICS]
    rows.append(("passed", f"{sum(1 for r in runs if r['passed'])}/{len(runs)}"))
    rows.append(("agent errors", f"{len(runs) - len(finished)}/{len(runs)}"))
    return rows


def table(headers, rows):
    lines = ["| " + " | ".join(headers) + " |", "|" + "---|" * len(headers)]
    lines += ["| " + " | ".join(row) + " |" for row in rows]
    return "\n".join(lines)


def results_tables(runs):
    out = []
    for task in task_names(runs):
        mine = [r for r in runs if r["task"] == task]
        out.append(f"### {task} ({len(mine)} runs)\n\n" + table([task, "median (min–max)"], summary_rows(mine)))
    return "\n\n".join(out)


def compare_tables(name_a, runs_a, name_b, runs_b):
    out = []
    for task in task_names(runs_a + runs_b):
        a = [r for r in runs_a if r["task"] == task]
        b = [r for r in runs_b if r["task"] == task]
        rows_a = dict(summary_rows(a)) if a else {}
        rows_b = dict(summary_rows(b)) if b else {}
        labels = list(dict.fromkeys(list(rows_a) + list(rows_b)))
        rows = [(label, rows_a.get(label, "-"), rows_b.get(label, "-")) for label in labels]
        out.append(f"### {task}\n\n" + table([task, f"{name_a} ({len(a)} runs)", f"{name_b} ({len(b)} runs)"], rows))
    return "\n\n".join(out)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def start_web(binary, click_log):
    port = free_port()
    proc = subprocess.Popen([binary, "-addr", f"127.0.0.1:{port}", "-other=", "-clicklog", click_log],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    url = f"http://127.0.0.1:{port}/"
    deadline = time.time() + 10
    while time.time() < deadline and proc.poll() is None:
        try:
            urllib.request.urlopen(url, timeout=1).close()
        except OSError:
            time.sleep(0.1)
            continue
        if proc.poll() is None:
            return proc, url + "?instrument=1"
    proc.kill()
    proc.wait()
    raise RuntimeError("the bench web server did not start")


def agent_env(bin_dir, home):
    env = {k: v for k, v in os.environ.items() if k not in HOST_SESSION_ENV and not k.startswith("TB_")}
    env["PATH"] = bin_dir + os.pathsep + env.get("PATH", "")
    env["TB_HOME"] = home
    return env


def agent_command(claude, prompt, model, budget):
    return [claude, "-p", prompt, "--output-format", "json", "--model", model,
            "--tools", "Bash", "--allowedTools", "Bash",
            "--permission-mode", "dontAsk", "--permission-prompts", "none",
            "--safe-mode", "--strict-mcp-config", "--no-session-persistence",
            "--max-budget-usd", str(budget)]


def signal_group(proc, sig):
    try:
        os.killpg(proc.pid, sig)
    except (ProcessLookupError, PermissionError):
        pass


def stop_group(proc, grace=10):
    if proc.poll() is not None:
        return
    signal_group(proc, signal.SIGTERM)
    try:
        proc.wait(timeout=grace)
    except subprocess.TimeoutExpired:
        signal_group(proc, signal.SIGKILL)
        proc.wait()


def run_agent(cmd, cwd, env, timeout, grace=10):
    proc = subprocess.Popen(cmd, cwd=cwd, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True, start_new_session=True)
    try:
        out, err = proc.communicate(timeout=timeout)
        return parse_result(out, err)
    except subprocess.TimeoutExpired:
        return error_result(f"timed out after {timeout} s")
    finally:
        stop_group(proc, grace)
        proc.stdout.close()
        proc.stderr.close()


def daemon_pid(home):
    text = read_or_empty(os.path.join(home, "daemon.log"))
    pids = re.findall(r"tb daemon listening on .* \(pid (\d+)\)\s*$", text, re.M)
    return int(pids[-1]) if pids else None


def alive(pid):
    try:
        os.kill(pid, 0)
        return True
    except OSError:
        return False


def same_binary(command, tb):
    return command == tb or command.startswith(tb + " ")


def is_our_daemon(pid, tb):
    ps = subprocess.run(["ps", "-p", str(pid), "-o", "command="], capture_output=True, text=True)
    return same_binary(ps.stdout.strip(), tb)


def stop_daemon(home, tb):
    pid = daemon_pid(home)
    if pid is None or not alive(pid) or not is_our_daemon(pid, tb):
        return
    env = dict(os.environ, TB_HOME=home)
    try:
        subprocess.run([tb, "close", "-d", "bench"], env=env, capture_output=True, timeout=30)
    except subprocess.TimeoutExpired:
        pass
    os.kill(pid, signal.SIGTERM)
    deadline = time.time() + 10
    while alive(pid) and time.time() < deadline:
        time.sleep(0.1)
    if alive(pid):
        os.kill(pid, signal.SIGKILL)


def one_run(name, task, args, tb, web):
    root = tempfile.mkdtemp(prefix="tbcost-", dir="/tmp")
    home, bin_dir, work = (os.path.join(root, d) for d in ("home", "bin", "work"))
    tb_log, click_log = os.path.join(root, "tb.jsonl"), os.path.join(root, "clicks.jsonl")
    wrapper = os.path.join(bin_dir, "tb")
    server = None
    try:
        for d in (home, bin_dir, work):
            os.mkdir(d)
        with open(wrapper, "w") as f:
            f.write(WRAPPER.format(python=sys.executable, home=home, tb=tb, log=tb_log))
        os.chmod(wrapper, 0o755)
        server, url = start_web(web, click_log)
        prompt = PROMPT.format(wrapper=wrapper, url=url, task=task["prompt"])
        result = run_agent(agent_command(args.claude, prompt, args.model, args.max_budget_usd),
                           work, agent_env(bin_dir, home), args.timeout)
        stop_daemon(home, tb)
        entries = read_jsonl(read_or_empty(tb_log))
        events = read_jsonl(read_or_empty(click_log))
    finally:
        stop_daemon(home, tb)
        if server:
            server.terminate()
            server.wait()
        shutil.rmtree(root, ignore_errors=True)
    run = {"task": name}
    run.update(tb_stats(entries))
    run.update(agent_stats(result))
    run.update(check_task(task, events))
    run["passed"] = not run["failed_checks"] and not run["agent_error"]
    run["tb_log"] = entries
    return run


def output_of(cmd):
    try:
        return subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, timeout=30).stdout.strip()
    except (OSError, subprocess.TimeoutExpired):
        return ""


def shown_path(path):
    rel = os.path.relpath(path, os.path.realpath(ROOT))
    return path if rel.startswith("..") else rel


def load_runs(path):
    return json.loads(read_or_empty(path) or "{}").get("runs", [])


def main():
    ap = argparse.ArgumentParser(description="What it costs an agent to do a job in the bench web app with tb.")
    ap.add_argument("--tb", default=os.path.join(ROOT, "bin", "tb"), help="the tb binary under test")
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--tasks", default="", help="comma-separated task names; default all")
    ap.add_argument("--out", default="", help="results file; default tb-agent-cost-<time>.json in the temp directory")
    ap.add_argument("--timeout", type=int, default=900, help="seconds per agent run")
    ap.add_argument("--max-budget-usd", type=float, default=2.0, help="spending cap per agent run")
    ap.add_argument("--claude", default="claude", help="the agent CLI, or a stand-in that takes the same arguments")
    ap.add_argument("--compare", nargs=2, metavar=("A.json", "B.json"), help="print two results files side by side")
    args = ap.parse_args()

    if args.compare:
        a, b = args.compare
        print(compare_tables(os.path.basename(a), load_runs(a), os.path.basename(b), load_runs(b)))
        return

    tasks = json.loads(read_or_empty(os.path.join(HERE, "tasks.json")))
    names = [n for n in args.tasks.split(",") if n] or list(tasks)
    unknown = [n for n in names if n not in tasks]
    if unknown:
        sys.exit(f"unknown tasks: {', '.join(unknown)} (have: {', '.join(tasks)})")
    tb = os.path.realpath(args.tb)
    if not os.access(tb, os.X_OK):
        sys.exit(f"{tb}: no tb binary; build it (scripts/build.sh --skip-agents) or pass --tb")

    build = tempfile.mkdtemp(prefix="tbcost-web-", dir="/tmp")
    web = os.path.join(build, "benchweb")
    stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    out = args.out or os.path.join(tempfile.gettempdir(), f"tb-agent-cost-{stamp}.json")
    doc = {
        "meta": {
            "date": stamp, "model": args.model, "tb": shown_path(tb),
            "tb_version": output_of([tb, "version"]),
            "commit": output_of(["git", "rev-parse", "--short", "HEAD"]),
            "agent_cli": output_of([args.claude, "--version"]),
        },
        "runs": [],
    }
    try:
        subprocess.run(["go", "build", "-o", web, "./bench/web"], cwd=ROOT, check=True)
        for name in names:
            for i in range(args.runs):
                run = one_run(name, tasks[name], args, tb, web)
                doc["runs"].append(run)
                with open(out, "w") as f:
                    json.dump(doc, f, indent=1)
                print(f"[{name}] run {i + 1}: {'pass' if run['passed'] else 'FAIL'}, {run['commands']} tb commands "
                      f"({run['looks']} looks, {run['failed']} failed), {run['turns']} turns, "
                      f"${run['cost_usd']:.2f}, {run['seconds']:.0f} s"
                      + (f"; agent error: {run['answer'][:200]}" if run["agent_error"] else ""),
                      file=sys.stderr, flush=True)
    finally:
        shutil.rmtree(build, ignore_errors=True)
    print(results_tables(doc["runs"]))
    print(f"\nresults: {out}")


if __name__ == "__main__":
    main()
