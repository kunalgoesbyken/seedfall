import json
from pathlib import Path

import pytest

from harness.gym_env import TASKS_DIR, GradeConfig, GymEnv, grade_binary, run_bounded, scrubbed_env, seedfall_src
from harness.reference import run_reference
from harness.report import aggregate, load_episodes

TASK = "kvlog-torn-tail"
SALT = "integration-test-salt"

pytestmark = pytest.mark.skipif(not grade_binary().is_file() or seedfall_src() is None, reason="needs bin/grade and the seedfall checkout")


def factory(task_id, episode):
    return GymEnv(task_id, episode, GradeConfig((str(grade_binary()),), salt=SALT))


def direct_reward(workspace: Path) -> float:
    command = [str(grade_binary()), "-task", str(TASKS_DIR / TASK), "-workspace", str(workspace)]
    _, out, _ = run_bounded(command, scrubbed_env({"SEEDFALL_GYM_SALT": SALT}), 600)
    return json.loads(out)["reward"]


def test_null_gives_the_buggy_reward_and_oracle_gives_one(tmp_path):
    null = run_reference("null", TASK, tmp_path, factory)
    oracle = run_reference("oracle", TASK, tmp_path, factory)
    assert null.reward == direct_reward(TASKS_DIR / TASK / "workspace")
    assert 0.0 < null.reward < 0.5
    assert oracle.reward == 1.0
    rows = {row["model"]: row for row in aggregate(load_episodes(tmp_path), by_task=False)}
    assert rows["null"]["pass@1"] == 0.0 and rows["oracle"]["pass@1"] == 1.0


def test_run_seeds_on_the_real_grader_shows_the_failure_and_the_fix(tmp_path):
    buggy = factory(TASK, 0)
    fixed = factory(TASK, 1)
    try:
        from harness.reference import apply_solution

        apply_solution(TASK, fixed.root)
        failing = buggy.tools.call("run_seeds", {"seeds": "1"}).text
        passing = fixed.tools.call("run_seeds", {"seeds": "1"}).text
    finally:
        buggy.close()
        fixed.close()
    assert "seed 1: FAIL" in failing and "acknowledged write lost" in failing and "trace tail:" in failing
    assert "seed 1: PASS" in passing


def test_run_seeds_reports_a_build_error(tmp_path):
    made = factory(TASK, 0)
    try:
        made.tools.call("edit_file", {"path": "server/main.go", "old": "package main", "new": "package main\nfunc ("})
        assert "build failed" in made.tools.call("run_seeds", {"seeds": "1"}).text
    finally:
        made.close()
