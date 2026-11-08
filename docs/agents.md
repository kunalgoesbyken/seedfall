# Using seedfall from AI coding agents

`seedfall mcp` serves seedfall's core operations over the [Model Context Protocol](https://modelcontextprotocol.io) on stdio. An agent such as Claude Code, Cursor or Codex can then sweep seeds, replay a failure and shrink it on its own, then use the exact fault schedule and trace to fix the bug and prove the fix. No API key and no hosted service are involved: the agent you already use does the reasoning.

## Setup

Install the binary first. Every integration below runs it:

```sh
go install github.com/kunalgoesbyken/seedfall/cmd/seedfall@latest
```

### Claude Code plugin

One command installs the MCP server, the four skills and the opt-in Stop hook:

```sh
claude plugin install seedfall --marketplace kunalgoesbyken/seedfall
```

Inside a session, `/plugin install seedfall --marketplace kunalgoesbyken/seedfall` does the same. On Claude Code versions before 2.1.275, add the marketplace first with `/plugin marketplace add kunalgoesbyken/seedfall`, then run `/plugin install seedfall@seedfall`. The skills then run as `/seedfall:seedfall-setup` and so on, and Claude also loads them on its own when a task matches.

### Skills for other agents

The skills follow the [Agent Skills](https://agentskills.io/specification) format, so any agent that reads `SKILL.md` files can use them. To install them with the [`skills` CLI](https://github.com/vercel-labs/skills):

```sh
npx skills add kunalgoesbyken/seedfall
```

### MCP server only

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
| `seedfall_init` | optional `dir`, `config`, `nodes`, `workload`, `dry_run` | The module's `main` packages and which become nodes, the packages that block a `GOOS=wasip1` build (compile errors and cgo, each with its import chain), the starter config, the files written, and the next steps. See [onboarding](#onboarding-a-new-module) |
| `seedfall_run` | `config`, `seeds` (for example `1-200`), optional `check` | Seeds passed, the lowest failing seed with its reason, [classification](reports.md#classification), the faults before the failure, the trace tail and a replay command, plus property coverage and, with `faults.buggify` on, `buggify_sites` ([buggify](buggify.md#coverage)) |
| `seedfall_replay` | `config`, `seed`, optional `faults` file | The same failure view for one seed, or the trace tail if it passes, with a `note` when a shrunk schedule's shorter duration ended the run before the workload finished. A failure view of a run that recorded operations also names its `history_file` and, on a linearizability violation, its `timeline_file` |
| `seedfall_shrink` | `config`, `seed`, optional `budget` (runs such as `300`, or time such as `10m`) | The original and minimal sizes (faults, duration and, with [`sim.Budget`](properties.md#workload-size), workload operations), the runs used, whether the budget ran out, the path of the saved schedule, and the failure view under that schedule with its replay command ([shrinking](how-it-works.md#shrinking)) |
| `seedfall_inspect` | `config`, `seed`, optional `faults`, `at`, `node`, `path` | Every node's disk at the failure or at virtual time `at`: per file the live and durable sizes, the byte ranges a crash would lose, whether its directory entry is durable, the last torn write, and removed entries a crash would bring back. With `path`, one file's durable content as text or a hex dump ([inspect.md](inspect.md)) |
| `seedfall_check` | `history`, optional `model` | Re-checks a recorded history file against a model: the verdict, each anomaly with its cycle, and on a linearizability violation the HTML timeline file ([properties.md](properties.md#history-files)) |
| `seedfall_verify` | `config`, `seed` and optional `faults`, and/or `report`; optional `seeds`, `check_seeds`, `mutate` | Whether the fix holds: each replayed schedule, the fresh sweep with its confidence, the determinism check and the mutants, plus a Markdown proof block for the PR ([verify.md](verify.md)) |
| `seedfall_cause` | `config`, `seed`, optional `points`, `branches` | The probability of failing from each moment of the run, with the faults between moments, so the agent can see which event made the failure inevitable ([cause.md](cause.md)) |

Every result is structured JSON, so the agent can read the seed, the faults and the trace directly.

## Prompts and resources

The server also serves what an agent needs in order to work without guessing:

| Kind | Name | Contents |
|---|---|---|
| Prompt | `seedfall-setup`, `seedfall-properties`, `seedfall-triage`, `seedfall-fix-and-prove` | The four [skills](#skills), so MCP clients without skill support get the same workflows |
| Resource | `seedfall://schema/config` | JSON Schema of `seedfall.json`, generated from the config types, with every field's default |
| Resource | `seedfall://docs/<name>` | Each page of these docs, for example `seedfall://docs/properties` for the property API and `seedfall://docs/faults` for what each fault does |

## Onboarding a new module

`seedfall init` (MCP: `seedfall_init`) is the first step in a repository that has never run seedfall:

```sh
seedfall init -n                     # report only
seedfall init                        # write seedfall.json and seedfall/workload/main.go
seedfall init -node ./cmd/server     # choose the nodes yourself; repeatable
seedfall init -json                  # the same report as JSON
```

It runs `go list` twice. The first run uses `GOOS=wasip1 -export -deps`, so every package is compiled for wasip1, and any package that fails is reported with its compiler error and the import chain from the node that pulls it in. The second uses `GOOS=linux CGO_ENABLED=1` and lists the dependencies that contain cgo files. Every `main` package that imports `net` becomes a node. `net_overlay` is turned on unless every node already uses `sim`.

The scaffolded workload follows the pattern every example shares: write keys, remember which ones were acknowledged, then read each one back after the faults. Its `put` and `get` return `errors.ErrUnsupported` until the agent implements them against the service's API. Until then the sweep fails with `the workload got acknowledged writes` never true, so an unwired workload can never pass. Existing files are never overwritten.

On a fresh copy of `examples/kvlog` with its configs removed, `seedfall init -node ./server` writes a config and a workload. Wiring `put` and `get` to kvlog's line protocol takes about twenty lines. The first sweep then fails on seed 1 with `acknowledged write lost`, and shrink brings it down to the same two faults as the [getting started guide](getting-started.md).

## Skills

The `skills/` directory holds four skills. They follow the order of a real adoption, and every command in them runs as written:

| Skill | Use it to |
|---|---|
| `seedfall-setup` | Install, run `seedfall init`, clear the wasip1 blockers, wire the workload and run the first sweep |
| `seedfall-properties` | Read the storage and replication code, write `sim` properties for its durability and consistency promises, and grade them with `seedfall mutate` until every exercised mutant is killed |
| `seedfall-triage` | Classify, shrink, find the decisive fault with `cause`, inspect the disk, and confirm the explanation with a replay of an edited schedule |
| `seedfall-fix-and-prove` | Fix the cause, replay the saved schedule, sweep, run `seedfall verify -mutate -md proof.md` and paste the proof into the pull request |

## Stop hook

The Claude Code plugin registers a Stop hook that runs `seedfall hook`. It does nothing unless you opt in by setting `SEEDFALL_HOOK_SEEDS`, for example in `.claude/settings.json`:

```json
{ "env": { "SEEDFALL_HOOK_SEEDS": "1-20" } }
```

When Claude finishes a turn in a directory that has `seedfall.json` (or the file named by `SEEDFALL_HOOK_CONFIG`), the hook sweeps those seeds. On a failure or an unmet property, it blocks the stop and hands Claude the seed and the replay and shrink commands. To avoid loops, it skips a stop that a hook has already continued once.

## Docs for models

The site serves [`llms.txt`](https://seedfall-lemon.vercel.app/llms.txt), an index of these docs and the skills, and [`llms-full.txt`](https://seedfall-lemon.vercel.app/llms-full.txt), their full text. `bash scripts/site-llms.sh` generates both from `docs/` and `skills/`.

## A verification loop

Ask the agent to treat a seed sweep as the definition of done for storage or replication changes:

1. Run `seedfall_run` over a few hundred seeds before changing anything, to record the baseline.
2. When a seed fails, run `seedfall_shrink` and read the minimal fault schedule. It usually names the exact window: "crash during disk op #56", then "crash". `seedfall_cause` shows which of those faults made the failure likely.
3. Fix the code, then `seedfall_replay` the saved schedule until it passes.
4. Run `seedfall_verify` with the seed and schedule and paste its `markdown` proof block into the pull request.

Shrunk schedules are small enough that an agent can reason about them step by step. `examples/kvlog` is a good first exercise: its `seedfall.always.json` shrinks to two faults.
