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
- the faults before the failure and the trace of the lowest failing seed. seedfall re-runs that seed to get them, which works because replays are exact;
- that seed's [timeline](timeline.md), embedded from its artifact folder, with a link to open it on its own.

## Classification

Every finding, and the failure `seedfall run` stops at, gets a class that says whether it is worth blocking on:

| Class | Meaning |
|---|---|
| `bug` | Replaying the seed fails again with the same trace. A real, reproducible failure of the system under test |
| `nondeterministic` | The replay diverges from the original run: something in the harness or the system leaks host nondeterminism. The first diverging trace line is shown |
| `timeout` | The real-time watchdog fired. This depends on host speed and load, not only on the seed. With `preempt.loops` a busy loop fails on `liveness` instead |

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

Keep `.seedfall/history.jsonl` between CI runs (cache or commit it) to get this classification on every pull request. The [GitHub Action](ci.md#history-between-runs) caches it for you.

## Reading findings back

`seedfall findings` reads the last report and the run history without running anything:

```sh
seedfall findings                          # .seedfall/report.html
seedfall findings -status new,regressed    # only what needs attention
seedfall findings -report ci/report.json -json
```

```
.seedfall/report.html: 1 of 6 seeds failed (seeds 1-6 of seedfall.always.json), 3 runs in history
  new       assert · client                              1 seeds  16.67%  e.g. seed 1
            client#1: acknowledged write lost: k96=v96, server now answers "NONE"
            replay: seedfall replay -c seedfall.always.json -seed 1
```

It lists each finding with its status, failing seeds, rate, example seed, reason and replay command, then the earlier findings this run did not see (`resolved` or `not seen`) and the properties that were never reached or never true. `-status` takes a comma-separated list of the statuses above and applies to both lists, `-history` points at a history file other than the one the report names, and `-json` prints the same object the `seedfall_findings` MCP tool returns, with every finding's example failure, trace tail, shrink command and artifacts folder, and every run of that config in the history.

## Properties and fault profile

The report also lists every property with how many seeds reached it and how many saw it true, flagging properties that were never reached or never true, and the fault profile the sweep ran with. Its header names the seedfall version, the Go version and, when the config is in a git repository, the commit it was run at. Schedules pinned with `seedfall pin` are replayed before the sweep and listed in a **Regressions** section ([pinned regressions](verify.md#pinned-regressions)); a regression that fails again fails the run.

## Exploration and throughput

A report from `seedfall run -until-novelty` or `seedfall explore -report` has an **Exploration** section: one row per batch or round with the runs so far, the new signals and functions it added, and the corpus size, so you can see where coverage flattened out, plus why it stopped. Every report's header and JSON (`seeds_per_second`, `virtual_seconds_per_wall_second`) also state throughput. A finding that needs a schedule to reproduce, as `seedfall explore` findings do, carries `schedule_file` in its example and its replay and shrink commands use it; `seedfall verify -report` replays that schedule instead of the bare seed ([explore.md](explore.md)).

## Utilization

A report from `seedfall run` has a **Utilization** section (`utilization` in `report.json`, in the `-json` summary and in the `seedfall_run` result) that answers whether to keep sweeping:

- `batches` splits the seeds that ran into up to ten ranges of nearly equal size, with how many new property outcomes each added. An outcome is a property being reached, being true or being false, and it counts toward the lowest seed that showed it, so the table is the same whatever order parallel runs finished in.
- `speedup_min`, `speedup_median` and `speedup_max` are virtual seconds per real second for one seed, including time spent waiting for a CPU under `-j`.
- `advice` is one sentence, also printed as `utilization:` at the end of the run, saying whether the last batches still added outcomes: more seeds are likely to help if the last batch did, may help if a new outcome came in the later half, and are unlikely to if the later half was quiet. Under ten seeds it says there are too few to compare. It is a heuristic over property outcomes only: it cannot see a bug no property or invariant catches, so read `No new property outcome` as "this config has stopped widening coverage", not as "no bugs remain".

`seedfall explore` reports have the Exploration section instead, because its runs are branches of earlier runs rather than a seed range.

On a terminal, `seedfall run` also shows a progress line on stderr, updated every second and erased when the sweep ends: seeds done, seeds per second, failures, estimated time left and virtual speedup. With `-until-novelty` the estimate is an upper bound, since the sweep can stop early. It is off with `-json`, with `-v`, and when stderr is not a terminal, so piped output and CI logs stay clean.

## Failure artifacts

Every failure seedfall reports gets a folder you can attach to an issue or upload from CI:

```
.seedfall/failures/seedfall.always-seed-1/
  report.json            what failed, classification, seedfall and Go versions, config hash, commit, replay command
  trace.txt              the full trace
  faults.json            the run's fault schedule, replayable with seedfall replay -faults
  history.jsonl          the recorded operations, when the workload records any
  linearizability.html   the timeline, on a linearizability violation
  timeline.html          a space-time diagram of nodes, messages, faults and operations with a trace explorer
  timeline.mmd           the same run as a Mermaid sequence diagram
  disk/<node>/           each node's durable disk image at the failure, as inspect -out exports it
```

`timeline.html` and `timeline.mmd` are described in [timelines and logs](timeline.md). The HTML triage report embeds each finding's `timeline.html` under the finding, linked relative to the report, so keep the report and `.seedfall/failures/` together when you move or upload them.

seedfall writes it by replaying the failing seed once more and capturing the disks at the failure, so it costs one run per reported failure, not one per seed. `seedfall run` writes it for the failure it stops at, `run -report` for the example seed of each finding (the report links it), `replay` for a failing replay, `shrink` for the minimal schedule, and a failing [pinned regression](verify.md#pinned-regressions) for itself. The folder is named after the config and the seed, `<config>-seed-N`, so two configs in one directory keep separate folders. `<config>` is the file name without `.json`, then `.<profile>` for a profile and `.set-<hash>` for `-set` overrides, a short hash of the overrides in order, as in `seedfall.fixed.set-1a2b3c4d-seed-2`. A run from the seed alone goes to `<config>-seed-N`; a replay from a schedule file to `<config>-seed-N-schedule`, a shrunk schedule to `<config>-seed-N-shrunk` and a pinned one to `<config>-seed-N-pinned`, so they do not overwrite each other. A new failure of the same seed replaces the folder. `run -json` writes artifacts only for failing regressions, not for every failing seed.

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

## GitHub

Two more commands read a report for GitHub, and the [GitHub Action](ci.md#github-actions) runs both:

```sh
seedfall report github  -report .seedfall/report.html
seedfall report comment -report .seedfall/report.html -repo owner/name -pr 12 -marker storage -link "$RUN_URL"
```

`report github` prints one `::error` workflow command per problem, most urgent first: pinned regressions that fail again, regressed, new and ongoing findings, then properties that were never reached or never true. A finding from a [property](properties.md) with a constant message, and an unmet property, carry the `file` and `line` of the call, as a path relative to the working directory. Run it from the repository root. A location outside that directory, such as a dependency in the module cache, is left off. It then appends `failing-seed` and `findings` to `-output`, default `$GITHUB_OUTPUT`, or prints them when neither is set. `-md file` also writes the Markdown that `report comment` would post, for a job summary or a later `report comment -body file`.

`report comment` posts the report to a pull request as a [`gh`](https://cli.github.com) comment, or `-body file` posts a Markdown file instead, such as a `verify -md` proof, with `-link` added at the end. It lists new, regressed, resolved and ongoing findings with replay commands, failing pinned regressions and unmet properties, with the most urgent sections first. Each comment starts with a hidden marker from `-marker`, and a later call with the same marker edits that comment instead of adding one, so give each job in a matrix its own. Failure text sits inside code spans so it cannot mention anyone or inject Markdown, and a comment too long for GitHub is cut with a note. `-dry-run` prints the comment.
