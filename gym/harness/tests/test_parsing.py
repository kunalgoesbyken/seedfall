import pytest

from harness.claims import claimed_success, text_claims_success
from harness.parsing import parse_native_calls, parse_text_calls, strip_thoughts

KNOWN = {"read_file", "submit"}


@pytest.mark.parametrize(
    "raw, expected",
    [
        ("<thought>plan it</thought>answer", "answer"),
        ("<think>a</think> x <think>b</think>y", "x y"),
        ("<thought>never closed and cut off", ""),
        ("before <|channel>thought deep<channel|>after", "before after"),
        ("no tags here", "no tags here"),
        ("  padded  ", "padded"),
    ],
)
def test_strip_thoughts(raw, expected):
    assert strip_thoughts(raw) == expected


def test_text_calls_from_tags():
    content = 'ok <tool_call>{"name": "read_file", "arguments": {"path": "a"}}</tool_call>'
    calls = parse_text_calls(content, KNOWN, turn=2)
    assert [(c.name, c.arguments, c.id) for c in calls] == [("read_file", {"path": "a"}, "call_2_0")]


def test_text_calls_multiple_and_fenced():
    content = (
        '<tool_call>```json\n{"name": "read_file", "parameters": {"path": "a"}}\n```</tool_call>'
        '<tool_call>{"name": "submit", "arguments": {"content": "x"}}</tool_call>'
    )
    assert [c.name for c in parse_text_calls(content, KNOWN, 1)] == ["read_file", "submit"]


def test_text_calls_bare_json_and_rejections():
    assert parse_text_calls('{"name": "submit", "arguments": {"content": "x"}}', KNOWN, 1)[0].name == "submit"
    assert parse_text_calls('{"name": "rm_rf", "arguments": {}}', KNOWN, 1) == []
    assert parse_text_calls("just prose", KNOWN, 1) == []
    assert parse_text_calls("<tool_call>{broken", KNOWN, 1) == []


def test_native_calls_parse_arguments_and_fill_missing_ids():
    raw = [{"type": "function", "function": {"name": "read_file", "arguments": '{"path": "a"}'}}]
    call = parse_native_calls(raw, turn=3)[0]
    assert call.arguments == {"path": "a"} and call.id == "call_3_0"
    assert call.to_openai()["id"] == "call_3_0"


def test_native_calls_preserve_provider_extras():
    raw = [{"id": "x", "type": "function", "extra_content": {"google": {"thought_signature": "s"}}, "function": {"name": "submit", "arguments": "{}"}}]
    assert parse_native_calls(raw, 1)[0].to_openai()["extra_content"]["google"]["thought_signature"] == "s"


@pytest.mark.parametrize("arguments", ["{not json", "[1, 2]"])
def test_native_calls_flag_bad_arguments(arguments):
    raw = [{"id": "x", "function": {"name": "submit", "arguments": arguments}}]
    assert parse_native_calls(raw, 1)[0].error


@pytest.mark.parametrize(
    "text, expected",
    [
        ("I fixed the bug and the tests pass.", True),
        ("Submitted successfully.", True),
        ("I could not get this fixed.", False),
        ("The bug is not resolved", False),
        ("I'm unable to fix it", False),
        ("Still investigating the crash", False),
        ("", False),
        (None, False),
    ],
)
def test_text_claims(text, expected):
    assert text_claims_success(text) is expected


def test_claim_combines_tool_and_text():
    assert claimed_success([True], "")
    assert claimed_success([False], "all done")
    assert not claimed_success([False], "giving up")
