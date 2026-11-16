from harness.parsing import ToolCall, text_tool_prompt

NATIVE = "native"
TEXT = "text"
EMPTY_REPLY = "(empty)"


class Transcript:
    def __init__(self, mode: str, system: str, task: str, schemas: list[dict]):
        self.mode = mode
        self.schemas = schemas
        system_text = system if mode == NATIVE else f"{system}\n\n{text_tool_prompt(schemas)}"
        self.messages = [{"role": "system", "content": system_text}, {"role": "user", "content": task}]

    @property
    def tool_schemas(self) -> list[dict] | None:
        return self.schemas if self.mode == NATIVE and self.schemas else None

    def add_assistant(self, content: str, calls: list[ToolCall]) -> dict:
        structured = bool(calls) and self.mode == NATIVE
        message = {"role": "assistant", "content": (content or None) if structured else (content or EMPTY_REPLY)}
        if structured:
            message["tool_calls"] = [call.to_openai() for call in calls]
        self.messages.append(message)
        return message

    def add_results(self, results: list[tuple[ToolCall, str]]) -> list[dict]:
        if self.mode == NATIVE:
            added = [{"role": "tool", "tool_call_id": call.id, "content": text} for call, text in results]
        else:
            body = "\n".join(f'<tool_result name="{call.name}">\n{text}\n</tool_result>' for call, text in results)
            added = [{"role": "user", "content": body}]
        self.messages.extend(added)
        return added

    def add_user(self, text: str) -> dict:
        message = {"role": "user", "content": text}
        self.messages.append(message)
        return message
