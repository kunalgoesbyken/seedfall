# seedfall-gym

An RL environment core. An agent gets a small Go system with a crash-consistency or distributed-systems bug and must fix the code. The reward comes from replaying hidden, deterministic fault seeds through [seedfall](../seedfall), a deterministic simulation tester: the fraction of hidden seeds on which the fixed system survives.

This repo holds the grader, the generated tasks, the task builder and the validation command. It does not contain the agent loop or the `verifiers` wiring.

## Layout

| Path | What |
|---|---|
| `grader/` | Go module. `grade`, `validate`, `build_tasks` commands and `internal/gym` library. Depends on seedfall through `replace ... => ../../seedfall`, drop the `replace` to use a published module |
| `tasks.json` | Declarative manifest: systems (which seedfall example dirs, base config), tasks, variants |
| `tasks_src/` | Patch files the manifest names, plus `author/*.py` that produced them from the pristine seedfall examples. `author/*_scale.py` hold the mutations of the generated tasks, `scale_specs.py` and `scale_symptoms.py` the manifest entries, and `scale_manifest.py` merges them into `tasks.json` |
| `tasks/<id>/` | Generated. `workspace/` (agent sees and edits), `harness/` (pristine workload, config, go.mod, progress floor), `solution/` (oracle files and `fix.patch`), `task.json`, `visible_seed` |
| `cheats/<id>/` | Generated. The scripted cheat for each task: the editable dir with every write rejected |
| `baselines.json` | Progress floors measured from the oracle by `validate -calibrate` |
| `splits.json` | Train and held-out split, written by `build_tasks` when it builds every task |
| `validation.json` | Output of the last full validation |
| `harness/` | Python agent harness and the env adapter `harness/gym_env.py` (`uv run python -m harness.run --env harness.gym_env:Factory ...`); `harness/reference.py` runs the null and oracle rows |
| `runs/` | Raw baseline trajectories, one JSONL per episode, and `results.json` the summary |

## Commands

All commands run from the repo root. `SEEDFALL_SRC` points at the seedfall checkout (the same one the grader links), the guest programs are built against it.

```sh
export SEEDFALL_SRC=D:/code/seedfall
(cd grader && go build -o ../bin/ ./grade ./validate ./build_tasks)

./bin/build_tasks.exe                       # regenerate tasks/ and cheats/ from tasks.json, and splits.json
./bin/build_tasks.exe -only kvlog-torn-tail  # one task, leaves splits.json alone
./bin/validate.exe -calibrate               # measure progress floors, then rebuild tasks (-floor-divisor 4 for a lower floor)
./bin/validate.exe                          # full validation, prints the table, writes validation.json
./bin/validate.exe -only kvlog-torn-tail    # one task
./bin/validate.exe -j 4 -report out.json    # cap the parallel runs of each grade, write the table elsewhere

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

Eight-core Windows machine shared with other jobs, `-n` hidden seeds from the dev salt (the heavier tasks use fewer seeds). "sec/grade" is one full grade of the buggy workspace (stage, `go build` for wasip1, compile, run `n` seeds, determinism rerun). Build time is 5 to 12 seconds of that.

| Task | System | Bug class | n | buggy pass rate | buggy reward | oracle | cheat | nop equals buggy | sec/grade | floor | split |
|---|---|---|---:|---:|---:|---:|---:|---|---:|---:|---|
| kvlog-torn-tail | kvlog | recovery keeps a torn log tail | 64 | 0.22 | 0.219 | 1.000 | 0 | yes | 7.1 | 2 | train |
| kvlog-fsyncgate | kvlog | failed fsync leaves the record in the page cache | 64 | 0.16 | 0.156 | 1.000 | 0 | yes | 8.0 | 16 | held-out |
| kvlog-no-checksum | kvlog | silent read corruption, no checksum | 64 | 0.36 | 0.359 | 1.000 | 0 | yes | 9.8 | 15 | train |
| kvlog-ack-before-fsync | kvlog | ack before the batched fsync | 64 | 0.14 | 0.141 | 1.000 | 0 | yes | 8.0 | 21 | held-out |
| symswap-dir-fsync | symswap | symlink swap without directory fsync | 64 | 0.34 | 0.344 | 1.000 | 0 | yes | 9.5 | 12 | train |
| ledger-nofsync | ledger | ack without fsync | 64 | 0.09 | 0.094 | 1.000 | 0 | yes | 10.9 | 35 | train |
| ledger-nofsync-heavy | ledger | same code, harsher faults (variant) | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 12.5 | 13 | train |
| httpkv-nofsync | httpkv | ack without fsync (HTTP) | 64 | 0.12 | 0.125 | 1.000 | 0 | yes | 26.5 | 13 | train |
| httpkv-nofsync-heavy | httpkv | same code, harsher faults (variant) | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 25.7 | 4 | train |
| liveness-redial | liveness | primary never reconnects to its replica | 64 | 0.33 | 0.000 (guard) | 1.000 | 0 | yes | 13.9 | 20 | held-out |
| liveness-dedupe | liveness | non-idempotent client retry | 64 | 0.20 | 0.203 | 1.000 | 0 | yes | 16.4 | 20 | train |
| aikv-haiku-1-tail | aikv (model-written store) | out-of-order writeback read as corruption, store never reopens | 32 | 0.00 | 0.000 | 1.000 | 0 | yes | 30.1 | 285 | held-out |
| kvlog-no-dir-sync | kvlog | log file's directory entry never fsynced (strict dirs) | 64 | 0.28 | 0.281 | 1.000 | 0 | yes | 8.5 | 19 | train |
| kvlog-sync-wrong-handle | kvlog | fsync called on the directory instead of the log file | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 9.0 | 13 | train |
| kvlog-truncate-off-by-one | kvlog | torn-tail repair cuts one byte too many, next record glued on | 64 | 0.22 | 0.219 | 1.000 | 0 | yes | 7.5 | 2 | train |
| kvlog-write-error-ignored | kvlog | write EIO ignored, PUT still acknowledged | 64 | 0.08 | 0.078 | 1.000 | 0 | yes | 11.4 | 17 | train |
| kvlog-no-reread | kvlog | transient flipped-bit read treated as a torn tail and truncated | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 7.3 | 14 | train |
| kvlog-sync-before-write | kvlog | fsync issued before the write it should cover | 64 | 0.08 | 0.078 | 1.000 | 0 | yes | 12.1 | 16 | train |
| kvlog-rollback-truncates-log | kvlog | failed-fsync rollback truncates the whole log | 64 | 0.08 | 0.078 | 1.000 | 0 | yes | 7.3 | 17 | train |
| kvlog-fsync-error-swallowed | kvlog | fsync EIO ignored, PUT still acknowledged | 64 | 0.08 | 0.078 | 1.000 | 0 | yes | 77.1 | 19 | train |
| symswap-gen-unsynced | symswap | generation file published without fsyncing its data | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 11.3 | 14 | train |
| symswap-no-publish-sync | symswap | no directory fsync after the symlink rename | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 11.7 | 12 | train |
| symswap-gc-before-publish | symswap | old generation deleted before the new pointer is durable | 64 | 0.36 | 0.359 | 1.000 | 0 | yes | 9.8 | 13 | train |
| ledger-torn-tail-kept | ledger | torn log tail left in place, next entries glued onto it | 64 | 0.03 | 0.031 | 1.000 | 0 | yes | 19.9 | 53 | train |
| ledger-sync-before-write | ledger | fsync issued before the write it should cover | 64 | 0.27 | 0.266 | 1.000 | 0 | yes | 10.6 | 16 | train |
| httpkv-no-dir-sync | httpkv | no directory fsync after creating the log (strict dirs) | 64 | 0.30 | 0.297 | 1.000 | 0 | yes | 16.6 | 15 | train |
| httpkv-torn-tail-kept | httpkv | torn log tail left in place, next records glued onto it | 64 | 0.03 | 0.031 | 1.000 | 0 | yes | 67.0 | 1 | train |
| metapage-single-meta-slot | metapage | metadata updated in place, no second slot to fall back to | 64 | 0.39 | 0.391 | 1.000 | 0 | yes | 9.0 | 3 | held-out |
| metapage-meta-unsynced | metapage | ack before the meta slot is fsynced | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 7.8 | 7 | train |
| liveness-log-unsynced | liveness | ack before the replica's log is fsynced | 64 | 0.03 | 0.031 | 1.000 | 0 | yes | 11.2 | 20 | train |
| liveness-applied-not-recovered | liveness | dedupe table not rebuilt from the log on restart | 64 | 0.44 | 0.438 | 1.000 | 0 | yes | 11.1 | 20 | train |
| liveness-backup-error-ignored | liveness | backup replication failure ignored, ack still sent | 64 | 0.02 | 0.016 | 1.000 | 0 | yes | 10.7 | 20 | train |
| aikv-sonnet-1-base-unsynced | aikv-sonnet-1 | compaction output renamed into place without fsyncing its data | 16 | 0.00 | 0.000 | 1.000 | 0 | yes | 35.1 | 846 | train |
| aikv-opus-1-fsync-error-ignored | aikv-opus-1 | fsync EIO ignored, write still acknowledged | 32 | 0.16 | 0.156 | 1.000 | 0 | yes | 23.1 | 24 | train |
| aikv-sonnet-3-compact-unsynced | aikv-sonnet-3 | compaction output renamed into place without fsyncing its data | 16 | 0.00 | 0.000 | 1.000 | 0 | yes | 42.0 | 813 | held-out |
| etcdraft-wal-unsynced | etcdraft | raft WAL appended but never fsynced before messages go out | 16 | 0.31 | 0.312 | 1.000 | 0 | yes | 13.3 | 1 | held-out |
| etcdraft-snapshot-data-unsynced | etcdraft | snapshot renamed into place without fsyncing its data | 16 | 0.38 | 0.375 | 1.000 | 0 | yes | 42.7 | 1 | train |
| raftkv-log-unsynced | raftkv | raft log store skips fsync | 16 | 0.31 | 0.312 | 1.000 | 0 | yes | 20.4 | 52 | held-out |
| liveness-apply-before-replicate | liveness | applies locally before replicating, the retry skips the backup | 64 | 0.03 | 0.031 | 1.000 | 0 | yes | 11.3 | 20 | train |
| metapage-append-offset-off-by-one | metapage | records appended one byte before the committed end | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 5.3 | 7 | held-out |
| symswap-fsync-wrong-dir | symswap | fsyncs the parent of the data directory | 64 | 0.00 | 0.000 | 1.000 | 0 | yes | 5.7 | 11 | held-out |
| httpkv-sync-before-write | httpkv | fsync issued before the write it should cover | 64 | 0.19 | 0.188 | 1.000 | 0 | yes | 46.5 | 21 | held-out |

Every task satisfies: oracle reward exactly 1.0, buggy below 0.5, cheat 0, a nop grade identical hash-for-hash to the buggy grade. The full machine-readable table is `validation.json`. The 12 first tasks keep the numbers they were calibrated with. `kvlog-sync-before-write` was calibrated with `-floor-divisor 4` (a quarter of the lowest oracle ack count): with the default half, one oracle seed with 21 acknowledged writes fell under a floor of 32 and the oracle scored 0.984. The two etcdraft tasks count only acknowledged writes, so a calibration seed with 0 or 1 acknowledged writes gives them a floor of 1.

Tasks 13 to 42 were generated the SWE-smith way: each seedfall example with a real workload is hardwired into its correct form (`kvlog-full.patch`, `symswap-full.patch`, ...), a hand-written mutation then breaks exactly one durability or correctness mechanism (a dropped or misdirected fsync, a missing directory fsync, an ignored I/O error, a misplaced step, a wrong offset), the three patches of each task (`-bug`, `-fix`, `-cheat`) come from `tasks_src/author/*_scale.py`, and `validate` keeps only the candidates that pass every rule above. The failing check always lives in the workload that the grader restores. Where the editable code holds `sim.Always` assertions (symswap, metapage) a probe replaced every one of them with a no-op and the buggy code still scored below 0.5 (0.00 to 0.31), so deleting the assertion does not pass the task.

42 tasks by system: kvlog 12, liveness 6, symswap 5, httpkv 5, ledger 4, aikv 4 (aikv-haiku-1, sonnet-1, opus-1, sonnet-3), metapage 3, etcdraft 2, raftkv 1. They cover 25 bug classes: ack-before-fsync 8, rename-before-data-fsync 4, torn-tail-recovery 3, fsync-before-write-order 3, missing-dir-fsync-on-create 2, fsync-wrong-target 2, fsync-error-ignored 2, and 18 classes with one task each (for example recovery off-by-one, append-offset off-by-one, write error ignored, transient read error destroys data, rollback truncates the whole log, in-place metadata update, dedupe table lost on restart, apply before replicate). The `bug_class` field of each task names its class.

### Dropped, and why

| Task | Reason |
|---|---|
| mmapwal | The failing property is a `sim.Always` inside the server; the fix is relaxing that assertion. An agent can delete the check and score 1.0, so it is a reward hack, not a bug |
| buggify (non-idempotent retry) | The fault is injected by `sim.Buggify` calls inside the server code. Deleting those calls makes every seed pass without fixing idempotency. Needs an injection point outside editable code before it can be a task |
| metapage-reorder | Built and validated, but no fault mix got the buggy reward below 0.84 (more crashes, disk latency, page-independent reordering, 400 writes; best 54 of 64 hidden seeds passing). Fails the buggy < 0.5 rule |
| upgrade-inplace | The in-place migration is crashed once per run. Configs where the oracle held 1.0 left the buggy code passing 89% to 97% of seeds. The one config that pushed buggy to 0.36 (`crash_per_sec` 3, `crash_on_io` 0.05) made the oracle fail 8 of 64 seeds |
| aikv-opus-2-header | Real bug, but only 3% to 6% of hidden seeds hit it (seed 99 of 100 in the original, buggy passes 15 of 16 at the harshest config tried), and a seed costs about 2 s of CPU |
| symswap-nonatomic-publish | Replaces the symlink with remove then symlink instead of a rename. Buggy passed 50% to 69% of seeds over four fault mixes (`crash_on_io` up to 0.15, disk latency up to 60 ms, 4 crashes per second); the crash has to land between the two calls |
| metapage-slot-crc-unchecked | Meta slot read without its CRC check. Buggy passed 69% at best (`crash_on_io` 0.1, 2 crashes per second): the write has to be torn inside the 20-byte slot |
| etcdraft-snapshot-dir-unsynced | No directory fsync after the snapshot rename. Buggy passed 8 of 8 seeds, also at 2 crashes per second with strict directories |
| aikv-haiku-2-compact-unsynced | Compaction output not fsynced before the rename. Buggy passed 8 of 8 seeds at 5000 operations and 1.5 crashes per second: the compacted file becomes the active log, so the next append's fsync flushes it |
| aikv-opus-3-active-dir-unsynced, aikv-opus-3-snapshot-unsynced, aikv-sonnet-2-commit-before-data-sync | Missing directory fsync on the new active log, unsynced snapshot data, commit record written before the data fsync. Buggy passed every seed tried (8 of 8, 3 of 3, 8 of 8) at 5000 operations and 1.5 crashes per second; the window is a single disk operation |
| aikv-haiku-3-fsync-error-ignored | Buggy failed every seed, but the unmodified store scored 0.969 as oracle (one of 32 seeds fails under fsync EIO), so the oracle is not clean |
| jmsraft-local-reads | Reads served from the local state machine, not linearizable on every seed, but a nop grade differed from the buggy grade: the Raft library's goroutines are not deterministic run to run under pauses, so seedfall cannot replay it hash for hash |
| symswap-stale-tmp-link | Stale `current.tmp` not removed before the symlink. Buggy passed 16 of 16: a crash almost never lands between the symlink and the rename |
| kvlog-checksum-key-only, kvlog-no-parent-sync | Superseded and not kept. A checksum over the key only is hit only when the one flipped bit falls in a value during the last restart (13 of 16 passed); a missing parent-directory fsync alone is a coin flip on whether the directory entry survives the first crash (44% to 50% passed). `kvlog-no-reread` and `kvlog-no-dir-sync` replace them |

The patches, configs and author scripts for dropped tasks are still in `tasks_src/`; they are just not in `tasks.json`.

## Train and held-out split

`splits.json` holds a deterministic split, written by `build_tasks` when it builds every task (`-held-out`, default 0.25). Tasks are grouped by (system, bug class) and a group never straddles the split. Groups are ordered by SHA-256 of a fixed salt and the group key, and moved to the held-out side while that keeps the held-out count at or below a quarter of the tasks. The result depends only on the manifest content, not on its order (`TestAssignSplitsIsDeterministicAndOrderFree`), and `TestAssignSplitsKeepsGroupsWhole` checks that no group is split. Adding or dropping a task can move groups, so regenerate it with the manifest.

Current split: 31 train and 11 held-out tasks (74% and 26%). The held-out set is `aikv-haiku-1-tail`, `aikv-sonnet-3-compact-unsynced`, `etcdraft-wal-unsynced`, `httpkv-sync-before-write`, `kvlog-ack-before-fsync`, `kvlog-fsyncgate`, `liveness-redial`, `metapage-append-offset-off-by-one`, `metapage-single-meta-slot`, `raftkv-log-unsynced`, `symswap-fsync-wrong-dir`. It holds three systems that train never sees (`aikv-haiku-1`, `aikv-sonnet-3`, `raftkv`), six bug classes that train never sees (ack-before-durable, append-offset-off-by-one, fsync-failure-leaves-record, in-place-metadata-update, no-reconnect-to-replica, torn-tail-treated-as-corruption), and four classes that train has only in other systems (ack-before-fsync, fsync-before-write-order, fsync-wrong-target, rename-before-data-fsync). So it tests unseen code and unseen bugs in familiar code, and none of it is a fault-mix variant of a train task: the two variant pairs (`ledger-nofsync` and `-heavy`, `httpkv-nofsync` and `-heavy`) are one group and both train.

## Baselines

Run on 2026-11-16 with the agent harness in `harness/` (`harness.gym_env:Factory`), 12 tasks, hidden grading with a secret local salt (64 hidden seeds per task, 32 for `aikv-haiku-1-tail`). Everything here was free tier except Haiku, which cost $0.076 in total. Raw trajectories are in `runs/`, the machine-readable table in `results.json`.

| model | episodes | pass@1 | mean reward | false-claim rate | mean turns | mean tokens | cost |
|---|---:|---:|---:|---:|---:|---:|---:|
| null (submit unchanged) | 12 | 0.000 | 0.163 | 1.000 | - | - | $0 |
| oracle (apply `solution/`) | 12 | 0.917 | 0.999 | 0.083 | - | - | $0 |
| gemini-3.1-flash-lite | 24 | 0.833 | 0.863 | 0.000 | 11.0 | 63k | $0 |
| gemini-3.5-flash-lite | 24 | 0.750 | 0.823 | 0.125 | 13.8 | 95k | $0 |
| claude-haiku-5-5 | 24 | 1.000 | 1.000 | 0.000 | 5.1 | 22k | $0.076 |
| gemini-3.5-flash | 1 | 1.000 | 1.000 | 0.000 | 20.0 | 269k | $0 |
| nvidia/nemotron-3-ultra-550b-a55b:free (OpenRouter) | 2 | 1.000 | 1.000 | 0.000 | 17.5 | 59k | $0 |

`pass@1` is the share of episodes with reward exactly 1.0. The false-claim rate is the share of episodes where the agent said it was done (called `submit`, or the final message claimed success) and the reward was below 1. `submit` counts as a claim, which is why null is 1.000. Cost is the harness ledger, not a bill. 12 tasks, 2 episodes per task for the three full rows, so n = 24 and one episode moves a rate by 4 points.

Per task, solved episodes out of episodes (mean reward in brackets):

| task | null | oracle | flash-lite 3.1 | flash-lite 3.5 | haiku-5-5 |
|---|---|---|---|---|---|
| aikv-haiku-1-tail | 0/1 (0.16) | 1/1 (1.00) | 1/2 (0.50) | 1/2 (0.58) | 2/2 (1.00) |
| httpkv-nofsync | 0/1 (0.09) | 1/1 (1.00) | 2/2 (1.00) | 2/2 (1.00) | 2/2 (1.00) |
| httpkv-nofsync-heavy | 0/1 (0.00) | 1/1 (1.00) | 2/2 (1.00) | 2/2 (1.00) | 2/2 (1.00) |
| kvlog-ack-before-fsync | 0/1 (0.12) | 1/1 (1.00) | 2/2 (1.00) | 2/2 (1.00) | 2/2 (1.00) |
| kvlog-fsyncgate | 0/1 (0.27) | 1/1 (1.00) | 1/2 (0.50) | 0/2 (0.25) | 2/2 (1.00) |
| kvlog-no-checksum | 0/1 (0.31) | 0/1 (0.98) | 0/2 (0.36) | 1/2 (0.95) | 2/2 (1.00) |
| kvlog-torn-tail | 0/1 (0.38) | 1/1 (1.00) | 2/2 (1.00) | 2/2 (1.00) | 2/2 (1.00) |
| ledger-nofsync | 0/1 (0.06) | 1/1 (1.00) | 2/2 (1.00) | 2/2 (1.00) | 2/2 (1.00) |
| ledger-nofsync-heavy | 0/1 (0.00) | 1/1 (1.00) | 2/2 (1.00) | 2/2 (1.00) | 2/2 (1.00) |
| liveness-dedupe | 0/1 (0.20) | 1/1 (1.00) | 2/2 (1.00) | 1/2 (0.60) | 2/2 (1.00) |
| liveness-redial | 0/1 (0.00) | 1/1 (1.00) | 2/2 (1.00) | 1/2 (0.50) | 2/2 (1.00) |
| symswap-dir-fsync | 0/1 (0.36) | 1/1 (1.00) | 2/2 (1.00) | 2/2 (1.00) | 2/2 (1.00) |

### Setup

- Tools: `list_files`, `read_file`, `write_file`, `edit_file`, `run_seeds(seeds)` and `submit()`. `run_seeds` calls `grade -seeds` on up to 4 integer seeds between 0 and 1000 (at most 12 calls per episode) and returns pass or fail, the reason and the last 20 trace lines. The hidden seeds, the salt and the guard report are never shown. The reward is a separate hidden-seed grade after the episode ends, in a clean process environment, with a process timeout.
- The prompt is the task's `symptom` from `task.json` plus a fixed system prompt. It does not contain the bug class or any solution text.
- Caps: 30 turns, 250k total tokens, 8192 completion tokens, 600 s per episode, native tool calls, provider default sampling. Episodes that hit a cap are still graded on the workspace as it stands.
- `harness/reference.py` produces the null and oracle rows with no model. Null reproduces the buggy reward exactly; oracle applies `solution/`.
- The tasks and `grade.exe` were run from a snapshot of commit `c373cdf` (seedfall `00f5aa2`), because other work in the same checkout was adding tasks while the runs were in progress.

### Caveats and findings

- Small n. 24 episodes per full row, 2 per task; the differences between the two Gemini rows are within noise. Rewards use one secret salt, so a task's buggy reward differs from the dev-salt table above by sampling noise (for example kvlog-torn-tail 0.375 here, 0.219 there).
- Free tiers cut the roster short. `gemini-3.5-flash` allows 20 requests per day, so one episode finished and the second hit a 429. `gemma-4-31b-it` through the Gemini API returned HTTP 500 and then 503 (high demand) and no episode finished; the OpenRouter `:free` Gemma copy was rate limited upstream on the first request. The OpenRouter nemotron row stopped at 2 episodes when the 50 requests per day budget ran out. Those three rows are anecdotes, not baselines; the three rows with n = 24 are.
- Most of this task set is solved by current small models. Haiku 5.5 solved all 24 episodes in about 5 turns, so with this prompt the set does not separate strong agents. The hard rows are `kvlog-fsyncgate`, `kvlog-no-checksum` and `aikv-haiku-1-tail` for the Gemini models.
- The agent sees a detailed symptom and can replay visible seeds up to 48 times, so a part of the score is test-time search on visible seeds, not blind repair. A tighter prompt or a smaller `run_seeds` budget would make the set harder.
- Oracle ceiling: with this hidden salt the oracle for `kvlog-no-checksum` scores 63 of 64 (0.984, reproduced twice), one seed loses an acknowledged write. The "oracle is exactly 1.0" rule above held on the dev salt only. Haiku found a stricter fix (re-read until every complete line verifies) that scored 1.0 on both episodes, so the task is solvable, but its reference solution is not a perfect one.
- Two episodes of `gemini-3.5-flash-lite` on `liveness-redial` and one of `gemini-3.1-flash-lite` on `aikv-haiku-1-tail` ended on the episode time or token cap with a partial result. Candidate code that never terminates is caught by the 300 s `run_seeds` timeout and costs the agent that call.

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

The tasks 13 to 42 use the other route, one new bug per task rather than a variant. `python tasks_src/author/<name>_scale.py` (run from `tasks_src/author`, `SEEDFALL_SRC` pointing at seedfall) writes `<system>-full.patch` (pristine example to its correct form) and, for each task, `<id>-bug.patch`, `<id>-fix.patch` and `<id>-cheat.patch`. `scale_specs.py` holds the manifest entry of each task (patch lists, fault config, symptom from `scale_symptoms.py`, visible seed) and `python scale_manifest.py` merges them into `tasks.json`, leaving the first ten entries untouched. The cheat of every task rejects every write (the aikv cheats also reject deletes, otherwise a no-op delete keeps the acknowledged-operation count above the floor). A cheat that still scores above 0 means the progress count is too loose: that is why the etcdraft client counts only `PUT` replies as acknowledged operations, not reads.

## Engine API notes

- `engine.Build` sends compiler output straight to `os.Stderr` and returns an opaque `exit status 1`. The grader swaps `os.Stderr` for a temp file during the call to return `build_error`; that is not safe if two grades share a process.
- `Runner.Run` has no context or deadline. A guest that spins is stopped only by the real-time `watchdog` setting (30 s default). One full-grade run of `liveness-dedupe` once stalled past two minutes and could not be reproduced afterwards, so the trainer should wrap `grade` in a process timeout.
- The wazero compile cache lives in `.seedfall/cache` next to the config. A fresh temp dir per grade means every grade recompiles, which with the `go build` is most of the 5 to 12 s fixed cost.
- `Result.Failed` is false for a run whose workload never finished (`Finished` false); the grader counts that as a failure.
- The seedfall checkout used here has uncommitted changes; the manifest records HEAD `00f5aa2`. The example sources the patches apply to are unchanged by them.
- Config path semantics: `pkg` paths resolve relative to the config, which is why the harness config lives at the task root and the editable dirs are copied beside it.
