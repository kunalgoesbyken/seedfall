# Using seedfall from AI coding agents

`seedfall mcp` serves seedfall's core operations over the [Model Context Protocol](https://modelcontextprotocol.io) on stdio. An agent such as Claude Code, Cursor or Codex can then sweep seeds, replay a failure and shrink it on its own, then use the exact fault schedule and trace to fix the bug and prove the fix. No API key and no hosted service are involved: the agent you already use does the reasoning.

## Setup

Install the binary:

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
```

Claude Code:

```sh
claude mcp add seedfall -- seedfall mcp
```

Cursor, or any client that reads an `mcpServers` file:

```json
{
  "mcpServers": {
    "seedfall": { "command": "seedfall", "args": ["mcp"] }
  }
}
```

Config paths in tool calls are resolved against the server's working directory, which is normally your project root.

## Tools

| Tool | Arguments | Returns |
|---|---|---|
| `seedfall_run` | `config`, `seeds` (for example `1-200`), optional `check` | Seeds passed, the lowest failing seed with its reason, the faults before the failure, the trace tail and a replay command, plus property coverage |
| `seedfall_replay` | `config`, `seed`, optional `faults` file | The same failure view for one seed, or the trace tail if it passes |
| `seedfall_shrink` | `config`, `seed` | The original and minimal fault counts, the path of the saved minimal schedule, and the failure view under that schedule |
| `seedfall_cause` | `config`, `seed`, optional `points`, `branches` | The probability of failing from each moment of the run, with the faults between moments, so the agent can see which event made the failure inevitable ([cause.md](cause.md)) |

Every result is structured JSON, so the agent can read the seed, the faults and the trace directly.

## A verification loop

Ask the agent to treat a seed sweep as the definition of done for storage or replication changes:

1. Run `seedfall_run` over a few hundred seeds before changing anything, to record the baseline.
2. When a seed fails, run `seedfall_shrink` and read the minimal fault schedule. It usually names the exact window: "crash during disk op #56", then "crash". `seedfall_cause` shows which of those faults made the failure likely.
3. Fix the code, then `seedfall_replay` the saved schedule until it passes.
4. Sweep a fresh seed range, ideally with `check: true`, and include the seed range and result in the pull request.

Shrunk schedules are small enough that an agent can reason about them step by step. `examples/kvlog` is a good first exercise: its `seedfall.always.json` shrinks to two faults.
