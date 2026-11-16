import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path
from statistics import fmean

from harness.claims import claimed_success
from harness.errors import STOP_STATUSES
from harness.trajectory import read_events

PASS_REWARD = 1.0
COLUMNS = ("model", "env", "task", "episodes", "errors", "incomplete", "pass@1", "mean_reward", "mean_turns", "mean_tokens", "total_cost_usd", "false_claim_rate")


def score_episode(events: list[dict]) -> dict | None:
    meta = next((e for e in events if e["type"] == "meta"), None)
    final = next((e for e in reversed(events) if e["type"] == "final"), None)
    if meta is None or final is None:
        return None
    assistants = [e for e in events if e["type"] == "message" and e.get("role") == "assistant"]
    tool_claims = [e["claimed_success"] for e in events if e["type"] == "tool_result" and e["finished"]]
    last_message = (assistants[-1].get("content") or "") if assistants else ""
    return {
        "model": meta["model"],
        "env": meta["env"],
        "task": meta["task"],
        "status": final["status"],
        "reward": final["reward"],
        "turns": len(assistants),
        "tokens": sum(e.get("usage", {}).get("total_tokens", 0) for e in assistants),
        "cost_usd": sum(e.get("cost_usd", 0.0) for e in assistants),
        "claimed": claimed_success(tool_claims, last_message),
    }


def load_episodes(root: Path) -> list[dict]:
    scored = (score_episode(read_events(path)) for path in sorted(Path(root).rglob("*.jsonl")))
    return [episode for episode in scored if episode]


def summarize(episodes: list[dict]) -> dict:
    scored = [e for e in episodes if e["reward"] is not None and e["status"] not in STOP_STATUSES]
    incomplete = sum(e["status"] in STOP_STATUSES for e in episodes)
    summary = {"episodes": len(scored), "errors": len(episodes) - len(scored) - incomplete, "incomplete": incomplete}
    if scored:
        summary |= {
            "pass@1": fmean(e["reward"] >= PASS_REWARD for e in scored),
            "mean_reward": fmean(e["reward"] for e in scored),
            "mean_turns": fmean(e["turns"] for e in scored),
            "mean_tokens": fmean(e["tokens"] for e in scored),
            "total_cost_usd": sum(e["cost_usd"] for e in scored),
            "false_claim_rate": fmean(e["claimed"] and e["reward"] < PASS_REWARD for e in scored),
        }
    return summary


def aggregate(episodes: list[dict], by_task: bool) -> list[dict]:
    groups = defaultdict(list)
    for episode in episodes:
        groups[(episode["model"], episode["env"], episode["task"] if by_task else "*")].append(episode)
    return [{"model": m, "env": e, "task": t, **summarize(group)} for (m, e, t), group in sorted(groups.items())]


def format_cell(value) -> str:
    return "-" if value is None else f"{value:.3f}" if isinstance(value, float) else str(value)


def markdown(rows: list[dict]) -> str:
    lines = ["| " + " | ".join(COLUMNS) + " |", "|" + "---|" * len(COLUMNS)]
    lines += ["| " + " | ".join(format_cell(row.get(column)) for column in COLUMNS) + " |" for row in rows]
    return "\n".join(lines)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="harness.report", description="Aggregate episode logs into a table.")
    parser.add_argument("runs", type=Path)
    parser.add_argument("--by-task", action="store_true")
    parser.add_argument("--json", type=Path, dest="json_path")
    args = parser.parse_args(argv)
    rows = aggregate(load_episodes(args.runs), args.by_task)
    print(markdown(rows))
    if args.json_path:
        args.json_path.write_text(json.dumps(rows, indent=2), encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
