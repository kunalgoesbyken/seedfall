import json
import os
import queue
import re
import threading
import time
from concurrent.futures import ThreadPoolExecutor

from . import scenarios as S
from .env import Env, EnvError

ALLOWED_KINDS = {"set_args", "restart"}
MAX_ACTIONS = 24
TOUCH_PENALTY = 0.5
DURATION = re.compile(r"([0-9]+(?:\.[0-9]+)?)(ms|s)")


def parse_ms(text):
    m = DURATION.fullmatch(text) if isinstance(text, str) else None
    if not m:
        raise ValueError(f"bad duration {text!r}")
    return float(m[1]) * (1.0 if m[2] == "ms" else 1000.0)


def parse_plan(text):
    body = text.partition("</think>")[2] if "</think>" in text else text
    start = body.find("[")
    if start < 0:
        raise ValueError("no JSON array")
    try:
        plan, _ = json.JSONDecoder().raw_decode(body[start:])
    except RecursionError:
        raise ValueError("JSON nested too deeply")
    if not isinstance(plan, list):
        raise ValueError("not an array")
    return plan


def validate(s, plan):
    if not plan or len(plan) > MAX_ACTIONS:
        raise ValueError("plan must have between 1 and %d actions" % MAX_ACTIONS)
    nodes = {f"n{i + 1}" for i in range(s.n)}
    for a in plan:
        if not isinstance(a, dict) or set(a) - {"at", "kind", "node", "args"}:
            raise ValueError("action is not an object with at, kind, node, args")
        if a.get("kind") not in ALLOWED_KINDS:
            raise ValueError("unknown kind")
        at = parse_ms(a.get("at"))
        if not S.WINDOW_MS <= at <= s.deadline_ms:
            raise ValueError("action before the first window ends or after the deadline")
        if a.get("node") not in nodes:
            raise ValueError("unknown node")
        args = a.get("args", [])
        if not isinstance(args, list) or not all(isinstance(x, str) for x in args):
            raise ValueError("args must be a list of strings")
        if a["kind"] == "set_args" and args != s.node_args(int(a["node"][1:]) - 1, s.target):
            raise ValueError("set_args must give the node's arguments for the target version")
        if a["kind"] == "restart":
            if len(args) != 1:
                raise ValueError("restart takes exactly one delay")
            down = parse_ms(args[0])
            if not s.min_down_ms <= down <= s.max_down_ms:
                raise ValueError("restart delay outside the allowed downtime")
    return plan


def doomed(s, plan):
    for i in s.stale:
        name = f"n{i + 1}"
        if not any(a["kind"] == "set_args" and a["node"] == name for a in plan):
            return True
    return False




def touches_current(s, plan):
    current = {f"n{i + 1}" for i in range(s.n)} - {f"n{i + 1}" for i in s.stale}
    return any(a["node"] in current for a in plan)


class Reward:
    def __init__(self, binary, tree, procs=2, jobs=2, skip_doomed=True):
        self.binary, self.jobs = os.path.abspath(binary), jobs
        self.dir = os.path.join(os.path.abspath(tree), "examples", "etcdraft")
        self.scen_dir = os.path.join(self.dir, "scen")
        os.makedirs(self.scen_dir, exist_ok=True)
        self.skip_doomed = skip_doomed
        self.envs = queue.Queue()
        for _ in range(procs):
            self.envs.put(Env(self.binary, self.dir, jobs))
        self.pool = ThreadPoolExecutor(procs)
        self.cache = {}
        self.lock = threading.Lock()
        self.written = set()
        self.stats = {"calls": 0, "episodes": 0, "seconds": 0.0, "doomed": 0, "cached": 0, "env_failures": 0}

    def config_path(self, s):
        with self.lock:
            if s.id not in self.written:
                S.write_config(s, self.scen_dir)
                self.written.add(s.id)
        return os.path.join("scen", f"{s.id}.json")

    def score_ops(self, s, ops, seeds):
        key = (s.id, json.dumps(ops, sort_keys=True), tuple(seeds))
        with self.lock:
            if key in self.cache:
                self.stats["cached"] += 1
                return self.cache[key]
        path = self.config_path(s)
        touched = touches_current(s, ops)
        if self.skip_doomed and doomed(s, ops):
            with self.lock:
                self.stats["doomed"] += 1
            result = {"reward": 0.0, "replay": 0.0, "touched": touched, "status": "doomed", "passes": 0}
        else:
            env = self.envs.get()
            cache = True
            try:
                t = time.time()
                v = env.verify(path, ops, list(seeds))
                dt = time.time() - t
                with self.lock:
                    self.stats["episodes"] += len(seeds)
                    self.stats["seconds"] += dt
                replay = float(v["reward"])
                result = {"reward": replay * (TOUCH_PENALTY if touched else 1.0), "replay": replay, "touched": touched, "status": "replayed", "passes": int(v["passed"]), "reasons": sorted({(x.get("reason") or "")[:160] for x in v["seeds"] if not x["passed"]})}
            except EnvError as e:
                result = {"reward": 0.0, "replay": 0.0, "touched": touched, "status": "rejected", "passes": 0, "error": str(e)[:200]}
            except Exception as e:
                result = {"reward": 0.0, "replay": 0.0, "touched": touched, "status": "env_failure", "passes": 0, "error": repr(e)[:200]}
            finally:
                if env.alive():
                    self.envs.put(env)
                else:
                    cache = False
                    if result["status"] == "rejected":
                        result["status"] = "env_failure"
                    with self.lock:
                        self.stats["env_failures"] += 1
                    env.close()
                    self.envs.put(Env(self.binary, self.dir, self.jobs))
            if not cache:
                return result
        with self.lock:
            self.cache[key] = result
        return result

    def prepare(self, s, text, seeds):
        try:
            plan = validate(s, parse_plan(text))
        except (ValueError, json.JSONDecodeError, TypeError, AttributeError) as e:
            return None, {"reward": 0.0, "status": "invalid", "passes": 0, "error": str(e)[:100]}
        return (s, plan, tuple(seeds)), None

    def score_text(self, s, text, seeds):
        key, result = self.prepare(s, text, seeds)
        return result if key is None else self.score_ops(*key)

    def score_many(self, items):
        with self.lock:
            self.stats["calls"] += 1
        prepared = [self.prepare(*it) for it in items]
        unique = {}
        for key, _ in prepared:
            if key is not None:
                unique.setdefault((key[0].id, json.dumps(key[1], sort_keys=True), key[2]), key)
        scored = dict(zip(unique, self.pool.map(lambda k: self.score_ops(*k), unique.values())))
        return [res if key is None else scored[(key[0].id, json.dumps(key[1], sort_keys=True), key[2])] for key, res in prepared]

    def close(self):
        while not self.envs.empty():
            self.envs.get().close()
        self.pool.shutdown()
