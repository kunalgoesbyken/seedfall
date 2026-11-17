import json
import os
import random
from dataclasses import asdict, dataclass

FIRST_PORT = 8000
GRACE_MS = 2000
WINDOW_MS = 1000


@dataclass(frozen=True)
class Scenario:
    id: str
    n: int
    target: int
    versions: tuple
    deadline_ms: int
    max_down_ms: int
    slo: float
    crash_per_sec: float
    partition_per_sec: float
    min_down_ms: int = 250

    @property
    def stale(self):
        return [i for i, v in enumerate(self.versions) if v != self.target]

    def node_args(self, i, version):
        return ["-id", str(i + 1), "-n", str(self.n), "-version", str(version)]


def sample(rng, id):
    n = rng.choices([3, 5], weights=[7, 3])[0]
    target = rng.choice([2, 3, 4])
    stale = rng.randint(1, n)
    behind = set(rng.sample(range(n), stale))
    versions = tuple(rng.randint(1, target - 1) if i in behind else target for i in range(n))
    return Scenario(
        id=id,
        n=n,
        target=target,
        versions=versions,
        deadline_ms=rng.choice([5000, 6000, 7000] if n == 3 else [7000, 8000, 9000]),
        max_down_ms=rng.choice([400, 600, 800]),
        slo=rng.choice([0.5, 0.6, 0.7]),
        crash_per_sec=rng.choice([0.01, 0.03, 0.06]),
        partition_per_sec=rng.choice([0.01, 0.03, 0.06]),
    )


def split(n_train, n_test, seed=0):
    rng = random.Random(seed)
    seen, train, test = set(), [], []
    while len(train) < n_train or len(test) < n_test:
        s = sample(rng, "")
        key = tuple((k, v) for k, v in asdict(s).items() if k != "id")
        if key in seen:
            continue
        seen.add(key)
        if len(test) < n_test and (len(train) >= n_train or rng.random() < 0.5):
            test.append(s)
        elif len(train) < n_train:
            train.append(s)
    train = [Scenario(**{**asdict(s), "id": f"train-{i:03d}", "versions": tuple(s.versions)}) for i, s in enumerate(train)]
    test = [Scenario(**{**asdict(s), "id": f"test-{i:03d}", "versions": tuple(s.versions)}) for i, s in enumerate(test)]
    return train, test


def config(s, pace_ms=40, clients=2):
    nodes = [{"name": f"n{i + 1}", "pkg": "../node", "args": s.node_args(i, s.versions[i])} for i in range(s.n)]
    addrs = ",".join(f"n{i + 1}:{FIRST_PORT}" for i in range(s.n))
    return {
        "nodes": nodes,
        "workload": {
            "name": "client",
            "pkg": "../opclient",
            "args": [
                "-nodes", addrs,
                "-clients", str(clients),
                "-span", f"{s.deadline_ms}ms",
                "-slo", str(s.slo),
                "-version", str(s.target),
                "-window", f"{WINDOW_MS}ms",
                "-grace", f"{GRACE_MS}ms",
                "-pace", f"{pace_ms}ms",
            ],
        },
        "duration": f"{s.deadline_ms // 1000 + 12}s",
        "latency": ["1ms", "8ms"],
        "faults": {
            "crash_per_sec": s.crash_per_sec,
            "restart_after": ["50ms", "400ms"],
            "partition_per_sec": s.partition_per_sec,
            "partition_for": ["100ms", "500ms"],
            "partition_topologies": ["one"],
            "torn_writes": True,
            "crash_on_io": 0.001,
        },
    }


def write_config(s, directory, **kw):
    path = os.path.join(directory, f"{s.id}.json")
    with open(path, "w") as f:
        json.dump(config(s, **kw), f)
    return path


def fmt_ms(ms):
    return f"{ms}ms"


def oracle_gap_ms(s, start_ms, down_ms, settle_ms=800, cap_ms=2000):
    k = len(s.stale)
    if k < 2:
        return cap_ms
    return max(1000, min(cap_ms, (s.deadline_ms - start_ms - down_ms - settle_ms) // (k - 1)))


def roll_plan(s, at_ms, down_ms=300):
    ops = []
    for i, t in zip(s.stale, at_ms):
        ops.append({"at": fmt_ms(t), "kind": "set_args", "node": f"n{i + 1}", "args": s.node_args(i, s.target)})
        ops.append({"at": fmt_ms(t), "kind": "restart", "node": f"n{i + 1}", "args": [fmt_ms(down_ms)]})
    return ops


def oracle_plan(s, start_ms=WINDOW_MS * 3 // 2, down_ms=None):
    down_ms = s.min_down_ms if down_ms is None else down_ms
    gap = oracle_gap_ms(s, start_ms, down_ms)
    return roll_plan(s, [start_ms + j * gap for j in range(len(s.stale))], down_ms)


def restart_all_plan(s, at_ms=1500, down_ms=300):
    ops = []
    for i in range(s.n):
        ops.append({"at": fmt_ms(at_ms), "kind": "set_args", "node": f"n{i + 1}", "args": s.node_args(i, s.target)})
        ops.append({"at": fmt_ms(at_ms), "kind": "restart", "node": f"n{i + 1}", "args": [fmt_ms(down_ms)]})
    return ops


def idle_plan(s):
    return []


def restart_stale_together_plan(s, at_ms=1500, down_ms=300):
    return roll_plan(s, [at_ms] * len(s.stale), down_ms)


def stale_at_deadline_plan(s, down_ms=None):
    return roll_plan(s, [s.deadline_ms] * len(s.stale), s.min_down_ms if down_ms is None else down_ms)


def restart_only_plan(s, start_ms=WINDOW_MS * 3 // 2, down_ms=None):
    down_ms = s.min_down_ms if down_ms is None else down_ms
    gap = oracle_gap_ms(s, start_ms, down_ms)
    return [{"at": fmt_ms(start_ms + j * gap), "kind": "restart", "node": f"n{i + 1}", "args": [fmt_ms(down_ms)]} for j, i in enumerate(s.stale)]


BASELINES = {
    "oracle_rolling": oracle_plan,
    "idle": idle_plan,
    "restart_all_together": restart_all_plan,
    "restart_stale_together": restart_stale_together_plan,
    "restart_without_set_args": restart_only_plan,
    "stale_at_deadline": stale_at_deadline_plan,
}
