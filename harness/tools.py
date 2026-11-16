from dataclasses import dataclass
from typing import Any, Callable, Protocol


@dataclass(frozen=True)
class Outcome:
    text: str
    finished: bool = False
    claimed_success: bool = False


@dataclass(frozen=True)
class Tool:
    name: str
    description: str
    parameters: dict
    handler: Callable[..., Any]

    def schema(self) -> dict:
        return {
            "type": "function",
            "function": {"name": self.name, "description": self.description, "parameters": self.parameters},
        }


class ToolSet(Protocol):
    def schemas(self) -> list[dict]: ...

    def call(self, name: str, arguments: dict) -> Outcome: ...


class Env(Protocol):
    system_prompt: str
    task_prompt: str
    tools: ToolSet

    def reward(self) -> float: ...

    def close(self) -> None: ...


def string_params(descriptions: dict[str, str], optional: tuple[str, ...] = ()) -> dict:
    return {
        "type": "object",
        "properties": {name: {"type": "string", "description": text} for name, text in descriptions.items()},
        "required": [name for name in descriptions if name not in optional],
    }


class ToolBox:
    def __init__(self, tools: list[Tool]):
        self.tools = {tool.name: tool for tool in tools}

    def schemas(self) -> list[dict]:
        return [tool.schema() for tool in self.tools.values()]

    def call(self, name: str, arguments: dict) -> Outcome:
        tool = self.tools.get(name)
        if tool is None:
            return Outcome(f"error: unknown tool {name!r}; available: {sorted(self.tools)}")
        try:
            result = tool.handler(**arguments)
        except TypeError as exc:
            return Outcome(f"error: bad arguments for {name}: {exc}")
        return result if isinstance(result, Outcome) else Outcome(str(result))
