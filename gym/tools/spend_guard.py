import json
import os
import sys
import urllib.request
from pathlib import Path

ENV_FILE = Path.home() / ".config" / "seedfall-gym" / ".env"
LEDGER = Path.home() / ".config" / "seedfall-gym" / "spend.json"
CAPS = {"anthropic_usd": float(os.environ.get("SFG_ANTHROPIC_CAP_USD", "40")), "openrouter_usd": 0.0}


def load_env():
    if ENV_FILE.exists():
        for line in ENV_FILE.read_text().splitlines():
            key, _, value = line.partition("=")
            if key and value:
                os.environ.setdefault(key.strip(), value.strip())


def openrouter_usage():
    key = os.environ.get("OPENROUTER_API_KEY")
    if not key:
        return None
    req = urllib.request.Request("https://openrouter.ai/api/v1/key", headers={"Authorization": f"Bearer {key}"})
    data = json.load(urllib.request.urlopen(req, timeout=20))["data"]
    return {"usd": data["usage"], "limit": data["limit"], "free_tier": data["is_free_tier"], "free_requests_left": data.get("free_model_daily_requests", {}).get("remaining")}


def anthropic_spend():
    if not LEDGER.exists():
        return 0.0
    return float(json.loads(LEDGER.read_text()).get("anthropic_usd", 0.0))


def main():
    load_env()
    report = {"openrouter": openrouter_usage(), "anthropic_usd": anthropic_spend(), "caps": CAPS, "gemini": "free tier, no billing account"}
    problems = []
    if report["openrouter"] and report["openrouter"]["usd"] > CAPS["openrouter_usd"]:
        problems.append("openrouter spend above $0")
    if report["anthropic_usd"] >= CAPS["anthropic_usd"]:
        problems.append("anthropic local cap reached")
    report["ok"] = not problems
    report["problems"] = problems
    print(json.dumps(report, indent=1))
    sys.exit(0 if report["ok"] else 1)


if __name__ == "__main__":
    main()
