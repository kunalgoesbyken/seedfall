# buntdb

A key-value server on [tidwall/buntdb](https://github.com/tidwall/buntdb) v1.3.2. It acknowledges `SET` only after the `Update` transaction commits. The client accepts any value at least as new as the last acknowledged one.

| Config | Result |
|---|---|
| `seedfall.json` | `SyncPolicy: Always`: passes (300 seeds tested) |
| `seedfall.torn.json` | adds torn writes and crashes mid-I/O: passes (300 seeds tested) |
| `seedfall.everysecond.json` | `SyncPolicy: EverySecond`: fails on seed 1, as documented (up to a second of writes can be lost) |
| `seedfall.shrink.json` | aggressive auto-shrink: fails on seed 27, an acknowledged key comes back missing |
| `seedfall.shrink.strict.json` | the same under strict POSIX directories: fails on seed 6 |

Root cause and verified fix: [docs/findings/tidwall-buntdb-shrink-fsync.md](../../docs/findings/tidwall-buntdb-shrink-fsync.md).
