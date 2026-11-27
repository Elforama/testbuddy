#!/usr/bin/env python3
import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import run

TASKS = json.loads(run.read_or_empty(os.path.join(run.HERE, "tasks.json")))
STUBBORN = [sys.executable, "-c", "import signal, time\nsignal.signal(signal.SIGTERM, signal.SIG_IGN)\nprint('up', flush=True)\ntime.sleep(60)"]


def click_log(targets, run_id="a", start=0):
    lines = [{"run": run_id, "t": start, "type": "load"}]
    for i, target in enumerate(targets):
        lines.append({"run": run_id, "t": start + 2 * i + 1, "type": "pointerdown", "target": target})
        lines.append({"run": run_id, "t": start + 2 * i + 2, "type": "click", "target": target})
    return "\n".join(json.dumps(line) for line in lines) + "\n"


def failed(task, targets):
    return run.check_task(TASKS[task], run.read_jsonl(click_log(targets)))["failed_checks"]


JOURNEY = ["scenario_journey", "username", "password", "login", "Item 3", "add_to_cart", "add_to_cart", "back",
           "open_live", "tap_me", "tap_me", "tap_me", "notify_switch", "back", "logout"]
CONTROLS = ["scenario_controls", "seg_l", "page", "radio_blue", "check_milk", "check_eggs", "switch_wifi",
            "stepper_plus", "stepper_plus", "stepper_plus", "chip_fast", "chip_good", "star_4"]
LISTS = ["scenario_lists", "open_long_list", "row_60", "back", "open_grid", "cell_50", "back", "open_two_pane",
         "mail_30", "back", "open_empty", "empty_add", "empty_add", "empty_add"]


class TbLog(unittest.TestCase):
    LOG = "\n".join(json.dumps(e) for e in [
        {"t": 1.0, "seconds": 0.1, "args": ["agent-guide"], "exit": 0},
        {"t": 2.0, "seconds": 1.5, "args": ["open", "-p", "web", "--headless", "--name", "bench"], "exit": 0},
        {"t": 3.0, "seconds": 0.2, "args": ["look", "-d", "bench"], "exit": 0},
        {"t": 4.0, "seconds": 0.3, "args": ["look", "-d", "bench", "--json"], "exit": 0},
        {"t": 5.0, "seconds": 0.4, "args": ["do", "-d", "bench", "tapOn: #logout\ntapOn: look"], "exit": 1},
        {"t": 6.0, "seconds": 0.1, "args": [], "exit": 2},
        {"t": 7.0, "seconds": 0.2, "args": ["close", "-d", "bench"], "exit": 0},
    ]) + "\n\n"

    def test_counts_commands_looks_and_failures(self):
        self.assertEqual(run.tb_stats(run.read_jsonl(self.LOG)), {"commands": 7, "looks": 2, "failed": 2})

    def test_empty_log(self):
        self.assertEqual(run.tb_stats(run.read_jsonl("")), {"commands": 0, "looks": 0, "failed": 0})


class ClickChecks(unittest.TestCase):
    def test_journey_passes(self):
        self.assertEqual(failed("journey", JOURNEY), [])

    def test_journey_passes_when_going_back_clicks_nothing(self):
        self.assertEqual(failed("journey", [t for t in JOURNEY if t != "back"]), [])

    def test_journey_fails_without_logout(self):
        self.assertIn("order", failed("journey", JOURNEY[:-1])[0])

    def test_journey_fails_with_a_third_add(self):
        targets = JOURNEY[:6] + ["add_to_cart"] + JOURNEY[6:]
        self.assertEqual(failed("journey", targets), [{"count": "add_to_cart", "is": 2}])

    def test_journey_fails_when_another_item_went_in_the_cart(self):
        each = {"each": "add_to_cart", "latest": "Item \\d+", "is": "Item 3"}
        targets = JOURNEY[:5] + ["back", "Item 30"] + JOURNEY[5:]
        self.assertEqual(failed("journey", targets), [each])
        targets = JOURNEY[:6] + ["back", "Item 4"] + JOURNEY[6:]
        self.assertEqual(failed("journey", targets), [each])

    def test_journey_allows_looking_at_another_item(self):
        targets = JOURNEY[:8] + ["Item 30", "back"] + JOURNEY[8:]
        self.assertEqual(failed("journey", targets), [])

    def test_journey_counts_taps_since_live_was_last_opened(self):
        targets = JOURNEY[:9] + ["tap_me", "back", "open_live"] + JOURNEY[9:]
        self.assertEqual(failed("journey", targets), [])

    def test_journey_fails_when_notifications_end_off(self):
        targets = JOURNEY[:13] + ["notify_switch"] + JOURNEY[13:]
        self.assertEqual(failed("journey", targets), [{"on": "notify_switch"}])

    def test_controls_passes(self):
        self.assertEqual(failed("controls", CONTROLS), [])

    def test_controls_overshoot_and_step_back(self):
        self.assertEqual(failed("controls", CONTROLS + ["stepper_plus", "stepper_minus", "star_5", "star_4"]), [])

    def test_controls_wrong_final_choice(self):
        got = failed("controls", CONTROLS + ["seg_s", "chip_cheap"])
        self.assertEqual(got, [{"last": "seg_[sml]", "is": "seg_l"}, {"off": "chip_cheap"}])

    def test_controls_ignores_other_ids_with_the_same_start(self):
        self.assertEqual(failed("controls", CONTROLS + ["seg_status", "radio_status", "star_status"]), [])

    def test_controls_reset_when_the_scenario_is_opened_again(self):
        self.assertTrue(failed("controls", CONTROLS + ["back", "scenario_controls"]))

    def test_lists_passes(self):
        self.assertEqual(failed("lists", LISTS), [])

    def test_lists_ignores_taps_on_the_mail_panes(self):
        self.assertEqual(failed("lists", LISTS + ["mail_body", "mail_list"]), [])

    def test_lists_fails_on_the_wrong_row(self):
        targets = [("row_61" if t == "row_60" else t) for t in LISTS]
        self.assertEqual(failed("lists", targets), [{"last": "row_\\d+", "is": "row_60"}])

    def test_only_the_last_page_load_counts(self):
        events = run.read_jsonl(click_log(JOURNEY, "a") + click_log(["login"], "b"))
        self.assertTrue(run.check_task(TASKS["journey"], events)["failed_checks"])
        events = run.read_jsonl(click_log(["login"], "a") + click_log(JOURNEY, "b"))
        self.assertEqual(run.check_task(TASKS["journey"], events)["failed_checks"], [])

    def test_no_clicks_fails(self):
        for task in TASKS.values():
            self.assertTrue(run.check_task(task, [])["failed_checks"])

    def test_pointerdowns_alone_do_not_count(self):
        events = [e for e in run.read_jsonl(click_log(JOURNEY)) if e["type"] != "click"]
        self.assertTrue(run.check_task(TASKS["journey"], events)["failed_checks"])

    def test_unknown_rule(self):
        with self.assertRaises(ValueError):
            run.check_one({"sometimes": "x"}, [])


class AgentResult(unittest.TestCase):
    def test_reads_the_result(self):
        stats = run.agent_stats(run.parse_result(json.dumps({
            "num_turns": 12, "total_cost_usd": 0.31, "duration_ms": 65400, "is_error": False, "result": "done",
            "usage": {"input_tokens": 40, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 9000, "output_tokens": 700},
        })))
        self.assertEqual((stats["turns"], stats["input_tokens"], stats["output_tokens"]), (12, 10040, 700))
        self.assertEqual((stats["cost_usd"], stats["seconds"], stats["agent_error"]), (0.31, 65.4, False))

    def test_output_that_is_not_json_is_an_error(self):
        stats = run.agent_stats(run.parse_result("Not logged in\n", "see claude login"))
        self.assertTrue(stats["agent_error"])
        self.assertIn("Not logged in", stats["answer"])
        self.assertTrue(run.agent_stats(run.parse_result("", ""))["agent_error"])

    def test_json_that_is_not_an_object_is_an_error(self):
        for out in ("[]", "null", "3", '"done"'):
            stats = run.agent_stats(run.parse_result(out))
            self.assertEqual((stats["agent_error"], stats["turns"]), (True, 0), out)

    def test_null_fields_count_as_zero(self):
        stats = run.agent_stats({"num_turns": None, "total_cost_usd": None, "duration_ms": None, "result": None,
                                 "usage": {"input_tokens": None, "output_tokens": None}})
        self.assertEqual((stats["turns"], stats["input_tokens"], stats["output_tokens"], stats["cost_usd"], stats["seconds"]),
                         (0, 0, 0, 0, 0))
        self.assertEqual(run.agent_stats({"usage": None})["input_tokens"], 0)
        self.assertEqual(run.agent_stats({"usage": [1]})["input_tokens"], 0)


def fake_run(task, commands, passed=True, **over):
    base = {"task": task, "commands": commands, "looks": 2, "failed": 1, "turns": 10, "input_tokens": 1000,
            "output_tokens": 100, "cost_usd": 0.5, "seconds": 30.0, "passed": passed, "agent_error": False}
    base.update(over)
    return base


def errored(task):
    return fake_run(task, 0, passed=False, looks=0, failed=0, turns=0, input_tokens=0, output_tokens=0,
                    cost_usd=0, seconds=0.0, agent_error=True)


class Tables(unittest.TestCase):
    def test_spread(self):
        self.assertEqual(run.spread([9, 30, 12], "{:.0f}"), "12 (9–30)")
        self.assertEqual(run.spread([1, 2, 3, 10], "{:.1f}"), "2.5 (1.0–10.0)")
        self.assertEqual(run.spread([0.256], "{:.2f}"), "0.26 (0.26–0.26)")
        self.assertEqual(run.spread([], "{:.0f}"), "-")

    def test_summary_rows(self):
        rows = dict(run.summary_rows([fake_run("journey", 9), fake_run("journey", 30, passed=False), fake_run("journey", 12)]))
        self.assertEqual(rows["tb commands"], "12 (9–30)")
        self.assertEqual(rows["cost (USD)"], "0.50 (0.50–0.50)")
        self.assertEqual(rows["passed"], "2/3")
        self.assertEqual(rows["agent errors"], "0/3")
        self.assertNotIn("reported correctly", rows)

    def test_errored_runs_stay_out_of_the_medians(self):
        rows = dict(run.summary_rows([fake_run("journey", 9), errored("journey"), fake_run("journey", 12)]))
        self.assertEqual(rows["tb commands"], "10 (9–12)")
        self.assertEqual(rows["turns"], "10 (10–10)")
        self.assertEqual(rows["passed"], "2/3")
        self.assertEqual(rows["agent errors"], "1/3")

    def test_only_errored_runs(self):
        rows = dict(run.summary_rows([errored("journey")]))
        self.assertEqual((rows["tb commands"], rows["passed"], rows["agent errors"]), ("-", "0/1", "1/1"))

    def test_one_table_per_task_in_run_order(self):
        text = run.results_tables([fake_run("lists", 5), fake_run("journey", 9), fake_run("lists", 7)])
        self.assertLess(text.index("### lists (2 runs)"), text.index("### journey (1 runs)"))
        self.assertIn("| tb commands | 6 (5–7) |", text)

    def test_compare(self):
        text = run.compare_tables("old.json", [fake_run("journey", 20)], "new.json",
                                  [fake_run("journey", 10), fake_run("lists", 4)])
        self.assertIn("| journey | old.json (1 runs) | new.json (1 runs) |", text)
        self.assertIn("| tb commands | 20 (20–20) | 10 (10–10) |", text)
        self.assertIn("| lists | old.json (0 runs) | new.json (1 runs) |", text)
        self.assertIn("| tb commands | - | 4 (4–4) |", text)


class OneRun(unittest.TestCase):
    def run_with(self, result, clicks):
        args = mock.Mock(claude="agent", model="m", max_budget_usd=1, timeout=5)

        def agent(cmd, cwd, env, timeout):
            self.root = os.path.dirname(cwd)
            with open(os.path.join(self.root, "clicks.jsonl"), "w") as f:
                f.write(click_log(clicks))
            if isinstance(result, BaseException):
                raise result
            return result

        with mock.patch.object(run, "start_web", return_value=(mock.Mock(), "http://127.0.0.1:1/?instrument=1")), \
                mock.patch.object(run, "run_agent", side_effect=agent), mock.patch.object(run, "stop_daemon"):
            return run.one_run("journey", TASKS["journey"], args, "/nowhere/tb", "/nowhere/web")

    def test_right_clicks_and_a_finished_agent_pass(self):
        got = self.run_with({"is_error": False, "num_turns": 3}, JOURNEY)
        self.assertEqual((got["passed"], got["agent_error"], got["commands"]), (True, False, 0))
        self.assertFalse(os.path.exists(self.root))

    def test_right_clicks_and_an_agent_error_do_not_pass(self):
        got = self.run_with(run.error_result("timed out after 5 s"), JOURNEY)
        self.assertEqual((got["passed"], got["agent_error"], got["failed_checks"]), (False, True, []))

    def test_the_temp_dir_goes_when_the_agent_is_interrupted(self):
        with self.assertRaises(KeyboardInterrupt):
            self.run_with(KeyboardInterrupt(), JOURNEY)
        self.assertFalse(os.path.exists(self.root))


class Wrapper(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        d = self.dir.name
        fake_tb = os.path.join(d, "fake-tb")
        with open(fake_tb, "w") as f:
            f.write('#!/bin/sh\necho "home=$TB_HOME args=$*"\n[ "$1" = look ]\n')
        os.chmod(fake_tb, 0o755)
        self.wrapper, self.log, self.home = os.path.join(d, "tb"), os.path.join(d, "tb.jsonl"), os.path.join(d, "home")
        with open(self.wrapper, "w") as f:
            f.write(run.WRAPPER.format(python=sys.executable, home=self.home, tb=fake_tb, log=self.log))
        os.chmod(self.wrapper, 0o755)

    def call(self, *args):
        return subprocess.run([self.wrapper, *args], capture_output=True, text=True)

    def test_logs_and_passes_the_exit_code(self):
        ok = self.call("look", "-d", "bench")
        bad = self.call("do", "tapOn: \"a\tb\"\ntapOn: c")
        self.assertEqual((ok.returncode, bad.returncode), (0, 1))
        self.assertIn("home=" + self.home, ok.stdout)
        entries = run.read_jsonl(run.read_or_empty(self.log))
        self.assertEqual([e["args"] for e in entries], [["look", "-d", "bench"], ["do", "tapOn: \"a\tb\"\ntapOn: c"]])
        self.assertEqual([e["exit"] for e in entries], [0, 1])

    def test_refuses_daemon_stop_and_release(self):
        stop = self.call("daemon", "stop")
        release = self.call("release", "-d", "bench")
        status = self.call("daemon", "status")
        self.assertEqual((stop.returncode, release.returncode, status.returncode), (2, 2, 1))
        self.assertEqual((stop.stdout, release.stdout), ("", ""))
        self.assertIn("not allowed", stop.stderr)
        self.assertIn("home=", status.stdout)
        entries = run.read_jsonl(run.read_or_empty(self.log))
        self.assertEqual([(e["args"][0], e["exit"]) for e in entries], [("daemon", 2), ("release", 2), ("daemon", 1)])
        self.assertEqual(run.tb_stats(entries)["failed"], 3)


class AgentCommand(unittest.TestCase):
    def test_bash_only_and_never_prompts(self):
        cmd = run.agent_command("claude", "the prompt", "sonnet", 2.0)
        self.assertEqual(cmd[:5], ["claude", "-p", "the prompt", "--output-format", "json"])
        self.assertEqual(cmd[cmd.index("--tools") + 1], "Bash")
        self.assertEqual(cmd[cmd.index("--permission-mode") + 1], "dontAsk")
        self.assertNotIn("--dangerously-skip-permissions", cmd)

    def test_prompt_is_the_same_for_every_build(self):
        prompt = run.PROMPT.format(wrapper="/w/tb", url="http://127.0.0.1:1/?instrument=1", task="T")
        self.assertNotIn("bin/tb", prompt)
        self.assertIn("tb open -p web --headless --name bench --app http://127.0.0.1:1/?instrument=1", prompt)

    def test_env_drops_host_session_markers_and_keeps_credentials(self):
        host = {"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": "s", "CLAUDE_CODE_MESSAGING_TOKEN": "t",
                "CLAUDE_CODE_ENTRYPOINT": "e", "TB_DEVICE": "emulator-5554", "TB_HOME": "/owner/home",
                "CLAUDE_CODE_OAUTH_TOKEN": "o", "CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_VERTEX": "1",
                "ANTHROPIC_API_KEY": "k", "PATH": "/usr/bin"}
        with mock.patch.dict(os.environ, host, clear=True):
            env = run.agent_env("/w/bin", "/w/home")
        self.assertEqual(env, {"CLAUDE_CODE_OAUTH_TOKEN": "o", "CLAUDE_CODE_USE_BEDROCK": "1", "CLAUDE_CODE_USE_VERTEX": "1",
                               "ANTHROPIC_API_KEY": "k", "PATH": "/w/bin" + os.pathsep + "/usr/bin", "TB_HOME": "/w/home"})


class Daemon(unittest.TestCase):
    def pid_from(self, log):
        with tempfile.TemporaryDirectory() as home:
            if log is not None:
                with open(os.path.join(home, "daemon.log"), "w") as f:
                    f.write(log)
            return run.daemon_pid(home)

    def test_pid_comes_from_the_listening_line(self):
        log = ("2026/11/27 02:27:05 tb daemon listening on /tmp/h/tb.sock (pid 4242)\n"
               "2026/11/27 02:27:06 chrome started (pid 5555)\n")
        self.assertEqual(self.pid_from(log), 4242)
        self.assertEqual(self.pid_from(log + "2026/11/27 02:30:00 tb daemon listening on /tmp/h/tb.sock (pid 4300)\n"), 4300)

    def test_no_pid(self):
        self.assertIsNone(self.pid_from("chrome started (pid 5555)\n"))
        self.assertIsNone(self.pid_from(None))

    def test_binary_path_must_match_exactly(self):
        tb = "/Users/me/Test Buddy/bin/tb"
        self.assertTrue(run.same_binary(tb + " daemon", tb))
        self.assertTrue(run.same_binary(tb, tb))
        self.assertFalse(run.same_binary(tb + "-old daemon", tb))
        self.assertFalse(run.same_binary("/Users/me/other/bin/tb daemon", tb))
        self.assertFalse(run.same_binary("", tb))


class StopAgent(unittest.TestCase):
    def test_timeout_kills_a_child_that_ignores_sigterm(self):
        made = []
        real = subprocess.Popen

        def spy(*a, **k):
            made.append(real(*a, **k))
            return made[-1]

        with mock.patch.object(subprocess, "Popen", spy):
            result = run.run_agent(STUBBORN, None, None, timeout=1, grace=0.5)
        self.assertEqual(result, {"is_error": True, "result": "timed out after 1 s"})
        self.assertEqual(made[0].returncode, -9)

    def test_interrupt_stops_the_child(self):
        made = []
        real = subprocess.Popen

        def spy(*a, **k):
            made.append(real(*a, **k))
            return made[-1]

        with mock.patch.object(subprocess, "Popen", spy), \
                mock.patch.object(real, "communicate", side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                run.run_agent([sys.executable, "-c", "import time; time.sleep(60)"], None, None, timeout=30, grace=5)
        self.assertEqual(made[0].returncode, -15)

    def test_a_finished_child_is_left_alone(self):
        result = run.run_agent([sys.executable, "-c", "print('{\"num_turns\": 2}')"], None, None, timeout=30)
        self.assertEqual(result, {"num_turns": 2})


class WebServer(unittest.TestCase):
    def test_a_server_that_exits_is_not_trusted(self):
        with self.assertRaises(RuntimeError):
            run.start_web("/usr/bin/false", "/dev/null")


if __name__ == "__main__":
    unittest.main()
