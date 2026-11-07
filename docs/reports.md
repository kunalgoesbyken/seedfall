# Triage reports

```sh
seedfall run -c seedfall.json -seeds 1-500 -report .seedfall/report.html
```

With `-report`, a sweep keeps going after the first failure, groups every failing seed into findings, and writes an HTML report plus a JSON version next to it (`report.json`). The terminal prints a summary:

```
16 of 60 seeds failed, 1 findings, in 4.867s
  new       assert · client                             16 seeds  e.g. seed 1  bug
report: .seedfall/report.html
```

## Findings

Failures are grouped by what failed, not by message text: the failure class (`property`, `assert`, `crash`, `exit`, `watchdog`, `deadlock`, `linearizability`, `nondeterminism`), the node, and, for properties, the property name. Use `sim.Always` with a constant message to get one finding per invariant.

For each finding the report shows:
- how many seeds failed and the rate, with findings under 1% marked rare;
- up to ten example seeds;
- ready-to-run `replay` and `shrink` commands;
- the faults before the failure and the trace of the lowest failing seed. seedfall re-runs that seed to get them, which works because replays are exact.

## Classification

Every finding, and the failure `seedfall run` stops at, gets a class that says whether it is worth blocking on:

| Class | Meaning |
|---|---|
| `bug` | Replaying the seed fails again with the same trace. A real, reproducible failure of the system under test |
| `nondeterministic` | The replay diverges from the original run: something in the harness or the system leaks host nondeterminism. The first diverging trace line is shown |
| `timeout` | The real-time watchdog fired. This depends on host speed and load, not only on the seed |

```
FAIL seed 1 at t=2.892s ...
class: bug
```

Classification replays only the failure that `run` reports and one representative seed per finding, so it adds one run per finding, never one per seed. It appears in the terminal summary, in the HTML report, as `classification` in `report.json`, and in the MCP `seedfall_run` and `seedfall_replay` results, so CI and agents can block on `bug` and quarantine the rest.

## History: new, ongoing, resolved, regressed

Every report appends a line to `.seedfall/history.jsonl`, keyed by config path. Findings are classified against that history:

| Status | Meaning |
|---|---|
| `new` | Never seen before for this config |
| `ongoing` | Seen in earlier runs and still failing |
| `not seen` | Absent from this run, but not enough clean seeds yet to be sure it is fixed |
| `resolved` | Absent for enough consecutive clean seeds to be 95% confident it is fixed |
| `regressed` | Failing again after it was resolved |

"Enough" depends on how often the finding used to fail. A finding that failed in a fraction *p* of seeds needs *n ≥ ln(0.05) / ln(1 − p)* clean seeds, counted across runs since it was last seen. A bug that failed 1 seed in 4 is resolved after 11 clean seeds; one that failed 1 seed in 100 needs 299.

```
  new       assert · client          16 seeds  e.g. seed 1
  ongoing   assert · client          15 seeds  e.g. seed 61
  not seen  assert · client          not seen in 5 seeds, but 11 are needed to be 95% sure it is fixed
  resolved  assert · client          not seen in 20 seeds since it failed 15 of 60
  regressed assert · client           6 seeds  e.g. seed 1
```

Keep `.seedfall/history.jsonl` between CI runs (cache or commit it) to get this classification on every pull request.

## Properties and fault profile

The report also lists every property with how many seeds reached it and how many saw it true, flagging properties that were never reached or never true, and the fault profile the sweep ran with.
