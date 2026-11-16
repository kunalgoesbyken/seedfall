import os
from dataclasses import dataclass, field

from harness.pricing import PRICES


@dataclass(frozen=True)
class Limits:
    rpm: int
    daily: int


@dataclass(frozen=True)
class Preset:
    name: str
    base_url: str | None
    key_env: str
    default_limits: Limits
    model_limits: dict[str, Limits] = field(default_factory=dict)
    model_suffix: str | None = None
    reset_utc_offset: int = 0
    key_required: bool = True
    usd_cap: float | None = None


@dataclass(frozen=True)
class Endpoint:
    preset: str
    model: str
    base_url: str
    key_env: str
    key_required: bool
    limits: Limits
    reset_utc_offset: int
    usd_cap: float | None


USD_CAP_ENV = "SFG_ANTHROPIC_CAP_USD"
DEFAULT_USD_CAP = 40.0

PRESETS = {
    "gemini": Preset(
        name="gemini",
        base_url="https://generativelanguage.googleapis.com/v1beta/openai/",
        key_env="GEMINI_API_KEY",
        default_limits=Limits(5, 100),
        model_limits={
            "gemini-3.5-flash": Limits(10, 250),
            "gemini-3.1-flash-lite": Limits(15, 500),
            "gemma-4-31b-it": Limits(15, 1500),
        },
        reset_utc_offset=-8,
    ),
    "openrouter": Preset(
        name="openrouter",
        base_url="https://openrouter.ai/api/v1",
        key_env="OPENROUTER_API_KEY",
        default_limits=Limits(20, 50),
        model_suffix=":free",
    ),
    "anthropic": Preset(
        name="anthropic",
        base_url="https://api.anthropic.com/v1/",
        key_env="ANTHROPIC_API_KEY",
        default_limits=Limits(50, 100_000),
        usd_cap=DEFAULT_USD_CAP,
    ),
    "local": Preset(
        name="local",
        base_url=None,
        key_env="LOCAL_API_KEY",
        default_limits=Limits(600, 1_000_000),
        key_required=False,
    ),
}


def resolve(
    preset_name: str,
    model: str,
    base_url: str | None = None,
    key_env: str | None = None,
    rpm: int | None = None,
    daily: int | None = None,
) -> Endpoint:
    if preset_name not in PRESETS:
        raise ValueError(f"unknown preset {preset_name!r}; choose from {sorted(PRESETS)}")
    preset = PRESETS[preset_name]
    if preset.model_suffix and not model.endswith(preset.model_suffix):
        raise ValueError(f"preset {preset.name} only allows models ending with {preset.model_suffix}")
    if preset.usd_cap and model not in PRICES:
        raise ValueError(f"no price known for {model}; add it to harness/pricing.py")
    url = base_url or preset.base_url
    if not url:
        raise ValueError(f"preset {preset.name} needs --base-url")
    limits = preset.model_limits.get(model, preset.default_limits)
    return Endpoint(
        preset=preset.name,
        model=model,
        base_url=url,
        key_env=key_env or preset.key_env,
        key_required=preset.key_required,
        limits=Limits(rpm or limits.rpm, daily or limits.daily),
        reset_utc_offset=preset.reset_utc_offset,
        usd_cap=float(os.environ.get(USD_CAP_ENV, preset.usd_cap)) if preset.usd_cap else None,
    )
