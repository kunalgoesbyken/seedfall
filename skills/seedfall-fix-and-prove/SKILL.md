---
name: seedfall-fix-and-prove
description: Fix a triaged seedfall failure and prove the fix. Replays the saved minimal fault schedule until it passes, sweeps fresh seeds, then runs seedfall verify for a 95%-confidence proof with determinism and mutant checks, and puts the Markdown proof block into the pull request. Use after seedfall-triage, or whenever a change to storage, recovery or replication code must be shown crash-safe before it is merged.
compatibility: Requires the seedfall CLI, the failing seedfall.json, and ideally the schedule written by seedfall shrink (.seedfall/seed-N.faults.json).
---

# Fix a seedfall failure and prove it

The fix is done when `seedfall verify` passes, not when one replay goes green. Below, `C` is the config that failed, `N` the seed, and `S` the schedule from `seedfall shrink`, `.seedfall/seed-N.faults.json`.

## 1. Fix the cause, not the symptom

Change the code that triage identified: fsync before acknowledging, truncate a torn tail during recovery, fsync the directory after a rename, stop acknowledging after a failed fsync. Do not change `C`, the workload's checks or the fault rates to make the failure go away. A fix that weakens the checks fails step 4 anyway.

## 2. Replay until it passes

```sh
seedfall replay -c C -seed N -faults S
seedfall replay -c C -seed N
```

Both must end with `seed N: ok, ... steps`. Seeds and schedules replay exactly, so a pass here is not luck. If it still fails, read the new failure: it may be the same bug further along, or a second bug the first one was hiding.

## 3. Sweep

```sh
seedfall run -c C -seeds 1-200
```

A new failure on another seed is a new finding. Triage it before going on.

## 4. Verify

```sh
seedfall verify -c C -seed N -faults S -mutate -md proof.md
```

If the failures came from a triage report, use `-report .seedfall/report.html` instead of, or as well as, `-seed`. Verify stops at the first stage that fails:

1. **Replay.** Each named schedule passes, twice, with identical traces.
2. **Fresh sweep.** Enough new seeds for 95% confidence at the failure's historical rate. Without a rate, verify assumes 1% and runs 299 seeds. `-seeds` overrides the range.
3. **Determinism.** `-check` fresh seeds (default 10) run twice with identical host calls and traces.
4. **Mutants** (`-mutate`). Every exercised mutant is still killed, so the fix did not pass by making the checks blind.

It prints `PASS` or `FAIL` and exits 0 or 1. Over MCP, `seedfall_verify` with `{"config": "C", "seed": N, "faults": "S", "mutate": true}` returns the same verdict, and its `markdown` field holds the proof block.

## 5. Put the proof in the pull request

Paste `proof.md`, or the MCP `markdown` field, into the pull request description unchanged, under a heading such as `## Crash-safety proof`. It names the config, the replayed schedules, the seed ranges and the confidence, so a reviewer can rerun the exact command it shows.

## 6. Keep it fixed

- Copy the schedule out of `.seedfall/`, which is usually gitignored, into the repository, for example `seedfall/seed-N.faults.json`. Then `seedfall replay -c C -seed N -faults seedfall/seed-N.faults.json` stays a regression test.
- Run the sweep from `go test` with `seedfalltest.Sweep(t, "seedfall.json", "1-100")` from `github.com/kunalgoesbyken/seedfall/seedfalltest`, or in CI with the `kunalgoesbyken/seedfall` GitHub Action.
