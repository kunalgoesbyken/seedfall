import json
from dataclasses import dataclass

THOUGHT_TAGS = (("<thought>", "</thought>"), ("<think>", "</think>"), ("<|channel>thought", "<channel|>"))
CALL_OPEN = "<tool_call>"
CALL_CLOSE = "</tool_call>"
FENCE = "```"


@dataclass(frozen=True)
class ToolCall:
    id: str
    name: str
    arguments: dict
    error: str | None = None
    raw: dict | None = None

    def to_openai(self) -> dict:
        if self.raw is not None:
            return self.raw
        return {"id": self.id, "type": "function", "function": {"name": self.name, "arguments": json.dumps(self.arguments)}}


def strip_thoughts(text: str) -> str:
    for opening, closing in THOUGHT_TAGS:
        while (start := text.find(opening)) != -1:
            end = text.find(closing, start + len(opening))
            text = text[:start] + (text[end + len(closing) :] if end != -1 else "")
    return text.strip()


def _json_object(body: str) -> dict | None:
    body = body.strip()
    if body.startswith(FENCE):
        body = body.removeprefix(FENCE).removeprefix("json").removesuffix(FENCE).strip()
    try:
        value = json.loads(body)
    except json.JSONDecodeError:
        return None
    return value if isinstance(value, dict) else None


def _as_call(obj: dict | None, known: set[str], call_id: str) -> ToolCall | None:
    if not obj or obj.get("name") not in known:
        return None
    arguments = next((obj[key] for key in ("arguments", "parameters", "args") if key in obj), {})
    if not isinstance(arguments, dict):
        return ToolCall(call_id, obj["name"], {}, error="arguments must be a JSON object")
    return ToolCall(call_id, obj["name"], arguments)


def parse_text_calls(content: str, known: set[str], turn: int) -> list[ToolCall]:
    bodies, rest = [], content
    while (start := rest.find(CALL_OPEN)) != -1:
        end = rest.find(CALL_CLOSE, start)
        if end == -1:
            break
        bodies.append(rest[start + len(CALL_OPEN) : end])
        rest = rest[end + len(CALL_CLOSE) :]
    if not bodies:
        bodies = [content]
    calls = (_as_call(_json_object(body), known, f"call_{turn}_{i}") for i, body in enumerate(bodies))
    return [call for call in calls if call]


def parse_native_calls(tool_calls: list[dict], turn: int) -> list[ToolCall]:
    calls = []
    for index, raw in enumerate(tool_calls):
        call_id = raw.get("id") or f"call_{turn}_{index}"
        function = raw.get("function", {})
        arguments = function.get("arguments") or "{}"
        error = None
        if isinstance(arguments, str):
            try:
                arguments = json.loads(arguments)
            except json.JSONDecodeError:
                arguments, error = {}, "arguments are not valid JSON"
        if not isinstance(arguments, dict):
            arguments, error = {}, "arguments must be a JSON object"
        calls.append(ToolCall(call_id, function.get("name", ""), arguments, error, {**raw, "id": call_id}))
    return calls


def text_tool_prompt(schemas: list[dict]) -> str:
    specs = "\n".join(json.dumps(schema["function"]) for schema in schemas)
    return (
        "You can call tools. To call one, reply with "
        f'{CALL_OPEN}{{"name": "<tool name>", "arguments": {{...}}}}{CALL_CLOSE} '
        "and nothing else. Tool results come back in the next message.\n"
        f"Available tools, one JSON spec per line:\n{specs}"
    )
