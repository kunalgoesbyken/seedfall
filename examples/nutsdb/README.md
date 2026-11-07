# nutsdb

A key-value server on [nutsdb/nutsdb](https://github.com/nutsdb/nutsdb) v1.1.0 with its default durability settings: `SyncEnable: true` (fsync every commit) and `RWMode: FileIO`. The segment size is lowered to 8 KB so data files rotate during a run, and automatic merging is off. On startup the server creates its bucket if it is missing. It acknowledges `SET` only after `db.Update` commits. The client accepts any value at least as new as the last acknowledged one.

nutsdb locks its directory with gofrs/flock, which returns `ErrUnsupported` under `GOOS=wasip1`. The module replaces it with [`../flockshim`](../flockshim), an in-process lock. Each node is a single process, so this is equivalent.

| Config | Result |
|---|---|
| `seedfall.json` | fails on seed 1: one crash and the server recovers 0 keys, because `bucket.Meta` was never fsynced |
| `seedfall.fsyncmeta.json` | the server fsyncs `bucket.Meta` itself after creating the bucket: passes (300 seeds tested) |
| `seedfall.torn.json` | the workaround plus torn writes and crashes mid-I/O: fails on seed 1, `Open` returns `crc error` on every restart |
| `seedfall.strict.json` | the workaround under strict POSIX directories: fails on seed 3, a rotated data file is lost |
| `seedfall.fsyncgate.json` | the workaround plus fsync `EIO`, supervised: fails on seed 1, later commits land after a hole that recovery stops at |
| `seedfall.nosync.json` | `SyncEnable: false`: fails on seed 1, as expected (negative control) |

Root causes and verified fixes: [bucket.Meta](../../docs/findings/nutsdb-bucket-meta-fsync.md), [torn tail](../../docs/findings/nutsdb-torn-tail-crc.md), [rotation and fsync errors](../../docs/findings/nutsdb-rotation-dir-fsync-and-eio.md).
