import argparse
import json
import os
import sys
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from harness.gym_env import SALT_ENV, GradeConfig, GradeError, GradeTimeout, GymEnv, parse_report, run_bounded, scrubbed_env
from harness.reference import apply_solution
from harness.trajectory import final_event, read_events, truncate

EDITS = ("write_file", "edit_file")
LOG_CHARS = 2000


def summary(report: dict) -> dict:
    guard = report.get("guard") or {}
    failed = [s for s in report.get("per_seed", []) if not s["passed"]]
    classes: dict[str, int] = {}
    for seed in failed:
        classes[seed.get("class", "")] = classes.get(seed.get("class", ""), 0) + 1
    return {
        "passed": report.get("passed"),
        "n": report.get("n"),
        "build_ok": report.get("build_ok"),
        "guard_tripped": guard.get("tripped"),
        "progress": guard.get("progress"),
        "failed_classes": classes,
    }


def hidden_grade(env: GymEnv, salt: str) -> dict:
    try:
        return parse_report(*run_bounded(env._command(), scrubbed_env({**env.cfg.extra_env, SALT_ENV: salt}), env.cfg.grade_timeout_s))
    except GradeTimeout:
        return {"reward": 0.0, "timeout": True}


def replay(path: Path, cfg: GradeConfig, salt: str, verify: bool) -> dict:
    events = read_events(path)
    meta = events[0]
    final = final_event(path)
    calls = [e for e in events if e["type"] == "tool_result"]
    last_run = max((i for i, e in enumerate(calls) if e["name"] == "run_seeds" and not e["text"].startswith("error:")), default=-1)
    row = {"file": f"{path.parent.name}/{path.name}", "model": meta["model"], "task": meta["task"], "status": final["status"] if final else None, "recorded": final["reward"] if final else None}
    env = GymEnv(meta["task"], meta["episode"], cfg)
    try:
        if meta["model"] == "oracle":
            apply_solution(meta["task"], env.root, cfg.tasks)
        for i, call in enumerate(calls):
            if call["name"] in EDITS:
                env.tools.call(call["name"], call["arguments"])
            elif verify and i == last_run:
                text = env.tools.call("run_seeds", call["arguments"]).text
                row["run_seeds_match"] = truncate(text, LOG_CHARS)[0] == call["text"]
        report = hidden_grade(env, salt)
        row["reward"] = float(report["reward"])
        row.update(summary(report))
    except GradeError as exc:
        row["error"] = str(exc)[:300]
    finally:
        env.close()
    return row


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="harness.regrade", description="Rebuild each saved episode's final workspace from its tool calls and grade it again, without a model.")
    parser.add_argument("--runs", type=Path, default=Path("runs"))
    parser.add_argument("--grade", type=Path, required=True)
    parser.add_argument("--tasks", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--verify", action="store_true", help="also replay each episode's last run_seeds call and compare its recorded text")
    parser.add_argument("--jobs", type=int, default=2)
    parser.add_argument("--model", nargs="*")
    args = parser.parse_args(argv)
    salt = os.environ.get(SALT_ENV)
    if not salt:
        parser.error(f"set {SALT_ENV}")
    cfg = GradeConfig((str(args.grade),), tasks=args.tasks, salt=salt)
    paths = sorted(p for p in args.runs.glob("*/*.jsonl") if final_event(p) is not None and (not args.model or read_events(p)[0]["model"] in args.model))
    with ThreadPoolExecutor(args.jobs) as pool:
        rows = list(pool.map(lambda p: replay(p, cfg, salt, args.verify), paths))
    args.out.write_text(json.dumps(rows, indent=2) + "\n", encoding="utf-8")
    print(f"{len(rows)} episodes written to {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
