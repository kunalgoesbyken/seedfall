import json

import pytest

from harness import presets
from harness.errors import MissingKey
from harness.keys import load_key, parse_env_file
from harness.run import main
from harness.trajectory import episode_id, final_event

ENV = "harness.fake_env:ReadFileEnv"


def guard(tmp_path, code):
    script = tmp_path / f"guard{code}.py"
    script.write_text(f"import sys; sys.exit({code})")
    return script


def invoke(server, tmp_path, *extra, tasks=("t",), episodes=2):
    return main(
        [
            "--env", ENV, "--task", *tasks, "--preset", "local", "--model", "mock", "--base-url", server.base_url,
            "--episodes", str(episodes), "--out", str(tmp_path / "runs"), "--budget-dir", str(tmp_path / "budget"), "--spend-guard", str(guard(tmp_path, 0)), *extra,
        ]
    )


def secret_for(task, episode):
    from harness.fake_env import ReadFileEnv

    env = ReadFileEnv(task, episode)
    env.close()
    return env.secret


def test_episode_id_is_deterministic_and_sensitive_to_every_field():
    base = episode_id("e:F", "t", "gemini", "m", 0)
    assert base == episode_id("e:F", "t", "gemini", "m", 0)
    assert len({base, episode_id("e:G", "t", "gemini", "m", 0), episode_id("e:F", "u", "gemini", "m", 0),
                episode_id("e:F", "t", "local", "m", 0), episode_id("e:F", "t", "gemini", "n", 0), episode_id("e:F", "t", "gemini", "m", 1)}) == 6


def test_cli_runs_episodes_and_resumes_after_budget_stop(server, tmp_path, capsys):
    server.push_call("submit", {"content": secret_for("t", 0)})
    assert invoke(server, tmp_path, "--daily-budget", "1") == 3
    out = capsys.readouterr()
    assert "status=finished" in out.out and "budget_exhausted" in out.err

    server.push_call("submit", {"content": secret_for("t", 1)})
    assert invoke(server, tmp_path, "--daily-budget", "2") == 0
    assert "skip" in capsys.readouterr().out
    files = sorted((tmp_path / "runs" / "mock").glob("*.jsonl"))
    assert len(files) == 2
    assert {final_event(f)["status"] for f in files} == {"finished"}
    assert len(server.requests) == 2


def test_cli_rerun_does_not_repeat_completed_episodes(server, tmp_path):
    server.push_call("submit", {"content": "wrong"})
    assert invoke(server, tmp_path, episodes=1) == 0
    assert invoke(server, tmp_path, episodes=1) == 0
    assert len(server.requests) == 1


def test_missing_key_message_has_no_secret(monkeypatch, tmp_path, capsys):
    monkeypatch.delenv("GEMINI_API_KEY", raising=False)
    monkeypatch.setattr("harness.keys.default_env_file", lambda: tmp_path / "none.env")
    code = main(["--env", ENV, "--task", "t", "--preset", "gemini", "--model", "gemini-3.1-flash-lite", "--out", str(tmp_path)])
    assert code == 2
    assert "GEMINI_API_KEY" in capsys.readouterr().err


def test_key_prefers_environment_then_env_file(monkeypatch, tmp_path):
    env_file = tmp_path / ".env"
    env_file.write_text("# comment\nexport FOO_KEY='from-file'\nBAR=\"x\"\n")
    monkeypatch.delenv("FOO_KEY", raising=False)
    assert load_key("FOO_KEY", env_file) == "from-file"
    monkeypatch.setenv("FOO_KEY", "from-env")
    assert load_key("FOO_KEY", env_file) == "from-env"
    assert parse_env_file(env_file) == {"FOO_KEY": "from-file", "BAR": "x"}
    monkeypatch.delenv("NOPE_KEY", raising=False)
    with pytest.raises(MissingKey):
        load_key("NOPE_KEY", env_file)


def test_openrouter_only_allows_free_models():
    presets.resolve("openrouter", "meta/some-model:free")
    with pytest.raises(ValueError):
        presets.resolve("openrouter", "openai/gpt-4o")


def test_preset_limits_and_overrides():
    endpoint = presets.resolve("gemini", "gemini-3.1-flash-lite", rpm=2, daily=5)
    assert (endpoint.limits.rpm, endpoint.limits.daily) == (2, 5)
    assert endpoint.base_url.startswith("https://generativelanguage.googleapis.com/")
    assert endpoint.key_env == "GEMINI_API_KEY"
    with pytest.raises(ValueError):
        presets.resolve("local", "m")


def test_anthropic_preset_defaults_and_price_guard(monkeypatch):
    monkeypatch.delenv("SFG_ANTHROPIC_CAP_USD", raising=False)
    endpoint = presets.resolve("anthropic", "claude-haiku-5-5")
    assert endpoint.base_url == "https://api.anthropic.com/v1/"
    assert endpoint.key_env == "ANTHROPIC_API_KEY"
    assert endpoint.usd_cap == 40.0
    monkeypatch.setenv("SFG_ANTHROPIC_CAP_USD", "5")
    assert presets.resolve("anthropic", "claude-opus-5-5").usd_cap == 5.0
    with pytest.raises(ValueError):
        presets.resolve("anthropic", "claude-unpriced")
    assert presets.resolve("gemini", "gemini-3.1-flash-lite").usd_cap is None


def test_failing_spend_guard_aborts_before_any_request(server, tmp_path, capsys):
    server.push_call("submit", {"content": "x"})
    code = main(
        ["--env", ENV, "--task", "t", "--preset", "local", "--model", "mock", "--base-url", server.base_url,
         "--out", str(tmp_path / "runs"), "--spend-guard", str(guard(tmp_path, 1))]
    )
    assert code == 4
    assert server.requests == []
    assert "spend guard failed" in capsys.readouterr().err


def test_openrouter_non_free_model_is_refused_by_cli(tmp_path, capsys):
    code = main(
        ["--env", ENV, "--task", "t", "--preset", "openrouter", "--model", "openai/gpt-4o",
         "--out", str(tmp_path / "runs"), "--spend-guard", str(guard(tmp_path, 0))]
    )
    assert code == 2
    assert ":free" in capsys.readouterr().err
