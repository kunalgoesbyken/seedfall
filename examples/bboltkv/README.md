# bboltkv

A key-value server on [etcd-io/bbolt](https://github.com/etcd-io/bbolt) v1.5.0, unmodified. bbolt doesn't build for `wasip1` (it needs `mmap` and `flock`), so the configs set `"compat_overlay": true` ([compat](../../docs/compat.md)). The server acknowledges `SET` only after `db.Update` returns. The client accepts any value at least as new as the last acknowledged one.

| Config | Result |
|---|---|
| `seedfall.json` | sync on, crashes and partitions: passes (300 seeds tested) |
| `seedfall.torn.json` | adds torn writes and crashes mid-I/O: passes (300 seeds tested) |
| `seedfall.fsyncgate.json` | fsync `EIO`, supervised: passes (300 seeds tested) |
| `seedfall.strict.json` | strict POSIX directories, the server fsyncs `data/` after `Open`: passes (300 seeds tested) |
| `seedfall.strict.nodirsync.json` | strict POSIX directories without that fsync: fails on seed 1, the database file vanishes with every acknowledged write |
| `seedfall.nosync.json` | `NoSync: true`: fails on seed 1, as expected (negative control) |

Root cause and verified fix: [docs/findings/bbolt-create-dir-fsync.md](../../docs/findings/bbolt-create-dir-fsync.md).
