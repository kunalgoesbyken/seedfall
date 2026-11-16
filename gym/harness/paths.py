from pathlib import Path


def config_dir() -> Path:
    return Path.home() / ".config" / "seedfall-gym"


def slug(text: str) -> str:
    return "".join(ch if ch.isalnum() or ch in "-_." else "_" for ch in text)
