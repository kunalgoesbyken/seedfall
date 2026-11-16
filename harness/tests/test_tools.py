import pytest

from harness.fake_env import ReadFileEnv
from harness.tools import Outcome, Tool, ToolBox, string_params
from harness.workspace import Workspace, workspace_tools


def echo(text: str) -> str:
    return text.upper()


def finish(note: str) -> Outcome:
    return Outcome(note, finished=True, claimed_success=True)


@pytest.fixture
def box():
    return ToolBox(
        [
            Tool("echo", "echo", string_params({"text": "t"}), echo),
            Tool("finish", "finish", string_params({"note": "n"}), finish),
        ]
    )


def test_schemas_are_openai_function_specs(box):
    schemas = box.schemas()
    assert [s["function"]["name"] for s in schemas] == ["echo", "finish"]
    assert schemas[0]["type"] == "function"
    assert schemas[0]["function"]["parameters"]["required"] == ["text"]


def test_dispatch_wraps_strings_and_passes_outcomes(box):
    assert box.call("echo", {"text": "a"}) == Outcome("A")
    assert box.call("finish", {"note": "ok"}).finished


def test_dispatch_reports_unknown_tool_and_bad_arguments(box):
    assert box.call("nope", {}).text.startswith("error: unknown tool")
    assert box.call("echo", {}).text.startswith("error: bad arguments")
    assert box.call("echo", {"text": "a", "extra": 1}).text.startswith("error: bad arguments")


def test_optional_parameters_are_not_required():
    assert string_params({"a": "x", "b": "y"}, optional=("b",))["required"] == ["a"]


def test_workspace_edit_requires_unique_match(tmp_path):
    ws = Workspace(tmp_path)
    ws.write_file("a.txt", "one two one")
    assert "found 2" in ws.edit_file("a.txt", "one", "1")
    assert "found 0" in ws.edit_file("a.txt", "three", "3")
    assert ws.edit_file("a.txt", "two", "2") == "edited a.txt"
    assert ws.read_file("a.txt") == "one 2 one"


def test_workspace_rejects_escape(tmp_path):
    with pytest.raises(ValueError):
        Workspace(tmp_path).read_file("../outside.txt")


def test_workspace_lists_nested_files(tmp_path):
    ws = Workspace(tmp_path)
    ws.write_file("pkg/a.go", "x")
    ws.write_file("b.go", "y")
    assert ws.list_files().splitlines() == ["b.go", "pkg/a.go"]


def test_workspace_tool_names(tmp_path):
    assert [t.name for t in workspace_tools(tmp_path)] == ["list_files", "read_file", "write_file", "edit_file"]


def test_fake_env_rewards_exact_secret():
    env = ReadFileEnv("t", 0)
    try:
        secret = env.tools.call("read_file", {"path": "secret.txt"}).text.strip()
        env.tools.call("submit", {"content": secret + " "})
        assert env.reward() == 0.0
        env.tools.call("submit", {"content": secret})
        assert env.reward() == 1.0
    finally:
        env.close()
