import json
import os
import re
import secrets
import shutil
import subprocess
import tempfile
from dataclasses import dataclass
from pathlib import Path

from harness.paths import config_dir
from harness.tools import Outcome, Tool, ToolBox, string_params
from harness.workspace import workspace_tools

REPO = Path(__file__).resolve().parent.parent
TASKS_ENV = "SEEDFALL_GYM_TASKS"
GRADE_ENV = "SEEDFALL_GYM_GRADE"
TASKS_DIR = Path(os.environ.get(TASKS_ENV) or REPO / "tasks")
SALT_ENV = "SEEDFALL_GYM_SALT"
SRC_ENV = "SEEDFALL_SRC"
MAX_VISIBLE_SEEDS = 4
MAX_SMALL_SEED = 1000
MAX_RUN_CALLS = 12
TRACE_LINES = 20
TRACE_LINE_CHARS = 200
REASON_CHARS = 400
RUN_TIMEOUT_S = 300.0
GRADE_TIMEOUT_S = 900.0
NOISE = "event gym.ack"

SYSTEM_PROMPT = """You are an engineer fixing a bug in a small Go system. The system runs inside a deterministic simulator that injects faults (crashes, torn and reordered disk writes, failed fsyncs, network partitions, connection resets). A fixed client workload talks to your code and checks the system's guarantees at the end.

You work in a workspace through tools. You cannot run a shell or the compiler yourself; the only way to execute the system is run_seeds, which builds your code and replays the simulator on the given seeds.

Rules:
- Edit only the files under the editable paths given in the task. Changes anywhere else are ignored.
- The workload, client and fault configuration are fixed and not visible to you.
- Keep the system working: it must still serve its normal operations. A server that refuses or drops writes does not count as a fix.
- Your final score is the fraction of a large set of hidden seeds on which the system survives, not the visible seeds. Fix the underlying problem in general; do not special-case seeds.
- Call submit when you are done."""

TASK_TEMPLATE = """Task: {task_id}
Editable paths (relative to the workspace root): {editable}

{symptom}

Tools: list_files, read_file, write_file, edit_file for the workspace; run_seeds(seeds) to replay up to {max_seeds} seeds (integers from 0 to {max_small}; seed {visible} reproduces the failure above) and see pass or fail with a reason and the end of the trace; submit() to finish."""


class GradeError(Exception):
    pass


class GradeTimeout(GradeError):
    pass


def load_salt(path: Path | None = None) -> str:
    path = path or config_dir() / "salt"
    if not path.is_file():
        path.parent.mkdir(parents=True, exist_ok=True)
        try:
            with path.open("x", encoding="utf-8") as handle:
                handle.write(secrets.token_hex(32))
        except FileExistsError:
            pass
    return path.read_text(encoding="utf-8").strip()


def grade_binary() -> Path:
    if os.environ.get(GRADE_ENV):
        return Path(os.environ[GRADE_ENV])
    return REPO / "bin" / ("grade.exe" if os.name == "nt" else "grade")


def seedfall_src() -> str | None:
    configured = os.environ.get(SRC_ENV)
    if configured:
        return configured
    sibling = REPO.parent / "seedfall"
    return str(sibling) if sibling.is_dir() else None


def scrubbed_env(extra: dict[str, str] | None = None) -> dict[str, str]:
    env = {name: value for name, value in os.environ.items() if name != SALT_ENV and not name.endswith("_API_KEY")}
    src = seedfall_src()
    if src:
        env[SRC_ENV] = src
    return {**env, **(extra or {})}


def kill_tree(process: subprocess.Popen) -> None:
    if os.name == "nt":
        subprocess.run(["taskkill", "/F", "/T", "/PID", str(process.pid)], capture_output=True)
    else:
        process.kill()


def run_bounded(command: list[str], env: dict[str, str], timeout: float) -> tuple[int, str, str]:
    process = subprocess.Popen(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace")
    try:
        out, err = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired:
        kill_tree(process)
        process.communicate()
        raise GradeTimeout(f"grade did not finish within {timeout:g}s") from None
    return process.returncode, out, err


def parse_report(returncode: int, out: str, err: str) -> dict:
    try:
        report = json.loads(out)
    except json.JSONDecodeError:
        raise GradeError(f"grade exited {returncode} without a report: {err.strip()[:REASON_CHARS]}") from None
    if not isinstance(report, dict):
        raise GradeError("grade report is not an object")
    return report


def parse_seeds(raw, visible: int) -> list[int]:
    if isinstance(raw, (int, float)) and not isinstance(raw, bool):
        raw = [raw]
    if isinstance(raw, str):
        raw = re.split(r"[\s,;]+", raw.strip().strip("[]()"))
    items = [item for item in (raw or []) if str(item).strip() != ""]
    if not items:
        raise ValueError("pass at least one seed, for example seeds=\"1,2\"")
    seeds = []
    for item in items:
        text = str(item).strip()
        if not re.fullmatch(r"\d{1,19}", text):
            raise ValueError(f"seed {text!r} is not a non-negative integer")
        seed = int(text)
        if seed != visible and seed > MAX_SMALL_SEED:
            raise ValueError(f"seed {seed} is not allowed; use integers from 0 to {MAX_SMALL_SEED}")
        if seed not in seeds:
            seeds.append(seed)
    if len(seeds) > MAX_VISIBLE_SEEDS:
        raise ValueError(f"at most {MAX_VISIBLE_SEEDS} seeds per call")
    return seeds


def trace_tail(lines: list[str]) -> list[str]:
    kept = [line for line in lines if NOISE not in line][-TRACE_LINES:]
    return [line[:TRACE_LINE_CHARS] for line in kept]


def format_seeds(report: dict, seeds: list[int]) -> str:
    if not report.get("build_ok", True):
        return f"build failed:\n{str(report.get('build_error', ''))[:2000]}"
    lines = []
    results = {entry["seed"]: entry for entry in report.get("per_seed", [])}
    for seed in seeds:
        entry = results.get(seed)
        if entry is None:
            lines.append(f"seed {seed}: no result")
        elif entry["passed"]:
            lines.append(f"seed {seed}: PASS (acks {entry.get('acks', 0)})")
        else:
            lines.append(f"seed {seed}: FAIL [{entry.get('class', '')}] {str(entry.get('reason', ''))[:REASON_CHARS]} (acks {entry.get('acks', 0)}, faults {len(entry.get('faults') or [])})")
            tail = trace_tail(entry.get("trace_tail") or [])
            if tail:
                lines.append("trace tail:")
                lines.extend(tail)
    guard = report.get("guard") or {}
    if guard.get("tripped"):
        lines.append("guard tripped (the workload made too little progress or results were not deterministic): " + json.dumps(guard)[:REASON_CHARS])
    return "\n".join(lines)


@dataclass(frozen=True)
class GradeConfig:
    grade: tuple[str, ...]
    tasks: Path = TASKS_DIR
    salt: str | None = None
    run_timeout_s: float = RUN_TIMEOUT_S
    grade_timeout_s: float = GRADE_TIMEOUT_S


class GymEnv:
    def __init__(self, task_id: str, episode: int = 0, cfg: GradeConfig | None = None):
        self.cfg = cfg or GradeConfig((str(grade_binary()),))
        self.task_id = task_id
        self.episode = episode
        self.task_dir = self.cfg.tasks / task_id
        if not (self.task_dir / "task.json").is_file():
            raise ValueError(f"unknown task {task_id!r}")
        spec = json.loads((self.task_dir / "task.json").read_text(encoding="utf-8"))
        self.visible = int((self.task_dir / "visible_seed").read_text(encoding="utf-8").strip())
        self.root = Path(tempfile.mkdtemp(prefix=f"gym-{task_id}-"))
        shutil.copytree(self.task_dir / "workspace", self.root, dirs_exist_ok=True)
        self.submitted = False
        self.run_calls = 0
        self.system_prompt = SYSTEM_PROMPT
        self.task_prompt = TASK_TEMPLATE.format(
            task_id=task_id,
            editable=", ".join(spec["editable"]),
            symptom=spec["symptom"],
            max_seeds=MAX_VISIBLE_SEEDS,
            max_small=MAX_SMALL_SEED,
            visible=self.visible,
        )
        run = Tool("run_seeds", f"Build the workspace and replay up to {MAX_VISIBLE_SEEDS} seeds in the simulator. Returns pass or fail per seed with the reason and the end of the trace.", string_params({"seeds": "comma separated integer seeds, for example 1,2"}), self._run_seeds)
        submit = Tool("submit", "Finish the episode once you believe the bug is fixed.", {"type": "object", "properties": {}}, self._submit)
        self.tools = ToolBox([*workspace_tools(self.root), run, submit])

    def _command(self, *extra: str) -> list[str]:
        return [*self.cfg.grade, "-task", str(self.task_dir), "-workspace", str(self.root), *extra]

    def _run_seeds(self, seeds="") -> Outcome:
        if self.run_calls >= MAX_RUN_CALLS:
            return Outcome(f"error: run_seeds is limited to {MAX_RUN_CALLS} calls per episode; call submit when ready")
        try:
            wanted = parse_seeds(seeds, self.visible)
        except ValueError as exc:
            return Outcome(f"error: {exc}")
        self.run_calls += 1
        command = self._command("-seeds", ",".join(map(str, wanted)))
        try:
            report = parse_report(*run_bounded(command, scrubbed_env(), self.cfg.run_timeout_s))
        except GradeTimeout as exc:
            return Outcome(f"error: {exc}; the code may hang or loop")
        except GradeError as exc:
            return Outcome(f"error: {exc}")
        return Outcome(format_seeds(report, wanted))

    def _submit(self) -> Outcome:
        self.submitted = True
        return Outcome("submitted", finished=True, claimed_success=True)

    def reward(self) -> float:
        salt = self.cfg.salt or load_salt()
        try:
            report = parse_report(*run_bounded(self._command(), scrubbed_env({SALT_ENV: salt}), self.cfg.grade_timeout_s))
        except GradeTimeout:
            return 0.0
        except GradeError as exc:
            raise GradeError(str(exc).replace(salt, "***")) from None
        return float(report["reward"])

    def close(self) -> None:
        shutil.rmtree(self.root, ignore_errors=True)


def Factory(task_id: str, episode: int = 0) -> GymEnv:
    return GymEnv(task_id, episode)
