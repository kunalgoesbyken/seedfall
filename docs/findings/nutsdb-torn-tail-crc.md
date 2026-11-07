# A torn write at the end of the active data file makes Open fail with "crc error" on every restart

**Repo:** nutsdb/nutsdb (tested on v1.1.0, still present on `main` at 39d6badd)
**Severity:** one crash in the middle of a commit that was never acknowledged leaves the database unopenable until someone deletes data by hand.

## Summary

`parseDataFiles` (db.go:487) scans each data file with `fileRecovery.readEntry`. When an entry's checksum doesn't match, `readEntry` returns `ErrCrc` (recovery_reader.go:86). The scan loop treats `io.EOF`, `io.ErrUnexpectedEOF`, `ErrEntryZero` and `ErrHeaderSizeOutOfBounds` as the end of the log (db.go:523), but not `ErrCrc`. Unless the offset is already past `SegmentSize`, it returns the error (db.go:530), and `Open` fails with `db.buildIndexes error: crc error` (db.go:148).

A torn tail is exactly what a crash during `writeData` (tx.go:496) leaves behind. Part of the commit buffer reaches the disk and the rest of the preallocated segment stays zero, so the last entry has a valid header and a bad checksum. That commit never returned to the caller, so it is safe to discard. Instead, every later `Open` fails the same way.

This matches field reports that were closed without a root cause: #569 (power loss on an embedded device, `crc error` on every boot, fixed only by deleting the data files), #141 and #63 (`db.buildIndexes error: when build activeDataIndex readAt err: crc error`).

## Reproduction

Reproduced deterministically with [seedfall](https://github.com/kunalgoesbyken/seedfall). It runs a small server on nutsdb v1.1.0 with the default options (`SyncEnable: true`, `RWMode: FileIO`) and an 8 KB segment size. It acknowledges `SET` only after `db.Update` returns. To keep this separate from the `bucket.Meta` durability issue, the server fsyncs `bucket.Meta` itself after creating its bucket. The simulated disk keeps only fsynced data across a crash, and with torn writes enabled a crash can keep a partial, unsynced write.

```
cd examples/nutsdb
seedfall replay -c seedfall.torn.json -seed 1
```

`seedfall shrink` reduces the schedule to a single crash during a disk write:

```
faults before the failure (1):
  271.223ms  crash server during disk op #56, restart at 635.343ms
```

On restart the server logs `open data/db: db.buildIndexes error: crc error` and exits. Every later restart fails the same way.

Controls on the same harness:

| Config | Stock v1.1.0 | Patched |
|---|---|---|
| crashes + partitions (`seedfall.fsyncmeta.json`) | 300/300 pass | — |
| plus torn writes + crash during I/O (`seedfall.torn.json`) | fails, seed 1 | 300/300 pass |

## Suggested fix

Treat a checksum failure in the active (last) data file as the end of the log, the same way a zero header is treated:

```go
if errors.Is(err, io.EOF) || errors.Is(err, ErrIndexOutOfBound) || errors.Is(err, io.ErrUnexpectedEOF) ||
	errors.Is(err, ErrEntryZero) || errors.Is(err, ErrHeaderSizeOutOfBounds) || errors.Is(err, ErrCrc) {
	break
}
```

`writeOff` is then set to the start of the torn entry, so the next commit overwrites it. Ideally the rest of the segment from that offset would also be zeroed, so that leftover bytes from the torn entry can't be misread after a shorter entry overwrites its start. A checksum failure in an older, sealed file is real corruption and should still fail `Open`.

## Fix verified

A local copy of v1.1.0 with `ErrCrc` added to that list (plus the `bucket.Meta` fsync) passes the failing seed and 300 seeds of `seedfall.torn.json`.
