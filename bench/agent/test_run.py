#!/usr/bin/env python3
import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import run

TASKS = json.load(open(os.path.join(run.HERE, "tasks.json")))


def click_log(targets, run_id="a", start=0):
    lines = [{"run": run_id, "t": start, "type": "load"}]
    for i, target in enumerate(targets):
        lines.append({"run": run_id, "t": start + 2 * i + 1, "type": "pointerdown", "target": target})
        lines.append({"run": run_id, "t": start + 2 * i + 2, "type": "click", "target": target})
    return "\n".join(json.dumps(line) for line in lines) + "\n"


def verdict(task, targets):
    return run.check_task(TASKS[task], run.parse_click_log(click_log(targets)))


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
        {"t": 6.0, "seconds": 0.1, "args": ["bogus"], "exit": 2},
        {"t": 7.0, "seconds": 0.2, "args": ["close", "-d", "bench"], "exit": 0},
    ]) + "\n\n"

    def test_counts_commands_looks_and_failures(self):
        stats = run.tb_stats(run.parse_tb_log(self.LOG))
        self.assertEqual((stats["commands"], stats["looks"], stats["failed"]), (7, 2, 2))
        self.assertTrue(stats["closed_session"])

    def test_empty_log(self):
        stats = run.tb_stats(run.parse_tb_log(""))
        self.assertEqual((stats["commands"], stats["looks"], stats["failed"], stats["closed_session"]), (0, 0, 0, False))

    def test_failed_close_is_not_closed(self):
        entries = [{"args": ["close", "-d", "bench"], "exit": 1}]
        self.assertFalse(run.tb_stats(entries)["closed_session"])


class ClickChecks(unittest.TestCase):
    def test_journey_passes(self):
        self.assertTrue(verdict("journey", JOURNEY)["passed"])

    def test_journey_fails_without_logout(self):
        got = verdict("journey", JOURNEY[:-1])
        self.assertFalse(got["passed"])
        self.assertIn("order", got["failed_checks"][0])

    def test_journey_fails_with_a_third_add(self):
        targets = JOURNEY[:6] + ["add_to_cart"] + JOURNEY[6:]
        self.assertEqual(verdict("journey", targets)["failed_checks"], [{"count": "add_to_cart", "is": 2}])

    def test_journey_counts_taps_since_live_was_last_opened(self):
        targets = JOURNEY[:9] + ["tap_me", "back", "open_live"] + JOURNEY[9:]
        self.assertTrue(verdict("journey", targets)["passed"])

    def test_journey_fails_when_notifications_end_off(self):
        targets = JOURNEY[:13] + ["notify_switch"] + JOURNEY[13:]
        self.assertEqual(verdict("journey", targets)["failed_checks"], [{"on": "notify_switch"}])

    def test_controls_passes(self):
        self.assertTrue(verdict("controls", CONTROLS)["passed"])

    def test_controls_overshoot_and_step_back(self):
        self.assertTrue(verdict("controls", CONTROLS + ["stepper_plus", "stepper_minus", "star_5", "star_4"])["passed"])

    def test_controls_wrong_final_choice(self):
        got = verdict("controls", CONTROLS + ["seg_s", "chip_cheap"])
        self.assertEqual(got["failed_checks"], [{"last": "seg_.*", "is": "seg_l"}, {"off": "chip_cheap"}])

    def test_controls_reset_when_the_scenario_is_opened_again(self):
        self.assertFalse(verdict("controls", CONTROLS + ["back", "scenario_controls"])["passed"])

    def test_lists_passes(self):
        self.assertTrue(verdict("lists", LISTS)["passed"])

    def test_lists_ignores_taps_on_the_mail_panes(self):
        self.assertTrue(verdict("lists", LISTS + ["mail_body", "mail_list"])["passed"])

    def test_lists_fails_on_the_wrong_row(self):
        targets = [("row_61" if t == "row_60" else t) for t in LISTS]
        self.assertEqual(verdict("lists", targets)["failed_checks"], [{"last": "row_\\d+", "is": "row_60"}])

    def test_only_the_last_page_load_counts(self):
        events = run.parse_click_log(click_log(JOURNEY, "a") + click_log(["login"], "b"))
        self.assertFalse(run.check_task(TASKS["journey"], events)["passed"])
        events = run.parse_click_log(click_log(["login"], "a") + click_log(JOURNEY, "b"))
        self.assertTrue(run.check_task(TASKS["journey"], events)["passed"])

    def test_no_clicks_fails(self):
        self.assertFalse(run.check_task(TASKS["journey"], [])["passed"])
        self.assertFalse(run.check_task(TASKS["controls"], [])["passed"])

    def test_pointerdowns_alone_do_not_count(self):
        events = [e for e in run.parse_click_log(click_log(JOURNEY)) if e["type"] != "click"]
        self.assertFalse(run.check_task(TASKS["journey"], events)["passed"])

    def test_unknown_rule(self):
        with self.assertRaises(ValueError):
            run.check_one({"sometimes": "x"}, [])


class Report(unittest.TestCase):
    def test_reported(self):
        answer = "Rating: 4.8 stars. Cart: 2. Counter: tapped 3. Switch: Notifications on."
        self.assertTrue(run.reported(TASKS["journey"], answer))
        self.assertFalse(run.reported(TASKS["journey"], answer.replace("4.8", "4.5")))
        self.assertFalse(run.reported(TASKS["journey"], None))


class AgentResult(unittest.TestCase):
    def test_reads_the_result(self):
        stats = run.agent_stats({
            "num_turns": 12, "total_cost_usd": 0.31, "duration_ms": 65400, "is_error": False, "result": "done",
            "usage": {"input_tokens": 40, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 9000, "output_tokens": 700},
        })
        self.assertEqual((stats["turns"], stats["input_tokens"], stats["output_tokens"]), (12, 10040, 700))
        self.assertEqual((stats["cost_usd"], stats["seconds"], stats["agent_error"]), (0.31, 65.4, False))

    def test_failed_agent(self):
        stats = run.agent_stats({"is_error": True, "result": "timed out after 5 s"})
        self.assertEqual((stats["turns"], stats["input_tokens"], stats["agent_error"]), (0, 0, True))


def fake_run(task, commands, passed=True, **over):
    base = {"task": task, "commands": commands, "looks": 2, "failed": 1, "turns": 10, "input_tokens": 1000,
            "output_tokens": 100, "cost_usd": 0.5, "seconds": 30.0, "passed": passed, "reported": passed}
    base.update(over)
    return base


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
        self.assertEqual(rows["reported correctly"], "2/3")

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


class Wrapper(unittest.TestCase):
    def test_wrapper_logs_and_passes_the_exit_code(self):
        import subprocess
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            fake_tb = os.path.join(d, "fake-tb")
            with open(fake_tb, "w") as f:
                f.write('#!/bin/sh\necho "home=$TB_HOME args=$*"\n[ "$1" = look ]\n')
            os.chmod(fake_tb, 0o755)
            wrapper, log = os.path.join(d, "tb"), os.path.join(d, "tb.jsonl")
            with open(wrapper, "w") as f:
                f.write(run.WRAPPER.format(python=sys.executable, home=os.path.join(d, "home"), tb=fake_tb, log=log))
            os.chmod(wrapper, 0o755)
            ok = subprocess.run([wrapper, "look", "-d", "bench"], capture_output=True, text=True)
            bad = subprocess.run([wrapper, "do", "tapOn: \"a\tb\"\ntapOn: c"], capture_output=True, text=True)
            self.assertEqual((ok.returncode, bad.returncode), (0, 1))
            self.assertIn("home=" + os.path.join(d, "home"), ok.stdout)
            entries = run.parse_tb_log(open(log).read())
            self.assertEqual([e["args"] for e in entries], [["look", "-d", "bench"], ["do", "tapOn: \"a\tb\"\ntapOn: c"]])
            self.assertEqual([e["exit"] for e in entries], [0, 1])


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


if __name__ == "__main__":
    unittest.main()
