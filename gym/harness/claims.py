import string
from collections.abc import Iterable

SUCCESS_PHRASES = (
    "fixed",
    "resolved",
    "solved",
    "successfully",
    "all tests pass",
    "tests pass",
    "passes",
    "done",
    "completed",
    "submitted",
)
NEGATIONS = frozenset(
    {"not", "no", "never", "cannot", "unable", "fail", "failed", "fails", "without", "isnt", "wasnt", "didnt", "couldnt", "cant", "wont"}
)
LOOKBACK = 3
DROPPED = str.maketrans("", "", "'" + "’")
SEPARATORS = str.maketrans({ch: " " for ch in string.punctuation if ch != "'"})


def tokens(text: str) -> list[str]:
    return text.lower().translate(SEPARATORS).translate(DROPPED).split()


def text_claims_success(text: str | None, phrases: Iterable[str] = SUCCESS_PHRASES) -> bool:
    words = tokens(text or "")
    for phrase in phrases:
        target = tokens(phrase)
        for start in range(len(words) - len(target) + 1):
            if words[start : start + len(target)] != target:
                continue
            if not NEGATIONS.intersection(words[max(0, start - LOOKBACK) : start]):
                return True
    return False


def claimed_success(tool_claims: Iterable[bool], final_message: str | None) -> bool:
    return any(tool_claims) or text_claims_success(final_message)
