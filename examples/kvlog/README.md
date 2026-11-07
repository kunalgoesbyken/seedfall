# kvlog

A log-structured key-value server that appends `key=value` lines to `data/kv.log`. It shows how a storage engine that looks correct loses acknowledged writes, one fault class at a time.

| Config | Server flags | Result |
|---|---|---|
| `seedfall.json` | `-sync batch` (acknowledges before fsync) | fails on seed 1 |
| `seedfall.always.json` | `-sync always` | **still fails** on seed 1. A crash mid-append leaves a torn tail; recovery doesn't truncate it, so the next record is glued onto garbage. Shrinks from 33 faults to 2 |
| `seedfall.fixed.json` | `-sync always -repair` | passes (3,000 seeds tested) |
| `seedfall.strict.json` | as fixed, plus `strict_dirs` | passes |
| `seedfall.fsyncgate.json` | as fixed, plus `fsync_eio` | **fails again** on seed 1. A failed fsync leaves the record in the page cache but not on disk, later fsyncs succeed, and recovery truncates at the hole, dropping every later acknowledged write. Shrinks from 37 faults to 2 |
| `seedfall.rewind.json` | adds `-rewind` (truncate the unsynced record when fsync fails) | passes (400 seeds tested) |

```sh
seedfall run -c seedfall.always.json -seeds 1-100
seedfall shrink -c seedfall.always.json -seed 1
go test ./...    # seedfall_test.go sweeps the fixed config from go test
```

See the [getting started guide](../../docs/getting-started.md) for a walkthrough.
