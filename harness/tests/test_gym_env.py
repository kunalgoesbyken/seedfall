import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

import pytest

from harness.client import Completion
from harness.episode import EpisodeConfig, run_episode
from harness.gym_env import (
    MAX_RUN_CALLS,
    MAX_SMALL_SEED,
    ENV_ALLOWLIST,
    TASKS_DIR,
    GradeConfig,
    GymEnv,
    load_salt,
    parse_seeds,
    scrubbed_env,
    trace_tail,
)
from harness.tests.fake_grade import HIDDEN
from harness.trajectory import TrajectoryLog

FAKE = Path(__file__).with_name("fake_grade.py")
SALT = "unit-test-salt-value"
SHIPPED = [
    "aikv-haiku-1-tail", "httpkv-nofsync", "httpkv-nofsync-heavy", "kvlog-ack-before-fsync", "kvlog-fsyncgate", "kvlog-no-checksum",
    "kvlog-torn-tail", "ledger-nofsync", "ledger-nofsync-heavy", "liveness-dedupe", "liveness-redial", "symswap-dir-fsync",
]
TASK_IDS = [task for task in SHIPPED if (TASKS_DIR / task / "task.json").is_file()]


def fake_cfg(**overrides):
    return GradeConfig((sys.executable, str(FAKE)), salt=SALT, **overrides)


@pytest.fixture
def env():
    made = GymEnv("kvlog-torn-tail", 0, fake_cfg())
    yield made
    made.close()


@pytest.fixture
def fake_log(env, tmp_path):
    path = tmp_path / "fake.log"
    env.cfg.extra_env["FAKE_LOG"] = str(path)
    return path


def calls(path):
    return [json.loads(line) for line in path.read_text().splitlines()] if path.is_file() else []


def test_the_twelve_shipped_tasks_are_present():
    assert TASK_IDS == SHIPPED


def test_env_matches_the_harness_protocol(env):
    names = [schema["function"]["name"] for schema in env.tools.schemas()]
    assert names == ["list_files", "read_file", "write_file", "edit_file", "run_seeds", "submit"]
    assert isinstance(env.system_prompt, str) and isinstance(env.task_prompt, str)
    assert callable(env.reward) and callable(env.close)


@pytest.mark.parametrize("task_id", TASK_IDS)
def test_prompts_use_the_symptom_and_do_not_leak_the_fix(task_id):
    spec = json.loads((TASKS_DIR / task_id / "task.json").read_text(encoding="utf-8"))
    made = GymEnv(task_id, 0, fake_cfg())
    try:
        prompt = made.system_prompt + made.task_prompt
        assert spec["symptom"] in made.task_prompt
        assert spec["bug_class"] not in prompt
        patch = (TASKS_DIR / task_id / "solution" / "fix.patch").read_text(encoding="utf-8")
        added = {line[1:].strip() for line in patch.splitlines() if line.startswith("+") and not line.startswith("+++")}
        assert [line for line in added if len(line) > 25 and line in prompt] == []
    finally:
        made.close()


def test_each_episode_gets_a_fresh_copy_and_the_task_dir_is_untouched(env):
    other = GymEnv("kvlog-torn-tail", 1, fake_cfg())
    try:
        assert env.root != other.root
        pristine = TASKS_DIR / "kvlog-torn-tail" / "workspace" / "server" / "main.go"
        original = pristine.read_text(encoding="utf-8")
        env.tools.call("write_file", {"path": "server/main.go", "content": "package main\n"})
        assert (other.root / "server" / "main.go").read_text(encoding="utf-8") == original
        assert pristine.read_text(encoding="utf-8") == original
        with pytest.raises(ValueError):
            env.tools.call("write_file", {"path": "../escape.txt", "content": "x"})
    finally:
        other.close()
    root = env.root
    env.close()
    assert not root.exists()


@pytest.mark.parametrize(
    "raw,expected",
    [("1", [1]), ("1, 2,3", [1, 2, 3]), ([1, "2"], [1, 2]), (5, [5]), ("[4,4]", [4]), ("0", [0]), (str(MAX_SMALL_SEED), [MAX_SMALL_SEED])],
)
def test_parse_seeds_accepts_visible_and_small_integers(raw, expected):
    assert parse_seeds(raw, visible=1) == expected


@pytest.mark.parametrize("raw", ["", "1,2,3,4,5", "-1", "1.5", "abc", "1001", str(HIDDEN[0]), "18446744073709551615", "1,,x", None])
def test_parse_seeds_rejects_everything_else(raw):
    with pytest.raises(ValueError):
        parse_seeds(raw, visible=1)


def test_visible_seed_file_is_allowed_even_above_the_small_range():
    assert parse_seeds("5000", visible=5000) == [5000]


def test_trace_tail_drops_ack_noise_and_truncates():
    lines = [f"l{i}" for i in range(50)] + ['    1.8s  event gym.ack from client#1: "k1"', "z" * 1000]
    tail = trace_tail(lines)
    assert len(tail) == 20 and all("gym.ack" not in line for line in tail)
    assert len(tail[-1]) == 200


def test_run_seeds_reports_pass_fail_reason_and_a_truncated_trace(env, fake_log):
    text = env.tools.call("run_seeds", {"seeds": "1,2"}).text
    assert "seed 1: FAIL [assert]" in text and "seed 2: PASS" in text
    assert "trace tail:" in text and "gym.ack" not in text
    assert len(text) < 8000
    argv = calls(fake_log)[0]["argv"]
    assert argv[argv.index("-seeds") + 1] == "1,2"
    assert str(env.root) in argv


def test_run_seeds_rejects_bad_seeds_without_running_grade(env, fake_log):
    for bad in ("1,2,3,4,5", "99999", str(HIDDEN[0]), "x"):
        assert env.tools.call("run_seeds", {"seeds": bad}).text.startswith("error")
    assert calls(fake_log) == []


def test_run_seeds_call_budget(env, fake_log):
    for _ in range(MAX_RUN_CALLS):
        assert not env.tools.call("run_seeds", {"seeds": "1"}).text.startswith("error")
    assert "limited" in env.tools.call("run_seeds", {"seeds": "1"}).text
    assert len(calls(fake_log)) == MAX_RUN_CALLS


def test_run_seeds_survives_a_grade_crash(env):
    env.cfg.extra_env["FAKE_MODE"] = "crash"
    assert "boom" in env.tools.call("run_seeds", {"seeds": "1"}).text


def test_hidden_seeds_and_salt_never_reach_the_agent(env, fake_log, monkeypatch):
    monkeypatch.setenv("SEEDFALL_GYM_SALT", "ambient-salt-from-the-parent-shell")
    monkeypatch.setenv("GEMINI_API_KEY", "ambient-api-key")
    seen = [env.system_prompt, env.task_prompt, env.tools.call("run_seeds", {"seeds": "1,2,3,4"}).text]
    seen += [env.tools.call("list_files", {}).text, env.tools.call("submit", {}).text]
    assert env.reward() == 0.5
    blob = "\n".join(seen)
    files = [path.read_text(encoding="utf-8", errors="ignore") for path in env.root.rglob("*") if path.is_file()]
    for secret in (SALT, "ambient-salt-from-the-parent-shell", "ambient-api-key", *map(str, HIDDEN)):
        assert secret not in blob
        assert all(secret not in text for text in files)
    visible, hidden = calls(fake_log)
    assert not visible["salt_in_env"] and not visible["key_in_env"]
    assert hidden["salt_in_env"] and not hidden["key_in_env"]
    assert "-seeds" not in hidden["argv"] and SALT not in " ".join(hidden["argv"])


def test_a_hanging_grade_times_out_instead_of_blocking():
    made = GymEnv("kvlog-torn-tail", 0, fake_cfg(run_timeout_s=1.0, grade_timeout_s=1.0, extra_env={"FAKE_MODE": "hang"}))
    try:
        started = time.monotonic()
        text = made.tools.call("run_seeds", {"seeds": "1"}).text
        assert "did not finish" in text and text.startswith("error")
        assert made.reward() == 0.0
        assert time.monotonic() - started < 30
    finally:
        made.close()


def test_a_broken_grader_is_an_error_not_a_zero():
    made = GymEnv("kvlog-torn-tail", 0, fake_cfg(extra_env={"FAKE_MODE": "crash"}))
    try:
        with pytest.raises(Exception, match="boom"):
            made.reward()
    finally:
        made.close()


def test_salt_is_generated_once_and_stable(tmp_path):
    path = tmp_path / "cfg" / "salt"
    first = load_salt(path)
    assert re.fullmatch(r"[0-9a-f]{64}", first)
    assert load_salt(path) == first


class SubmitClient:
    def complete(self, messages, tools=None, **kwargs):
        call = {"id": "c1", "type": "function", "function": {"name": "submit", "arguments": "{}"}}
        usage = {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
        return Completion({"role": "assistant", "content": None, "tool_calls": [call]}, usage, 0.0, "stop", 1)


def test_an_episode_through_the_runner_scores_the_workspace(tmp_path):
    made = GymEnv("kvlog-torn-tail", 0, fake_cfg())
    log = TrajectoryLog(tmp_path / "e.jsonl")
    meta = {"episode_id": "e", "env": "x", "task": "t", "model": "m", "preset": "p"}
    try:
        result = run_episode(made, SubmitClient(), EpisodeConfig(), log, meta)
    finally:
        log.close()
        made.close()
    assert result.status == "finished" and result.reward == 0.5 and result.claimed_success


def test_child_environment_is_an_allowlist(monkeypatch):
    monkeypatch.setenv("SEEDFALL_GYM_SALT", "s3cret")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "k")
    monkeypatch.setenv("SOME_OTHER_TOKEN", "t")
    monkeypatch.setenv("GOFLAGS", "-mod=mod")
    monkeypatch.setenv("GOTOOLCHAIN", "auto")
    child = scrubbed_env({"EXTRA": "1"})
    assert set(child) <= set(ENV_ALLOWLIST) | {"SEEDFALL_SRC", "GOTOOLCHAIN", "EXTRA"}
    assert child["GOTOOLCHAIN"] == "local" and child["EXTRA"] == "1" and "GOFLAGS" not in child
    assert "PATH" in child


def test_run_seeds_output_is_redacted_if_the_salt_ever_appears(env):
    env.cfg.extra_env["FAKE_MODE"] = "echo_salt"
    env.cfg.extra_env["FAKE_ECHO"] = SALT
    text = env.tools.call("run_seeds", {"seeds": "1"}).text
    assert SALT not in text and "***" in text


ESCAPES = ["..", "../x.txt", "server/../../x.txt", r"C:\Windows\win.ini", "C:/Windows/win.ini", "/etc/passwd", r"\\host\share\x"]


@pytest.mark.parametrize("path", ESCAPES)
def test_file_tools_reject_paths_outside_the_workspace(env, path, tmp_path):
    for name, args in (
        ("read_file", {"path": path}),
        ("write_file", {"path": path, "content": "x"}),
        ("edit_file", {"path": path, "old": "a", "new": "b"}),
        ("list_files", {"path": path}),
    ):
        try:
            outcome = env.tools.call(name, args)
        except (ValueError, OSError):
            continue
        assert outcome.text.startswith("error"), (name, path, outcome.text)


def test_file_tools_reject_symlinks_that_leave_the_workspace(env, tmp_path):
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "secret.txt").write_text("outside", encoding="utf-8")
    link = env.root / "link"
    try:
        link.symlink_to(outside, target_is_directory=True)
    except OSError:
        made = subprocess.run(["cmd", "/c", "mklink", "/J", str(link), str(outside)], capture_output=True) if os.name == "nt" else None
        if made is None or made.returncode != 0:
            pytest.skip("cannot create a symlink or junction on this machine")
    for name, args in (("read_file", {"path": "link/secret.txt"}), ("write_file", {"path": "link/new.txt", "content": "x"}), ("list_files", {"path": "link"})):
        with pytest.raises(ValueError):
            env.tools.call(name, args)
    assert not (outside / "new.txt").exists()


@pytest.mark.parametrize("task_id", TASK_IDS)
def test_workspace_holds_only_the_workspace_files(task_id):
    made = GymEnv(task_id, 0, fake_cfg())
    try:
        pristine = TASKS_DIR / task_id / "workspace"
        expected = sorted(p.relative_to(pristine).as_posix() for p in pristine.rglob("*") if p.is_file())
        actual = sorted(p.relative_to(made.root).as_posix() for p in made.root.rglob("*") if p.is_file())
        assert actual == expected
        assert not {"solution", "tasks", "harness", "cheats", "fix.patch"} & {part for name in actual for part in name.split("/")}
    finally:
        made.close()
