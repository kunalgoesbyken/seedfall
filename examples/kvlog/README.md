# kvlog

A log-structured key-value server that appends `key=value` lines to `data/kv.log`. It shows how a storage engine that looks correct loses acknowledged writes, one fault class at a time.

Every config other than `seedfall.json` extends it (`"extends": "seedfall.json"`) and sets only what differs, usually the server flags and one fault ([config.md](../../docs/config.md#extends)).

| Config | Server flags | Result |
|---|---|---|
| `seedfall.json` | `-sync batch` (acknowledges before fsync) | fails on seed 1 |
| `seedfall.always.json` | `-sync always` | **still fails** on seed 1. A crash mid-append leaves a torn tail; recovery doesn't truncate it, so the next record is glued onto garbage. Shrinks from 33 faults to 2, with the run cut from 60s to 991ms and the workload from 200 writes to 63, saved in `torntail.faults.json` |
| `seedfall.fixed.json` | `-sync always -repair` | passes (3,000 seeds tested) |
| `seedfall.strict.json` | as fixed, plus `strict_dirs` | passes |
| `seedfall.fsyncgate.json` | as fixed, plus `fsync_eio` | **fails again** on seed 1. A failed fsync leaves the record in the page cache but not on disk, later fsyncs succeed, and recovery truncates at the hole, dropping every later acknowledged write. Shrinks from 37 faults to 2 |
| `seedfall.rewind.json` | adds `-rewind` (truncate the unsynced record when fsync fails) | passes (400 seeds tested) |
| `seedfall.readcorrupt.json` | as fixed, plus `read_corrupt` | **fails** on seed 1. A read during recovery returns one flipped bit, and the server rebuilds its index from it: an acknowledged key goes missing or holds the wrong value. Shrinks to 3 faults, saved in `readcorrupt.faults.json` |
| `seedfall.checksum.json` | adds `-checksum` (a CRC32 on every record, re-read the log when one fails), plus `corrupt_on_crash` | passes (80 seeds tested), and passes `readcorrupt.faults.json`. A record that keeps failing at the tail is a torn write and is truncated; one that keeps failing in the middle stops the server |
| `seedfall.diskerrors.json` | as checksum, plus `disk_bytes`, `write_eio`, `write_enospc`, `read_eio` and `disk_latency`, supervised | passes (60 seeds tested). Writes are refused once the 1500-byte disk is full and the client is told; a failed read at startup exits, and the supervisor restarts the server |
| `seedfall.lostwrite.json` | as checksum, plus `lost_write` | **fails** on seed 9. An fsync reports success but the page keeps its older content, so after a crash the records appended since read as zeros; recovery takes them for a torn tail and drops acknowledged records. A checksum cannot tell, and one copy cannot bring them back |
| `seedfall.misdirected.json` | as checksum, plus `misdirected_write`, 400 writes so the log spans two pages | **fails** on seed 4: after a crash the checksum catches the page that landed on the wrong offset and the server refuses to start rather than serve wrong data |
| `seedfall.bitrot.json` | as fixed, plus `bitrot_per_sec` | **fails** on seed 1. A bit flips in a stored record, and after the next restart the server answers `VAL vs` for `v3` |

```sh
seedfall run -c seedfall.always.json -seeds 1-100
seedfall shrink -c seedfall.always.json -seed 1
seedfall inspect -c seedfall.misdirected.json -seed 4 -node server   # the corrupted ranges
go test ./...    # seedfall_test.go sweeps the fixed config from go test
```

`seedfall-regressions/seedfall.fixed-seed-1.json` is `torntail.faults.json` pinned for the fixed config with `seedfall pin -c seedfall.fixed.json -seed 1 -faults torntail.faults.json`, so every `seedfall run -c seedfall.fixed.json` and the go test replay the torn tail first and fail if `-repair` stops repairing it.

The client wraps its write count in `sim.Budget`, so `seedfall shrink` can lower it ([workload size](../../docs/properties.md#workload-size)).

See the [getting started guide](../../docs/getting-started.md) for a walkthrough.
