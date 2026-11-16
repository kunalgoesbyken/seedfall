import time
from dataclasses import dataclass
from email.utils import parsedate_to_datetime

import openai

from harness.errors import FatalAPIError, RetriesExhausted

RETRYABLE_STATUSES = frozenset({408, 409, 429})
FATAL_STATUSES = frozenset({401, 403})


@dataclass(frozen=True)
class RetryPolicy:
    max_attempts: int = 6
    base: float = 2.0
    cap: float = 60.0
    max_retry_after: float = 300.0


def backoff_delay(attempt: int, policy: RetryPolicy, rng) -> float:
    ceiling = min(policy.cap, policy.base * 2**attempt)
    return ceiling / 2 + rng.uniform(0, ceiling / 2)


def retry_after_seconds(headers, now=time.time) -> float | None:
    value = headers.get("retry-after")
    if value is None:
        return None
    try:
        return max(0.0, float(value))
    except ValueError:
        pass
    try:
        return max(0.0, parsedate_to_datetime(value).timestamp() - now())
    except (TypeError, ValueError):
        return None


def failure_delay(exc: Exception, attempt: int, policy: RetryPolicy, rng, describe) -> float:
    if isinstance(exc, openai.APIStatusError):
        status = exc.status_code
        if status in FATAL_STATUSES:
            raise FatalAPIError(describe(exc)) from exc
        if status not in RETRYABLE_STATUSES and status < 500:
            raise exc
        hinted = retry_after_seconds(exc.response.headers)
        if hinted is not None:
            if hinted > policy.max_retry_after:
                raise RetriesExhausted(f"{describe(exc)}; retry-after {hinted:.0f}s") from exc
            return hinted + rng.uniform(0, 1)
    elif not isinstance(exc, openai.APIConnectionError):
        raise exc
    return backoff_delay(attempt, policy, rng)
