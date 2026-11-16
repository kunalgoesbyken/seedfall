import json
import random
import time
from dataclasses import dataclass, replace
from pathlib import Path

import openai

from harness.errors import RetriesExhausted
from harness.limits import Budget, SpendLedger, Throttle
from harness.paths import config_dir, slug
from harness.presets import Endpoint
from harness.pricing import cost_usd
from harness.retry import RetryPolicy, failure_delay

EMPTY_MESSAGE = {"role": "assistant", "content": ""}
LEDGER_DEFAULT_MAX_TOKENS = 4096


@dataclass(frozen=True)
class Completion:
    message: dict
    usage: dict
    latency: float
    finish_reason: str | None
    attempts: int
    cost_usd: float = 0.0


class ChatClient:
    def __init__(
        self,
        transport,
        model: str,
        throttle: Throttle,
        budget: Budget,
        policy: RetryPolicy | None = None,
        sleep=time.sleep,
        rng=random,
        clock=time.monotonic,
        redact: str | None = None,
        ledger: SpendLedger | None = None,
    ):
        self.transport = transport
        self.model = model
        self.throttle = throttle
        self.budget = budget
        self.policy = policy or RetryPolicy()
        self.sleep = sleep
        self.rng = rng
        self.clock = clock
        self.redact = redact
        self.ledger = ledger

    def describe(self, exc: Exception) -> str:
        text = f"{type(exc).__name__}: {str(exc)[:300]}"
        return text.replace(self.redact, "***") if self.redact else text

    def complete(self, messages, tools=None, *, max_tokens=None, temperature=None, timeout=None) -> Completion:
        kwargs = {"model": self.model, "messages": messages}
        optional = {"tools": tools, "max_tokens": max_tokens, "temperature": temperature, "timeout": timeout}
        kwargs.update({name: value for name, value in optional.items() if value is not None})
        if self.ledger and "max_tokens" not in kwargs:
            kwargs["max_tokens"] = LEDGER_DEFAULT_MAX_TOKENS
        last = None
        for attempt in range(self.policy.max_attempts):
            if self.ledger:
                self.ledger.ensure_room(self._worst_case(kwargs))
            self.budget.consume()
            self.throttle.wait()
            started = self.clock()
            try:
                response = self.transport.chat.completions.create(**kwargs)
            except openai.OpenAIError as exc:
                last = exc
                delay = failure_delay(exc, attempt, self.policy, self.rng, self.describe)
                if attempt + 1 < self.policy.max_attempts:
                    self.sleep(delay)
                continue
            completion = self._completion(response, self.clock() - started, attempt + 1)
            return self._billed(completion)
        raise RetriesExhausted(self.describe(last))

    def _worst_case(self, kwargs: dict) -> float:
        prompt_upper_bound = len(json.dumps([kwargs["messages"], kwargs.get("tools")]))
        return cost_usd(self.model, prompt_upper_bound, kwargs["max_tokens"])

    def _billed(self, completion: Completion) -> Completion:
        if not self.ledger:
            return completion
        usage = completion.usage
        usd = cost_usd(self.model, usage["prompt_tokens"], usage["completion_tokens"])
        self.ledger.add(usd)
        return replace(completion, cost_usd=usd)

    @staticmethod
    def _completion(response, latency: float, attempts: int) -> Completion:
        choice = response.choices[0] if response.choices else None
        message = choice.message.model_dump(exclude_none=True) if choice else dict(EMPTY_MESSAGE)
        usage = response.usage
        return Completion(
            message=message,
            usage={
                "prompt_tokens": usage.prompt_tokens if usage else 0,
                "completion_tokens": usage.completion_tokens if usage else 0,
                "total_tokens": usage.total_tokens if usage else 0,
            },
            latency=latency,
            finish_reason=choice.finish_reason if choice else None,
            attempts=attempts,
        )


def build_client(endpoint: Endpoint, key: str, budget_dir: Path | None = None, spend_path: Path | None = None, **overrides) -> ChatClient:
    transport = openai.OpenAI(base_url=endpoint.base_url, api_key=key, max_retries=0)
    budget_root = budget_dir or config_dir() / "budget"
    budget_path = budget_root / f"{endpoint.preset}__{slug(endpoint.model)}.json"
    ledger = SpendLedger(spend_path or config_dir() / "spend.json", endpoint.usd_cap) if endpoint.usd_cap else None
    return ChatClient(
        transport,
        endpoint.model,
        Throttle(endpoint.limits.rpm),
        Budget(budget_path, endpoint.limits.daily, endpoint.reset_utc_offset),
        redact=key,
        ledger=ledger,
        **overrides,
    )
