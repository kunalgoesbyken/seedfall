# DRAFT ISSUE (not filed): FileSnapshotStore doesn't fsync the snapshot directory before rename, so a crash can leave an empty snapshot after logs were compacted

**Repo:** hashicorp/raft (tested on v1.8.0)
**Severity:** a node can't restart (it panics in `NewRaft`). This needs strict POSIX directory-durability semantics.

## Summary

`FileSnapshotSink` fsyncs `state.bin` and `meta.json`, renames the temporary snapshot directory into place, and then fsyncs the parent directory (`file_snapshot.go`, `Close` → `finalize`/`writeMeta` → `os.Rename` → `parentFH.Sync()`). It never fsyncs the **snapshot directory itself**. POSIX only makes the directory entries for `state.bin` and `meta.json` durable once their containing directory is fsynced. Fsyncing the files makes their *contents* durable, not their names.

After the snapshot is finalized, raft compacts the log (`DeleteRange` up to the snapshot minus trailing logs). If the machine crashes before the filesystem makes those entries durable, then after reboot:

1. `data/snapshots/snapshots/<term>-<index>-<ts>/` exists, because the rename and the parent fsync are durable, but it is **empty**.
2. `FileSnapshotStore.getSnapshots` can't read `meta.json`, so it skips the snapshot, and `restoreSnapshot` restores nothing (`snapshotIndex = 0`).
3. The log store's first index is now past 1 because of the compaction, so `NewRaft` panics in the configuration scan:

```
[ERROR] n2: failed to get log: index=1 error="log not found"
panic: log not found
github.com/hashicorp/raft.NewRaft(...)  api.go:617
```

The node panics on every restart until an operator intervenes.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall). It runs a 3-node cluster built on hashicorp/raft v1.8.0 (`NetworkTransport`, `FileSnapshotStore`, and an fsyncing file log store that also fsyncs its directories) on a simulated disk with strict POSIX directory semantics:

```
cd examples/raftkv
seedfall replay -c strict.lin.json -seed 7
```

The failure needs one fault: crash n2 once after its first snapshot. A diagnostic listing at restart shows `snapshot 2-72-1767225601464 contains []`. The same seed passes when directory entries are treated as durable at creation (`seedfall.lin.json`).

## Real-world exposure

On ext4 (data=ordered), XFS and btrfs, fsyncing a newly created file in practice also persists its directory entry, so typical Linux deployments are probably not affected. POSIX doesn't guarantee that behavior, though. Filesystems that don't provide it, or future changes to these filesystems, would expose the bug. This is the bug class the ALICE study ("All File Systems Are Not Created Equal", OSDI 2014) found in LevelDB and other systems, and it's why PostgreSQL fsyncs directories explicitly.

## Suggested fix

Before the rename in `FileSnapshotSink.Close`, open `s.dir` and `Sync()` it, skipping this on Windows the same way the existing parent-directory sync does (#242). It's about five lines and has the same platform handling as the existing code.

## Fix verified

I patched a local copy of v1.8.0 to open and `Sync()` `s.dir` before the `os.Rename` in `FileSnapshotSink.Close`:
- seed 7 passes;
- seeds 1–120 under strict directory semantics pass.

Without the patch, the sweep failed on seed 7.
