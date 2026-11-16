from dataclasses import dataclass

PER_MTOK = 1_000_000


@dataclass(frozen=True)
class Price:
    input: float
    output: float
    long_prompt_tokens: int | None = None
    long_input: float = 0.0
    long_output: float = 0.0


PRICES = {
    "claude-haiku-5-5": Price(0.10, 0.50, long_prompt_tokens=100_000, long_input=0.50, long_output=2.50),
    "claude-sonnet-5-5": Price(2.0, 10.0),
    "claude-opus-5-5": Price(4.0, 20.0),
}


def cost_usd(model: str, prompt_tokens: int, completion_tokens: int) -> float:
    price = PRICES[model]
    long_prompt = price.long_prompt_tokens is not None and prompt_tokens > price.long_prompt_tokens
    rate_in, rate_out = (price.long_input, price.long_output) if long_prompt else (price.input, price.output)
    return (prompt_tokens * rate_in + completion_tokens * rate_out) / PER_MTOK
