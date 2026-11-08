---
name: seedfall-triage
description: Triage a failing seedfall seed down to a root cause before touching code. Classifies the failure, shrinks the fault schedule, finds the fault that made the failure inevitable with seedfall cause, inspects the simulated disk at the failure, then checks the hypothesis with a counterfactual replay of an edited schedule. Use when seedfall run, the seedfall GitHub Action or a CI sweep reports FAIL seed N.
compatibility: Requires the seedfall CLI and the seedfall.json that failed. The seedfall MCP server is optional; every step has a CLI form.
---

# Triage a failing seed

Every seedfall failure replays exactly from its seed. Do not guess from the failure message. Narrow the run to the few faults that matter, find where the outcome was decided, look at the bytes, and test the explanation by replaying a variation before you edit code. Below, `C` is the config and `N` the seed.

## 1. Classify

```sh
seedfall run -c C -seeds N
```

The output ends with `class:`.
- `bug`: the replay fails with the same trace. Continue.
- `nondeterministic`: something leaks host nondeterminism, such as real time, unseeded randomness or map iteration feeding I/O. The first diverging trace line is shown; `seedfall run -seeds N -check` goes further and names the first host call that differs (a clock read, a `random_get`, a file or network call) with its node and virtual time. Fix the leak first: it is a harness problem, not the bug.
- `timeout`: the real-time watchdog fired. A busy loop that never yields, or a host that is too slow. Rerun on an idle machine, or raise `watchdog`, before treating it as a bug.

The line after it, `artifacts: .seedfall/failures/<config>-seed-N`, is a folder with the full trace, a replayable `faults.json`, the operation history, each node's disk image at the failure, and `timeline.html` and `timeline.mmd`, a space-time diagram of every node, message and fault. Read `trace.txt` there instead of rerunning for it, and attach the folder if you file an issue.

Many failing seeds? `seedfall run -c C -seeds 1-500 -report .seedfall/report.html` groups them into findings, each with an example seed. Triage one example seed per finding. Over MCP, `seedfall_findings` reads that report back (findings, their status across runs, unmet properties) without running anything.

## 2. Shrink

```sh
seedfall shrink -c C -seed N
```

This writes the minimal schedule to `.seedfall/seed-N.faults.json` and prints it, for example:

```
  427.268ms  crash server during disk op #56, restart at 761.268ms
     802ms  crash server, restart at 802ms
...
shrunk from 33 faults, 1m0s, 200 workload ops
         to 2 faults, 991ms, 63 workload ops
```

Every fault left is needed for the failure. Shrink also shortens downtimes, partitions and pauses, shrinks clock jumps, narrows partition sides and moves faults earlier, so the times that are left matter too. It trims the run's duration and, if the workload sizes itself with `sim.Budget`, its operation count. On a long run, add `-budget 300` (runs) or `-budget 10m` (time); if the output says the budget ran out, the schedule still fails the same way but may not be minimal. `crash ... during disk op #k` means the node died in the middle of its k-th disk operation, usually between a write and its fsync, or between a rename and the directory fsync.

A failure with class `liveness` (the workload or the `finally` check did not finish within `liveness.within`, or a `sim.Eventually` never held) shrinks the same way. The faults left are the ones that leave the system stuck even after the `recovery:` line in the trace, where every fault stops and crashed nodes restart. Read the node output after that line to see which node stopped making progress.

## 3. Find the decisive moment

```sh
seedfall cause -c C -seed N
```

seedfall replays the run up to many moments, re-randomizes everything after each one, and prints the probability of failing from that moment on. The fault just before the jump to near 100% is the cause; faults after it are just how the damage gets observed. Raise `-points` and `-branches` for a finer curve.

Read what each node did around that moment with `seedfall logs -c C -seed N -node server -from 400ms -to 500ms` (add `-net-trace` for every message sent and read, `-grep` for a pattern, `-json` for slog fields). To stop the run there and look around, `seedfall debug -c C -seed N -at 427ms` opens a prompt: `where`, `net` (messages in flight), `faults`, `ls`, `cat` and `stat` on a node's live disk, `step`, `until <regex>` and `branch`. Over MCP these are `seedfall_logs` and `seedfall_debug` with a `commands` list.

## 4. Look at the disk

```sh
seedfall inspect -c C -seed N -faults .seedfall/seed-N.faults.json
seedfall inspect -c C -seed N -faults .seedfall/seed-N.faults.json -at 427ms
seedfall inspect -c C -seed N -faults .seedfall/seed-N.faults.json -node server -cat data/kv.log
```

Inspect shows each node's files at the failure, or at the virtual time `-at`, with live and durable sizes, the byte ranges a crash would lose, directory entries that are not durable yet, the last torn write, and removed entries a crash would bring back. Take `-at` just before and just after the decisive fault from step 3. `-cat` prints one file's durable content. `-out dir` exports the durable images so the project's own tools can open them.

A `linearizability` or `isolation` failure is about the recorded history, not the disk. The failing run saved it to `.seedfall/seed-N.history.jsonl`, and a linearizability violation also to the timeline `.seedfall/seed-N.history.html`; find the first operation that no order explains. For `isolation`, the trace names each anomaly with its dependency cycle (`T4 -rw z-> T0 -wr z-> T6 -ww y-> T4`) and every transaction on it. `seedfall check -history .seedfall/seed-N.history.jsonl -model M` re-checks the file without running anything, also over MCP as `seedfall_check`.

## 5. Check the hypothesis with a replay

Write the explanation down as one sentence, for example "the crash in op #56 leaves a torn record, and recovery appends after it instead of truncating". Then test it by changing one thing and replaying.

- **Drop a fault.** Copy `.seedfall/seed-N.faults.json` to `hypothesis.json` and delete one entry. The schedule is a JSON object whose `faults` array holds `{"at", "kind", "node", "until", "op", ...}`, plus `duration` and `ops` when shrink trimmed them (older files are a bare array of faults). Then run `seedfall replay -c C -seed N -faults hypothesis.json`. If it now passes, that fault is necessary. If it still fails, compare the failure message with the original: a different message means you uncovered a second bug, so note it and triage it separately.
- **Move a fault.** Change `op` on a `crash_io` entry, or `at` on a `crash`, to put the crash just before or after the suspect operation, then replay again. On a `buggify` entry, `op` is which hit of that site on that node fires.
- **Change the world.** In a copy of `C`, turn off `torn_writes`, or turn `strict_dirs` on or off, and replay the same seed. If the failure needs torn writes, the bug is in recovery parsing. If it needs `strict_dirs`, a directory fsync is missing.

`replay` exits 1 and prints the trace when the seed fails, and ends with `seed N: ok, ... steps` when it passes. Over MCP, use `seedfall_replay` with `faults`, `seedfall_shrink`, `seedfall_cause` and `seedfall_inspect`, which take the same arguments as JSON.

`seedfall trace diff .seedfall/failures/<config>-seed-N hypothesis-trace.txt` shows the first line where two traces part, for a replay saved with `seedfall logs ... > hypothesis-trace.txt`.

Then confirm the explanation in the source: find the code that issued the disk operation named in the trace, and the code that read it back on restart.

## 6. Hand off

Record the seed, the minimal schedule file, the decisive fault, what inspect showed, and the replay that confirmed the hypothesis. Then use the `seedfall-fix-and-prove` skill.
