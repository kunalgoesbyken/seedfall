# badgerkv

A key-value server on [dgraph-io/badger](https://github.com/dgraph-io/badger) v4.9.6, unmodified. badger builds for `wasip1`, but `Open` fails there because ristretto's `mmap` returns `ENOSYS`. The configs set `"compat_overlay": true`, which emulates ristretto's writable `mmap` with `msync` written back through real fsyncs ([compat](../../docs/compat.md)). The server acknowledges `SET` only after `db.Update` returns with `SyncWrites` on. Memtables and value log files are 1 MB so that the emulated mappings stay small. The client accepts any value at least as new as the last acknowledged one.

badger is a heavy guest: a seed takes tens of seconds, so it is not part of `scripts/regress.sh`.

| Config | Result |
|---|---|
| `seedfall.json` | `SyncWrites: true`, crashes and partitions: passes (4 seeds tested) |
| `seedfall.nosync.json` | `SyncWrites: false`: fails on seed 1, as expected (negative control) |
| `seedfall.strict.json` | strict POSIX directories: fails on seed 1 after one crash. badger creates each memtable WAL (`*.mem`) without fsyncing the directory, which is the likely cause; not yet shrunk or verified with a fix |
