# Testing seedfall itself

seedfall is only useful if its own model of crashes, faults and replay is right, so that model has its own tests. This page lists what they check and how to run them.

## What the tests check

| Area | Test file | What it proves |
|---|---|---|
| Disk durability | `engine/memfs_test.go` | Synced writes survive a crash, unsynced writes and never-synced files do not, `O_SYNC` makes every write durable, an unsynced truncate is undone |
| Torn writes | `engine/memfs_test.go` | A torn crash keeps an in-order prefix of the pending writes, the last one possibly cut mid-way, and records the tear |
| Out-of-order writeback | `engine/memfs_test.go` | Writes split at 4 KiB page boundaries, pages persist independently (a later page can survive without an earlier one), and writes to one page persist in order |
| fsync `EIO` | `engine/memfs_test.go` | A failed fsync drops the pending writes while they stay readable, and a retried fsync does not bring them back |
| Disk faults | `engine/diskfaults_test.go` | `disk_bytes` cuts a write short and then refuses it, flagged so the guest gets `ENOSPC`, while overwrites still fit; a failed write writes nothing but counts as a disk operation; a corrupted read flips the returned buffer and never the stored data; a lost write keeps the old page on disk (zeros for a new page) and a misdirected write puts the page at another offset, while the page cache keeps the new content, and rewriting the page later writes it in full; crash corruption flips exactly one bit inside the bytes that were being written; bit rot flips one stored bit; `fsync_corrupts` flips only the durable copy; a misdirected write is chosen from the seed and operation number and replays from its recorded fault |
| File system calls | `engine/memfs_test.go` | `OpenFile` flags, `Mkdir`, `Rename`, `Unlink`, `Rmdir` and `Readdir` return the POSIX errors and results |
| `strict_dirs` | `engine/namespace_test.go` | New entries need the parent directory fsynced to survive, a rename reverts without a directory fsync, and a torn crash recovers an in-order prefix of directory operations |
| Fault schedules | `engine/faults_test.go` | Schedules are identical for the same seed and differ across seeds, stay inside the time horizon and in order, never crash the workload unless asked, and never plan runtime-only faults; swarm rates stay in `[0, 2 x rate)`, swarm mixes are deterministic per seed, and partition sides are never empty |
| Network faults | `engine/netfaults_test.go` | Every partition topology has the promised shape (a ring gives each node a distinct majority), the default `half` topology keeps the old schedule, a one-way clog holds one direction only, `jam` accepts late without an error, `slow` adds its delay, per-link latency overrides apply one way, a reset breaks both ends, including connections waiting to be accepted, while the listener stays open, a slow node stretches its latency and timers, `sim.QuietFor` heals clogs, swizzles clog one node at a time and unclog in random order, and bad settings, including network faults on a single-node config, are rejected |
| Scheduler | `engine/sched_test.go` | The next step is the earliest one; at the same time a planned fault goes before a restart and a restart before a resume; every resume due at that time is a candidate for the seeded scheduler; a paused node waits for its pause to end |
| Shrinking | `engine/shrink_test.go`, `cmd/seedfall/shrink_test.go` | Against a fake runner, `Shrink` keeps the original failure, ignores a different failure, and returns a 1-minimal schedule; delta debugging finds one needed fault among 64 in fewer runs than removing one at a time; downtimes, clock jumps, partition sides and fault times shrink to just what the failure needs, while faults recorded at run time keep their operation; the duration and a `sim.Budget` workload are trimmed, and a workload without `sim.Budget` is left alone; the result and run count are the same for 1, 3 and 8 jobs; a run or time budget stops it with the failure intact; failure matching ignores numbers only. Schedule files round-trip, the older fault arrays still read, and a passing replay gets a note only when a shortened duration ended it before the workload finished |
| Network | `engine/net_test.go` | Messages on one connection arrive in order under random latency, reads wait for arrival, a partition holds messages until it heals, close delivers EOF after data, and a crash resets every connection |
| Linearizability | `engine/history_test.go` | Every model (`kv`, `cas`, `append`, `queue`, `set`, `counter`) accepts and rejects known histories, including unknown, pending and failed operations; operations a model does not have, and inputs or outputs of the wrong type, are rejected; a history survives the JSONL round trip and re-checks the same; a custom Porcupine model checks a history through `PorcupineOperations` |
| Transactional anomalies | `engine/elle_test.go` | The list-append checker finds `G0`, `G1a`, `G1b`, `G1c`, `G-single`, `G2`, internal inconsistency, incompatible orders, duplicate elements and appends, and garbage reads in minimal histories, passes serializable ones and reads of unknown transactions, explains the cycle, and rejects malformed transactions |
| End to end | `engine/guest_test.go` | A small guest in `engine/testdata/guest` runs under crashes, partitions, torn writes, out-of-order writeback, crashes during disk operations and pauses: two runs of a seed give byte-identical traces, `-check` passes, a sweep finds the same failing seeds with 1 or 4 jobs, a failure replays exactly from its saved fault schedule, and a shrunk schedule keeps the failure, replays exactly and is 1-minimal. `sim.Budget` caps the client's writes when a schedule sets `ops`, the trace shows the cap, the run is shorter and replays exactly. Under clogs, swizzles, resets, slow nodes and partition topologies, two runs of a seed and a replay of its fault schedule give byte-identical traces. Under every disk fault the same holds, a full disk and `write_enospc` reach the guest as `syscall.ENOSPC`, and a failure caused by a lost write shrinks to a schedule that keeps the lost write and replays exactly |
| Liveness and workload faults | `engine/liveness_test.go` | Against the guest: with `liveness`, a workload that never finishes fails as `liveness` at `duration + within` and without it only hits the horizon; the recovery phase restarts a node that would stay down and heals a partition, so the workload finishes; a `finally` check starts only after the workload, with no fault after it, and its `sim.Eventually` counts as a property; an `Eventually` that never holds fails as `liveness` with its message; `include_workload` crashes and pauses the client, open operations are reported and stay sound for linearizability, and a replay of the recorded faults is byte-identical; every one of several workloads must finish. Without the guest: `supervise` never restarts a workload that exits with an error, `"faults": false` keeps a node out of every planned fault and every partition, the workload is a fault target only when asked, configs with `workloads[]`, `finally` and `liveness` are validated, and `sim.Eventually` is cataloged |
| Assertions and events | `engine/assertions_test.go`, `sim/sim_test.go` | The catalog finds numeric and grouped assertions with their message at the right argument; Antithesis guidance records keep the smallest margin for `always` and the largest for `sometimes`, and guidance without an assertion creates nothing; `lifecycle.SendEvent` becomes a trace event; margins aggregate across seeds; outside the simulator the numeric and grouped forms fail with JSON details and `sim.Event` does nothing |
| Determinism hash | `engine/hostcalls_test.go` | The host-call hash is stable, checkpointed every 4,096 calls and sensitive to field boundaries, and a divergence report names the first differing call with the call before it, including a run that ends early |
| Mutants | `engine/mutation_test.go` | `rename_not_atomic` loses both names in a crash before the next fsync in loose and strict mode, but not a rename to a new name or one a later fsync closed; `truncate_not_durable` brings back the old tail; `net_drop_after_close` drops only an unread last chunk; `net_dup_chunk` duplicates a seeded share of writes and replays them identically; network mutants skip nodes that take no faults; `monotonic_jump` moves the monotonic clock only when active; `-only` parses and rejects typos. `engine/appmutant_test.go`: source mutants find comparisons, early error returns and `Sync` calls and still parse, the selection is seeded and ordered, and a mutant's overlay merges with the `net_overlay` one |
| Confidence math | `report/report_test.go` | `SeedsToConfirm` and the detectable failure rate against known values, findings moving through new, ongoing, not seen, resolved and regressed across runs, and the JSON report against a golden file |
| Proof rendering | `verify/verify_test.go` | How many seeds a proof needs, and the text and Markdown proof blocks against golden files |

`scripts/regress.sh` then runs every example against its expected pass or fail result, replays saved fault schedules, and checks that replays are identical.

## Running them

```sh
go test ./...                 # everything, including the wasip1 guest build
go test -short ./...          # skip the tests that build and run a wasip1 guest
go test -race ./...           # what CI runs; needs cgo
go test ./report ./verify -update   # rewrite golden files after an intended change, then review the diff
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
bash scripts/regress.sh       # every example; takes hours
```

govulncheck also reports standard library vulnerabilities, so run it with the latest Go patch release, as CI does with `go-version: stable`.

## CI

| Workflow | When | What |
|---|---|---|
| `ci.yml` | every push to `main` and every pull request | gofmt, `go vet`, staticcheck and govulncheck; `go test -race ./...` on Ubuntu, macOS and Windows; and two example sweeps through the GitHub Action with `-check` |
| `regress.yml` | nightly, or by hand | `bash scripts/regress.sh` |
| `codeql.yml` | every push to `main`, every pull request, weekly | CodeQL analysis of the Go code |

Dependabot opens weekly updates for the root module and the GitHub Actions. The examples are left alone on purpose: each pins the library version its findings were made against.
