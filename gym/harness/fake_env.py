import hashlib
import shutil
import tempfile
from pathlib import Path

from harness.tools import Outcome, Tool, ToolBox, string_params
from harness.workspace import workspace_tools

SECRET_FILE = "secret.txt"


class ReadFileEnv:
    system_prompt = "You are a careful assistant working in a small workspace. Use the tools to act."

    def __init__(self, task_id: str, episode: int = 0):
        self.root = Path(tempfile.mkdtemp(prefix="harness-fake-"))
        self.secret = "token-" + hashlib.sha256(f"{task_id}:{episode}".encode()).hexdigest()[:12]
        (self.root / SECRET_FILE).write_text(self.secret + "\n", encoding="utf-8")
        self.submitted: str | None = None
        self.task_prompt = f"Read {SECRET_FILE} in the workspace, then call submit with its exact content, without surrounding whitespace."
        submit = Tool("submit", "Submit the final answer and end the episode.", string_params({"content": "the answer"}), self._submit)
        self.tools = ToolBox([*workspace_tools(self.root), submit])

    def _submit(self, content: str) -> Outcome:
        self.submitted = content
        return Outcome("submitted", finished=True, claimed_success=True)

    def reward(self) -> float:
        return 1.0 if self.submitted == self.secret else 0.0

    def close(self) -> None:
        shutil.rmtree(self.root, ignore_errors=True)
