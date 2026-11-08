---
name: seedfall-properties
description: Read a Go service's storage and replication code, turn its durability and consistency promises into seedfall properties (sim.Assert, sim.Always, sim.Sometimes, sim.Reachable, sim.Unreachable, sim.Eventually, linearizability through sim.Put, sim.Get and sim.Invoke, and transactional isolation through sim.Txn), and grade them with seedfall mutate until every exercised mutant is killed. Use after seedfall-setup, when asked what to check, to strengthen a workload, or when a green sweep might be proving nothing.
compatibility: Requires Go 1.25 or newer, the seedfall CLI, and a seedfall.json that already runs (see seedfall-setup).
---

# Write properties that catch real durability bugs

A passing sweep proves only what the workload checks. Find the service's promises, check each one, prove that the faults reached the dangerous paths, then break the disk on purpose and confirm the checks notice.

## 1. Find the promises

Read the code, not the README. For each item below, note the file and line:

- **Acknowledgement.** Where does the service answer OK, return 2xx or ack a producer? What must already be fsynced at that point: the data file, the directory entry, the index?
- **Recovery.** What does startup do with a torn last record, a missing file, a leftover temp file or a half-finished compaction?
- **Atomic replace.** For each write-temp-then-rename, is the file fsynced before the rename, and is the parent directory fsynced after it?
- **Deletes and compaction.** Can a crash bring back deleted data, or lose data that had been copied?
- **Replication.** When is a write committed? Can a follower serve stale reads? Can two leaders coexist?

Write each promise as one sentence, for example "every acknowledged write is readable after any number of crashes".

## 2. Turn each promise into a check

The full reference is the MCP resource `seedfall://docs/properties` (docs/properties.md).

| Promise | Check |
|---|---|
| Acknowledged writes survive | The workload records each acknowledged write, then reads it back after the faults: `sim.Assert(got == want, "acknowledged write lost: "+key)` |
| State never goes backwards | In the node: `sim.AlwaysGreaterThanOrEqualTo(len(log), committed, "log never shrinks below the commit index")`. The numeric forms (`AlwaysGreaterThan`, `AlwaysLessThan`, `SometimesGreaterThan`, ... and their `OrEqualTo` variants) put `left` and `right` in the failure and report a margin: the smallest one shows how close an `always` came to failing |
| One of several things holds | `sim.AlwaysSome(map[string]bool{"primary": p >= 0, "backup": b >= 0}, "some replica answers")`; `sim.SometimesAll(...)` for a combination that must happen at least once |
| Something must never happen | `sim.Unreachable("two leaders in the same term", term)` |
| A dangerous path actually ran | `sim.Sometimes(tornTail, "recovery found a torn tail")`, `sim.Reachable("a node restored from a snapshot")` |
| Concurrent clients see a linearizable history | Wrap each call: `op := sim.Put(client, key, value)`, then `op.Ok("")`, `op.Unknown()` on a timeout, or `op.Failed()` when it definitely did not apply. Use `sim.Get` for reads |
| A lock, queue, log, set or counter behaves like one | Set `"checker": {"model": "cas"}` (or `queue`, `append`, `set`, `counter`) in seedfall.json and record each call with `sim.Invoke(client, f, key, value)`, for example `sim.Invoke(c, "cas", "lock", []string{"", me})` then `op.Ok(swapped)`, or `sim.Invoke(c, "dequeue", "q", nil)` then `op.Ok(item)`. The table of operations per model is in docs/properties.md, Other models |
| Transactions are serializable | Set `"checker": {"model": "list-append"}`, record each transaction with `op := sim.Txn(client, sim.Read("x"), sim.Append("y", unique))`, and pass the micro-ops back with each read's `Value` set to the list it returned: `op.Ok(mops)`. Appended values must be unique per key. Failures name the anomaly (`G-single`, `G2`, ...) and the cycle |
| The system recovers | Set `"liveness": {"within": "10s"}` in seedfall.json: after `duration` the faults stop, crashed nodes restart, and a workload that does not finish within 10s fails as `liveness`. Inside a program, `sim.Eventually(func() bool { return served() }, "cluster serves reads again", 10*time.Second)` |
| The end state is right | A `"finally": {"name": "check", "pkg": "./check"}` program runs after the workload with faults stopped; check convergence and exactly-once effects there with `sim.Eventually` and `sim.Always` |
| Client retries are safe | `"include_workload": true` in `faults` crashes and pauses the workload too. Derive request ids from `sim.Node()` and saved progress, so a restarted client resends the same id, then check in `finally` that nothing was applied twice |

Rules:
- Keep messages constant string literals and pass changing values as extra arguments. seedfall catalogs literal messages before the sweep, and an `Always` or `Reachable` that no seed reaches fails the sweep.
- Add a `Sometimes` for each fault-dependent path you care about. The coverage table then proves the faults reached it.
- Use `sim.Event("name", details)` to mark a moment (a recovery finishing, a leader change) in the trace without asserting anything; triage then reads it next to the faults.
- Properties can live in nodes. Outside the simulator, `Always`, `Unreachable` and `Eventually` panic on violation and the others do nothing, so only put invariants in node code that must hold in production too.

## 3. Sweep and read the coverage table

```sh
seedfall run -c seedfall.json -seeds 1-100
```

Over MCP: `seedfall_run`, which returns `properties` with `seeds_reached`, `seeds_true`, a `problem` for each one and a `margin` for numeric ones.

- An `always` numeric property whose smallest margin stays far from 0 never got near failing: the faults may not be stressing it. A margin of exactly 0 on an `OrEqualTo` means the boundary was reached.

- A `Sometimes` that is never true means the faults never reached that path. Raise `crash_on_io` (around 0.005 to 0.02), set `torn_writes`, lengthen `duration`, or make the workload write more.
- If no outside fault can reach a path (a lost reply after a successful apply, an error return, a minimal quorum), mark it in the service with `if sim.Buggify("site") { ... }` and set `"buggify": true` in `faults`. The sweep then lists each site as reached, enabled and fired; see the MCP resource `seedfall://docs/buggify` (docs/buggify.md).
- A failure here may be a real bug. Switch to the `seedfall-triage` skill.

## 4. Grade the checks with mutants

```sh
seedfall mutate -c seedfall.json -seeds 1-30
```

The baseline must pass first. Then each mutant weakens one disk, network or clock guarantee under the nodes. Run a subset with `-only rename_not_atomic,net_dup_chunk`, or over MCP with `seedfall_mutate`:

| Mutant survived | Meaning | Fix |
|---|---|---|
| `fsync_noop` | Nothing checks that acknowledged data survives a crash, or crashes never land between a write and its fsync | Read back every acknowledged write after crashes. Raise `crash_on_io` and set `torn_writes` |
| `dirsync_noop` | Directory fsyncs are untested | Add `"strict_dirs": true` to `faults`, then check data that depends on a create or rename |
| `osync_ignored` | Writes to `O_SYNC` files are untested | Check the data written through those files |
| `fsync_corrupts` | Nothing compares the data read back after a crash with what was acknowledged | Read back every acknowledged value after crashes and compare it byte for byte, not only its presence |
| `rename_not_atomic` | Nothing checks a file replaced by rename (state, manifest, snapshot) after a crash | Read back the replaced file after crashes and assert it holds either the old or the new version. Raise `crash_on_io` so crashes land right after a rename |
| `truncate_not_durable` | Nothing checks a file's length after a crash that followed a truncation | Read back truncated or `O_TRUNC`-rewritten files after crashes and compare their whole content |
| `net_drop_after_close` | A response or final message cut off by a close goes unnoticed | Check that every request the client counts as done really got its full answer, and that the server applied every message sent before a close |
| `net_dup_chunk` | A duplicated request or message is not caught | Assert upper bounds too (nothing applied twice, `sim.AlwaysLessThanOrEqualTo(total, sent, ...)`), not only that acknowledged data survived |
| `monotonic_jump` | Timeouts and leases never meet a clock that runs backwards, or nothing checks what they protect | Turn on `clock_jump_per_sec`, then check lease-protected reads with a linearizability checker |

`not exercised` means the operation never ran, or had no effect in this config. That is fine when the service never uses it, such as `dirsync_noop` without `strict_dirs`, or `monotonic_jump` without `clock_jump_per_sec`.

Repeat steps 2 to 4 until every exercised mutant is killed, or until you can state why a survivor is equivalent, for example data the service never promises to keep. `seedfall mutate` exits 1 while any mutant survives.

## 5. Widen the fault model

Once the basic sweep and the mutants pass, add one fault class at a time and sweep again: `fsync_eio` (around 0.01), `strict_dirs`, `partition_per_sec`, then `pause_per_sec` and `clock_jump_per_sec` for lease and timeout logic, and for consensus or leader election `partition_topologies`, `clog_per_sec` and `slow_per_sec` (one-way links, jammed links and slow leaders). For storage code, add the disk faults next: `disk_bytes` and `write_eio` for write error paths, `read_corrupt` and `corrupt_on_crash` for checksums, and on replicated systems `lost_write`, `misdirected_write` and `bitrot_per_sec` (see the MCP resource `seedfall://docs/faults`, section Disk faults). Set `swarm` for long CI sweeps. A fault class that breaks the service is a finding, not noise: triage it.
