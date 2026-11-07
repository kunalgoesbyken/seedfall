# DRAFT ISSUE (not filed): rosedblabs/wal appends after a torn chunk, so one crash mid-write makes later fsynced writes unreachable and rosedb can no longer open

**Repo:** rosedblabs/wal (tested on v1.3.8, `Sync: true`). This affects rosedblabs/rosedb v2, which replays the WAL on `Open`.
**Severity:** acknowledged, fsynced writes are lost to replay, and rosedb's `Open` fails with `invalid crc` after a single crash during a write.
**Searched upstream:** no matching issue in rosedblabs/wal or rosedblabs/rosedb. rosedb#56 ("Crash resilience", 2021) got the answer that rosedb "always starts normally whatever the crash".

## What happens

1. With `Sync: true`, `WAL.Write` fsyncs before returning, so a crash can only tear the chunk being written, which was never acknowledged.
2. On restart, `Open` opens the last segment and `openSegmentFile` seeks to the end of the file (segment.go:116, :126). Nothing checks the tail, so the torn chunk stays.
3. New writes are appended **after the torn bytes**, fsynced and acknowledged.
4. On the next restart, sequential replay (`NewReader().Next()`) hits the torn chunk and returns `ErrInvalidCRC` (segment.go:453). Everything written after it is unreachable to replay, even though it was fsynced and acknowledged.

rosedb v2.4.0 `loadIndexFromWAL` (db.go:686-691) returns any non-EOF reader error from `Open`. In rosedb, the same crash therefore makes the database unopenable rather than silently losing data.

A related hazard: `readInternal` slices `block[chunkOffset : chunkOffset+chunkHeaderSize]` and then `block[start:start+length]` (segment.go:440-447) without checking either range against the segment size. On a torn tail, those reads see stale buffer bytes, and a large garbage `length` can panic with `slice bounds out of range`. Compare rosedb#315, which reports that panic from `segment.go readInternal`.

## Reproduction

The harness is an append-only server that replays the WAL on startup. It stops at the first replay error and keeps serving, which is the most lenient behavior a consumer could choose. A client appends 200 entries, then verifies every acknowledged one.

```
cd examples/wals
seedfall replay -c rosedb.json -seed 1
  427ms  crash server during disk op #56        <- torn chunk, never acknowledged
  server#2| replay stopped after 26 entries: invalid crc, the data may be corrupted
  1.705s crash server                            <- any later crash
  server#3| replay stopped after 26 entries: invalid crc …
  FAIL client#1: acknowledged entry 27 lost
```

It shrinks to 2 faults: one crash during a write, then any crash.

## Strict POSIX directories

`rotateActiveSegment` (wal.go:316-329) creates the next segment file but never fsyncs the WAL directory. Under seedfall's `strict_dirs` model, one crash after a rotation loses the whole new segment (`rosedb.strict.json`, seed 4, one plain crash, acknowledged entry 116 lost). This has the same ext4/XFS caveat as the hashicorp/raft and tidwall/wal directory findings: those filesystems usually persist the entry when the file is fsynced.

## Verified fix

These changes are applied to a local copy in `.prof/rosedb-wal`:

1. `segment.repairTail()`, called from `Open` on the active segment, scans the segment with its own reader, truncates and fsyncs at the end of the last valid chunk, and resets `currentBlockNumber` and `currentBlockSize`.
2. `readInternal` returns `ErrInvalidCRC` when the header or the payload would extend past the segment size, instead of reading stale buffer bytes.
3. `rotateActiveSegment` fsyncs the WAL directory after creating the new segment.

| Variant | `rosedb.json` | `rosedb.strict.json` |
|---|---|---|
| v1.3.8 | FAIL seed 1 (2 faults) | FAIL |
| + tail repair + bounds check | ok, 1000 seeds | FAIL seed 4 (segment lost) |
| + directory fsync on rotation | ok, 1000 seeds | ok, 1000 seeds |

Not covered: a torn record that spans blocks (`ChunkTypeFirst` with no `Last`). Reading it returns `io.EOF` instead of an error, so repair wouldn't truncate it. The test payloads are under 32 KB, so this path wasn't exercised.
