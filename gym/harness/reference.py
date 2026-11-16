import argparse
import shutil
import sys
from dataclasses import asdict
from pathlib import Path

from harness.episode import EpisodeResult
from harness.gym_env import TASKS_DIR, Factory
from harness.parsing import ToolCall
from harness.trajectory import TrajectoryLog, episode_id, is_complete

KINDS = ("null", "oracle")
ENV_SPEC = "harness.gym_env:Factory"
PRESET = "reference"
PATCH_NAME = "fix.patch"


def apply_solution(task_id: str, root: Path, tasks: Path = TASKS_DIR) -> None:
    solution = tasks / task_id / "solution"
    for source in sorted(p for p in solution.rglob("*") if p.is_file() and p.name != PATCH_NAME):
        target = root / source.relative_to(solution)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, target)


def run_reference(kind: str, task_id: str, out: Path, factory=Factory) -> EpisodeResult:
    path = out / kind / f"{episode_id(ENV_SPEC, task_id, PRESET, kind, 0)}.jsonl"
    env = factory(task_id, 0)
    log = TrajectoryLog(path)
    try:
        log.write("meta", episode_id=path.stem, env=ENV_SPEC, task=task_id, episode=0, preset=PRESET, model=kind, tools=list(env.tools.tools))
        if kind == "oracle":
            apply_solution(task_id, env.root)
        call = ToolCall("ref-submit", "submit", {})
        outcome = env.tools.call(call.name, call.arguments)
        log.tool_result(1, call, outcome, 0.0)
        result = EpisodeResult("finished", env.reward(), 0, 0, outcome.claimed_success, "", 0.0)
        log.write("final", **asdict(result))
        return result
    finally:
        log.close()
        env.close()


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="harness.reference", description="Model-free null and oracle rows.")
    parser.add_argument("--task", nargs="+")
    parser.add_argument("--kind", nargs="+", choices=KINDS, default=list(KINDS))
    parser.add_argument("--out", type=Path, default=Path("runs"))
    args = parser.parse_args(argv)
    tasks = args.task or sorted(p.name for p in TASKS_DIR.iterdir() if (p / "task.json").is_file())
    for kind in args.kind:
        for task in tasks:
            path = args.out / kind / f"{episode_id(ENV_SPEC, task, PRESET, kind, 0)}.jsonl"
            if is_complete(path):
                print(f"skip {kind} {task} (already complete)")
                continue
            result = run_reference(kind, task, args.out)
            print(f"{kind} {task} reward={result.reward}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
