# bitcask

A key-value server on [prologic/bitcask](https://git.mills.io/prologic/bitcask) v2.2.0 (`go.mills.io/bitcask/v2`) at its strongest settings: `WithSyncWrites(true)` (fsync every `Put`) and `WithAutoRecovery(true)` (repair a torn last datafile on open). `MaxDatafileSize` is lowered to 4 KB so datafiles rotate during a run. The server acknowledges `SET` only after `Put` returns. The client accepts any value at least as new as the last acknowledged one.

bitcask locks its directory with gofrs/flock, which returns `ErrUnsupported` under `GOOS=wasip1`. The module replaces it with [`../flockshim`](../flockshim), an in-process lock. Each node is a single process, so this is equivalent. logrus is raised from v1.9.3, which does not build for `wasip1`, to v1.10.2.

| Config | Result |
|---|---|
| `seedfall.json` | crashes and partitions: passes (300 seeds tested) |
| `seedfall.nosync.json` | `SyncWrites: false`: fails on seed 1, as expected (negative control) |
| `seedfall.torn.json` | adds torn writes and crashes mid-I/O: fails on seed 1, `config.json` is torn and every `Open` returns `ErrBadConfig` |
| `seedfall.recovery.json` | the same, with the server fsyncing `config.json` after `Open`: fails on seed 1, auto-recovery can't delete an index that doesn't exist |
| `seedfall.strict.json` | `config.json` workaround under strict POSIX directories: fails on seed 2, a rotated datafile is lost |
| `seedfall.fsyncgate.json` | `config.json` workaround plus fsync `EIO`, supervised: fails on seed 1, auto-recovery truncates at the hole a failed fsync left |

Root causes and verified fixes: [config.json](../../docs/findings/bitcask-config-json-torn.md), [auto-recovery on torn tails](../../docs/findings/bitcask-autorecovery-torn-tail.md), [unsynced recovered datafile](../../docs/findings/bitcask-recovered-datafile-fsync.md), [rotation and fsync errors](../../docs/findings/bitcask-rotation-dir-fsync-and-eio.md).
