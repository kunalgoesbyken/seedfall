# Shrink renames the rewritten AOF over the database without fsyncing it, so a crash after a shrink can wipe the database

**Upstream:** [tidwall/buntdb#126](https://github.com/tidwall/buntdb/issues/126)

**Repo:** tidwall/buntdb (tested on v1.3.2)
**Severity:** a crash after a background shrink can lose acknowledged writes, up to the whole database, even with `SyncPolicy: Always`.

## Summary

`(*DB).Shrink` (buntdb.go:636) writes the compacted AOF into `<path>.tmp`, copies the tail of the live AOF into it, closes it (buntdb.go:750) and renames it over the database file (buntdb.go:757, `renameFile`). It never calls `f.Sync()` on the temporary file, and never fsyncs the parent directory after the rename.

- **Missing file fsync.** After the rename, the database name points at an inode whose data may exist only in the page cache. If the machine crashes before writeback, the database file comes back empty or truncated. `load` treats a short file as a torn tail and truncates it, so buntdb opens successfully and silently serves an empty or partial database. With `SyncPolicy: Always`, the next `Update` fsyncs the new file and closes the window. So the exposure lasts from the shrink until the next write: on an idle or read-mostly database that can be indefinitely.
- **Missing directory fsync.** Under POSIX, the rename is durable only once the directory is fsynced. If the rename is lost, the name points back at the old AOF. Writes acknowledged after the shrink went to the new inode, so they are lost.

Shrinking runs automatically from `backgroundManager` (buntdb.go:547) whenever the AOF grows past `AutoShrinkMinSize` and `AutoShrinkPercentage`, so applications don't have to call `Shrink` to be exposed.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall). It runs a small server on buntdb v1.3.2 with `SyncPolicy: Always`, which acknowledges `SET` only after `Update` returns. A client overwrites 40 keys 400 times and then checks that every acknowledged value survived. The simulated disk keeps only fsynced data across a crash. `AutoShrinkMinSize` is lowered to 2 KB so that shrinks happen during the run.

```
cd examples/buntdb
seedfall replay -c seedfall.shrink.json -seed 27
```

This config needs no directory-durability rules. A server that had recovered all 40 keys restarts after a shrink with `recovered 6 keys`, and the client reports `acknowledged write lost: k22=v262, server now answers "NONE"`. `seedfall shrink` reduces the schedule to four plain crashes and one partition, with no torn writes; the partition keeps writes from arriving, so nothing fsyncs the new file before the crash.

With strict POSIX directory semantics (`seedfall.shrink.strict.json`, seed 6), the lost rename shows up instead: `acknowledged write lost: k1=v321, server now answers "VAL v281"`. The database came back as the pre-shrink AOF.

Controls on the same harness:

| Config | Stock v1.3.2 | Patched |
|---|---|---|
| `SyncPolicy: Always`, crashes + partitions (`seedfall.json`) | 300/300 seeds pass | — |
| plus torn writes + crash during I/O (`seedfall.torn.json`) | 300/300 seeds pass | — |
| plus aggressive auto-shrink (`seedfall.shrink.json`) | fails, seed 27 | 300/300 pass (file fsync alone is enough) |
| plus strict directory durability (`seedfall.shrink.strict.json`) | fails, seed 6 | 300/300 pass (file fsync + directory fsync) |
| `SyncPolicy: EverySecond` (`seedfall.everysecond.json`) | fails, seed 1 (expected and documented: up to one second of writes can be lost) | — |

## Real-world exposure

Renaming an unsynced file over an existing one is the pattern behind the 2009 ext4 zero-length-file reports. ext4's default `auto_da_alloc` heuristic forces writeback on rename-over-existing, and btrfs has a similar rename flush, so the file-data half is probably masked on default ext4 and btrfs. Neither behavior is guaranteed by POSIX. It doesn't apply to ext4 mounted with `noauto_da_alloc`, and I haven't verified XFS or other filesystems. The directory-fsync half needs a crash in the short window before the journal commits the rename.

## Suggested fix

In `Shrink`, before `f.Close()`:

```go
if err := f.Sync(); err != nil {
	return err
}
```

After `renameFile` succeeds, fsync the parent directory (skipped on Windows, where directories can't be opened for sync):

```go
if d, err := os.Open(filepath.Dir(fname)); err == nil {
	_ = d.Sync()
	_ = d.Close()
}
```

## Fix verified

A local copy of v1.3.2 with these two changes passes the failing seeds and 300 seeds each of `seedfall.shrink.json` and `seedfall.shrink.strict.json`.
