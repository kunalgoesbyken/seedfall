# Acknowledged writes can be lost after a datafile rotation (no directory fsync) or after a failed fsync (offset not rewound)

**Repo:** prologic/bitcask on git.mills.io, module `go.mills.io/bitcask/v2` (tested on v2.2.0, which is `main` at a5ea7328ad)
**Severity:** lower than the `config.json` and auto-recovery issues. Both need a less common event, a crash shortly after a rotation or an fsync error, and both lose acknowledged writes silently with `WithSyncWrites(true)`.

## 1. A new datafile is created without fsyncing the directory

`maybeRotate` (bitcask.go:262) closes the current datafile and creates the next one with `data.NewOnDiskDatafile` (bitcask.go:277), which opens it with `O_CREATE|O_APPEND` (internal/data/datafile.go:63). Every `Put` into the new file is fsynced, but the directory entry for the file never is. On filesystems that need an explicit directory fsync to persist a new name, a crash can drop the whole file and every acknowledged record in it.

Reproduction (stock v2.2.0; the server fsyncs `config.json` after `Open` to work around the separate `config.json` issue, and the disk uses strict POSIX directory semantics):

```
cd examples/bitcask
seedfall replay -c seedfall.strict.json -seed 2
```

`seedfall shrink` reduces it to one plain crash after the client finished writing:

```
faults before the failure (1):
  6.681412s  crash server, restart at 6.843388s
```

The server restarts with `recovered 180 keys`, and the client reports `acknowledged write lost: k249=v249, server now answers "NONE"`.

**Fix:** fsync the directory at the end of `maybeRotate`, after the new datafile is created.

## 2. A failed fsync leaves a hole that auto-recovery truncates at

`WriteBatch` (batch.go:87) appends each entry with `b.current.Write` (batch.go:102), which advances the datafile's offset (internal/data/ondisk.go:169), and then calls `Sync` (batch.go:108). If `Sync` fails, the `Put` returns an error and is not acknowledged, but the bytes stay in the file at that offset. On Linux, a failed fsync can drop the dirty pages and clear the error ("fsyncgate"), so they may never reach the disk. The next `Put` appends after them, and its fsync succeeds. After a crash, the durable file has zeros where the failed record was. Auto-recovery decodes a zero key size as `errInvalidKeyOrValueSize`, treats it as a corrupt tail, and throws away everything after it, including every later acknowledged record.

Reproduction (stock v2.2.0, same workaround, supervised so that the server can exit on an I/O error):

```
cd examples/bitcask
seedfall replay -c seedfall.fsyncgate.json -seed 1
```

```
faults before the failure (2):
  900.606ms  fsync on server fails with EIO at disk op #116, unsynced writes dropped
   1.7052s  crash server, restart at 1.942589s
```

The first restart also shows the separate missing-index bug (`error deleting the index on recovery`). The supervised restart after that reports `recovered 54 keys`, and the client reports `acknowledged write lost: k55=v55, server now answers "NONE"`.

**Fix:** on a failed `Sync`, truncate the active datafile back to the entry's offset, so the next write replaces the lost bytes:

```go
if err := b.current.Sync(); err != nil {
	return errors.Join(err, b.current.Truncate(offset))
}
```

`Truncate` is a new `Datafile` method that calls `os.File.Truncate` and resets the in-memory offset. Refusing further writes until the database is reopened would also work.

## Harness

[seedfall](https://github.com/kunalgoesbyken/seedfall) runs a small server on bitcask v2.2.0 with `WithSyncWrites(true)`, `WithAutoRecovery(true)` and a 4 KB `MaxDatafileSize`. It acknowledges `SET` only after `Put` returns. A client writes 300 keys and then checks that every acknowledged key is still there. The simulated disk keeps only fsynced data across a crash.

## Fix verified

| Config | Stock v2.2.0 | Patched |
|---|---|---|
| crashes + partitions (`seedfall.json`) | 300/300 pass | — |
| plus strict directory durability (`seedfall.strict.json`) | fails, seed 2 | 300/300 pass |
| plus fsync `EIO`, supervised (`seedfall.fsyncgate.json`) | fails, seed 1 | 300/300 pass |

The patched copy carries all six fixes from these drafts. It also passes 300 seeds of strict directories and of fsync `EIO`, each combined with torn writes and crashes during I/O.
