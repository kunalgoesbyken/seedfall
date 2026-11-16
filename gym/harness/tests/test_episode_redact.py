from types import SimpleNamespace

from harness.episode import EpisodeRunner


def test_describe_redacts_client_secret():
    runner = EpisodeRunner.__new__(EpisodeRunner)
    runner.client = SimpleNamespace(redact="sk-secret-value")
    assert runner._describe(RuntimeError("bad Bearer sk-secret-value")) == "bad Bearer ***"


def test_describe_without_secret_passes_text():
    runner = EpisodeRunner.__new__(EpisodeRunner)
    runner.client = SimpleNamespace()
    assert runner._describe(ValueError("plain")) == "plain"
