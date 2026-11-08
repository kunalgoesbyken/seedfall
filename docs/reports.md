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

Failures are grouped by what failed, not by message text: the failure class (`property`, `assert`, `crash`, `exit`, `watchdog`, `deadlock`, `liveness`, `linearizability`, `isolation`, `nondeterminism`), the node, and, for properties, the property name. Use `sim.Always` with a constant message to get one finding per invariant.

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

The report also lists every property with how many seeds reached it and how many saw it true, flagging properties that were never reached or never true, and the fault profile the sweep ran with. Its header names the seedfall version, the Go version and, when the config is in a git repository, the commit it was run at. Schedules pinned with `seedfall pin` are replayed before the sweep and listed in a **Regressions** section ([pinned regressions](verify.md#pinned-regressions)); a regression that fails again fails the run.

## Failure artifacts

Every failure seedfall reports gets a folder you can attach to an issue or upload from CI:

```
.seedfall/failures/seedfall.always-seed-1/
  report.json            what failed, classification, seedfall and Go versions, config hash, commit, replay command
  trace.txt              the full trace
  faults.json            the run's fault schedule, replayable with seedfall replay -faults
  history.jsonl          the recorded operations, when the workload records any
  linearizability.html   the timeline, on a linearizability violation
  disk/<node>/           each node's durable disk image at the failure, as inspect -out exports it
```

seedfall writes it by replaying the failing seed once more and capturing the disks at the failure, so it costs one run per reported failure, not one per seed. `seedfall run` writes it for the failure it stops at, `run -report` for the example seed of each finding (the report links it), `replay` for a failing replay, `shrink` for the minimal schedule, and a failing [pinned regression](verify.md#pinned-regressions) for itself. The folder is named after the config and the seed, `<config>-seed-N`, so two configs in one directory keep separate folders. A run from the seed alone goes to `<config>-seed-N`; a replay from a schedule file to `<config>-seed-N-schedule`, a shrunk schedule to `<config>-seed-N-shrunk` and a pinned one to `<config>-seed-N-pinned`, so they do not overwrite each other. A new failure of the same seed replaces the folder. `run -json` writes artifacts only for failing regressions, not for every failing seed.

`report.json` has `reproduced`, which is false when the replay did not fail the same way as the reported run, `classification` ([above](#classification)), `seedfall_version` (from the binary's build information), `config_sha256` (of the resolved config after `extends`, the profile and `-set`, plus the bytes of every file seeded onto a node's disk), `commit` (`git rev-parse HEAD` in the config's directory, so uncommitted changes are not reflected) and `replay`, a command that replays `faults.json` exactly:

```sh
seedfall replay -c seedfall.always.json -seed 1 -faults .seedfall/failures/seedfall.always-seed-1/faults.json
```

Over MCP, failures from `seedfall_run`, `seedfall_replay` and `seedfall_shrink` carry the folder as `artifacts_dir`.

## Notifications and issues

Two commands turn a report into alerts and issues, so a nightly sweep needs no glue code:

```sh
seedfall report notify -report .seedfall/report.html -webhook "$SEEDFALL_WEBHOOK" -link "$CI_RUN_URL"
seedfall report issue  -report .seedfall/report.html -repo owner/name
```

`report notify` posts one message with the report's `new` and `regressed` findings, each with its failing seeds, rate, example seed, reason and replay command, to an incoming webhook. The body is `{"text": ..., "content": ...}`, the fields Slack and Discord incoming webhooks read, and the message is cut to Discord's 2000-character limit with a count of the findings left out. `-webhook` defaults to `$SEEDFALL_WEBHOOK`, and errors never print the URL, which holds the webhook's secret. Each finding sent is recorded in `.seedfall/history.jsonl` against the report it came from, so posting the same report twice sends nothing the second time; an `ongoing` finding is never sent. Because new and regressed come from that history, keep the file between CI runs (see [above](#history-new-ongoing-resolved-regressed)), or every run's findings are new and every run notifies. `-dry-run` prints the message instead.

`report issue` files one GitHub issue per finding with the [`gh` CLI](https://cli.github.com), titled `seedfall finding: <key>`, for example `seedfall finding: assert · client`. The body has the counts, status and class, the reason, the replay and shrink commands, the example seeds, the faults before the failure, the trace tail, the artifacts folder, and the seedfall version, commit and Go version. Before filing, it reads the titles of the repository's 1000 most recent open issues and skips a finding whose title is among them, so a finding has one open issue whether or not the history is saved, and gets a new one if it comes back after its issue was closed. `gh` needs to be logged in, or `GH_TOKEN` set, with permission to write issues. `-dry-run` prints each issue without calling `gh`.

The HTML report has a **Copy issue markdown** button on every finding with the same body, and the text itself under it, for filing by hand in any tracker.
