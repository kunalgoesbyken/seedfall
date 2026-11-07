# wals

One append-only log server (`logserver`) with two backends, [tidwall/wal](https://github.com/tidwall/wal) and [rosedblabs/wal](https://github.com/rosedblabs/wal), both configured to fsync every write. The server replays the log on startup and acknowledges an append only after the library's write returns. The client appends 200 entries of varying size and verifies that every acknowledged entry is intact at its index, and that every stored entry is one it actually sent.

| Config | Result |
|---|---|
| `tidwall.json` | fails on seed 1: one crash mid-write and `Open` returns `log corrupt` on every restart (tidwall/wal#21) |
| `tidwall.strict.json` | fails on seed 1 |
| `rosedb.json` | fails on seed 1: after a crash mid-write, new writes land after the torn chunk and are lost to replay. 2 faults |
| `rosedb.strict.json` | fails on seed 1 |

Root causes and verified fixes: [tidwall/wal](../../docs/findings/tidwall-wal-torn-tail.md), [rosedblabs/wal](../../docs/findings/rosedblabs-wal-torn-tail.md).
