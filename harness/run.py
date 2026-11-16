import argparse
import importlib
import os
import subprocess
import sys
from pathlib import Path

from harness import presets
from harness.client import build_client
from harness.episode import EpisodeConfig, run_episode
from harness.errors import STOP_STATUSES, MissingKey
from harness.keys import load_key
from harness.paths import slug
from harness.trajectory import TrajectoryLog, episode_id, is_complete
from harness.transcript import NATIVE, TEXT

EXIT_STOPPED = 3
EXIT_GUARD = 4
SPEND_GUARD = Path(__file__).resolve().parent.parent / "tools" / "spend_guard.py"
LOCAL_PLACEHOLDER_KEY = "none"


def load_factory(spec: str):
    module_name, _, attr = spec.partition(":")
    if not module_name or not attr:
        raise ValueError("--env must look like module:Factory")
    return getattr(importlib.import_module(module_name), attr)


def parse_args(argv=None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(prog="harness.run", description="Run an LLM agent on an environment.")
    parser.add_argument("--env", required=True, help="module:Factory, called as Factory(task_id, episode)")
    parser.add_argument("--task", nargs="+", required=True)
    parser.add_argument("--preset", required=True, choices=sorted(presets.PRESETS))
    parser.add_argument("--model", required=True)
    parser.add_argument("--episodes", type=int, default=1)
    parser.add_argument("--out", type=Path, default=Path("runs"))
    parser.add_argument("--base-url")
    parser.add_argument("--key-env")
    parser.add_argument("--rpm", type=int)
    parser.add_argument("--daily-budget", type=int)
    parser.add_argument("--spend-guard", type=Path, default=SPEND_GUARD)
    parser.add_argument("--budget-dir", type=Path)
    parser.add_argument("--max-turns", type=int, default=EpisodeConfig.max_turns)
    parser.add_argument("--max-total-tokens", type=int, default=EpisodeConfig.max_total_tokens)
    parser.add_argument("--max-completion-tokens", type=int, default=EpisodeConfig.max_completion_tokens)
    parser.add_argument("--timeout", type=float, default=EpisodeConfig.timeout_s)
    parser.add_argument("--tool-mode", choices=(NATIVE, TEXT), default=NATIVE)
    parser.add_argument("--temperature", type=float)
    return parser.parse_args(argv)


def episode_config(args) -> EpisodeConfig:
    return EpisodeConfig(
        max_turns=args.max_turns,
        max_total_tokens=args.max_total_tokens,
        max_completion_tokens=args.max_completion_tokens,
        timeout_s=args.timeout,
        tool_mode=args.tool_mode,
        temperature=args.temperature,
    )


def resolve_key(endpoint) -> str:
    if endpoint.key_required or os.environ.get(endpoint.key_env):
        return load_key(endpoint.key_env)
    return LOCAL_PLACEHOLDER_KEY


def spend_guard_passes(script: Path) -> bool:
    done = subprocess.run([sys.executable, str(script)], capture_output=True, text=True)
    if done.returncode != 0:
        print(f"spend guard failed (exit {done.returncode}); aborting", done.stdout, done.stderr, sep="\n", file=sys.stderr)
    return done.returncode == 0


def summary(eid: str, task: str, result) -> str:
    return f"{eid} task={task} status={result.status} reward={result.reward} turns={result.turns} tokens={result.tokens}"


def main(argv=None) -> int:
    args = parse_args(argv)
    if not spend_guard_passes(args.spend_guard):
        return EXIT_GUARD
    try:
        endpoint = presets.resolve(args.preset, args.model, args.base_url, args.key_env, args.rpm, args.daily_budget)
    except ValueError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    try:
        key = resolve_key(endpoint)
    except MissingKey as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    client = build_client(endpoint, key, args.budget_dir)
    factory = load_factory(args.env)
    cfg = episode_config(args)
    out_dir = args.out / slug(endpoint.model)
    for task in args.task:
        for index in range(args.episodes):
            eid = episode_id(args.env, task, endpoint.preset, endpoint.model, index)
            path = out_dir / f"{eid}.jsonl"
            if is_complete(path):
                print(f"skip {eid} task={task} (already complete)")
                continue
            meta = {"episode_id": eid, "env": args.env, "task": task, "episode": index, "preset": endpoint.preset, "model": endpoint.model}
            env = factory(task, index)
            log = TrajectoryLog(path)
            try:
                result = run_episode(env, client, cfg, log, meta)
            finally:
                log.close()
                env.close()
            print(summary(eid, task, result))
            if result.status in STOP_STATUSES:
                print(f"stopped: {result.status} ({result.error}); rerun the same command to resume", file=sys.stderr)
                return EXIT_STOPPED
    return 0


if __name__ == "__main__":
    sys.exit(main())
