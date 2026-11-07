# Open creates the database file without fsyncing its directory, so one crash can lose the whole database

**Upstream:** not filed yet.

**Repo:** etcd-io/bbolt (tested on v1.5.0, the latest release)
**Severity:** low. It needs a filesystem where a new file's name is durable only after the parent directory is fsynced, plus a crash before anything else syncs that directory. On such filesystems every transaction committed with fsync and acknowledged after `Open` can disappear together with the file.

## Summary

`Open` creates the data file with `os.O_CREATE` (db.go:239). For a new, empty file, `init` writes the two meta pages, the freelist and an empty leaf, and calls `fdatasync` (db.go:646, db.go:683). The directory holding the file is never fsynced, there or anywhere else in bbolt. Under POSIX, `fdatasync` on the file makes its data durable but not its directory entry. After a crash the name can be gone, so the next `Open` creates a fresh empty database, and every committed transaction is lost even though each `Commit` fsynced and returned nil.

Applications that create their database directory themselves usually fsync that directory's parent, but nothing in bbolt's documentation says callers must also fsync the database's own directory after `Open`.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall), running the unmodified library on `wasip1` through `compat_overlay`. A small server opens `data/kv.db` with the default options (sync on), fsyncs `.` after creating `data/`, and acknowledges `SET` only after `db.Update` returns. A client writes 300 keys and then checks that every acknowledged key is still there. With `strict_dirs`, a directory entry survives a crash only after its directory is fsynced.

```
cd examples/bboltkv
seedfall replay -c seedfall.strict.nodirsync.json -seed 1
```

`seedfall shrink` reduces the schedule to one fault, saved as `createdir.faults.json`:

```
faults before the failure (1):
   1.7052s  crash server, restart at 1.942589s
```

The restarted server recovers 0 keys, and the client reports `acknowledged write lost: k0=v0, server now answers "NONE"`.

| Config | Stock v1.5.0 | Patched |
|---|---|---|
| sync on, crashes, partitions (`seedfall.json`) | 300/300 pass | — |
| plus torn writes and crashes mid-I/O (`seedfall.torn.json`) | 300/300 pass | — |
| plus fsync `EIO`, supervised (`seedfall.fsyncgate.json`) | 300/300 pass | — |
| strict directories, the server fsyncs `data/` after `Open` (`seedfall.strict.json`) | 300/300 pass | — |
| strict directories, no extra fsync (`seedfall.strict.nodirsync.json`) | fails, seed 1 | 300/300 pass |
| `NoSync: true` (`seedfall.nosync.json`) | fails, seed 1 (expected) | — |

## Suggested fix

After `init` has written and synced a new file, fsync its directory:

```go
dir, err := os.Open(filepath.Dir(db.path))
if err != nil {
	return err
}
defer dir.Close()
return dir.Sync()
```

## Fix verified

A local copy of v1.5.0 with the directory fsync at the end of `init` passes seed 1 and 300 seeds of `seedfall.strict.nodirsync.json`.
