# `bucket.Meta` is never fsynced, so a crash can make recovery drop every entry in the bucket

**Repo:** nutsdb/nutsdb (tested on v1.1.0, still present on `main` at 39d6badd)
**Severity:** with the default `SyncEnable: true`, one OS crash or power loss after a bucket is created can silently lose every acknowledged write in that bucket.

## Summary

Bucket definitions live in `<dir>/bucket.Meta`. `(*BucketManager).SubmitPendingBucketChange` (bucket_manager.go:55) appends the encoded buckets with `bm.fd.Write` (bucket_manager.go:79) and returns. Nothing in the package ever calls `Sync` on that file. `Commit` fsyncs the data file in `writeData` (tx.go:516) and then calls `SubmitBucket` (tx.go:270), so a transaction that creates a bucket is acknowledged while its bucket record is still only in the page cache.

On open, `rebuildBucketManager` (db.go:1035) loads buckets from `bucket.Meta`, and `parseDataFiles` then skips every entry whose bucket ID is unknown (db.go:501, `GetBucketById(...); errors.Is(err, ErrBucketNotExist) { continue }`). If the bucket record didn't reach the disk, every entry in that bucket is dropped during recovery, even though the entries themselves were fsynced. `Open` succeeds and reports an empty bucket. The usual startup code (`if !tx.ExistBucket(...) { tx.NewBucket(...) }`) then recreates the bucket, and the application carries on without its data.

Exposure lasts until the kernel writes `bucket.Meta` back on its own, typically up to 30 seconds after each bucket is created. A process crash (`kill -9`, panic) is not enough, because the page cache survives; it takes an OS crash or power loss.

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall). It runs a small server on nutsdb v1.1.0 with the default options (`SyncEnable: true`, `RWMode: FileIO`), an 8 KB segment size and merges disabled. On startup the server creates the `kv` bucket if it is missing. It acknowledges `SET` only after `db.Update` returns. A client writes 300 keys and then checks that every acknowledged key is still there. The simulated disk keeps only fsynced data across a crash.

```
cd examples/nutsdb
seedfall replay -c seedfall.json -seed 1
```

`seedfall shrink` reduces the schedule to a single crash:

```
faults before the failure (1):
   1.7052s  crash server, restart at 1.942589s
```

The server restarts with `recovered 0 keys`, although every write it acknowledged before the crash had been fsynced to `0.dat`. The client then reports `acknowledged write lost: k0=v0, server now answers "NONE"`.

Controls on the same harness:

| Config | Stock v1.1.0 | Patched |
|---|---|---|
| `SyncEnable: true`, crashes + partitions (`seedfall.json`) | fails, seed 1 | 300/300 pass |
| the same, with the server fsyncing `bucket.Meta` itself after creating the bucket (`seedfall.fsyncmeta.json`) | 300/300 pass | — |
| `SyncEnable: false` (`seedfall.nosync.json`) | fails, seed 1 (expected: commits are not fsynced) | — |

The app-side workaround passing on the stock library isolates the cause to `bucket.Meta`.

## Suggested fix

In `SubmitPendingBucketChange`, fsync after the write:

```go
if _, err := bm.fd.Write(bytes); err != nil {
	return err
}
return bm.fd.Sync()
```

`bucket.Meta` is created by `NewBucketManager` on the first `Open`, so the directory should also be fsynced once after creating it.

## Fix verified

A local copy of v1.1.0 with the fsync added passes the failing seed and 300 seeds of `seedfall.json` without the app-side workaround.
