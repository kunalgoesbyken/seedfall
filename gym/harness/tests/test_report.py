import json

from harness.report import aggregate, load_episodes, main, markdown
from harness.trajectory import TrajectoryLog


def write_episode(path, *, model="m1", task="t", reward=1.0, status="finished", tool_claim=False, final_text="", turns=2, tokens=10):
    log = TrajectoryLog(path)
    log.write("meta", episode_id=path.stem, model=model, env="e:F", task=task)
    log.message({"role": "system", "content": "s"})
    for turn in range(turns):
        last = turn == turns - 1
        log.message({"role": "assistant", "content": final_text if last else None, "turn": turn + 1, "usage": {"total_tokens": tokens}})
    if tool_claim is not None:
        log.write("tool_result", turn=turns, name="submit", text="", finished=True, claimed_success=tool_claim)
    log.write("final", status=status, reward=reward)
    log.close()


def test_aggregates_pass_reward_turns_tokens_and_false_claims(tmp_path):
    write_episode(tmp_path / "a.jsonl", reward=1.0, tool_claim=True)
    write_episode(tmp_path / "b.jsonl", reward=0.0, tool_claim=True, turns=4)
    write_episode(tmp_path / "c.jsonl", reward=0.5, tool_claim=False, final_text="Everything is fixed now")
    write_episode(tmp_path / "d.jsonl", reward=0.0, tool_claim=False, final_text="I give up")
    [row] = aggregate(load_episodes(tmp_path), by_task=False)
    assert row["episodes"] == 4
    assert row["pass@1"] == 0.25
    assert row["mean_reward"] == 0.375
    assert row["mean_turns"] == 2.5
    assert row["mean_tokens"] == 25
    assert row["false_claim_rate"] == 0.5


def test_groups_by_model_and_optionally_task(tmp_path):
    write_episode(tmp_path / "a.jsonl", model="m1", task="x")
    write_episode(tmp_path / "b.jsonl", model="m1", task="y", reward=0.0)
    write_episode(tmp_path / "c.jsonl", model="m2", task="x")
    assert [(r["model"], r["episodes"]) for r in aggregate(load_episodes(tmp_path), False)] == [("m1", 2), ("m2", 1)]
    assert len(aggregate(load_episodes(tmp_path), True)) == 3


def test_incomplete_and_error_episodes_are_excluded_from_metrics(tmp_path):
    write_episode(tmp_path / "a.jsonl", reward=1.0, tool_claim=True)
    write_episode(tmp_path / "b.jsonl", reward=None, status="budget_exhausted", tool_claim=None)
    write_episode(tmp_path / "c.jsonl", reward=None, status="error", tool_claim=None)
    [row] = aggregate(load_episodes(tmp_path), False)
    assert (row["episodes"], row["incomplete"], row["errors"], row["pass@1"]) == (1, 1, 1, 1.0)


def test_logs_without_final_event_are_ignored(tmp_path):
    (tmp_path / "partial.jsonl").write_text(json.dumps({"type": "meta", "model": "m", "env": "e", "task": "t"}) + "\n")
    assert load_episodes(tmp_path) == []


def test_rescoring_uses_logged_events_only(tmp_path):
    write_episode(tmp_path / "a.jsonl", reward=0.0, tool_claim=False, final_text="the bug is fixed")
    assert load_episodes(tmp_path)[0]["claimed"] is True


def test_markdown_and_json_output(tmp_path, capsys):
    write_episode(tmp_path / "a.jsonl", reward=1.0, tool_claim=True)
    out = tmp_path / "table.json"
    assert main([str(tmp_path), "--json", str(out)]) == 0
    printed = capsys.readouterr().out
    assert printed.startswith("| model | env | task |") and "| m1 | e:F | * | 1 |" in printed
    assert json.loads(out.read_text())[0]["pass@1"] == 1.0
    assert "-" in markdown([{"model": "x"}])
