# Acknowledged writes can be lost after a data-file rotation (no directory fsync) or after a failed fsync (offset not rewound)

**Repo:** nutsdb/nutsdb (tested on v1.1.0, still present on `main` at 39d6badd)
**Severity:** lower than the `bucket.Meta` and torn-tail issues. Both need a less common event, a crash shortly after a rotation or an fsync error, and both lose acknowledged writes silently.

Two related durability gaps in the commit path, both with `SyncEnable: true`.

## 1. A new data file is created without fsyncing the directory

When a commit doesn't fit in the active file, `rotateActiveFile` (tx.go:471) opens the next `<id>.dat` through `FileManager.GetDataFile` (tx.go:487). That reaches `FdManager.GetFd`, which creates the file with `O_CREATE` (internal/fileio/fd_manager.go:68), and `fileio.Truncate` preallocates it. Commits written to the new file are fsynced, but the directory entry for the file never is. On filesystems that need an explicit directory fsync to persist a new name (POSIX; ext4 with `data=writeback`, XFS), a crash can drop the whole file and every acknowledged commit in it. Recovery then sees only the older files.

Reproduction (stock v1.1.0; the server fsyncs `bucket.Meta` itself to avoid the separate `bucket.Meta` issue):

```
cd examples/nutsdb
seedfall replay -c seedfall.strict.json -seed 3
```

`seedfall shrink` reduces it to one plain crash:

```
faults before the failure (1):
  4.619248s  crash server, restart at 4.956679s
```

The server restarts with `recovered 247 keys` out of the 277 it acknowledged. The client reports `acknowledged write lost: k248=v248, server now answers "NONE"`. With 8 KB segments, `k248` onwards lived in the newest data file, whose name was never made durable.

**Fix:** fsync the directory after creating a data file in `rotateActiveFile` (and after the first `setActiveFile` on an empty directory):

```go
if err := syncDir(tx.db.opt.Dir); err != nil {
	return err
}
```

## 2. A failed fsync leaves a hole that recovery treats as the end of the log

`writeData` (tx.go:496) writes the commit at `ActualSize`, advances `writeOff` and `ActualSize` (tx.go:512-513), and only then calls `Sync` (tx.go:516). If `Sync` fails, the commit returns an error and is not acknowledged, but the offsets stay advanced. On Linux, a failed fsync can drop the dirty pages and clear the error ("fsyncgate"), so those bytes may never reach the disk. The next commit is written after the hole, and its fsync succeeds. After a crash, recovery reads zeros at the hole, treats them as the end of the file (`ErrEntryZero` at db.go:523, `entry == nil` at db.go:533), and silently drops every later acknowledged commit in that file.

Reproduction (stock v1.1.0, same workaround, supervised so that the server can exit on a startup I/O error):

```
cd examples/nutsdb
seedfall replay -c seedfall.fsyncgate.json -seed 1
```

```
faults before the failure (2):
  900.606ms  fsync on server fails with EIO at disk op #170, unsynced writes dropped
   1.7052s  crash server, restart at 1.942589s
```

The server restarts with `recovered 54 keys`, and the client reports `acknowledged write lost: k55=v55, server now answers "NONE"`. Every write acknowledged after the failed fsync is gone.

**Fix:** on a failed `Sync`, rewind the offsets so that the next commit rewrites the same region:

```go
if err := tx.db.ActiveFile.rwManager.Sync(); err != nil {
	tx.db.ActiveFile.writeOff -= int64(l)
	tx.db.ActiveFile.ActualSize -= int64(l)
	return 0, err
}
```

Refusing further writes until the database is reopened (what PostgreSQL does) would also work.

## Fix verified

| Config | Stock v1.1.0 | Patched |
|---|---|---|
| crashes + partitions (`seedfall.fsyncmeta.json`) | 300/300 pass | — |
| plus strict directory durability (`seedfall.strict.json`) | fails, seed 3 | 300/300 pass (directory fsync on rotation) |
| plus fsync `EIO` (`seedfall.fsyncgate.json`) | fails, seed 1 | 300/300 pass (offset rewind) |

The patched copy also carries the `bucket.Meta` fsync and the torn-tail fix. With all four changes it passes 300 seeds of each config, including torn writes, crashes during I/O and strict directories combined.
