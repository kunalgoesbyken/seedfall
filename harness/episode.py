import time
from dataclasses import asdict, dataclass

from harness.claims import claimed_success
from harness.errors import STOP_STATUSES, StopRun
from harness.parsing import ToolCall, parse_native_calls, parse_text_calls, strip_thoughts
from harness.tools import Env, Outcome
from harness.trajectory import TrajectoryLog, truncate
from harness.transcript import NATIVE, Transcript

NUDGE = "You did not call a tool. Call one of the available tools; when you are finished, call the final submit or finish tool."
MIN_REQUEST_TIMEOUT = 1.0


@dataclass(frozen=True)
class EpisodeConfig:
    max_turns: int = 30
    max_total_tokens: int = 200_000
    max_completion_tokens: int | None = 4096
    timeout_s: float = 600.0
    request_timeout_s: float = 120.0
    tool_mode: str = NATIVE
    temperature: float | None = None
    max_tool_chars: int = 16_000
    nudge: str = NUDGE


@dataclass(frozen=True)
class EpisodeResult:
    status: str
    reward: float | None
    turns: int
    tokens: int
    claimed_success: bool
    final_message: str
    wall_s: float
    cost_usd: float = 0.0
    error: str | None = None


class EpisodeRunner:
    def __init__(self, env: Env, client, cfg: EpisodeConfig, log: TrajectoryLog, clock=time.monotonic):
        self.env = env
        self.client = client
        self.cfg = cfg
        self.log = log
        self.clock = clock
        self.transcript = Transcript(cfg.tool_mode, env.system_prompt, env.task_prompt, env.tools.schemas())
        self.known = {schema["function"]["name"] for schema in env.tools.schemas()}
        self.turns = 0
        self.tokens = 0
        self.cost = 0.0
        self.nudged = False
        self.tool_claims: list[bool] = []
        self.final_message = ""

    def run(self) -> EpisodeResult:
        started = self.clock()
        for message in self.transcript.messages:
            self.log.message(message)
        error = None
        try:
            status = self._loop(started + self.cfg.timeout_s)
        except StopRun as exc:
            status, error = exc.status, str(exc)
        except Exception as exc:
            status, error = "error", f"{type(exc).__name__}: {exc}"
        reward = None
        if status not in STOP_STATUSES and status != "error":
            reward, status, error = self._score(status)
        result = EpisodeResult(
            status=status,
            reward=reward,
            turns=self.turns,
            tokens=self.tokens,
            claimed_success=claimed_success(self.tool_claims, self.final_message),
            final_message=self.final_message,
            wall_s=round(self.clock() - started, 3),
            cost_usd=round(self.cost, 6),
            error=error,
        )
        self.log.write("final", **asdict(result))
        return result

    def _score(self, status: str):
        try:
            return float(self.env.reward()), status, None
        except Exception as exc:
            return None, "error", f"reward failed: {type(exc).__name__}: {exc}"

    def _loop(self, deadline: float) -> str:
        for _ in range(self.cfg.max_turns):
            remaining = deadline - self.clock()
            if remaining <= 0:
                return "timeout"
            stop = self._turn(min(self.cfg.request_timeout_s, max(MIN_REQUEST_TIMEOUT, remaining)))
            if stop:
                return stop
            if self.tokens >= self.cfg.max_total_tokens:
                return "max_tokens"
        return "max_turns"

    def _turn(self, request_timeout: float) -> str | None:
        self.turns += 1
        completion = self.client.complete(
            self.transcript.messages,
            self.transcript.tool_schemas,
            max_tokens=self.cfg.max_completion_tokens,
            temperature=self.cfg.temperature,
            timeout=request_timeout,
        )
        self.tokens += completion.usage["total_tokens"]
        self.cost += completion.cost_usd
        raw = completion.message.get("content") or ""
        content = strip_thoughts(raw)
        calls = self._calls(completion.message, content)
        message = self.transcript.add_assistant(content, calls)
        self.final_message = content
        self.log.message(
            {
                **message,
                "turn": self.turns,
                "usage": completion.usage,
                "latency_s": round(completion.latency, 4),
                "finish_reason": completion.finish_reason,
                "attempts": completion.attempts,
                "cost_usd": completion.cost_usd,
                "thoughts_stripped": len(content) != len(raw.strip()),
            }
        )
        if not calls:
            return self._no_call()
        self.nudged = False
        return self._execute(calls)

    def _calls(self, message: dict, content: str) -> list[ToolCall]:
        if message.get("tool_calls"):
            return parse_native_calls(message["tool_calls"], self.turns)
        return parse_text_calls(content, self.known, self.turns)

    def _no_call(self) -> str | None:
        if self.nudged:
            return "no_tool_call"
        self.nudged = True
        self.log.message(self.transcript.add_user(self.cfg.nudge))
        return None

    def _execute(self, calls: list[ToolCall]) -> str | None:
        results = []
        for call in calls:
            started = self.clock()
            outcome = self._dispatch(call)
            self.log.tool_result(self.turns, call, outcome, self.clock() - started)
            results.append((call, truncate(outcome.text, self.cfg.max_tool_chars)[0]))
            if outcome.finished:
                self.tool_claims.append(outcome.claimed_success)
                return "finished"
        self.transcript.add_results(results)
        return None

    def _dispatch(self, call: ToolCall) -> Outcome:
        if call.error:
            return Outcome(f"error: {call.error}")
        try:
            return self.env.tools.call(call.name, call.arguments)
        except Exception as exc:
            return Outcome(f"error: {type(exc).__name__}: {exc}")


def run_episode(env: Env, client, cfg: EpisodeConfig, log: TrajectoryLog, meta: dict, clock=time.monotonic) -> EpisodeResult:
    log.write("meta", **meta, config=asdict(cfg), tools=[schema["function"]["name"] for schema in env.tools.schemas()])
    return EpisodeRunner(env, client, cfg, log, clock).run()
