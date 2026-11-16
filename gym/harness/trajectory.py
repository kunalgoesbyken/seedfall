import hashlib
import json
import time
from pathlib import Path

from harness.errors import STOP_STATUSES


def episode_id(env_spec: str, task: str, preset: str, model: str, index: int) -> str:
    digest = hashlib.sha256("\0".join([env_spec, task, preset, model, str(index)]).encode("utf-8")).hexdigest()
    return f"ep-{digest[:16]}"


def truncate(text: str, limit: int) -> tuple[str, bool]:
    return (text, False) if len(text) <= limit else (text[:limit], True)


class TrajectoryLog:
    def __init__(self, path: Path, log_chars: int = 2000):
        self.path = Path(path)
        self.log_chars = log_chars
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._file = self.path.open("w", encoding="utf-8")

    def write(self, kind: str, **fields) -> None:
        self._file.write(json.dumps({"type": kind, "ts": time.time(), **fields}, ensure_ascii=False) + "\n")
        self._file.flush()

    def message(self, message: dict) -> None:
        self.write("message", **message)

    def tool_result(self, turn: int, call, outcome, latency: float) -> None:
        text, truncated = truncate(outcome.text, self.log_chars)
        self.write(
            "tool_result",
            turn=turn,
            tool_call_id=call.id,
            name=call.name,
            arguments=call.arguments,
            text=text,
            truncated=truncated,
            full_chars=len(outcome.text),
            finished=outcome.finished,
            claimed_success=outcome.claimed_success,
            latency_s=round(latency, 4),
        )

    def close(self) -> None:
        self._file.close()


def read_events(path: Path) -> list[dict]:
    with Path(path).open(encoding="utf-8") as handle:
        return [json.loads(line) for line in handle if line.strip()]


def final_event(path: Path) -> dict | None:
    events = read_events(path) if Path(path).is_file() else []
    return next((event for event in reversed(events) if event["type"] == "final"), None)


def is_complete(path: Path) -> bool:
    final = final_event(path)
    return final is not None and final["status"] not in STOP_STATUSES
