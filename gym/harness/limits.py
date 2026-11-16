import json
import os
import time
from collections import deque
from datetime import datetime, timedelta, timezone
from pathlib import Path

from harness.errors import BudgetExhausted


class Throttle:
    def __init__(self, rpm, clock=time.monotonic, sleep=time.sleep, window=60.0):
        self.rpm = rpm
        self.clock = clock
        self.sleep = sleep
        self.window = window
        self.stamps = deque()

    def wait(self):
        while True:
            now = self.clock()
            while self.stamps and now - self.stamps[0] >= self.window:
                self.stamps.popleft()
            if len(self.stamps) < self.rpm:
                self.stamps.append(now)
                return
            self.sleep(self.window - (now - self.stamps[0]))


def utc_now():
    return datetime.now(timezone.utc)


class Budget:
    def __init__(self, path: Path, limit: int, utc_offset_hours: int = 0, now=utc_now):
        self.path = Path(path)
        self.limit = limit
        self.offset = timedelta(hours=utc_offset_hours)
        self.now = now

    def _day(self) -> str:
        return (self.now() + self.offset).date().isoformat()

    def used(self) -> int:
        if not self.path.is_file():
            return 0
        state = json.loads(self.path.read_text(encoding="utf-8"))
        return state["count"] if state["day"] == self._day() else 0

    def remaining(self) -> int:
        return max(0, self.limit - self.used())

    def consume(self) -> None:
        used = self.used()
        if used >= self.limit:
            raise BudgetExhausted(f"daily budget of {self.limit} requests used ({self.path.stem})")
        self._write({"day": self._day(), "count": used + 1})

    def _write(self, state: dict) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_suffix(".tmp")
        tmp.write_text(json.dumps(state), encoding="utf-8")
        os.replace(tmp, self.path)


class SpendLedger:
    def __init__(self, path: Path, cap_usd: float, key: str = "anthropic_usd"):
        self.path = Path(path)
        self.cap = cap_usd
        self.key = key

    def _state(self) -> dict:
        return json.loads(self.path.read_text(encoding="utf-8")) if self.path.is_file() else {}

    def spent(self) -> float:
        return float(self._state().get(self.key, 0.0))

    def ensure_room(self, worst_case_usd: float) -> None:
        spent = self.spent()
        if spent + worst_case_usd >= self.cap:
            raise BudgetExhausted(f"USD cap {self.cap:.2f} would be reached (spent {spent:.4f}, worst case {worst_case_usd:.4f})")

    def add(self, usd: float) -> None:
        state = self._state()
        state[self.key] = float(state.get(self.key, 0.0)) + usd
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_suffix(".tmp")
        tmp.write_text(json.dumps(state), encoding="utf-8")
        os.replace(tmp, self.path)
