---
name: seedfall-properties
description: Read a Go service's storage and replication code, turn its durability and consistency promises into seedfall properties (sim.Assert, sim.Always, sim.Sometimes, sim.Reachable, sim.Unreachable, and linearizability through sim.Put and sim.Get), and grade them with seedfall mutate until every exercised durability mutant is killed. Use after seedfall-setup, when asked what to check, to strengthen a workload, or when a green sweep might be proving nothing.
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
| State never goes backwards | In the node: `sim.Always(len(log) >= committed, "log never shrinks below the commit index", len(log), committed)` |
| Something must never happen | `sim.Unreachable("two leaders in the same term", term)` |
| A dangerous path actually ran | `sim.Sometimes(tornTail, "recovery found a torn tail")`, `sim.Reachable("a node restored from a snapshot")` |
| Concurrent clients see a linearizable history | Wrap each call: `op := sim.Put(client, key, value)`, then `op.Ok("")`, `op.Unknown()` on a timeout, or `op.Failed()` when it definitely did not apply. Use `sim.Get` for reads |
| The system recovers | `sim.QuietFor(10 * time.Second)`, then assert that it serves again within that window |

Rules:
- Keep messages constant string literals and pass changing values as extra arguments. seedfall catalogs literal messages before the sweep, and an `Always` or `Reachable` that no seed reaches fails the sweep.
- Add a `Sometimes` for each fault-dependent path you care about. The coverage table then proves the faults reached it.
- Properties can live in nodes. Outside the simulator, `Always` and `Unreachable` panic on violation and the others do nothing, so only put invariants in node code that must hold in production too.

## 3. Sweep and read the coverage table

```sh
seedfall run -c seedfall.json -seeds 1-100
```

Over MCP: `seedfall_run`, which returns `properties` with `seeds_reached`, `seeds_true` and a `problem` for each one.

- A `Sometimes` that is never true means the faults never reached that path. Raise `crash_on_io` (around 0.005 to 0.02), set `torn_writes`, lengthen `duration`, or make the workload write more.
- If no outside fault can reach a path (a lost reply after a successful apply, an error return, a minimal quorum), mark it in the service with `if sim.Buggify("site") { ... }` and set `"buggify": true` in `faults`. The sweep then lists each site as reached, enabled and fired; see the MCP resource `seedfall://docs/buggify` (docs/buggify.md).
- A failure here may be a real bug. Switch to the `seedfall-triage` skill.

## 4. Grade the checks with mutants

```sh
seedfall mutate -c seedfall.json -seeds 1-30
```

The baseline must pass first. Then each mutant weakens one disk guarantee under the nodes:

| Mutant survived | Meaning | Fix |
|---|---|---|
| `fsync_noop` | Nothing checks that acknowledged data survives a crash, or crashes never land between a write and its fsync | Read back every acknowledged write after crashes. Raise `crash_on_io` and set `torn_writes` |
| `dirsync_noop` | Directory fsyncs are untested | Add `"strict_dirs": true` to `faults`, then check data that depends on a create or rename |
| `osync_ignored` | Writes to `O_SYNC` files are untested | Check the data written through those files |
| `fsync_corrupts` | Nothing compares the data read back after a crash with what was acknowledged | Read back every acknowledged value after crashes and compare it byte for byte, not only its presence |

`not exercised` means the operation never ran, or had no effect in this config. That is fine when the service never uses it, such as `dirsync_noop` without `strict_dirs`.

Repeat steps 2 to 4 until every exercised mutant is killed, or until you can state why a survivor is equivalent, for example data the service never promises to keep. `seedfall mutate` exits 1 while any mutant survives.

## 5. Widen the fault model

Once the basic sweep and the mutants pass, add one fault class at a time and sweep again: `fsync_eio` (around 0.01), `strict_dirs`, `partition_per_sec`, then `pause_per_sec` and `clock_jump_per_sec` for lease and timeout logic, and for consensus or leader election `partition_topologies`, `clog_per_sec` and `slow_per_sec` (one-way links, jammed links and slow leaders). For storage code, add the disk faults next: `disk_bytes` and `write_eio` for write error paths, `read_corrupt` and `corrupt_on_crash` for checksums, and on replicated systems `lost_write`, `misdirected_write` and `bitrot_per_sec` (see the MCP resource `seedfall://docs/faults`, section Disk faults). Set `swarm` for long CI sweeps. A fault class that breaks the service is a finding, not noise: triage it.
