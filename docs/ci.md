# Running seedfall in CI

## GitHub Actions

```yaml
jobs:
  simulate:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: write
    steps:
      - uses: actions/checkout@v4
      - uses: kunalgoesbyken/seedfall@main
        id: seedfall
        with:
          config: seedfall.json
          seeds: 1-500
          check: "true"
          comment: "true"
```

The inputs below other than `config`, `profile`, `seeds`, `check`, `go-version`, `report` and `artifact-name` are newer than v0.2.0, hence `@main` until the next release.

### Inputs

| Input | Default | Meaning |
|---|---|---|
| `command` | `run` | `run`, `verify` or `mutate` ([below](#verify-and-mutate)) |
| `config` | `seedfall.json` | Path to the world description |
| `profile` | | Profile from the config's `profiles` to merge over it ([config.md](config.md#profiles)) |
| `seeds` | | Seed or inclusive range. Empty uses the command's own default: `run` sweeps `1-200`, `mutate` `1-100`, and `verify` as many fresh seeds as 95% confidence needs |
| `rotate-seeds` | `false` | With `run`, move `seeds` forward by its own width for each workflow run ([below](#rotating-seeds)) |
| `check` | `false` | With `run`, run every seed twice and fail at the first host call or trace line that differs |
| `args` | | Extra flags for the command, separated by spaces, such as `-timeout 10m`, `-only fsync_noop` or `-seed 1`. Quoting is not interpreted |
| `version` | | seedfall release to download, or `latest` ([below](#which-seedfall-runs)) |
| `go-version` | `stable` | Go version for building your nodes, and seedfall when it is built from source |
| `report` | `true` | With `run`, sweep every seed and upload an HTML triage report. `false` stops at the first failure |
| `artifact-name` | `seedfall-report` | Name of the uploaded artifact, also used to keep each job's cache and pull request comment apart. Make it unique per job in a matrix |
| `cache` | `true` | Keep the run history and compiled modules between runs ([below](#history-between-runs)) |
| `comment` | `false` | Post or update one pull request comment with the outcome. Needs `pull-requests: write` |
| `webhook` | | Slack or Discord incoming webhook URL, best from a secret. Posts the report's new and regressed findings with `seedfall report notify` |
| `file-issues` | `false` | File a GitHub issue for each finding without an open one, with `seedfall report issue` |

An input that does not apply is an error rather than being ignored: `check` or `rotate-seeds` with `verify` or `mutate`, `comment` with `report: "false"`, and `webhook` or `file-issues` with `report: "false"` or a `verify` or `mutate` command.

The job fails if any seed or [pinned regression](verify.md#pinned-regressions) fails. The triage report (see [reports.md](reports.md)) is uploaded as an artifact, together with the [failure artifacts](reports.md#failure-artifacts) under `failures/`.

### Outputs

| Output | Meaning |
|---|---|
| `status` | `passed`, `failed`, or `error` when seedfall could not finish, for example a config that does not build. With `report: "false"` a `run` that cannot start also reads `failed`, because nothing distinguishes the two |
| `failing-seed` | With a `run` report, the seed to look at first: a pinned regression that fails again, else the first regressed, new, then ongoing finding. Empty on a clean run |
| `findings` | With a `run` report, how many findings the report has |
| `report-path` | The HTML report on the runner, or the Markdown from `verify` or `mutate`. Runner temp is cleared when the job ends |

```yaml
      - if: failure() && steps.seedfall.outputs.status == 'failed'
        run: echo "replay with seed ${{ steps.seedfall.outputs.failing-seed }}"
```

The job summary shows the same Markdown as the [pull request comment](#pull-request-comments), with seedfall's output folded below it. When seedfall cannot finish, such as a config that does not build or a `mutate` baseline that already fails, the summary says so with the last lines of output. Problems also appear as error annotations on the run and the pull request's changed files. A finding from a property with a constant message is annotated at its source line, from the same catalog that reports properties that were never reached. Other failures, such as a crash or a `sim.Assert`, are annotated without a file. GitHub only shows a limited number of annotations per step, so failing regressions come first, then regressed, new and ongoing findings, then unmet properties.

### Which seedfall runs

With `version` empty, the action uses the release that matches the ref in `uses:`, so `@v1.2.3` downloads v1.2.3 for the runner's platform and checks it against the release's `checksums.txt`. A checksum mismatch fails the job. If the ref has no release, such as a branch, a commit or a release older than the automated pipeline like v0.2.0, seedfall is built from the action's source instead. Windows runners and `uses: ./` always build from source. The job log has a notice saying whether seedfall was installed from a release or built from source.

Set `version: v1.2.3`, or `latest`, to run a specific release regardless of the ref. An explicit version never falls back: a missing release fails the job, and it needs a Linux or macOS runner and a released action. Go is set up either way, because seedfall compiles your nodes with it.

### History between runs

New, ongoing, resolved and regressed need the run history ([reports.md](reports.md#history-new-ongoing-resolved-regressed)), so with `cache` on the action restores and saves `.seedfall/history.jsonl` and `.seedfall/cache` next to the config, with `actions/cache`. It saves even when the sweep fails, since that is the run whose history matters most.

The key is the config file's hash, the `artifact-name`, the branch and the run, and restoring falls back to the newest entry for that config and name from this branch, then from any branch the run can read, so a pull request starts from its base branch's history. Changing the config starts a new history. An `extends` parent is not part of the hash. Keep `artifact-name` unique per job, or jobs in a matrix overwrite each other's history.

Set `cache: "false"` if you keep `.seedfall/history.jsonl` some other way, such as committing it.

### Pull request comments

`comment: "true"` posts one comment per job and edits it on later pushes, so a pull request does not collect one per run. It lists new, regressed and resolved findings, ongoing ones, failing pinned regressions and unmet properties, each failure with its replay command, and links the workflow run. It is the job summary's Markdown, so with `verify` or `mutate` it is the proof or the mutant table, and when seedfall cannot finish it says so. A clean run also updates it, so a stale failure does not linger. Only a comment written by the token's own account is edited, so a comment someone else posts with the same marker is left alone. The comment never includes seedfall's raw output; that stays in the job summary.

The job needs `pull-requests: write`. Pull requests from forks run with a read-only token, so the action logs a warning and the job's result is unaffected. `seedfall report comment` does the same from any CI, and `seedfall report github` writes the annotations and outputs ([reports.md](reports.md#github)).

### Verify and mutate

`command: verify` runs [`seedfall verify`](verify.md), and `args` names what to verify:

```yaml
      - uses: kunalgoesbyken/seedfall@main
        with:
          command: verify
          config: seedfall.fixed.json
          args: -seed 1 -faults testdata/torntail.faults.json -mutate
          comment: "true"
```

`command: mutate` runs [`seedfall mutate`](mutate.md) and fails if a mutant survives:

```yaml
      - uses: kunalgoesbyken/seedfall@main
        with:
          command: mutate
          seeds: 1-50
          args: -only fsync_noop,net_dup_chunk
```

Both write Markdown to the job summary and the artifact (`proof.md`, `mutants.md`). `failing-seed` and `findings` stay empty, because neither command writes a triage report.

### Rotating seeds

`rotate-seeds: "true"` keeps exploring new schedules. Run number `n` sweeps the range `n - 1` widths after `seeds`: with `seeds: 1-200`, run 1 sweeps 1-200, run 2 201-400, and run 8 1401-1600. Re-running a job keeps its run number, so it sweeps the same seeds. Every failure names its seed, so it stays exactly reproducible, and [pinned regressions](verify.md#pinned-regressions) still replay on every run.

Outside the action, `seedfall run -seeds 1-200 -rotate N` sweeps the Nth range after `-seeds`, and refuses a rotation past the largest seed.

### Nightly sweep

A template for a scheduled sweep, with alerts and issues for new and regressed findings:

```yaml
name: nightly-simulation
on:
  schedule:
    - cron: "17 3 * * *"
  workflow_dispatch:
jobs:
  simulate:
    runs-on: ubuntu-latest
    timeout-minutes: 60
    permissions:
      contents: read
      issues: write
    steps:
      - uses: actions/checkout@v4
      - uses: kunalgoesbyken/seedfall@main
        with:
          seeds: 1-500
          rotate-seeds: "true"
          args: -timeout 45m
          webhook: ${{ secrets.SEEDFALL_WEBHOOK }}
          file-issues: "true"
```

The history is what limits alerts to new and regressed findings, so leave `cache` on. The message links the workflow run, and each issue names the seed, the replay command, the seedfall version and the commit ([notifications and issues](reports.md#notifications-and-issues)).

### Several configs

Run a matrix with a unique `artifact-name` per job:

```yaml
jobs:
  simulate:
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { name: storage, config: examples/ledger/seedfall.json, seeds: 1-60 }
          - { name: upgrade, config: examples/upgrade/seedfall.json, seeds: 1-50 }
    steps:
      - uses: actions/checkout@v4
      - uses: kunalgoesbyken/seedfall@main
        with:
          config: ${{ matrix.config }}
          seeds: ${{ matrix.seeds }}
          artifact-name: seedfall-${{ matrix.name }}
```

This repository's own [`ci.yml`](../.github/workflows/ci.yml) sweeps several examples this way, and runs `verify` and `mutate` too.

## Machine-readable output

`seedfall run -json` sweeps every seed in the range and prints one JSON object per line as each seed finishes, then a summary object. Seeds finish in parallel, so lines are not in seed order.

```sh
seedfall run -c seedfall.always.json -seeds 1-100 -json | jq -c 'select(.type == "seed" and .passed == false)'
```

```json
{"type":"seed","seed":1,"passed":false,"finished":false,"failure":{"class":"assert","node":"client"},"reason":"client#1: acknowledged write lost: k96=v96, server now answers \"NONE\"","virtual_seconds":2.8918212,"faults":3,"host_calls":6810,"determinism_hash":"53786fa3165d442b"}
```

| Field | Meaning |
|---|---|
| `type` | `seed` or `summary` |
| `seed`, `passed`, `finished` | The seed, whether it passed, and whether the workload finished before the time horizon |
| `failure` | `class`, plus `node` and `property` when known, the same key the triage report groups by |
| `reason` | The failure message |
| `virtual_seconds` | Simulated time the run covered |
| `faults` | Faults that happened during the run |
| `host_calls`, `determinism_hash` | How many host calls the run made, and the running hash over them ([determinism check](properties.md#determinism-check)). It covers only simulated values, so the same seed and guest binary should give the same hash on any machine, and a CI matrix can compare it across operating systems to check |

Before the seed lines comes one `{"type": "regression", ...}` line per [pinned regression](verify.md#pinned-regressions) and `-seeds-file` seed, with `source`, `seed` and `passed`, plus `failure`, `reason`, `classification` and `artifacts` when it fails again.

The summary has `passed`, `failed`, `unfinished`, `regressions_failed`, `unmet_properties` and `properties`, the same coverage table `seedfall_run` returns over MCP, with a `problem` on each property that was never reached or never true and a `margin` on each numeric property. With `faults.buggify` on it also has `buggify_sites`: each `sim.Buggify` site with `seeds_reached`, `seeds_enabled`, `seeds_fired`, `fired` and a `problem` of `never reached` or `never fired`, which does not change the exit code. The exit code is 1 if any seed or regression failed or any property is unmet. `-json` cannot be combined with `-report`.

## Time budgets

`seedfall run -timeout 10m` stops starting seeds after ten minutes of wall-clock time, so a CI job can use a wide range and take whatever fits:

```sh
seedfall run -seeds 1-1000000 -timeout 10m -report .seedfall/report.html
```

Seeds already running finish, so the sweep can run slightly past the limit, and [pinned regressions](verify.md#pinned-regressions) always run and count toward it. The run prints `stopped: time limit reached after N of M seeds`, the same text is `stopped` in the JSON summary, report and `seedfall_run` result, and the exit code is decided as usual: 0 if every seed that ran passed and every property was met. A property that needs more seeds than fit fails as `never reached`, as it would with a shorter range, so give the sweep enough time, or [pin](verify.md#pinned-regressions) a seed that reaches the rare path. Each seed stays exactly replayable, but which seeds ran depends on the machine's speed, so compare runs by seed and not by count.

## Rotating seeds outside GitHub

`seedfall run -seeds 1-200 -rotate N` sweeps the Nth range of the same width after `-seeds`, so any CI system can pass its build number and explore new seeds on each run while every failure stays replayable by seed. `-rotate 0` is the plain range. The run prints `rotation N: seeds A-B`, and the report's `seeds` is the rotated range. MCP's `seedfall_run` takes the same value as `rotate`.

## `go test`

```go
import "github.com/kunalgoesbyken/seedfall/seedfalltest"

func TestDurabilityUnderCrashes(t *testing.T) {
	seedfalltest.Sweep(t, "seedfall.json", "1-100")
}
```

`Sweep` first replays every [pinned regression](verify.md#pinned-regressions) for the config. A failure reports the seed, the reason, the trace tail, the classification, a replay command and the [failure artifacts](reports.md#failure-artifacts) folder. A `Sometimes` property that is never satisfied also fails the test.

Three more helpers turn known bugs into tests, each replaying for the config's full duration and expecting a pass:

```go
func TestPinnedRegressions(t *testing.T) {
	seedfalltest.Pinned(t, "seedfall.json")
}

func TestTornTailStaysFixed(t *testing.T) {
	seedfalltest.Replay(t, "seedfall.json", "testdata/torntail.faults.json")
}

func TestKnownSeeds(t *testing.T) {
	seedfalltest.Seeds(t, "seedfall.json", []uint64{1, 9})
}
```

`Pinned` runs each schedule in `seedfall-regressions/` as a subtest, and skips when there is none. `Replay` takes any schedule file with a `seed`, such as one written by `seedfall shrink`. `Seeds` runs each seed with its own faults as a subtest. `examples/kvlog/seedfall_test.go` uses all four.

## Speed

Runs are independent, so a sweep scales with cores: `seedfall run -j N` defaults to every CPU. Light servers take tens of milliseconds per seed; a 3-node hashicorp/raft cluster takes a few seconds. Each guest is compiled once per process and every seed reuses the compiled module, so per-seed cost is booting the nodes and running them. Compiled modules are also cached in `.seedfall/cache`, one entry per set of guest binaries, and only the 8 most recently used entries are kept, so the cache stays bounded however often you rebuild. Add that directory to your CI cache to skip compilation; the first build of a large guest such as nats-server takes about a minute.
