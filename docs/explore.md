# Feedback-guided exploration

A plain sweep draws a fresh random seed every time. A failure that needs two rare things to happen in the same run, each with odds of one in a thousand, needs on the order of a million seeds. `seedfall explore` looks for that kind of failure with feedback: it keeps the runs that reached something new, and spends further runs going deeper from there instead of starting over.

```sh
seedfall explore -c seedfall.json -seeds 1-8 -rounds 30 -batch 8
```

```
round 1   runs 8     +4 signals (4)  +625 functions (625)  corpus 4  failures 0  551ms
    new: property "level 1 reached" = true
    new: property "level 2 reached" = true
    new: property "level 3 reached" = true
    new: property "level 4 reached" = true
round 2   runs 16    +0 signals (4)  +0 functions (625)  corpus 4  failures 0  935ms
round 3   runs 24    +1 signals (5)  +0 functions (625)  corpus 5  failures 0  1.289s
    new: property "level 5 reached" = true
round 4   runs 32    +1 signals (6)  +2 functions (627)  corpus 5  failures 1  1.628s
    new: property "level 6 reached" = true

stopped: failure found
FAIL seed 6 at t=923.003008ms (924 scheduler steps, 56ms real)
  client#1: the vault opened after 6 consecutive lucky draws
```

That is [examples/explore](../examples/explore): 20 plain seeds never reach level 6 (and the sweep passes), while exploration finds it in 32 runs. A failure is reported the usual way, and the artifact is an ordinary schedule file.

## How it works

Round 1 runs the `-seeds` range and any entries left in the corpus by earlier runs. Every run reports **signals**:

| Signal | Meaning |
|---|---|
| `property "msg" = true` | a property fired with this outcome, optionally `during crash,partition`: the fault kinds with an open window when it first fired |
| `fault crash server` | a fault of this kind applied to this node, including a `buggify` firing with its site |
| function | a function reached for the first time, per node, counting every function in the binary (the Go runtime included, so some of them are the runtime reacting to the run). This is coarse, function-entry coverage, not basic blocks |

A run that adds a signal or a function joins the corpus as a schedule: its seed, recorded faults and branches. Every further round draws `-batch` children, one at a time from the exploration's own seed:

- pick a corpus entry, more often when it holds rare signals
- with odds 3 of 5, branch at the moment just after one of its signals fired, more often a rare one, and re-randomize the future. This is the checkpoint step: a run that reached the rare state continues from there, so a rare state followed by another rare event no longer needs both to happen in the same attempt
- with odds 1 of 5, re-key one stream (`sched`, `net`, `disk`, `disklatency` or `random`) from a random moment
- otherwise add, remove or shift a fault

Branching uses the machinery [`seedfall cause`](cause.md) already had, with branches now recorded in the run and chained, so a child of a child branches again. A branch at virtual time *T* re-keys the streams at the first scheduler step at or after *T*. Nothing is snapshotted: replay is exact, so reaching *T* again means re-running the seed.

The exploration stops at the first of these:

| Flag | Stops after |
|---|---|
| a failure | the first failing run, unless `-keep-going` |
| `-rounds N` | N rounds |
| `-budget 10m` | that much wall-clock time, checked between rounds |
| `-until-novelty N` (default 8) | N rounds in a row that added nothing |

## Determinism

The choices come from `-explore-seed` (default 1) and every result is merged in a fixed order, so a given corpus and flags produce the same rounds at any `-j` as long as a `-budget` does not cut them short: a `-budget` depends on the machine's speed, so use `-rounds` for an exploration that must repeat exactly in CI.

Every failure is a seed plus a schedule file, so nothing is lost for the rest of the tools:

```sh
seedfall replay  -seed 6 -faults .seedfall/failures/seedfall-seed-6-explore-504fa1fb9883/faults.json
seedfall shrink  -seed 6 -faults .seedfall/failures/seedfall-seed-6-explore-504fa1fb9883/faults.json
seedfall inspect -seed 6 -faults .seedfall/failures/seedfall-seed-6-explore-504fa1fb9883/faults.json
seedfall pin     -seed 6 -faults .seedfall/failures/seedfall-seed-6-explore-504fa1fb9883/faults.json
```

Those commands and the report's replay and shrink lines use the file because the seed alone would not reproduce the run. See [the schedule format](faults.md#editing-a-schedule).

## Corpus

Corpus entries are written to `.seedfall/corpus/<config>/<seed>-<id>.json` and replayed in the first round of the next `seedfall explore`, so a search keeps building where it left off. Pass `-corpus dir` to keep them somewhere committed, like Go fuzz testdata. An entry whose faults no longer fit the config (a node was renamed, for example) or that cannot be read is skipped and counted as stale. Delete the directory to start over.

## What re-keying does not reach

Re-keying a run changes the random streams seedfall owns: the scheduler, the network, the disk, I/O faults, buggify firings and what `random_get` returns to the guest, which reaches `crypto/rand` and `sim.Random`. Go's `math/rand` and `math/rand/v2` top-level functions are seeded once, when the process starts, and keep their own sequence, so a branch does not change what they return in a process that is already running. Workload randomness that should re-roll on a branch has to come from `crypto/rand` or `sim.Random`.

## Stopping a plain sweep

`seedfall run -until-novelty N` stops a sweep once N batches of seeds in a row added nothing, so a wide range does not need to be run in full once coverage has flattened out. `-batch` sets the seeds per batch (default 20) and the verdict for each seed is the same as without the flag; only how many seeds run changes, and the point a run stops does not depend on `-j`:

```sh
seedfall run -seeds 1-5000 -until-novelty 5 -batch 50
```

```
round  runs  new signals  signals  new functions  functions
  1     4     3            3        624            624
  2     8     1            4        1              625
  3     12    0            4        0              625
stopped: seed range finished
```

The same growth table is in the JSON output (`exploration`), `seedfall_run` and the **Exploration** section of the [triage report](reports.md).

## Throughput

Every sweep reports runs per second and virtual time simulated per wall second, the kind of efficiency numbers a deterministic simulation lives on. They are in the last lines of `seedfall run`, the JSON summary as `seeds_per_second` and `virtual_seconds_per_wall_second`, and the report header.

Numbers from one Windows 11 machine, `-j 2`, seedfall built with this repository's Go toolchain:

| Example | Seeds | Time | Note |
|---|---|---|---|
| `examples/kvlog` `seedfall.fixed.json` | 20 | 1.84 s to 2.12 s, 39 to 45x virtual time | without feedback instrumentation |
| `examples/kvlog` `seedfall.fixed.json` | 20 | 1.90 s to 2.13 s | with `-until-novelty`, which adds the function bitmap: no measurable slowdown |
| `examples/natscluster` | 3 | 15.2 s at `-j 1` | a 3-node cluster, about 5 s a seed |

A CPU profile of kvlog (10 seeds, `-j 1`, taken with a one-off build because `-cpuprofile` is refused on Windows) spent 55% of its samples in the guest's compiled code, 22% in wazero's call path to and from the guest, and 3.7% instantiating modules; the per-step watchdog timer did not appear in the top of the profile.

`seedfall run -cpuprofile cpu.pprof` is meant to write a Go pprof profile on platforms where Go's profiler can unwind through wazero's compiled code; it has not been verified on Linux or macOS. seedfall refuses it on Windows: the Go runtime crashes unwinding those frames, consistently with parallel guests and intermittently at one guest, reported with a `fatal error` from `runtime.sigprof`.

There is no snapshotting. `seedfall cause` and every branched run re-execute the seed from the start, in parallel on `-j` workers.

## From an agent

The MCP tool `seedfall_explore` takes the same options and returns the rounds, the stop reason and the failure. `seedfall_run` accepts `until_novelty` and `batch`. See [agents.md](agents.md).
