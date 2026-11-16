# seedfall-gym

An RL environment core. An agent gets a small Go system with a crash-consistency or distributed-systems bug and must fix the code. The reward comes from replaying hidden, deterministic fault seeds through [seedfall](../seedfall), a deterministic simulation tester: the fraction of hidden seeds on which the fixed system survives.

This repo holds the grader, the generated tasks, the task builder and the validation command. It does not contain the agent loop or the `verifiers` wiring.

## Layout

| Path | What |
|---|---|
| `grader/` | Go module. `grade`, `validate`, `build_tasks` commands and `internal/gym` library. Depends on seedfall through `replace ... => ../../seedfall`, drop the `replace` to use a published module |
| `tasks.json` | Declarative manifest: systems (which seedfall example dirs, base config), tasks, variants |
| `tasks_src/` | Patch files the manifest names, plus `author/*.py` that produced them from the pristine seedfall examples |
| `tasks/<id>/` | Generated. `workspace/` (agent sees and edits), `harness/` (pristine workload, config, go.mod, progress floor), `solution/` (oracle files and `fix.patch`), `task.json`, `visible_seed` |
| `cheats/<id>/` | Generated. The scripted cheat for each task: the editable dir with every write rejected |
| `baselines.json` | Progress floors measured from the oracle by `validate -calibrate` |
| `validation.json` | Output of the last full validation |

## Commands

All commands run from the repo root. `SEEDFALL_SRC` points at the seedfall checkout (the same one the grader links), the guest programs are built against it.

```sh
export SEEDFALL_SRC=D:/code/seedfall
(cd grader && go build -o ../bin/ ./grade ./validate ./build_tasks)

./bin/build_tasks.exe                       # regenerate tasks/ and cheats/ from tasks.json
./bin/validate.exe -calibrate               # measure progress floors, then rebuild tasks
./bin/validate.exe                          # full validation, prints the table, writes validation.json
./bin/validate.exe -only kvlog-torn-tail    # one task

SEEDFALL_GYM_SALT=<secret> ./bin/grade.exe -task tasks/kvlog-torn-tail -workspace <dir>          # hidden seeds, JSON
./bin/grade.exe -task tasks/kvlog-torn-tail -workspace <dir> -dev -json=false                    # dev salt, one line
./bin/grade.exe -task tasks/kvlog-torn-tail -workspace <dir> -seeds 1,2,3                        # visible seeds, with reasons and trace
```

`grade` flags: `-task`, `-workspace`, `-n` (default: the task's `default_n`, else 64), `-salt-env` (default `SEEDFALL_GYM_SALT`), `-dev` (use a fixed development salt only when the variable is empty), `-seeds` (explicit seeds), `-seedfall`, `-j`, `-json` (default on). The JSON object has `task, n, total, passed, failed, build_ok, build_error, reward, per_seed[{seed,passed,class,reason,hash,acks}], guard{...}, build_seconds, run_seconds`. With `-seeds`, failed seeds also carry `faults` (the fault schedule that was applied) and `trace_tail` (last 40 trace lines).

Hidden seeds are `HMAC-SHA256(salt, taskID || 0x00 || be64(i))` truncated to a uint64. The report lists the hidden seeds, so only the trainer should see the full output; hand the agent the `-seeds` mode.

## Grading and guards

`reward = passed / n`, and 0 if the build fails or a guard trips. A seed passes only if the run did not fail, the workload finished, and the workload saw enough acknowledged operations.

- Harness restore: the grader stages a fresh temp copy of `tasks/<id>/harness` (workload, config, go.mod) and copies only the task's `editable` paths from the agent workspace over it. Edits elsewhere are ignored and listed in `guard.harness.ignored_files`.
- Progress floor: each workload emits a `gym.ack` event per acknowledged operation. The grader counts them from the workload instance only (the server cannot forge them). The floor is half the lowest count the oracle reached over 128 calibration seeds (`baselines.json`). A seed below it fails with class `progress`, and if more than half the seeds are below it the guard trips and the reward is 0. A server that refuses or errors every write cannot pass. A workload that never finishes (hung server) also fails the seed, class `unfinished`.
- Determinism: the first two seeds are run again and must give the same host-call hash and verdict.

Caveat on the progress guard: a bug that itself starves the workload (`liveness-redial`) trips it on the buggy code, so that task's buggy reward is 0 rather than its pass rate of 0.33. A partial fix on such a task also scores 0 until most seeds make progress.

## Tasks and measured numbers

Eight-core Windows machine, `-n` hidden seeds from the dev salt. "sec/grade" is one full grade of the buggy workspace (stage, `go build` for wasip1, compile, run `n` seeds, determinism rerun). Build time is 5 to 12 seconds of that.

| Task | System | Bug class | n | buggy pass rate | buggy reward | oracle | cheat | nop equals buggy | sec/grade | floor |
|---|---|---|---:|---:|---:|---:|---:|---|---:|---:|
| kvlog-torn-tail | kvlog | recovery keeps a torn log tail | 64 | 0.22 | 0.219 | 1.000 | 0 | yes | 7.1 | 2 |
| kvlog-fsyncgate | kvlog | failed fsync leaves the record in the page cache | 64 | 0.16 | 0.156 | 1.000 | 0 | yes | 8.0 | 16 |
| kvlog-no-checksum | kvlog | silent read corruption, no checksum | 64 | 0.36 | 0.359 | 1.000 | 0 | yes | 9.8 | 15 |
| kvlog-ack-before-fsync | kvlog | ack before the batched fsync | 64 | 0.14 | 0.141 | 1.000 | 0 | yes | 8.0 | 21 |
| symswap-dir-fsync | symswap | symlink swap without directory fsync | 64 | 0.34 | 0.344 | 1.000 | 0 | yes | 9.5 | 12 |
| ledger-nofsync | ledger | ack without fsync | 64 | 0.09 | 0.094 | 1.000 | 0 | yes | 10.9 | 35 |
| ledger-nofsync-heavy | ledger | same code, harsher faults (variant) | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 12.5 | 13 |
| httpkv-nofsync | httpkv | ack without fsync (HTTP) | 64 | 0.12 | 0.125 | 1.000 | 0 | yes | 26.5 | 13 |
| httpkv-nofsync-heavy | httpkv | same code, harsher faults (variant) | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 25.7 | 4 |
| liveness-redial | liveness | primary never reconnects to its replica | 64 | 0.33 | 0.000 (guard) | 1.000 | 0 | yes | 13.9 | 20 |
| liveness-dedupe | liveness | non-idempotent client retry | 64 | 0.20 | 0.203 | 1.000 | 0 | yes | 16.4 | 20 |
| aikv-haiku-1-tail | aikv (model-written store) | out-of-order writeback read as corruption, store never reopens | 32 | 0.00 | 0.000 | 1.000 | 0 | yes | 30.1 | 285 |

Every task satisfies: oracle reward exactly 1.0, buggy below 0.5, cheat 0, a nop grade identical hash-for-hash to the buggy grade. The full machine-readable table is `validation.json`.

### Dropped, and why

| Task | Reason |
|---|---|
| mmapwal | The failing property is a `sim.Always` inside the server; the fix is relaxing that assertion. An agent can delete the check and score 1.0, so it is a reward hack, not a bug |
| buggify (non-idempotent retry) | The fault is injected by `sim.Buggify` calls inside the server code. Deleting those calls makes every seed pass without fixing idempotency. Needs an injection point outside editable code before it can be a task |
| metapage-reorder | Built and validated, but no fault mix got the buggy reward below 0.84 (more crashes, disk latency, page-independent reordering, 400 writes; best 54 of 64 hidden seeds passing). Fails the buggy < 0.5 rule |
| upgrade-inplace | The in-place migration is crashed once per run. Configs where the oracle held 1.0 left the buggy code passing 89% to 97% of seeds. The one config that pushed buggy to 0.36 (`crash_per_sec` 3, `crash_on_io` 0.05) made the oracle fail 8 of 64 seeds |
| aikv-opus-2-header | Real bug, but only 3% to 6% of hidden seeds hit it (seed 99 of 100 in the original, buggy passes 15 of 16 at the harshest config tried), and a seed costs about 2 s of CPU |

The patches, configs and author scripts for dropped tasks are still in `tasks_src/`; they are just not in `tasks.json`.

## How tasks are built

`tasks.json` has `systems` (a map from tree names to seedfall example directories, the editable paths, and the base seedfall config) and `tasks`. `build_tasks` checks out the trees, then applies patch lists with `git apply`:

- `patches.buggy`: pristine example to the task's buggy workspace. Flags and the guarded fix code are removed and the buggy behavior is hardwired; the progress hook (`sim.Event("gym.ack", ...)`) is added to the workload.
- `patches.fixed`: buggy to the oracle. This is also written to `solution/fix.patch`.
- `patches.cheat`: buggy to the cheat that rejects every write.

The workspace is the editable trees of the buggy checkout; the harness is the other trees plus `go.mod`, the merged `seedfall.json` and `baseline.json`. The seedfall `replace` directive is stripped from the example go.mod and re-added at grade time pointing at the seedfall checkout.

### Adding tasks by mutation

A task can carry `variants`, and a task can set `extends`. A child inherits everything from its parent. Its `config` is deep-merged over the parent's (arrays and scalars replace, `null` deletes a key), its patch lists are appended after the parent's, and `n`, `difficulty`, `bug_class`, `symptom` and `visible_seed` fall back to the parent. So a new task is a few lines:

```json
{ "id": "ledger-nofsync-heavy", "config": { "faults": { "crash_per_sec": 1.5, "crash_on_io": 0.02 } } }
```

Two kinds of mutation fit this: fault-mix variants (config merge only, as above) and code variants (an extra `patches.buggy` entry that renames, reorders or changes constants in the parent's buggy tree, with a matching `patches.fixed` entry). After adding entries run `build_tasks`, `validate -calibrate`, `build_tasks`, `validate`; a task that fails the validation rules does not ship.

## Engine API notes

- `engine.Build` sends compiler output straight to `os.Stderr` and returns an opaque `exit status 1`. The grader swaps `os.Stderr` for a temp file during the call to return `build_error`; that is not safe if two grades share a process.
- `Runner.Run` has no context or deadline. A guest that spins is stopped only by the real-time `watchdog` setting (30 s default). One full-grade run of `liveness-dedupe` once stalled past two minutes and could not be reproduced afterwards, so the trainer should wrap `grade` in a process timeout.
- The wazero compile cache lives in `.seedfall/cache` next to the config. A fresh temp dir per grade means every grade recompiles, which with the `go build` is most of the 5 to 12 s fixed cost.
- `Result.Failed` is false for a run whose workload never finished (`Finished` false); the grader counts that as a failure.
- The seedfall checkout used here has uncommitted changes; the manifest records HEAD `00f5aa2`. The example sources the patches apply to are unchanged by them.
- Config path semantics: `pkg` paths resolve relative to the config, which is why the harness config lives at the task root and the editable dirs are copied beside it.
