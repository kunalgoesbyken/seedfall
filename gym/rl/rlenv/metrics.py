import json
import statistics


def summarize(results):
    rewards = [r["reward"] for r in results]
    status = {}
    for r in results:
        status[r["status"]] = status.get(r["status"], 0) + 1
    n = len(results)
    return {
        "n": n,
        "mean_reward": statistics.fmean(rewards) if rewards else 0.0,
        "valid_rate": 1 - status.get("invalid", 0) / n if n else 0.0,
        "pass_all_rate": sum(1 for r in rewards if r == 1.0) / n if n else 0.0,
        "nonzero_rate": sum(1 for r in rewards if r > 0) / n if n else 0.0,
        "mean_replay": statistics.fmean(r.get("replay", 0.0) for r in results) if results else 0.0,
        "touch_rate": sum(1 for r in results if r.get("touched")) / n if n else 0.0,
        "status": status,
    }


def per_scenario(items, results):
    out = {}
    for (s, _, _), r in zip(items, results):
        out.setdefault(s.id, []).append(r["reward"])
    return {k: statistics.fmean(v) for k, v in out.items()}


def distinct_per_scenario(items):
    texts = {}
    for s, text, _ in items:
        texts.setdefault(s.id, set()).add(text)
    return statistics.fmean(len(v) for v in texts.values()) if texts else 0.0


def jsonl_append(path, row):
    with open(path, "a") as f:
        f.write(json.dumps(row) + "\n")
