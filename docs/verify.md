# Proving a fix with `seedfall verify`

`seedfall verify` is the definition of done for a change that fixes a seedfall failure, whether a person or an AI agent wrote it. It answers one question with evidence: is the failure gone, and how sure are we?

```sh
seedfall verify -c seedfall.fixed.json -seed 1 -faults .seedfall/seed-1.faults.json -mutate -md proof.md
```

```
verify seedfall.fixed.json
  pass  replay seed 1 with .seedfall/seed-1.faults.json  passes, twice, with identical traces
  pass  fresh sweep, seeds 2-300                         299 of 299 pass: 95% confidence that a failure with rate >= 0.997% would have shown up
  pass  determinism, seeds 2-11                          every seed ran twice with identical traces
  pass  mutant fsync_noop                                killed by seed 2
  pass  mutant dirsync_noop                              not exercised
  pass  mutant osync_ignored                             not exercised
  pass  mutant fsync_corrupts                            killed by seed 2
PASS
```

Against the unfixed config the same command stops at the first check and exits 1:

```
verify seedfall.always.json
  FAIL  replay seed 1 with .seedfall/seed-1.faults.json  seed 1 FAILS [bug]: client#1: acknowledged write lost: k63=v63, server now answers "NONE"
  skip  fresh sweep                                      an earlier check failed
  skip  determinism                                      an earlier check failed
FAIL
```

## What it checks

In order, stopping at the first stage that fails:

1. **Replay.** Every failing schedule you name is replayed and must pass now. Each replay runs twice and must produce identical traces, so a pass cannot be luck.
2. **Fresh sweep.** New seeds, starting after the highest seed already known, must all pass. By default verify runs as many as needed for 95% confidence that a failure with the historical rate would have shown up: *n = ⌈ln(0.05) / ln(1 − p)⌉*, the same rule the [triage report](reports.md) uses to call a finding resolved. A finding that failed 27.5% of seeds needs 10 clean seeds; when no rate is known, verify assumes a rare failure (1%) and runs 299. `-seeds` overrides the range.
3. **Determinism.** The first `-check` fresh seeds (default 10) run twice and must produce identical traces, as with `run -check`.
4. **Mutants** (with `-mutate`). The [durability mutants](mutate.md) run over the fresh seeds, and every exercised mutant must still be killed. This catches a "fix" that passes by weakening the checks.

Any failure is printed with its [classification](reports.md#classification), so a determinism leak is not mistaken for the bug coming back.

## What to verify

| Flags | Replays |
|---|---|
| `-seed N` | seed N with its generated faults |
| `-seed N -faults file.json` | seed N under a saved schedule, for example one written by `seedfall shrink`. Its faults and `ops` cap are kept, but it runs for the config's full `duration`: a shrunk schedule's shorter duration could end the run before the workload checks anything |
| `-report .seedfall/report.html` | every example seed of every finding in a [triage report](reports.md), plus any schedule `shrink` saved for those seeds in `.seedfall/`. The findings' failure rates set the sweep size |

`-seed` and `-report` can be combined. Point `-c` at the config of the code you changed; the report can come from an earlier run of the same or another config.

```
$ seedfall verify -c seedfall.fixed.json -report .seedfall/report.html
verify seedfall.fixed.json
  pass  replay assert · client seed 1                                     passes, twice, with identical traces
  pass  replay assert · client seed 1 with .seedfall/seed-1.faults.json   passes, twice, with identical traces
  pass  replay assert · client seed 12                                    passes, twice, with identical traces
  ...
  pass  fresh sweep, seeds 41-50                                          10 of 10 pass: 95% confidence that a failure with rate >= 25.9% would have shown up (historical rate 27.5%)
  pass  determinism, seeds 41-50                                          every seed ran twice with identical traces
PASS
```

## The proof block

`-md proof.md` writes the result as Markdown for the pull request description:

```markdown
### seedfall verify: PASS

Config `seedfall.fixed.json`, reproduce with `seedfall verify -c seedfall.fixed.json -seed 1 -faults .seedfall/seed-1.faults.json -mutate`

| | Check | Result |
|---|---|---|
| pass | replay seed 1 with .seedfall/seed-1.faults.json | passes, twice, with identical traces |
| pass | fresh sweep, seeds 2-300 | 299 of 299 pass: 95% confidence that a failure with rate >= 0.997% would have shown up |
| pass | determinism, seeds 2-11 | every seed ran twice with identical traces |
| pass | mutant fsync_noop | killed by seed 2 |
| pass | mutant dirsync_noop | not exercised |
| pass | mutant osync_ignored | not exercised |
| pass | mutant fsync_corrupts | killed by seed 2 |
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-c` | `seedfall.json` | Config of the code under test |
| `-seed` | | Failing seed to replay |
| `-faults` | | Fault schedule for `-seed` |
| `-report` | | Triage report (`.html` or `.json`) whose findings to replay |
| `-seeds` | enough for 95% confidence | Fresh seed range to sweep |
| `-check` | `10` | Fresh seeds to run twice for the determinism check |
| `-mutate` | off | Also require every exercised durability mutant to be killed |
| `-md` | | Write the Markdown proof block to this file |
| `-j` | all CPUs | Parallel runs |

The exit status is 0 only when every check passes.

## From an agent

The MCP tool `seedfall_verify` takes the same inputs (`config`, `seed`, `faults`, `report`, `seeds`, `check_seeds`, `mutate`) and returns each check as structured JSON, the overall `passed` verdict, and the proof block as `markdown`. See [agents.md](agents.md).
