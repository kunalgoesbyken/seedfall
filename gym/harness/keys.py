import os
from pathlib import Path

from harness.errors import MissingKey
from harness.paths import config_dir


def default_env_file() -> Path:
    return config_dir() / ".env"


def parse_env_file(path: Path) -> dict[str, str]:
    if not path.is_file():
        return {}
    values = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip().removeprefix("export ").strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        name, _, value = line.partition("=")
        values[name.strip()] = value.strip().strip("\"'")
    return values


def load_key(name: str, env_file: Path | None = None) -> str:
    value = os.environ.get(name) or parse_env_file(env_file or default_env_file()).get(name)
    if not value:
        raise MissingKey(f"{name} is not set in the environment or the env file")
    return value
