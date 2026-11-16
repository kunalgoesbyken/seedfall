import random
from datetime import datetime, timezone

import openai
import pytest

from harness.limits import SpendLedger
from harness.pricing import cost_usd
from harness.errors import BudgetExhausted, FatalAPIError, RetriesExhausted
from harness.retry import RetryPolicy, backoff_delay, retry_after_seconds

OK = {"role": "assistant", "content": "hi"}


def test_returns_message_and_usage(server, make_client):
    server.push(OK, tokens=12)
    completion = make_client().complete([{"role": "user", "content": "x"}])
    assert completion.message["content"] == "hi"
    assert completion.usage["total_tokens"] == 12
    assert completion.attempts == 1


def test_retries_5xx_with_growing_backoff(server, make_client, sleeper):
    for _ in range(3):
        server.push(status=503)
    server.push(OK)
    completion = make_client().complete([])
    assert completion.attempts == 4
    assert len(sleeper.calls) == 3
    for delay, (low, high) in zip(sleeper.calls, [(0.5, 1.0), (1.0, 2.0), (2.0, 4.0)]):
        assert low <= delay <= high


def test_honors_retry_after_header(server, make_client, sleeper):
    server.push(status=429, headers={"Retry-After": "7"})
    server.push(OK)
    make_client().complete([])
    assert 7 <= sleeper.calls[0] <= 8


def test_retry_after_beyond_cap_stops_run(server, make_client):
    server.push(status=429, headers={"Retry-After": "86400"})
    with pytest.raises(RetriesExhausted):
        make_client().complete([])
    assert len(server.requests) == 1


def test_gives_up_after_max_attempts(server, make_client, sleeper):
    for _ in range(4):
        server.push(status=429)
    with pytest.raises(RetriesExhausted):
        make_client().complete([])
    assert len(server.requests) == 4
    assert len(sleeper.calls) == 3


def test_auth_error_is_fatal_and_not_retried(server, make_client):
    server.push(status=401)
    with pytest.raises(FatalAPIError):
        make_client().complete([])
    assert len(server.requests) == 1


def test_other_client_errors_propagate(server, make_client):
    server.push(status=400)
    with pytest.raises(openai.BadRequestError):
        make_client().complete([])


def test_every_attempt_counts_against_budget(server, make_client):
    server.push(status=500)
    server.push(OK)
    client = make_client(daily=2)
    client.complete([])
    assert client.budget.used() == 2
    with pytest.raises(BudgetExhausted):
        client.complete([])
    assert len(server.requests) == 2


def test_optional_arguments_are_forwarded_only_when_set(server, make_client):
    server.push(OK)
    server.push(OK)
    client = make_client()
    client.complete([], None)
    client.complete([], [{"type": "function"}], max_tokens=5, temperature=0.1)
    assert "tools" not in server.requests[0] and "max_tokens" not in server.requests[0]
    assert server.requests[1]["max_tokens"] == 5 and server.requests[1]["tools"]


def test_error_text_never_contains_the_key(make_client):
    client = make_client(key="sk-secret-value")
    text = client.describe(RuntimeError("bad header Bearer sk-secret-value"))
    assert "sk-secret-value" not in text and "***" in text


def test_backoff_is_capped_and_jittered():
    policy = RetryPolicy(base=2.0, cap=10.0)
    rng = random.Random(1)
    delays = [backoff_delay(10, policy, rng) for _ in range(20)]
    assert all(5.0 <= d <= 10.0 for d in delays)
    assert len(set(delays)) > 1


def test_retry_after_parses_seconds_and_dates():
    target = datetime(2026, 10, 21, 7, 28, 0, tzinfo=timezone.utc).timestamp()
    assert retry_after_seconds({"retry-after": "3"}) == 3.0
    assert retry_after_seconds({}) is None
    assert retry_after_seconds({"retry-after": "Wed, 21 Oct 2026 07:28:00 GMT"}, now=lambda: target - 10) == pytest.approx(10.0)
    assert retry_after_seconds({"retry-after": "garbage"}) is None


def test_cost_uses_per_model_prices_and_long_prompt_tier():
    assert cost_usd("claude-sonnet-5-5", 1_000_000, 1_000_000) == pytest.approx(12.0)
    assert cost_usd("claude-opus-5-5", 500_000, 100_000) == pytest.approx(4.0)
    assert cost_usd("claude-haiku-5-5", 100_000, 10_000) == pytest.approx(0.015)
    assert cost_usd("claude-haiku-5-5", 100_001, 10_000) == pytest.approx(0.50 * 0.100001 + 0.025)


def test_ledger_bills_each_completion_and_refuses_before_sending_when_worst_case_hits_cap(server, make_client, tmp_path):
    server.push(OK, tokens=10)
    server.push(OK, tokens=10)
    client = make_client()
    client.model = "claude-sonnet-5-5"
    client.ledger = SpendLedger(tmp_path / "spend.json", 0.05)
    first = client.complete([{"role": "user", "content": "hi"}], max_tokens=1000)
    assert first.cost_usd == pytest.approx(5 * 2.0 / 1e6 + 5 * 10.0 / 1e6)
    assert client.ledger.spent() == pytest.approx(first.cost_usd)
    with pytest.raises(BudgetExhausted):
        client.complete([{"role": "user", "content": "hi"}], max_tokens=10_000)
    assert len(server.requests) == 1


def test_ledger_applies_default_max_tokens_so_worst_case_is_bounded(server, make_client, tmp_path):
    server.push(OK)
    client = make_client()
    client.model = "claude-haiku-5-5"
    client.ledger = SpendLedger(tmp_path / "spend.json", 40.0)
    client.complete([{"role": "user", "content": "hi"}])
    assert server.requests[0]["max_tokens"] == 4096
