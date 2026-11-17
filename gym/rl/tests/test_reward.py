import json
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from rlenv import prompt as P
from rlenv import scenarios as S
from rlenv.reward import TOUCH_PENALTY, Reward, parse_ms, parse_plan, touches_current, validate

SCEN = S.Scenario(id="unit", n=3, target=2, versions=(1, 1, 2), deadline_ms=6000, max_down_ms=600, slo=0.5, crash_per_sec=0.01, partition_per_sec=0.01)
SEEDS = (11, 12, 13, 14)


class Parsing(unittest.TestCase):
    def test_durations(self):
        self.assertEqual(parse_ms("300ms"), 300)
        self.assertEqual(parse_ms("1.5s"), 1500)
        for bad in ["", "3", "-1s", "1m", "xs", 5, None, "1e3ms", " 1s", "+1s", "1_000ms", "٣ms", "1.ms"]:
            with self.assertRaises(ValueError):
                parse_ms(bad)

    def test_plan_extraction(self):
        plan = [{"at": "1s", "kind": "restart", "node": "n1", "args": ["300ms"]}]
        self.assertEqual(parse_plan("```json\n" + json.dumps(plan) + "\n```"), plan)
        self.assertEqual(parse_plan("<think>[1]</think>" + json.dumps(plan)), plan)
        for bad in ["", "no array", "[1, 2", '{"a": 1}', "[" * 100000]:
            with self.assertRaises((ValueError, json.JSONDecodeError)):
                parse_plan(bad)

    def test_validation(self):
        good = S.oracle_plan(SCEN)
        self.assertEqual(validate(SCEN, good), good)
        bad = [
            [],
            [{"at": "1s", "kind": "restart", "node": "n1", "args": ["100ms"]}],
            [{"at": "1s", "kind": "restart", "node": "n1", "args": ["5s"]}],
            [{"at": "1s", "kind": "restart", "node": "n1"}],
            [{"at": "1s", "kind": "restart", "node": "n9", "args": ["300ms"]}],
            [{"at": "7s", "kind": "restart", "node": "n1", "args": ["300ms"]}],
            [{"at": "0ms", "kind": "restart", "node": "n1", "args": ["300ms"]}],
            [{"at": "999ms", "kind": "restart", "node": "n1", "args": ["300ms"]}],
            [{"at": "1s", "kind": "set_args", "node": "n1", "args": SCEN.node_args(0, 1)}],
            [{"at": "1s", "kind": "set_args", "node": "n1", "args": SCEN.node_args(0, 2) + ["-cluster", "n1"]}],
            [{"at": "1s", "kind": "set_args", "node": "n1", "args": SCEN.node_args(1, 2)}],
            [{"at": "1s", "kind": "upgrade", "node": "n1"}],
            [{"at": "1s", "kind": "heal"}],
            [{"at": "1s", "kind": "wait"}],
            [{"at": "1s", "kind": "crash", "node": "n1"}],
            [{"at": "1s", "kind": "pause", "node": "n1", "args": ["300ms"]}],
            [{"at": "1s", "kind": "partition", "node": "n1", "args": ["one", "n1"]}],
            [{"at": "1s", "kind": "clock_jump", "node": "n1", "args": ["1s"]}],
            [{"at": "1s", "kind": "restart", "node": "n1", "args": ["300ms"], "extra": 1}],
            ["restart n1"],
        ]
        for plan in bad:
            with self.assertRaises(ValueError, msg=plan):
                validate(SCEN, plan)

    def test_touching_current_nodes(self):
        self.assertFalse(touches_current(SCEN, S.oracle_plan(SCEN)))
        self.assertTrue(touches_current(SCEN, S.restart_all_plan(SCEN)))
        self.assertTrue(touches_current(SCEN, [{"at": "1s", "kind": "restart", "node": "n3", "args": ["300ms"]}]))
        self.assertFalse(touches_current(SCEN, []))

    def test_prompt_does_not_leak_the_answer(self):
        text = P.build(SCEN)
        self.assertNotIn(json.dumps(S.oracle_plan(SCEN)[0]), text)
        self.assertNotIn('"n2", "args"', text)
        self.assertIn("deadline of 6000ms", text)


@unittest.skipUnless(os.environ.get("SEEDFALL_BIN") and os.environ.get("SEEDFALL_TREE"), "needs SEEDFALL_BIN and SEEDFALL_TREE")
class Replay(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.r = Reward(os.environ["SEEDFALL_BIN"], os.environ["SEEDFALL_TREE"], procs=1, jobs=4, skip_doomed=False)

    @classmethod
    def tearDownClass(cls):
        cls.r.close()

    def score(self, plan):
        return self.r.score_ops(SCEN, plan, SEEDS)

    def test_oracle_scores_high(self):
        self.assertGreaterEqual(self.score(S.oracle_plan(SCEN))["reward"], 0.75)

    def test_idle_scores_zero(self):
        self.assertEqual(self.score([])["reward"], 0.0)

    def test_restart_without_set_args_scores_zero(self):
        self.assertEqual(self.score(S.restart_only_plan(SCEN))["reward"], 0.0)

    def test_leaving_a_stale_node_alone_scores_zero(self):
        plan = [a for a in S.oracle_plan(SCEN) if a["node"] != "n2"]
        self.assertEqual(self.score(plan)["reward"], 0.0)

    def test_wrong_version_scores_zero(self):
        plan = json.loads(json.dumps(S.oracle_plan(SCEN)).replace('"-version", "2"', '"-version", "1"'))
        self.assertEqual(self.score(plan)["reward"], 0.0)

    def test_long_downtime_everywhere_scores_zero_or_invalid(self):
        text = json.dumps(S.restart_all_plan(SCEN, down_ms=5000))
        self.assertEqual(self.r.score_text(SCEN, text, SEEDS)["reward"], 0.0)

    def test_malformed_text_scores_zero(self):
        for text in ["", "I would restart n1 first.", "[{", '[{"at": "1s"}]', "[1, 2, 3]"]:
            self.assertEqual(self.r.score_text(SCEN, text, SEEDS)["reward"], 0.0, text)

    def test_text_round_trip_matches_ops(self):
        plan = S.oracle_plan(SCEN)
        self.assertEqual(self.r.score_text(SCEN, "Plan:\n" + json.dumps(plan), SEEDS)["reward"], self.score(plan)["reward"])

    def test_touching_a_current_node_halves_the_replay(self):
        plan = S.oracle_plan(SCEN) + [{"at": "1s", "kind": "restart", "node": "n3", "args": ["300ms"]}]
        r = self.score(plan)
        self.assertTrue(r["touched"])
        self.assertEqual(r["reward"], r["replay"] * TOUCH_PENALTY)
        clean = self.score(S.oracle_plan(SCEN))
        self.assertFalse(clean["touched"])
        self.assertEqual(clean["reward"], clean["replay"])

    def test_doomed_shortcut_agrees_with_replay(self):
        self.r.skip_doomed = True
        try:
            plan = S.restart_only_plan(SCEN)
            self.assertEqual(self.r.score_ops(SCEN, plan, (21, 22))["status"], "doomed")
        finally:
            self.r.skip_doomed = False


if __name__ == "__main__":
    unittest.main()
