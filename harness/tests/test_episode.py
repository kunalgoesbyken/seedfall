import json

import pytest

from harness.episode import EpisodeConfig, run_episode
from harness.fake_env import ReadFileEnv
from harness.trajectory import TrajectoryLog, read_events


@pytest.fixture
def env():
    env = ReadFileEnv("task", 0)
    yield env
    env.close()


def play(env, client, tmp_path, cfg=None, clock=None):
    log = TrajectoryLog(tmp_path / "ep.jsonl", log_chars=40)
    kwargs = {"clock": clock} if clock else {}
    try:
        result = run_episode(env, client, cfg or EpisodeConfig(), log, {"episode_id": "ep-1", "model": "m", "env": "e", "task": "t"}, **kwargs)
    finally:
        log.close()
    return result, read_events(tmp_path / "ep.jsonl")


def test_success_path_logs_everything(server, make_client, env, tmp_path):
    server.push_call("read_file", {"path": "secret.txt"}, "c1", tokens=20)
    server.push_call("submit", {"content": env.secret}, "c2", tokens=30)
    result, events = play(env, make_client(), tmp_path)
    assert (result.status, result.reward, result.turns, result.tokens, result.claimed_success) == ("finished", 1.0, 2, 50, True)
    kinds = [e["type"] for e in events]
    assert kinds[0] == "meta" and kinds[-1] == "final"
    assert kinds.count("tool_result") == 2
    assistants = [e for e in events if e["type"] == "message" and e["role"] == "assistant"]
    assert assistants[0]["usage"]["total_tokens"] == 20 and "latency_s" in assistants[0]
    assert events[0]["tools"] == ["list_files", "read_file", "write_file", "edit_file", "submit"]
    assert events[-1]["reward"] == 1.0


def test_tool_results_are_truncated_in_log_but_not_in_context(server, make_client, env, tmp_path):
    env.tools.call("write_file", {"path": "big.txt", "content": "z" * 500})
    server.push_call("read_file", {"path": "big.txt"}, "c1")
    server.push_call("submit", {"content": "x"}, "c2")
    result, events = play(env, make_client(), tmp_path)
    logged = next(e for e in events if e["type"] == "tool_result" and e["name"] == "read_file")
    assert logged["truncated"] and len(logged["text"]) == 40 and logged["full_chars"] == 500
    sent = server.requests[1]["messages"][-1]
    assert sent["role"] == "tool" and len(sent["content"]) == 500


def test_wrong_submission_with_claim_scores_zero(server, make_client, env, tmp_path):
    server.push_call("submit", {"content": "wrong"})
    result, _ = play(env, make_client(), tmp_path)
    assert result.reward == 0.0 and result.claimed_success


def test_tool_errors_are_returned_to_the_model(server, make_client, env, tmp_path):
    server.push_call("read_file", {"path": "../x"}, "c1")
    server.push_call("missing_tool", {}, "c2")
    server.push_call("submit", {"content": env.secret}, "c3")
    result, events = play(env, make_client(), tmp_path)
    errors = [e["text"] for e in events if e["type"] == "tool_result"]
    assert "ValueError" in errors[0] and "unknown tool" in errors[1]
    assert result.reward == 1.0


def test_invalid_json_arguments_become_a_tool_error(server, make_client, env, tmp_path):
    call = {"id": "c1", "type": "function", "function": {"name": "read_file", "arguments": "{oops"}}
    server.push({"role": "assistant", "content": None, "tool_calls": [call]})
    server.push_call("submit", {"content": env.secret})
    _, events = play(env, make_client(), tmp_path)
    assert "not valid JSON" in next(e for e in events if e["type"] == "tool_result")["text"]


def test_no_tool_call_is_nudged_once_then_ends(server, make_client, env, tmp_path):
    server.push_text("I think I am done.")
    server.push_text("Still done.")
    result, events = play(env, make_client(), tmp_path)
    assert result.status == "no_tool_call" and result.turns == 2 and len(server.requests) == 2
    assert server.requests[1]["messages"][-1]["role"] == "user"
    assert result.reward == 0.0 and result.claimed_success


def test_nudge_recovers_when_model_then_calls_a_tool(server, make_client, env, tmp_path):
    server.push_text("hmm")
    server.push_call("submit", {"content": env.secret})
    result, _ = play(env, make_client(), tmp_path)
    assert result.status == "finished" and result.reward == 1.0


def test_thought_tags_are_stripped_before_replay(server, make_client, env, tmp_path):
    server.push({"role": "assistant", "content": "<thought>secret plan</thought>", "tool_calls": [
        {"id": "c1", "type": "function", "function": {"name": "read_file", "arguments": json.dumps({"path": "secret.txt"})}}]})
    server.push_call("submit", {"content": env.secret})
    result, events = play(env, make_client(), tmp_path)
    replayed = server.requests[1]["messages"]
    assert all("secret plan" not in str(m) for m in replayed)
    assert next(e for e in events if e["type"] == "message" and e["role"] == "assistant")["thoughts_stripped"]


def test_text_tool_calls_work_in_native_mode(server, make_client, env, tmp_path):
    body = json.dumps({"name": "submit", "arguments": {"content": env.secret}})
    server.push_text(f"<thought>x</thought><tool_call>{body}</tool_call>")
    result, _ = play(env, make_client(), tmp_path)
    assert result.status == "finished" and result.reward == 1.0
    assert server.requests[0]["tools"]


def test_text_mode_sends_no_tools_and_replies_as_user(server, make_client, env, tmp_path):
    read = json.dumps({"name": "read_file", "arguments": {"path": "secret.txt"}})
    submit = json.dumps({"name": "submit", "arguments": {"content": env.secret}})
    server.push_text(f"<tool_call>{read}</tool_call>")
    server.push_text(f"<tool_call>{submit}</tool_call>")
    result, _ = play(env, make_client(), tmp_path, EpisodeConfig(tool_mode="text"))
    assert result.reward == 1.0
    assert "tools" not in server.requests[0]
    assert "read_file" in server.requests[0]["messages"][0]["content"]
    assert server.requests[1]["messages"][-1]["role"] == "user"
    assert "<tool_result" in server.requests[1]["messages"][-1]["content"]


def test_max_turns(server, make_client, env, tmp_path):
    for _ in range(5):
        server.push_call("list_files", {})
    result, _ = play(env, make_client(), tmp_path, EpisodeConfig(max_turns=3))
    assert result.status == "max_turns" and result.turns == 3 and result.reward == 0.0


def test_max_total_tokens(server, make_client, env, tmp_path):
    for _ in range(5):
        server.push_call("list_files", {}, tokens=100)
    result, _ = play(env, make_client(), tmp_path, EpisodeConfig(max_total_tokens=250))
    assert result.status == "max_tokens" and result.turns == 3


def test_wall_clock_timeout(server, make_client, env, tmp_path):
    ticks = iter(range(0, 1000, 40))
    for _ in range(5):
        server.push_call("list_files", {})
    result, _ = play(env, make_client(), tmp_path, EpisodeConfig(timeout_s=100), clock=lambda: next(ticks))
    assert result.status == "timeout" and result.turns < 5


def test_budget_exhaustion_stops_cleanly_without_reward(server, make_client, env, tmp_path):
    server.push_call("list_files", {})
    result, events = play(env, make_client(daily=1), tmp_path)
    assert result.status == "budget_exhausted" and result.reward is None
    assert events[-1]["type"] == "final" and events[-1]["status"] == "budget_exhausted"


def test_unexpected_exception_is_an_error_episode(server, make_client, env, tmp_path):
    server.push(status=400)
    result, _ = play(env, make_client(), tmp_path)
    assert result.status == "error" and result.reward is None and "BadRequestError" in result.error
