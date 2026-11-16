import json
from datetime import datetime, timedelta, timezone

import pytest

from harness.errors import BudgetExhausted
from harness.limits import Budget, SpendLedger, Throttle


class FakeTime:
    def __init__(self):
        self.now = 0.0
        self.slept = []

    def clock(self):
        return self.now

    def sleep(self, seconds):
        self.slept.append(seconds)
        self.now += seconds


def test_throttle_allows_burst_then_waits_for_window():
    time = FakeTime()
    throttle = Throttle(3, clock=time.clock, sleep=time.sleep)
    for _ in range(3):
        throttle.wait()
    assert time.slept == []
    throttle.wait()
    assert time.slept == [60.0]


def test_throttle_slides_window():
    time = FakeTime()
    throttle = Throttle(2, clock=time.clock, sleep=time.sleep)
    throttle.wait()
    time.now = 30.0
    throttle.wait()
    throttle.wait()
    assert time.slept == [30.0]
    assert time.now == 60.0


def test_budget_persists_across_instances(tmp_path):
    path = tmp_path / "b" / "m.json"
    first = Budget(path, 3)
    first.consume()
    first.consume()
    second = Budget(path, 3)
    assert second.used() == 2
    second.consume()
    with pytest.raises(BudgetExhausted):
        Budget(path, 3).consume()


def test_budget_resets_on_new_day(tmp_path):
    clock = {"now": datetime(2026, 11, 16, 12, tzinfo=timezone.utc)}
    budget = Budget(tmp_path / "m.json", 1, now=lambda: clock["now"])
    budget.consume()
    with pytest.raises(BudgetExhausted):
        budget.consume()
    clock["now"] += timedelta(days=1)
    assert budget.remaining() == 1
    budget.consume()


def test_budget_day_follows_utc_offset(tmp_path):
    clock = {"now": datetime(2026, 11, 16, 6, tzinfo=timezone.utc)}
    budget = Budget(tmp_path / "m.json", 1, utc_offset_hours=-8, now=lambda: clock["now"])
    budget.consume()
    clock["now"] = datetime(2026, 11, 16, 9, tzinfo=timezone.utc)
    assert budget.remaining() == 1


def test_spend_ledger_accumulates_and_keeps_other_keys(tmp_path):
    path = tmp_path / "spend.json"
    path.write_text('{"other": 7, "anthropic_usd": 0.25}')
    SpendLedger(path, 1.0).add(0.5)
    assert SpendLedger(path, 1.0).spent() == pytest.approx(0.75)
    assert json.loads(path.read_text())["other"] == 7
    assert not path.with_suffix(".tmp").exists()


def test_spend_ledger_refuses_when_worst_case_reaches_cap(tmp_path):
    ledger = SpendLedger(tmp_path / "spend.json", 1.0)
    ledger.add(0.6)
    ledger.ensure_room(0.39)
    with pytest.raises(BudgetExhausted):
        ledger.ensure_room(0.4)


def test_spend_ledger_missing_file_is_zero_and_cap_is_exclusive(tmp_path):
    ledger = SpendLedger(tmp_path / "nested" / "spend.json", 0.5)
    assert ledger.spent() == 0.0
    with pytest.raises(BudgetExhausted):
        ledger.ensure_room(0.5)
    ledger.add(0.1)
    assert (tmp_path / "nested" / "spend.json").is_file()
